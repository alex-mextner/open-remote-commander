package pathpolicy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrOutsideRoots = errors.New("path is outside allowed roots")

type Policy struct {
	roots []string
}

func New(roots []string) (*Policy, error) {
	if len(roots) == 0 {
		return nil, errors.New("at least one allowed root is required")
	}
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("absolute root %q: %w", root, err)
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("resolve root %q: %w", root, err)
		}
		st, err := os.Stat(resolved)
		if err != nil || !st.IsDir() {
			return nil, fmt.Errorf("allowed root %q is not a directory", root)
		}
		out = append(out, filepath.Clean(resolved))
	}
	return &Policy{roots: dedupe(out)}, nil
}

func (p *Policy) Roots() []string { return append([]string(nil), p.roots...) }

// ResolveExisting resolves symlinks and rejects a target outside every root.
func (p *Policy) ResolveExisting(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	resolved = filepath.Clean(resolved)
	if !p.allowed(resolved) {
		return "", ErrOutsideRoots
	}
	return resolved, nil
}

// ResolveForCreate resolves the nearest existing ancestor and reconstructs the
// missing suffix. This prevents a pre-existing symlink in a parent component
// from escaping an allowed root. Callers still need to prefer atomic writes;
// no portable userspace check can eliminate every concurrent symlink race.
func (p *Policy) ResolveForCreate(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	cur := abs
	var suffix []string
	for {
		_, err := os.Lstat(cur)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", ErrOutsideRoots
		}
		suffix = append(suffix, filepath.Base(cur))
		cur = parent
	}
	resolved, err := filepath.EvalSymlinks(cur)
	if err != nil {
		return "", err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, suffix[i])
	}
	resolved = filepath.Clean(resolved)
	if !p.allowed(resolved) {
		return "", ErrOutsideRoots
	}
	return resolved, nil
}

func (p *Policy) allowed(path string) bool {
	for _, root := range p.roots {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
