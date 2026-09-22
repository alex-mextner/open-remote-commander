package controlplane

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/authn"
	"github.com/alex-mextner/open-remote-commander/internal/protocol"
	"github.com/alex-mextner/open-remote-commander/internal/relay"
	"github.com/alex-mextner/open-remote-commander/internal/store"
	"github.com/alex-mextner/open-remote-commander/internal/ws"
	apiv1 "github.com/alex-mextner/open-remote-commander/pkg/api/v1"
)

const maxJSONBody = 64 << 10

type HTTP struct {
	store          store.Store
	hub            *relay.Hub
	verifier       authn.Verifier
	baseURL        string
	allowedOrigins map[string]struct{}
	log            *slog.Logger
	pairingSecret  []byte
	pairLimiter    *ipLimiter
	tokenLimiter   *ipLimiter
}

func New(st store.Store, hub *relay.Hub, verifier authn.Verifier, baseURL string, origins []string, logger *slog.Logger, pairingSecret []byte) *HTTP {
	m := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		m[strings.TrimRight(origin, "/")] = struct{}{}
	}
	return &HTTP{store: st, hub: hub, verifier: verifier, baseURL: strings.TrimRight(baseURL, "/"), allowedOrigins: m, log: logger, pairingSecret: append([]byte(nil), pairingSecret...), pairLimiter: newIPLimiter(0.2, 5), tokenLimiter: newIPLimiter(1, 10)}
}

func (h *HTTP) Register(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/pairings", h.cors(http.HandlerFunc(h.startPairing)))
	mux.Handle("POST /api/v1/pairings/approve", h.cors(h.requireUser("orc:tools", h.approvePairing)))
	mux.Handle("POST /api/v1/pairings/token", h.cors(http.HandlerFunc(h.consumePairing)))
	mux.Handle("GET /api/v1/devices", h.cors(h.requireUser("orc:tools", h.listDevices)))
	mux.Handle("DELETE /api/v1/devices/{device_id}", h.cors(h.requireUser("orc:tools", h.revokeDevice)))
	mux.HandleFunc("OPTIONS /api/v1/{rest...}", h.options)
	mux.HandleFunc("GET /agent/v1/connect", h.agentConnect)
	mux.HandleFunc("GET /pair", h.pairPage)
	mux.HandleFunc("POST /pair/approve", h.pairApproveForm)
}

type userHandler func(http.ResponseWriter, *http.Request, authn.Identity)

func (h *HTTP) requireUser(scope string, next userHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := authn.BearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		id, err := authn.Require(r.Context(), h.verifier, token, scope)
		if err != nil {
			status := http.StatusUnauthorized
			if errors.Is(err, authn.ErrScope) {
				status = http.StatusForbidden
			}
			writeError(w, status, "unauthorized", "access denied")
			return
		}
		next(w, r, id)
	}
}

func (h *HTTP) startPairing(w http.ResponseWriter, r *http.Request) {
	if !h.pairLimiter.allow(r, "pair-start") {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many pairing attempts")
		return
	}
	var in apiv1.PairingStartRequest
	if err := decodeJSON(w, r, &in); err != nil {
		return
	}
	in.DeviceName = strings.TrimSpace(in.DeviceName)
	if in.DeviceName == "" || len(in.DeviceName) > 160 {
		writeError(w, 400, "invalid_arguments", "device_name is required and must be <= 160 bytes")
		return
	}
	deviceCode, err := randomSecret(32)
	if err != nil {
		writeError(w, 500, "internal_error", "could not create pairing")
		return
	}
	userCode, err := humanCode()
	if err != nil {
		writeError(w, 500, "internal_error", "could not create pairing")
		return
	}
	expires := time.Now().UTC().Add(10 * time.Minute)
	p := store.Pairing{DeviceCodeHash: store.HashSecret(deviceCode), UserCodeHash: h.hashUserCode(userCode), DeviceName: in.DeviceName, ExpiresAt: expires}
	if err := h.store.CreatePairing(r.Context(), p); err != nil {
		h.log.Warn("create pairing", "error", err)
		writeError(w, 500, "internal_error", "could not create pairing")
		return
	}
	verify := h.baseURL + "/pair"
	writeJSON(w, http.StatusCreated, apiv1.PairingStartResponse{
		DeviceCode: deviceCode, UserCode: userCode, VerificationURI: verify,
		VerificationURIComplete: verify + "?user_code=" + url.QueryEscape(userCode), ExpiresIn: 600, Interval: 3,
	})
}

