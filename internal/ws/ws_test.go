package ws

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLargeMessageRoundTrip(t *testing.T) {
	const frameLimit = int64(16 << 20)
	const size = 9 << 20
	got := make(chan []byte, 1)
	errs := make(chan error, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Upgrade(w, r, frameLimit)
		if err != nil {
			errs <- err
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		msg, err := conn.ReadMessage()
		if err != nil {
			errs <- err
			return
		}
		got <- msg
	}))
	defer ts.Close()
	client, err := Dial(context.Background(), "ws"+strings.TrimPrefix(ts.URL, "http"), "", frameLimit)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	payload := bytes.Repeat([]byte("x"), size)
	_ = client.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := client.WriteMessage(payload); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errs:
		t.Fatal(err)
	case msg := <-got:
		if !bytes.Equal(msg, payload) {
			t.Fatalf("payload mismatch: got=%d want=%d", len(msg), len(payload))
		}
	case <-time.After(12 * time.Second):
		t.Fatal("timed out waiting for large websocket message")
	}
}
