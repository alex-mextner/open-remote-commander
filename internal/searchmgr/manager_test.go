package searchmgr

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestContentSearchPagesResults(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one needle\ntwo needle\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := New(2)
	id, err := m.Start(Query{Path: root, Pattern: "needle", SearchType: "content", MaxResults: 10})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	var all []Match
	for {
		r, err := m.GetMore(id, 1)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, r.Results...)
		if r.Done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("search timeout")
		}
		time.Sleep(time.Millisecond)
	}
	if len(all) != 2 || all[0].Line != 1 || all[1].Line != 2 {
		t.Fatalf("matches=%+v", all)
	}
}

func TestRegexSearch(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte("func HelloWorld() {}\n"), 0600)
	m := New(1)
	id, err := m.Start(Query{Path: root, Pattern: `Hello.*`, Regex: true, SearchType: "content"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		r, _ := m.GetMore(id, 10)
		if r.Done {
			if len(r.Results) != 1 {
				t.Fatalf("results=%+v", r.Results)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(time.Millisecond)
	}
}
