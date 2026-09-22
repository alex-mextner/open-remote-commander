package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/authn"
	"github.com/alex-mextner/open-remote-commander/internal/protocol"
	"github.com/alex-mextner/open-remote-commander/internal/relay"
	"github.com/alex-mextner/open-remote-commander/internal/store"
	"github.com/alex-mextner/open-remote-commander/internal/ws"
)

func TestAgentConnectAcceptsProtocolFrameLimit(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateDevice(ctx, store.Device{ID: "d1", OwnerSubject: "alice", Name: "test"}, "device-token"); err != nil {
		t.Fatal(err)
	}
	hub := relay.NewHub(2)
	verifier, err := authn.NewHMACVerifier([]byte("01234567890123456789012345678901"), "aud")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(st, hub, verifier, "http://example.test", nil, slog.New(slog.NewTextHandler(io.Discard, nil)), []byte("pairing-secret-01234567890123456789")).Register(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	raw, err := json.Marshal(strings.Repeat("x", 5<<20))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(protocol.Frame{Type: protocol.TypePing, ID: "large", Args: raw})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= 4<<20 {
		t.Fatalf("test payload=%d does not exceed legacy frame limit", len(payload))
	}

	client, err := ws.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/agent/v1/connect?device_id=d1", "device-token", protocol.MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := client.WriteMessage(payload); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	reply, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var pong protocol.Frame
	if err := json.Unmarshal(reply, &pong); err != nil {
		t.Fatal(err)
	}
	if pong.Type != protocol.TypePong || pong.ID != "large" {
		t.Fatalf("unexpected pong: %+v", pong)
	}
}
