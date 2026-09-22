package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/protocol"
	"github.com/alex-mextner/open-remote-commander/internal/store"
	"github.com/alex-mextner/open-remote-commander/internal/ws"
)

func TestHubCallLargeResult(t *testing.T) {
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

	client, err := ws.Dial(context.Background(), "ws"+strings.TrimPrefix(ts.URL, "http"), "", protocol.MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	h := NewHub(2)
	h.Register(store.Device{ID: "d1", OwnerSubject: "alice", Name: "test"}, <-serverConn)

	done := make(chan error, 1)
	go func() {
		b, err := client.ReadMessage()
		if err != nil {
			done <- err
			return
		}
		var call protocol.Frame
		if err := json.Unmarshal(b, &call); err != nil {
			done <- err
			return
		}
		raw, err := json.Marshal(map[string]string{"data": strings.Repeat("x", 9<<20)})
		if err != nil {
			done <- err
			return
		}
		payload, err := json.Marshal(protocol.Frame{Type: protocol.TypeResult, ID: call.ID, OK: true, Result: raw})
		if err != nil {
			done <- err
			return
		}
		done <- client.WriteMessage(payload)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := h.Call(ctx, "alice", "d1", "read_multiple_files", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(res.Value, &got); err != nil {
		t.Fatal(err)
	}
	if len(got["data"]) != 9<<20 {
		t.Fatalf("large result bytes=%d want=%d", len(got["data"]), 9<<20)
	}
}
