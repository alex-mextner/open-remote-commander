package authn

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

// LoopbackTrust bridges an outbound private tunnel to the normal bearer-auth
// path without persisting a second credential. It must only wrap a listener
// that is itself bound to loopback.
type LoopbackTrust struct {
	token    string
	verifier Verifier
}
type loopbackTrustVerifier struct {
	base     Verifier
	token    string
	identity Identity
}

func NewLoopbackTrust(base Verifier, subject string) (*LoopbackTrust, error) {
	if base == nil {
		return nil, errors.New("base verifier is required")
	}
	if strings.TrimSpace(subject) == "" {
		return nil, errors.New("trusted subject is required")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	id := Identity{
		Subject: subject,
		Scopes:  map[string]struct{}{"orc:tools": {}},
		Expiry:  time.Now().AddDate(100, 0, 0),
	}
	return &LoopbackTrust{
		token: token,
		verifier: &loopbackTrustVerifier{
			base: base, token: token, identity: id,
		},
	}, nil
}
func (t *LoopbackTrust) Verifier() Verifier { return t.verifier }

func (t *LoopbackTrust) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) == "" && remoteIsLoopback(r.RemoteAddr) {
			r.Header.Set("Authorization", "Bearer "+t.token)
		}
		next.ServeHTTP(w, r)
	})
}

func (v *loopbackTrustVerifier) Verify(ctx context.Context, token string) (Identity, error) {
	if subtle.ConstantTimeCompare([]byte(token), []byte(v.token)) == 1 {
		return v.identity, nil
	}
	return v.base.Verify(ctx, token)
}

func remoteIsLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
