package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/executor"
	"github.com/alex-mextner/open-remote-commander/internal/pathpolicy"
	"github.com/alex-mextner/open-remote-commander/internal/processmgr"
	"github.com/alex-mextner/open-remote-commander/internal/protocol"
	"github.com/alex-mextner/open-remote-commander/internal/ws"
)

func TestHandleCallWritesLargeMultiReadResult(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, "a.bin"), filepath.Join(root, "b.bin")}
	for _, path := range paths {
		data := make([]byte, 3<<20)
		if _, err := rand.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := pathpolicy.New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	exec := executor.New(policy, processmgr.New(2, 1<<20, time.Minute, false))
	serverConn := make(chan *ws.Conn, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := ws.Upgrade(w, r, protocol.MaxFrameBytes)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		serverConn <- conn
	}))
	defer ts.Close()
	client, err := ws.Dial(context.Background(), "ws"+strings.TrimPrefix(ts.URL, "http"), "", protocol.MaxFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-serverConn
	defer server.Close()

	args, err := json.Marshal(map[string]any{"paths": paths, "max_bytes_per_file": 3 << 20})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	done := make(chan struct{})
	go func() {
		handleCall(context.Background(), server, exec, protocol.Frame{
			Type: protocol.TypeCall, ID: "large", Tool: "read_multiple_files", Args: args,
		}, logger)
		close(done)
	}()
	_ = client.SetReadDeadline(time.Now().Add(10 * time.Second))
	payload, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(payload)) >= protocol.MaxFrameBytes {
		t.Fatalf("payload=%d exceeds frame=%d", len(payload), protocol.MaxFrameBytes)
	}
	var result protocol.Frame
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Error != nil {
		t.Fatalf("remote result failed: %+v", result.Error)
	}
	var files map[string]any
	if err := json.Unmarshal(result.Result, &files); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleCall did not finish after result was read")
	}
	t.Logf("large agent result payload=%d bytes", len(payload))
}
