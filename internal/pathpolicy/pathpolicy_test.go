package pathpolicy

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveExistingInsideRoot(t *testing.T) {
	root := t.TempDir()
	p, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := p.ResolveExisting(file)
	if err != nil {
		t.Fatal(err)
	}
	if got != file {
		t.Fatalf("got %q want %q", got, file)
	}
}

func TestResolveExistingRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	p, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.ResolveExisting(filepath.Join(root, "escape", "secret"))
	if !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("got %v, want ErrOutsideRoots", err)
	}
}

func TestResolveForCreateRejectsSymlinkedParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	p, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.ResolveForCreate(filepath.Join(root, "escape", "new.txt"))
	if !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("got %v, want ErrOutsideRoots", err)
	}
}

func FuzzResolveForCreateNoPanic(f *testing.F) {
	f.Add("a/b/c")
	f.Add("../outside")
	f.Fuzz(func(t *testing.T, suffix string) {
		root := t.TempDir()
		p, err := New([]string{root})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = p.ResolveForCreate(filepath.Join(root, suffix))
	})
}
