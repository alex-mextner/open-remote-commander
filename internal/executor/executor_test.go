package executor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/pathpolicy"
	"github.com/alex-mextner/open-remote-commander/internal/processmgr"
	"github.com/alex-mextner/open-remote-commander/internal/protocol"
)

func TestWriteThenReadFile(t *testing.T) {
	root := t.TempDir()
	p, err := pathpolicy.New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	e := New(p, processmgr.New(2, 64<<10, time.Second, false))
	path := filepath.Join(root, "x.txt")
	write, _ := json.Marshal(map[string]any{"path": path, "content": "hello", "encoding": "utf-8"})
	if _, err := e.Execute(context.Background(), "write_file", write); err != nil {
		t.Fatal(err)
	}
	read, _ := json.Marshal(map[string]any{"path": path})
	got, err := e.Execute(context.Background(), "read_file", read)
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["content"] != "hello" {
		t.Fatalf("content=%v", m["content"])
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0077 != 0 {
		t.Fatalf("unexpected permissive mode: %o", st.Mode().Perm())
	}
}

func TestEditBlockRequiresExactReplacementCount(t *testing.T) {
	root := t.TempDir()
	p, err := pathpolicy.New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	e := New(p, processmgr.New(2, 64<<10, time.Second, false))
	path := filepath.Join(root, "edit.txt")
	if err := os.WriteFile(path, []byte("x x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bad, _ := json.Marshal(map[string]any{"file_path": path, "old_string": "x", "new_string": "y", "expected_replacements": 1})
	if _, err := e.Execute(context.Background(), "edit_block", bad); err == nil {
		t.Fatal("ambiguous edit accepted")
	}
	good, _ := json.Marshal(map[string]any{"file_path": path, "old_string": "x", "new_string": "y", "expected_replacements": 2})
	res, err := e.Execute(context.Background(), "edit_block", good)
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["replacements"] != 2 {
		t.Fatalf("result=%v", res)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "y y\n" {
		t.Fatalf("content=%q", b)
	}
}

func TestReadMultipleFilesKeepsPerFileErrors(t *testing.T) {
	root := t.TempDir()
	p, err := pathpolicy.New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	e := New(p, processmgr.New(2, 64<<10, time.Second, false))
	good := filepath.Join(root, "ok.txt")
	_ = os.WriteFile(good, []byte("ok"), 0600)
	raw, _ := json.Marshal(map[string]any{"paths": []string{good, filepath.Join(root, "missing.txt")}, "max_bytes_per_file": 1024})
	res, err := e.Execute(context.Background(), "read_multiple_files", raw)
	if err != nil {
		t.Fatal(err)
	}
	files := res.(map[string]any)["files"].([]map[string]any)
	if len(files) != 2 {
		t.Fatalf("files=%v", files)
	}
	if files[0]["content"] != "ok" {
		t.Fatalf("first=%v", files[0])
	}
	if files[1]["error"] == nil {
		t.Fatalf("missing per-file error: %v", files[1])
	}
}

func TestKillProcessDisabledByDefault(t *testing.T) {
	root := t.TempDir()
	p, _ := pathpolicy.New([]string{root})
	e := New(p, processmgr.New(1, 64<<10, time.Second, false))
	raw, _ := json.Marshal(map[string]any{"pid": 12345})
	if _, err := e.Execute(context.Background(), "kill_process", raw); err == nil {
		t.Fatal("kill_process unexpectedly enabled")
	}
}

func TestGetConfigContainsNoSecrets(t *testing.T) {
	root := t.TempDir()
	p, _ := pathpolicy.New([]string{root})
	e := New(p, processmgr.New(1, 64<<10, time.Second, false))
	res, err := e.Execute(context.Background(), "get_config", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	for _, forbidden := range []string{"token", "secret", "agent_token", "pairing_secret"} {
		if _, ok := m[forbidden]; ok {
			t.Fatalf("config contains forbidden key %q: %#v", forbidden, m)
		}
	}
}

func TestEncodedReadBudgetsFitRelayFrame(t *testing.T) {
	const envelopeHeadroom = 1 << 20
	for name, rawBytes := range map[string]int{
		"single-read": maxReadBytes,
		"multi-read":  maxCombinedReadBytes,
	} {
		encoded := base64.StdEncoding.EncodedLen(rawBytes) + envelopeHeadroom
		if int64(encoded) > protocol.MaxFrameBytes {
			t.Errorf("%s encoded budget=%d exceeds relay frame=%d", name, encoded, protocol.MaxFrameBytes)
		}
	}
}
