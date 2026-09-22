package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type verifierFunc func(context.Context, string) (Identity, error)

func (f verifierFunc) Verify(ctx context.Context, token string) (Identity, error) {
	return f(ctx, token)
}

func TestLoopbackTrustInjectsOnlyForLoopbackWithoutAuthorization(t *testing.T) {
	base := verifierFunc(func(context.Context, string) (Identity, error) {
		return Identity{}, ErrInvalidToken
	})
	trust, err := NewLoopbackTrust(base, "alex")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://localhost/mcp", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	trust.Middleware(next).ServeHTTP(rec, req)
	if got == "" {
		t.Fatal("loopback request did not receive injected bearer")
	}
	token := got[len("Bearer "):]
	id, err := trust.Verifier().Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "alex" || !id.HasScope("orc:tools") || !id.Expiry.After(time.Now()) {
		t.Fatalf("unexpected identity: %+v", id)
	}
	got = ""
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://localhost/mcp", nil)
	req.RemoteAddr = "192.0.2.10:54321"
	rec = httptest.NewRecorder()
	trust.Middleware(next).ServeHTTP(rec, req)
	if got != "" {
		t.Fatalf("non-loopback request received auth: %q", got)
	}

	got = ""
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://localhost/mcp", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Authorization", "Bearer caller-token")
	rec = httptest.NewRecorder()
	trust.Middleware(next).ServeHTTP(rec, req)
	if got != "Bearer caller-token" {
		t.Fatalf("existing authorization overwritten: %q", got)
	}
}
