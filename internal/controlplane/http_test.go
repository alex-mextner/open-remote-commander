package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/authn"
	"github.com/alex-mextner/open-remote-commander/internal/relay"
	"github.com/alex-mextner/open-remote-commander/internal/store"
	apiv1 "github.com/alex-mextner/open-remote-commander/pkg/api/v1"
)

func TestPairingHTTPFlow(t *testing.T) {
	st := store.NewMemory()
	hub := relay.NewHub(2)
	v, err := authn.NewHMACVerifier([]byte("01234567890123456789012345678901"), "aud")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(st, hub, v, "http://example.test", nil, slog.New(slog.NewTextHandler(io.Discard, nil)), []byte("pairing-secret-01234567890123456789")).Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	startBody, _ := json.Marshal(apiv1.PairingStartRequest{DeviceName: "Laptop"})
	resp, err := http.Post(ts.URL+"/api/v1/pairings", "application/json", bytes.NewReader(startBody))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("start status=%d", resp.StatusCode)
	}
	var start apiv1.PairingStartResponse
	if err := json.NewDecoder(resp.Body).Decode(&start); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	token, _ := v.Mint("alice", []string{"orc:tools"}, time.Minute)
	approveBody, _ := json.Marshal(apiv1.PairingApproveRequest{UserCode: start.UserCode})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/pairings/approve", bytes.NewReader(approveBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("approve status=%d", resp.StatusCode)
	}
	resp.Body.Close()

	pollBody, _ := json.Marshal(apiv1.PairingTokenRequest{DeviceCode: start.DeviceCode})
	resp, err = http.Post(ts.URL+"/api/v1/pairings/token", "application/json", bytes.NewReader(pollBody))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("token status=%d", resp.StatusCode)
	}
	var tok apiv1.PairingTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if tok.DeviceID == "" || tok.AccessToken == "" {
		t.Fatalf("token response=%+v", tok)
	}
	if _, err := st.AuthenticateDevice(context.Background(), string(tok.DeviceID), tok.AccessToken); err != nil {
		t.Fatal(err)
	}
}
