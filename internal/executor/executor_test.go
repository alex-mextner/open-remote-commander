package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/pathpolicy"
	"github.com/alex-mextner/open-remote-commander/internal/processmgr"
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
