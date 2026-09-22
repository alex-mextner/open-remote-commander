package authn

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrScope        = errors.New("insufficient scope")
)

type Identity struct {
	Subject string
	Scopes  map[string]struct{}
	Expiry  time.Time
}

func (i Identity) HasScope(scope string) bool {
	_, ok := i.Scopes[scope]
	return ok
}

type Verifier interface {
	Verify(context.Context, string) (Identity, error)
}

type Claims struct {
	Subject  string   `json:"sub"`
	Audience string   `json:"aud"`
	Scopes   []string `json:"scope"`
	Expiry   int64    `json:"exp"`
	IssuedAt int64    `json:"iat"`
}

type HMACVerifier struct {
	secret   []byte
	audience string
	now      func() time.Time
}

func NewHMACVerifier(secret []byte, audience string) (*HMACVerifier, error) {
	if len(secret) < 32 {
		return nil, errors.New("HMAC secret must be at least 32 bytes")
	}
	if strings.TrimSpace(audience) == "" {
		return nil, errors.New("audience is required")
	}
	return &HMACVerifier{secret: append([]byte(nil), secret...), audience: audience, now: time.Now}, nil
}

func (v *HMACVerifier) Mint(subject string, scopes []string, ttl time.Duration) (string, error) {
	if strings.TrimSpace(subject) == "" {
		return "", errors.New("subject is required")
	}
	if ttl <= 0 || ttl > 30*24*time.Hour {
		return "", errors.New("ttl must be between 1ns and 30d")
	}
	now := v.now()
	claims := Claims{Subject: subject, Audience: v.audience, Scopes: scopes, Expiry: now.Add(ttl).Unix(), IssuedAt: now.Unix()}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signing := header + "." + payload
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write([]byte(signing))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signing + "." + sig, nil
}

func (v *HMACVerifier) Verify(_ context.Context, token string) (Identity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Identity{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || subtle.ConstantTimeCompare(got, expected) != 1 {
		return Identity{}, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Identity{}, ErrInvalidToken
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Identity{}, ErrInvalidToken
	}
	now := v.now().Unix()
	if c.Subject == "" || c.Audience != v.audience || c.Expiry <= now || c.IssuedAt > now+60 {
		return Identity{}, ErrInvalidToken
	}
	scopes := make(map[string]struct{}, len(c.Scopes))
	for _, s := range c.Scopes {
		if s != "" {
			scopes[s] = struct{}{}
		}
	}
	return Identity{Subject: c.Subject, Scopes: scopes, Expiry: time.Unix(c.Expiry, 0)}, nil
}

type IntrospectionVerifier struct {
	Endpoint     *url.URL
	ClientID     string
	ClientSecret string
	Audience     string
	HTTPClient   *http.Client
	CacheTTL     time.Duration

	mu    sync.Mutex
	cache map[[32]byte]cacheEntry
}

type cacheEntry struct {
	identity Identity
	expires  time.Time
}

type introspectionResponse struct {
	Active bool            `json:"active"`
	Sub    string          `json:"sub"`
	Scope  any             `json:"scope"`
	Exp    json.RawMessage `json:"exp"`
	Aud    any             `json:"aud"`
}

func (v *IntrospectionVerifier) Verify(ctx context.Context, token string) (Identity, error) {
	if token == "" || v.Endpoint == nil || v.Endpoint.Scheme != "https" || v.Audience == "" {
		return Identity{}, ErrInvalidToken
	}
	hash := sha256.Sum256([]byte(token))
	now := time.Now()
	v.mu.Lock()
	if v.cache != nil {
		if e, ok := v.cache[hash]; ok && now.Before(e.expires) && now.Before(e.identity.Expiry) {
			v.mu.Unlock()
			return e.identity, nil
		}
	}
	v.mu.Unlock()

	form := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.Endpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, fmt.Errorf("introspection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if v.ClientID != "" {
		req.SetBasicAuth(v.ClientID, v.ClientSecret)
	}
	client := v.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("introspection: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return Identity{}, ErrInvalidToken
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, 64<<10))
	dec.UseNumber()
	var ir introspectionResponse
	if err := dec.Decode(&ir); err != nil {
		return Identity{}, ErrInvalidToken
	}
	if !ir.Active || ir.Sub == "" || !audienceContains(ir.Aud, v.Audience) {
		return Identity{}, ErrInvalidToken
	}
	exp, err := parseUnix(ir.Exp)
	if err != nil || !exp.After(now) {
		return Identity{}, ErrInvalidToken
	}
	scopes := parseScopes(ir.Scope)
	id := Identity{Subject: ir.Sub, Scopes: scopes, Expiry: exp}
	cacheTTL := v.CacheTTL
	if cacheTTL <= 0 || cacheTTL > 30*time.Second {
		cacheTTL = 15 * time.Second
	}
	cacheExp := now.Add(cacheTTL)
	if exp.Before(cacheExp) {
		cacheExp = exp
	}
	v.mu.Lock()
	if v.cache == nil {
		v.cache = make(map[[32]byte]cacheEntry)
	}
	if len(v.cache) >= 1024 {
		for k, e := range v.cache {
			if now.After(e.expires) {
				delete(v.cache, k)
			}
		}
		if len(v.cache) >= 1024 {
			for k := range v.cache {
				delete(v.cache, k)
				break
			}
		}
	}
	v.cache[hash] = cacheEntry{identity: id, expires: cacheExp}
	v.mu.Unlock()
	return id, nil
}

func parseUnix(raw json.RawMessage) (time.Time, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, errors.New("missing exp")
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		i, err := n.Int64()
		if err == nil {
			return time.Unix(i, 0), nil
		}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		i, err := strconv.ParseInt(s, 10, 64)
		if err == nil {
			return time.Unix(i, 0), nil
		}
	}
	return time.Time{}, errors.New("invalid exp")
}

func audienceContains(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return x == want
	case []any:
		for _, item := range x {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	case []string:
		for _, s := range x {
			if s == want {
				return true
			}
		}
	}
	return false
}

func parseScopes(v any) map[string]struct{} {
	out := map[string]struct{}{}
	switch x := v.(type) {
	case string:
		for _, s := range strings.Fields(x) {
			out[s] = struct{}{}
		}
	case []any:
		for _, item := range x {
			if s, ok := item.(string); ok && s != "" {
				out[s] = struct{}{}
			}
		}
	}
	return out
}

func BearerToken(r *http.Request) (string, bool) {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) < 8 || !strings.EqualFold(h[:7], "Bearer ") {
		return "", false
	}
	t := strings.TrimSpace(h[7:])
	return t, t != ""
}

func Require(ctx context.Context, verifier Verifier, token string, scope string) (Identity, error) {
	id, err := verifier.Verify(ctx, token)
	if err != nil {
		return Identity{}, err
	}
	if scope != "" && !id.HasScope(scope) {
		return Identity{}, ErrScope
	}
	return id, nil
}