func (h *HTTP) approvePairing(w http.ResponseWriter, r *http.Request, id authn.Identity) {
	if !h.pairLimiter.allow(r, "pair-approve") {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many pairing attempts")
		return
	}
	var in apiv1.PairingApproveRequest
	if err := decodeJSON(w, r, &in); err != nil {
		return
	}
	code := normalizeUserCode(in.UserCode)
	if len(code) != 8 {
		writeError(w, 400, "invalid_arguments", "invalid user_code")
		return
	}
	if err := h.store.ApprovePairing(r.Context(), h.hashUserCode(code), id.Subject); err != nil {
		switch {
		case errors.Is(err, store.ErrPairingExpired):
			writeError(w, 410, "expired_token", "pairing expired")
		case errors.Is(err, store.ErrPairingUsed):
			writeError(w, 410, "invalid_grant", "pairing already used")
		default:
			writeError(w, 404, "not_found", "pairing not found")
		}
		return
	}
	writeJSON(w, 200, map[string]any{"approved": true})
}

func (h *HTTP) consumePairing(w http.ResponseWriter, r *http.Request) {
	if !h.tokenLimiter.allow(r, "pair-token") {
		w.Header().Set("Retry-After", "3")
		writeError(w, http.StatusTooManyRequests, "slow_down", "polling too quickly")
		return
	}
	var in apiv1.PairingTokenRequest
	if err := decodeJSON(w, r, &in); err != nil {
		return
	}
	if len(in.DeviceCode) < 32 || len(in.DeviceCode) > 256 {
		writeError(w, 400, "invalid_arguments", "invalid device_code")
		return
	}
	deviceID, err := randomID()
	if err != nil {
		writeError(w, 500, "internal_error", "could not issue credentials")
		return
	}
	deviceToken, err := randomSecret(32)
	if err != nil {
		writeError(w, 500, "internal_error", "could not issue credentials")
		return
	}
	d, err := h.store.ConsumePairing(r.Context(), store.HashSecret(in.DeviceCode), store.Device{ID: deviceID}, deviceToken)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrPairingPending):
			writeError(w, 428, "authorization_pending", "pairing has not been approved yet")
		case errors.Is(err, store.ErrPairingExpired):
			writeError(w, 410, "expired_token", "pairing expired")
		case errors.Is(err, store.ErrPairingUsed):
			writeError(w, 410, "invalid_grant", "pairing already used")
		default:
			writeError(w, 400, "invalid_grant", "invalid device_code")
		}
		return
	}
	writeJSON(w, 200, apiv1.PairingTokenResponse{DeviceID: apiv1.DeviceID(d.ID), AccessToken: deviceToken, TokenType: "Bearer"})
}

func (h *HTTP) listDevices(w http.ResponseWriter, r *http.Request, id authn.Identity) {
	devices, err := h.store.ListDevices(r.Context(), id.Subject)
	if err != nil {
		writeError(w, 500, "internal_error", "could not list devices")
		return
	}
	out := make([]apiv1.Device, 0, len(devices))
	for _, d := range devices {
		out = append(out, apiv1.Device{ID: apiv1.DeviceID(d.ID), Name: d.Name, Online: h.hub.IsOnline(id.Subject, d.ID), LastSeenAt: d.LastSeenAt, CreatedAt: d.CreatedAt,
			Capabilities: apiv1.DeviceCapabilities{Filesystem: true, Processes: true}})
	}
	writeJSON(w, 200, map[string]any{"devices": out})
}

