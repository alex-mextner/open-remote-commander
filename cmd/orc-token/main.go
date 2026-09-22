package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/authn"
)

func main() {
	sub := flag.String("sub", "dev-user", "token subject")
	scope := flag.String("scope", "orc:tools", "space- or comma-separated scopes")
	ttl := flag.Duration("ttl", time.Hour, "token lifetime")
	audience := flag.String("audience", env("ORC_AUTH_AUDIENCE", "http://127.0.0.1:8080/mcp"), "resource audience")
	flag.Parse()
	secret := os.Getenv("ORC_HMAC_SECRET")
	v, err := authn.NewHMACVerifier([]byte(secret), *audience)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	scopes := strings.Fields(strings.ReplaceAll(*scope, ",", " "))
	token, err := v.Mint(*sub, scopes, *ttl)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println(token)
}

func env(k, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return fallback
}
