package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/audit"
	"github.com/alex-mextner/open-remote-commander/internal/authn"
	"github.com/alex-mextner/open-remote-commander/internal/config"
	"github.com/alex-mextner/open-remote-commander/internal/controlplane"
	"github.com/alex-mextner/open-remote-commander/internal/mcpserver"
	"github.com/alex-mextner/open-remote-commander/internal/relay"
	"github.com/alex-mextner/open-remote-commander/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.LoadServer()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(2)
	}

	st, err := buildStore(cfg)
	if err != nil {
		logger.Error("store initialization failed", "error", err)
		os.Exit(2)
	}
	verifier, err := buildVerifier(cfg)
	if err != nil {
		logger.Error("auth initialization failed", "error", err)
		os.Exit(2)
	}
	mcpVerifier := verifier
	mcpWrap := func(h http.Handler) http.Handler { return h }
	if cfg.MCPTrustLoopback {
		trust, err := authn.NewLoopbackTrust(verifier, cfg.MCPTrustedSubject)
		if err != nil {
			logger.Error("trusted loopback initialization failed", "error", err)
			os.Exit(2)
		}
		mcpVerifier = trust.Verifier()
		mcpWrap = trust.Middleware
	}

	hub := relay.NewHub(cfg.MaxInFlightPerDevice)
	auditWriter := audit.New(st, logger, 2048)
	defer auditWriter.Close()

	metadataURL := cfg.PublicBaseURL + "/.well-known/oauth-protected-resource/mcp"
	mcpSrv := mcpserver.New(st, hub, auditWriter, mcpVerifier, metadataURL)
	cp := controlplane.New(st, hub, verifier, cfg.PublicBaseURL, cfg.AllowedOrigins, logger, []byte(cfg.PairingSecret))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	registerMetadataRoutes(mux, cfg)
	mux.Handle("/mcp", originGuard(cfg.AllowedOrigins, mcpWrap(mcpSrv.Handler())))
	cp.Register(mux)

	httpServer := &http.Server{
		Addr: cfg.ListenAddr, Handler: securityHeaders(mux), ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		logger.Info("server listening", "addr", cfg.ListenAddr, "public_base_url", cfg.PublicBaseURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("graceful shutdown failed", "error", err)
	}
}

func buildStore(cfg config.Server) (store.Store, error) {
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		return store.NewMemory(), nil
	}
	return store.NewNeonHTTP(cfg.DatabaseURL, cfg.NeonHTTPEndpoint)
}

func buildVerifier(cfg config.Server) (authn.Verifier, error) {
	switch cfg.AuthMode {
	case "dev":
		return authn.NewHMACVerifier([]byte(cfg.HMACSecret), cfg.AuthAudience)
	case "introspection":
		u, err := url.Parse(cfg.IntrospectionURL)
		if err != nil {
			return nil, err
		}
		return &authn.IntrospectionVerifier{Endpoint: u, ClientID: cfg.IntrospectionID, ClientSecret: cfg.IntrospectionSecret, Audience: cfg.AuthAudience}, nil
	default:
		return nil, errors.New("unsupported auth mode")
	}
}

func registerMetadataRoutes(mux *http.ServeMux, cfg config.Server) {
	if cfg.MCPTrustLoopback {
		return
	}
	metadata := protectedResourceMetadata(cfg)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", metadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", metadata)
}

func protectedResourceMetadata(cfg config.Server) http.HandlerFunc {
	issuer := cfg.AuthIssuer
	if issuer == "" {
		issuer = cfg.PublicBaseURL
	}
	resource := cfg.PublicBaseURL + "/mcp"
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource": resource, "authorization_servers": []string{issuer},
			"scopes_supported": []string{"orc:tools"}, "bearer_methods_supported": []string{"header"},
		})
	}
}

func originGuard(allowed []string, next http.Handler) http.Handler {
	set := map[string]struct{}{}
	for _, v := range allowed {
		set[strings.TrimRight(v, "/")] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
		if origin != "" {
			if _, ok := set[origin]; !ok {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}