func (h *HTTP) revokeDevice(w http.ResponseWriter, r *http.Request, id authn.Identity) {
	deviceID := r.PathValue("device_id")
	if deviceID == "" {
		writeError(w, 400, "invalid_arguments", "device_id is required")
		return
	}
	if err := h.store.RevokeDevice(r.Context(), id.Subject, deviceID); err != nil {
		writeError(w, 404, "not_found", "device not found")
		return
	}
	h.hub.Disconnect(deviceID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) agentConnect(w http.ResponseWriter, r *http.Request) {
	deviceID := strings.TrimSpace(r.URL.Query().Get("device_id"))
	token, ok := authn.BearerToken(r)
	if deviceID == "" || !ok {
		writeError(w, 401, "unauthorized", "invalid device credentials")
		return
	}
	if strings.TrimSpace(r.Header.Get("Origin")) != "" {
		writeError(w, 403, "forbidden", "browser websocket origins are not accepted")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	device, err := h.store.AuthenticateDevice(ctx, deviceID, token)
	cancel()
	if err != nil {
		writeError(w, 401, "unauthorized", "invalid device credentials")
		return
	}
	conn, err := ws.Upgrade(w, r, protocol.MaxFrameBytes)
	if err != nil {
		h.log.Debug("websocket upgrade failed", "error", err)
		return
	}
	touchCtx, touchCancel := context.WithTimeout(context.Background(), 3*time.Second)
	err = h.store.TouchDevice(touchCtx, device.ID)
	touchCancel()
	if err != nil {
		h.log.Warn("touch device", "device_id", device.ID, "error", err)
	}
	h.hub.Register(device, conn)
	h.log.Info("device connected", "device_id", device.ID)
}

var pairPageTemplate = template.Must(template.New("pair").Parse(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer"><title>Pair device · Open Remote Commander</title><style>body{font:16px system-ui;max-width:520px;margin:8vh auto;padding:24px;color:#171717}label{display:block;margin:16px 0 6px}input{width:100%;box-sizing:border-box;padding:12px;border:1px solid #aaa;border-radius:8px}button{margin-top:18px;padding:11px 16px;border:0;border-radius:8px;background:#111;color:#fff;font-weight:650}.note{color:#666;font-size:14px}</style></head><body><h1>Pair a device</h1><p>Confirm the code printed by <code>orc-agent</code>. The access token is submitted once over HTTPS and is never stored by this page.</p><form method="post" action="/pair/approve"><label>Pairing code</label><input name="user_code" value="{{.Code}}" autocomplete="one-time-code" required><label>Access token</label><input name="access_token" type="password" autocomplete="off" required><button>Approve device</button></form><p class="note">The token needs the <code>orc:tools</code> scope.</p></body></html>`))

func (h *HTTP) pairPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pairPageTemplate.Execute(w, map[string]string{"Code": r.URL.Query().Get("user_code")})
}

func (h *HTTP) pairApproveForm(w http.ResponseWriter, r *http.Request) {
	if !h.pairLimiter.allow(r, "pair-form-approve") {
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	id, err := authn.Require(r.Context(), h.verifier, r.Form.Get("access_token"), "orc:tools")
	if err != nil {
		http.Error(w, "invalid token or scope", http.StatusUnauthorized)
		return
	}
	code := normalizeUserCode(r.Form.Get("user_code"))
	if len(code) != 8 || h.store.ApprovePairing(r.Context(), h.hashUserCode(code), id.Subject) != nil {
		http.Error(w, "invalid or expired pairing code", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, "<!doctype html><meta charset=utf-8><title>Paired</title><p>Device approved. You may close this page.</p>")
}

func (h *HTTP) hashUserCode(code string) string {
	mac := hmac.New(sha256.New, h.pairingSecret)
	_, _ = mac.Write([]byte(normalizeUserCode(code)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *HTTP) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
		if origin != "" {
			if _, ok := h.allowedOrigins[origin]; !ok {
				writeError(w, http.StatusForbidden, "forbidden", "origin not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		next.ServeHTTP(w, r)
	})
}

func (h *HTTP) options(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
	if origin == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, ok := h.allowedOrigins[origin]; !ok {
		writeError(w, http.StatusForbidden, "forbidden", "origin not allowed")
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.Header().Add("Vary", "Origin")
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, 400, "invalid_json", "invalid request body")
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func randomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("dev_%x", b), nil
}

func humanCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	r := make([]byte, 8)
	if _, err := rand.Read(r); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(r[i])%len(alphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:]), nil
}

func normalizeUserCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}
