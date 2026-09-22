package authn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestHMACVerifier(t *testing.T) {
	v, err := NewHMACVerifier([]byte("01234567890123456789012345678901"), "https://example.test/mcp")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := v.Mint("alice", []string{"orc:tools"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	id, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "alice" || !id.HasScope("orc:tools") {
		t.Fatalf("identity=%+v", id)
	}
	other, _ := NewHMACVerifier([]byte("01234567890123456789012345678901"), "https://other.test/mcp")
	if _, err := other.Verify(context.Background(), tok); err == nil {
		t.Fatal("audience mismatch accepted")
	}
}

func TestIntrospectionVerifier(t *testing.T) {
	exp := time.Now().Add(time.Minute).Unix()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("token") != "good" {
			_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "sub": "alice", "scope": "orc:tools", "exp": exp, "aud": []string{"https://example.test/mcp"}})
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	v := &IntrospectionVerifier{Endpoint: u, Audience: "https://example.test/mcp", HTTPClient: ts.Client()}
	id, err := v.Verify(context.Background(), "good")
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "alice" || !id.HasScope("orc:tools") {
		t.Fatalf("id=%+v", id)
	}
	v.Audience = "https://wrong.test/mcp"
	v.cache = nil
	if _, err := v.Verify(context.Background(), "good"); err == nil {
		t.Fatal("audience mismatch accepted")
	}
}
