package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alex-mextner/open-remote-commander/internal/config"
)

func TestRegisterMetadataRoutesSkipsTrustedLoopbackMode(t *testing.T) {
	mux := http.NewServeMux()
	registerMetadataRoutes(mux, config.Server{
		MCPTrustLoopback: true,
		PublicBaseURL:    "http://127.0.0.1:8080",
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:8080/.well-known/oauth-protected-resource/mcp", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
func TestRegisterMetadataRoutesPublishesInAuthenticatedMode(t *testing.T) {
	mux := http.NewServeMux()
	registerMetadataRoutes(mux, config.Server{
		PublicBaseURL: "http://127.0.0.1:8080",
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://127.0.0.1:8080/.well-known/oauth-protected-resource/mcp", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
