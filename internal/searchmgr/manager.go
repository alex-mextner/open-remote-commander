package searchmgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrNotFound = errors.New("search session not found")
	ErrLimit    = errors.New("too many active search sessions")
)

type Query struct {
	Path          string
	Pattern       string
	SearchType    string
	Regex         bool
	CaseSensitive bool
	IncludeHidden bool
	MaxResults    int
	MaxFileBytes  int64
}

type Match struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Preview string `json:"preview,omitempty"`
	Kind    string `json:"kind"`
}

type MoreResult struct {
	Results    []Match `json:"results"`
	Done       bool    `json:"done"`
	Error      string  `json:"error,omitempty"`
	TotalFound int     `json:"total_found"`
}

type Summary struct {
	SessionID  string `json:"session_id"`
	Done       bool   `json:"done"`
	Buffered   int    `json:"buffered"`
	Returned   int    `json:"returned"`
	TotalFound int    `json:"total_found"`
}

type Manager struct {
	mu          sync.RWMutex
	sessions    map[string]*session
	maxSessions int
}

type session struct {
	mu      sync.Mutex
	results []Match
	next    int
	done    bool
	err     string
	cancel  context.CancelFunc
}

func New(maxSessions int) *Manager {
	if maxSessions <= 0 {
		maxSessions = 8
	}
	return &Manager{sessions: make(map[string]*session), maxSessions: maxSessions}
}

func (m *Manager) Start(q Query) (string, error) {
	q.Path = filepath.Clean(q.Path)
	q.Pattern = strings.TrimSpace(q.Pattern)
	if q.Pattern == "" {
		return "", errors.New("search pattern is required")
	}
	if q.SearchType == "" {
		q.SearchType = "content"
	}
	if q.SearchType != "content" && q.SearchType != "files" {
		return "", errors.New("search_type must be content or files")
	}
	if q.MaxResults <= 0 {
		q.MaxResults = 1000
	}
	if q.MaxResults > 5000 {
		q.MaxResults = 5000
	}
	if q.MaxFileBytes <= 0 {
		q.MaxFileBytes = 2 << 20
	}
	if q.MaxFileBytes > 16<<20 {
		q.MaxFileBytes = 16 << 20
	}

	matcher, err := newMatcher(q.Pattern, q.Regex, q.CaseSensitive)
	if err != nil {
		return "", err
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	s := &session{cancel: cancel}

	m.mu.Lock()
	active := 0
	for _, existing := range m.sessions {
		existing.mu.Lock()
		if !existing.done {
			active++
		}
		existing.mu.Unlock()
	}
	if active >= m.maxSessions {
		m.mu.Unlock()
		cancel()
		return "", ErrLimit
	}
	m.sessions[id] = s
	m.mu.Unlock()

	go m.run(ctx, s, q, matcher)
	time.AfterFunc(15*time.Minute, func() {
		m.mu.Lock()
		if m.sessions[id] == s {
			delete(m.sessions, id)
		}
		m.mu.Unlock()
		cancel()
	})
	return id, nil
}

func (m *Manager) GetMore(id string, limit int) (MoreResult, error) {
	s := m.lookup(id)
	if s == nil {
		return MoreResult{}, ErrNotFound
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	end := s.next + limit
	if end > len(s.results) {
		end = len(s.results)
	}
	out := append([]Match(nil), s.results[s.next:end]...)
	s.next = end
	return MoreResult{Results: out, Done: s.done && s.next >= len(s.results), Error: s.err, TotalFound: len(s.results)}, nil
}

func (m *Manager) Stop(id string) error {
	s := m.lookup(id)
	if s == nil {
		return ErrNotFound
	}
	s.cancel()
	return nil
}

func (m *Manager) List() []Summary {
	m.mu.RLock()
	pairs := make([]struct {
		id string
		s  *session
	}, 0, len(m.sessions))
	for id, s := range m.sessions {
		pairs = append(pairs, struct {
			id string
			s  *session
		}{id, s})
	}
	m.mu.RUnlock()
	out := make([]Summary, 0, len(pairs))
	for _, p := range pairs {
		p.s.mu.Lock()
		out = append(out, Summary{SessionID: p.id, Done: p.s.done, Buffered: len(p.s.results) - p.s.next, Returned: p.s.next, TotalFound: len(p.s.results)})
		p.s.mu.Unlock()
	}
	return out
}

func (m *Manager) lookup(id string) *session {
	m.mu.RLock()
	s := m.sessions[id]
	m.mu.RUnlock()
	return s
}

func (m *Manager) run(ctx context.Context, s *session, q Query, match matcher) {
	defer func() { s.mu.Lock(); s.done = true; s.mu.Unlock() }()
	err := filepath.WalkDir(q.Path, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if path != q.Path && !q.IncludeHidden && isHidden(filepath.Base(path)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if s.count() >= q.MaxResults {
			return errEnough
		}
		if q.SearchType == "files" {
			rel, _ := filepath.Rel(q.Path, path)
			idx := match.index(rel)
			if idx >= 0 {
				s.add(Match{Path: path, Column: idx + 1, Kind: "file"})
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > q.MaxFileBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || int64(len(data)) > q.MaxFileBytes || !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			idx := match.index(line)
			if idx < 0 {
				continue
			}
			preview := line
			if len(preview) > 1000 {
				preview = preview[:1000]
			}
			s.add(Match{Path: path, Line: i + 1, Column: idx + 1, Preview: preview, Kind: "content"})
			if s.count() >= q.MaxResults {
				return errEnough
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errEnough) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		s.mu.Lock()
		s.err = err.Error()
		s.mu.Unlock()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		s.mu.Lock()
		s.err = "search timed out"
		s.mu.Unlock()
	}
}

func (s *session) add(v Match) { s.mu.Lock(); s.results = append(s.results, v); s.mu.Unlock() }
func (s *session) count() int  { s.mu.Lock(); defer s.mu.Unlock(); return len(s.results) }

var errEnough = errors.New("result limit reached")

type matcher interface{ index(string) int }
type literalMatcher struct {
	needle        string
	caseSensitive bool
}

func (m literalMatcher) index(s string) int {
	if !m.caseSensitive {
		s = strings.ToLower(s)
	}
	return strings.Index(s, m.needle)
}

type regexMatcher struct{ re *regexp.Regexp }

func (m regexMatcher) index(s string) int {
	loc := m.re.FindStringIndex(s)
	if loc == nil {
		return -1
	}
	return loc[0]
}
func newMatcher(pattern string, regexMode, caseSensitive bool) (matcher, error) {
	if regexMode {
		p := pattern
		if !caseSensitive {
			p = "(?i)" + p
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		return regexMatcher{re}, nil
	}
	if !caseSensitive {
		pattern = strings.ToLower(pattern)
	}
	return literalMatcher{needle: pattern, caseSensitive: caseSensitive}, nil
}
func isHidden(name string) bool { return len(name) > 1 && name[0] == '.' }
func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "search_" + hex.EncodeToString(b), nil
}
