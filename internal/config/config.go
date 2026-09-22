package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	ListenAddr           string
	PublicBaseURL        string
	AuthMode             string
	AuthAudience         string
	AuthIssuer           string
	HMACSecret           string
	PairingSecret        string
	IntrospectionURL     string
	IntrospectionID      string
	IntrospectionSecret  string
	DatabaseURL          string
	NeonHTTPEndpoint     string
	AllowedOrigins       []string
	MaxInFlightPerDevice int
	MCPTrustLoopback     bool
	MCPTrustedSubject    string
}

type Agent struct {
	ServerURL        string
	DeviceID         string
	AgentToken       string
	DeviceName       string
	AllowedRoots     []string
	MaxProcesses     int
	MaxProcessBytes  int
	MaxRuntime       time.Duration
	AllowShell       bool
	AllowKillProcess bool
}

func LoadServer() (Server, error) {
	c := Server{
		ListenAddr:           env("ORC_LISTEN_ADDR", "127.0.0.1:8080"),
		PublicBaseURL:        strings.TrimRight(env("ORC_PUBLIC_BASE_URL", "http://127.0.0.1:8080"), "/"),
		AuthMode:             env("ORC_AUTH_MODE", "dev"),
		AuthAudience:         env("ORC_AUTH_AUDIENCE", "http://127.0.0.1:8080/mcp"),
		AuthIssuer:           strings.TrimRight(os.Getenv("ORC_AUTH_ISSUER"), "/"),
		HMACSecret:           os.Getenv("ORC_HMAC_SECRET"),
		PairingSecret:        os.Getenv("ORC_PAIRING_SECRET"),
		IntrospectionURL:     os.Getenv("ORC_INTROSPECTION_URL"),
		IntrospectionID:      os.Getenv("ORC_INTROSPECTION_CLIENT_ID"),
		IntrospectionSecret:  os.Getenv("ORC_INTROSPECTION_CLIENT_SECRET"),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		NeonHTTPEndpoint:     os.Getenv("ORC_NEON_HTTP_ENDPOINT"),
		AllowedOrigins:       splitCSV(os.Getenv("ORC_ALLOWED_ORIGINS")),
		MaxInFlightPerDevice: envInt("ORC_MAX_IN_FLIGHT_PER_DEVICE", 16),
		MCPTrustLoopback:     envBool("ORC_MCP_TRUST_LOOPBACK", false),
		MCPTrustedSubject:    strings.TrimSpace(os.Getenv("ORC_MCP_TRUSTED_SUBJECT")),
	}
	if c.ListenAddr == "" {
		return Server{}, errors.New("listen address is required")
	}
	u, err := url.Parse(c.PublicBaseURL)
	if err != nil || u.Host == "" {
		return Server{}, errors.New("invalid ORC_PUBLIC_BASE_URL")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u.Hostname())) {
		return Server{}, errors.New("ORC_PUBLIC_BASE_URL must use https outside loopback")
	}
	if c.MaxInFlightPerDevice < 1 || c.MaxInFlightPerDevice > 128 {
		return Server{}, errors.New("ORC_MAX_IN_FLIGHT_PER_DEVICE must be between 1 and 128")
	}
	if len(c.PairingSecret) < 32 {
		return Server{}, errors.New("ORC_PAIRING_SECRET must be at least 32 bytes")
	}
	if c.MCPTrustLoopback {
		if c.MCPTrustedSubject == "" {
			return Server{}, errors.New("ORC_MCP_TRUSTED_SUBJECT is required when ORC_MCP_TRUST_LOOPBACK=true")
		}
		host, _, err := net.SplitHostPort(c.ListenAddr)
		if err != nil || !isLoopback(host) {
			return Server{}, errors.New("ORC_MCP_TRUST_LOOPBACK requires an explicit loopback ORC_LISTEN_ADDR")
		}
	}
	switch c.AuthMode {
	case "dev":
		if len(c.HMACSecret) < 32 {
			return Server{}, errors.New("ORC_HMAC_SECRET must be at least 32 bytes in dev auth mode")
		}
	case "introspection":
		iu, err := url.Parse(c.IntrospectionURL)
		if err != nil || iu.Scheme != "https" || iu.Host == "" {
			return Server{}, errors.New("ORC_INTROSPECTION_URL must be an https URL")
		}
		if c.AuthIssuer == "" {
			return Server{}, errors.New("ORC_AUTH_ISSUER is required in introspection mode")
		}
	default:
		return Server{}, fmt.Errorf("unsupported ORC_AUTH_MODE %q", c.AuthMode)
	}
	return c, nil
}

func LoadAgent() (Agent, error) {
	roots := splitPathList(os.Getenv("ORC_ALLOWED_ROOTS"))
	if len(roots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return Agent{}, errors.New("ORC_ALLOWED_ROOTS is required when home cannot be resolved")
		}
		roots = []string{home}
	}
	for i, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return Agent{}, fmt.Errorf("allowed root %q: %w", root, err)
		}
		roots[i] = abs
	}
	c := Agent{
		ServerURL:        strings.TrimRight(env("ORC_SERVER_URL", "http://127.0.0.1:8080"), "/"),
		DeviceID:         os.Getenv("ORC_DEVICE_ID"),
		AgentToken:       os.Getenv("ORC_AGENT_TOKEN"),
		DeviceName:       env("ORC_DEVICE_NAME", hostname()),
		AllowedRoots:     roots,
		MaxProcesses:     envInt("ORC_MAX_PROCESSES", 8),
		MaxProcessBytes:  envInt("ORC_MAX_PROCESS_BUFFER_BYTES", 1<<20),
		MaxRuntime:       envDuration("ORC_MAX_PROCESS_RUNTIME", time.Hour),
		AllowShell:       envBool("ORC_ALLOW_SHELL", false),
		AllowKillProcess: envBool("ORC_ALLOW_KILL_PROCESS", false),
	}
	u, err := url.Parse(c.ServerURL)
	if err != nil || u.Host == "" {
		return Agent{}, errors.New("invalid ORC_SERVER_URL")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || !isLoopback(u.Hostname())) {
		return Agent{}, errors.New("ORC_SERVER_URL must use https outside loopback")
	}
	if c.MaxProcesses < 1 || c.MaxProcesses > 64 {
		return Agent{}, errors.New("ORC_MAX_PROCESSES must be between 1 and 64")
	}
	if c.MaxProcessBytes < 64<<10 || c.MaxProcessBytes > 32<<20 {
		return Agent{}, errors.New("ORC_MAX_PROCESS_BUFFER_BYTES must be between 64KiB and 32MiB")
	}
	return c, nil
}

func env(k, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return fallback
}

func envInt(k string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envDuration(k string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func envBool(k string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(k)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitPathList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	sep := string(os.PathListSeparator)
	if runtime.GOOS == "windows" && strings.Contains(v, ",") && !strings.Contains(v, sep) {
		sep = ","
	}
	var out []string
	for _, p := range strings.Split(v, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unnamed-device"
	}
	return h
}
