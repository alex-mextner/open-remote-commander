package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/protocol"
	"github.com/alex-mextner/open-remote-commander/internal/store"
	"github.com/alex-mextner/open-remote-commander/internal/ws"
)

func TestHubCallRoundTrip(t *testing.T) {
	serverConn := make(chan *ws.Conn, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Upgrade(w, r, protocol.MaxFrameBytes)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		serverConn <- c
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	client, err := ws.Dial(context.Background(), wsURL, "", protocol.MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-serverConn

	h := NewHub(2)
	h.Register(store.Device{ID: "d1", OwnerSubject: "alice", Name: "test"}, server)

	done := make(chan error, 1)
	go func() {
		b, err := client.ReadMessage()
		if err != nil {
			done <- err
			return
		}
		var f protocol.Frame
		if err := json.Unmarshal(b, &f); err != nil {
			done <- err
			return
		}
		if f.Type != protocol.TypeCall || f.Tool != "ping" {
			done <- &unexpectedFrame{f}
			return
		}
		out, _ := json.Marshal(map[string]any{"ok": true})
		payload, _ := json.Marshal(protocol.Frame{Type: protocol.TypeResult, ID: f.ID, OK: true, Result: out})
		done <- client.WriteMessage(payload)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := h.Call(ctx, "alice", "d1", "ping", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var got map[string]bool
	if err := json.Unmarshal(res.Value, &got); err != nil {
		t.Fatal(err)
	}
	if !got["ok"] {
		t.Fatalf("result=%s", res.Value)
	}
	if _, err := h.Call(ctx, "bob", "d1", "ping", map[string]any{}); !errors.Is(err, ErrOffline) {
		t.Fatalf("cross-owner error=%v", err)
	}
}

type unexpectedFrame struct{ f protocol.Frame }

func (e *unexpectedFrame) Error() string {
	return "unexpected relay frame: " + e.f.Type + "/" + e.f.Tool
}
