package config

import (
	"strings"
	"testing"
)

func setValidServerEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ORC_PAIRING_SECRET", strings.Repeat("p", 32))
	t.Setenv("ORC_HMAC_SECRET", strings.Repeat("h", 32))
	t.Setenv("ORC_AUTH_MODE", "dev")
}

func TestLoadServerRejectsTrustedMCPWhenListenerIsNotLoopback(t *testing.T) {
	setValidServerEnv(t)
	t.Setenv("ORC_MCP_TRUST_LOOPBACK", "true")
	t.Setenv("ORC_MCP_TRUSTED_SUBJECT", "alex")
	t.Setenv("ORC_LISTEN_ADDR", "0.0.0.0:8080")
	t.Setenv("ORC_PUBLIC_BASE_URL", "https://orc.example.com")
	if _, err := LoadServer(); err == nil {
		t.Fatal("LoadServer accepted trusted MCP on non-loopback listener")
	}
}
func TestLoadServerAllowsTrustedMCPOnLoopback(t *testing.T) {
	setValidServerEnv(t)
	t.Setenv("ORC_MCP_TRUST_LOOPBACK", "true")
	t.Setenv("ORC_MCP_TRUSTED_SUBJECT", "alex")
	t.Setenv("ORC_LISTEN_ADDR", "127.0.0.1:8080")
	t.Setenv("ORC_PUBLIC_BASE_URL", "http://127.0.0.1:8080")
	cfg, err := LoadServer()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MCPTrustLoopback || cfg.MCPTrustedSubject != "alex" {
		t.Fatalf("trusted config not loaded: %+v", cfg)
	}
}
