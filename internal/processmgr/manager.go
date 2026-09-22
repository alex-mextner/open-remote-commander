package processmgr

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("process session not found")
	ErrLimit    = errors.New("process concurrency limit reached")
)

type StartRequest struct {
	Argv    []string          `json:"argv,omitempty"`
	Command string            `json:"command,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type StartResult struct {
	ProcessID string `json:"process_id"`
	PID       int    `json:"pid"`
}

type ReadResult struct {
	Data       string `json:"data"`
	NextCursor int64  `json:"next_cursor"`
	Truncated  bool   `json:"truncated"`
	Exited     bool   `json:"exited"`
	ExitCode   *int   `json:"exit_code,omitempty"`
}

type Manager struct {
	mu         sync.RWMutex
	procs      map[string]*process
	maxProcs   int
	maxBuffer  int
	maxRuntime time.Duration
	allowShell bool
	active     int
}

type process struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	buf      *ring
	done     chan struct{}
	exitCode *int
	exitedAt time.Time
	cancel   context.CancelFunc
}

type ring struct {
	data []byte
	base int64
	max  int
}

func New(maxProcs, maxBuffer int, maxRuntime time.Duration, allowShell bool) *Manager {
	return &Manager{procs: map[string]*process{}, maxProcs: maxProcs, maxBuffer: maxBuffer, maxRuntime: maxRuntime, allowShell: allowShell}
}

func (m *Manager) Start(req StartRequest) (StartResult, error) {
	m.mu.Lock()
	if m.active >= m.maxProcs {
		m.mu.Unlock()
		return StartResult{}, ErrLimit
	}
	id, err := randomID()
	if err != nil {
		m.mu.Unlock()
		return StartResult{}, err
	}
	ctx := context.Background()
	var cancel context.CancelFunc
	if m.maxRuntime > 0 {
		ctx, cancel = context.WithTimeout(ctx, m.maxRuntime)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	cmd, err := commandFor(ctx, req, m.allowShell)
	if err != nil {
		cancel()
		m.mu.Unlock()
		return StartResult{}, err
	}
	if req.Cwd != "" {
		cmd.Dir = req.Cwd
	}
	if len(req.Env) > 0 {
		keys := make([]string, 0, len(req.Env))
		for k := range req.Env {
			if strings.ContainsAny(k, "=\x00") || len(k) > 256 {
				cancel()
				m.mu.Unlock()
				return StartResult{}, errors.New("invalid environment variable name")
			}
			keys = append(keys, k)
		}
		if len(keys) > 64 {
			cancel()
			m.mu.Unlock()
			return StartResult{}, errors.New("too many environment overrides")
		}
		sort.Strings(keys)
		env := os.Environ()
		for _, k := range keys {
			v := req.Env[k]
			if strings.ContainsRune(v, '\x00') || len(v) > 32<<10 {
				cancel()
				m.mu.Unlock()
				return StartResult{}, errors.New("invalid environment variable value")
			}
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		m.mu.Unlock()
		return StartResult{}, err
	}
	p := &process{cmd: cmd, stdin: stdin, buf: &ring{max: m.maxBuffer}, done: make(chan struct{}), cancel: cancel}
	cmd.Stdout = outputWriter{p: p}
	cmd.Stderr = outputWriter{p: p, prefix: "[stderr] "}
	if err := cmd.Start(); err != nil {
		cancel()
		m.mu.Unlock()
		return StartResult{}, err
	}
	m.procs[id] = p
	m.active++
	m.mu.Unlock()

	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		p.exitCode = &code
		p.exitedAt = time.Now()
		_ = p.stdin.Close()
		close(p.done)
		p.mu.Unlock()
		cancel()

		m.mu.Lock()
		if m.active > 0 {
			m.active--
		}
		m.mu.Unlock()

		// Preserve output briefly so callers can collect the final status, then
		// release the session entry to keep long-running agents bounded.
		time.AfterFunc(10*time.Minute, func() {
			m.mu.Lock()
			if m.procs[id] == p {
				delete(m.procs, id)
			}
			m.mu.Unlock()
		})
	}()
	return StartResult{ProcessID: id, PID: cmd.Process.Pid}, nil
}

func (m *Manager) Read(id string, cursor int64, maxBytes int) (ReadResult, error) {
	p, err := m.get(id)
	if err != nil {
		return ReadResult{}, err
	}
	if maxBytes <= 0 || maxBytes > 256<<10 {
		maxBytes = 64 << 10
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	data, next, truncated := p.buf.read(cursor, maxBytes)
	var exitCopy *int
	if p.exitCode != nil {
		v := *p.exitCode
		exitCopy = &v
	}
	return ReadResult{Data: string(data), NextCursor: next, Truncated: truncated, Exited: p.exitCode != nil, ExitCode: exitCopy}, nil
}

func (m *Manager) Write(id string, data []byte) error {
	if len(data) > 64<<10 {
		return errors.New("stdin chunk exceeds 64KiB")
	}
	p, err := m.get(id)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exitCode != nil {
		return errors.New("process already exited")
	}
	_, err = p.stdin.Write(data)
	return err
}

func (m *Manager) Terminate(id string) error {
	p, err := m.get(id)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.exitCode != nil {
		return nil
	}
	p.cancel()
	if p.cmd.Process != nil {
		return p.cmd.Process.Kill()
	}
	return nil
}

func (m *Manager) CleanupExited(olderThan time.Duration) {
	cutoff := time.Now().Add(-olderThan)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.procs {
		p.mu.Lock()
		exited := p.exitCode != nil
		exitedAt := p.exitedAt
		p.mu.Unlock()
		if exited && !exitedAt.IsZero() && !exitedAt.After(cutoff) {
			delete(m.procs, id)
		}
	}
}

func (m *Manager) get(id string) (*process, error) {
	m.mu.RLock()
	p := m.procs[id]
	m.mu.RUnlock()
	if p == nil {
		return nil, ErrNotFound
	}
	return p, nil
}

type outputWriter struct {
	p      *process
	prefix string
}

func (w outputWriter) Write(b []byte) (int, error) {
	w.p.mu.Lock()
	defer w.p.mu.Unlock()
	if w.prefix != "" && len(b) > 0 {
		w.p.buf.write([]byte(w.prefix))
	}
	w.p.buf.write(b)
	return len(b), nil
}

func (r *ring) write(p []byte) {
	if r.max <= 0 {
		return
	}
	if len(p) >= r.max {
		r.base += int64(len(r.data) + len(p) - r.max)
		r.data = append(r.data[:0], p[len(p)-r.max:]...)
		return
	}
	r.data = append(r.data, p...)
	if len(r.data) > r.max {
		drop := len(r.data) - r.max
		copy(r.data, r.data[drop:])
		r.data = r.data[:r.max]
		r.base += int64(drop)
	}
}

func (r *ring) read(cursor int64, max int) ([]byte, int64, bool) {
	truncated := cursor < r.base
	if cursor < r.base {
		cursor = r.base
	}
	end := r.base + int64(len(r.data))
	if cursor > end {
		cursor = end
	}
	startIdx := int(cursor - r.base)
	available := len(r.data) - startIdx
	if available > max {
		available = max
	}
	out := bytes.Clone(r.data[startIdx : startIdx+available])
	return out, cursor + int64(available), truncated
}

func commandFor(ctx context.Context, req StartRequest, allowShell bool) (*exec.Cmd, error) {
	if len(req.Argv) > 0 {
		if len(req.Argv) > 128 {
			return nil, errors.New("too many argv entries")
		}
		for _, a := range req.Argv {
			if strings.ContainsRune(a, '\x00') || len(a) > 64<<10 {
				return nil, errors.New("invalid argv")
			}
		}
		return exec.CommandContext(ctx, req.Argv[0], req.Argv[1:]...), nil // #nosec G204 -- arbitrary local execution is the tool's explicit purpose
	}
	if strings.TrimSpace(req.Command) == "" {
		return nil, errors.New("argv or command is required")
	}
	if !allowShell {
		return nil, errors.New("shell commands are disabled; use argv")
	}
	if len(req.Command) > 128<<10 {
		return nil, errors.New("command exceeds 128KiB")
	}
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd.exe", "/D", "/S", "/C", req.Command), nil // #nosec G204 -- explicit privileged shell tool
	}
	return exec.CommandContext(ctx, "/bin/sh", "-lc", req.Command), nil // #nosec G204 -- explicit privileged shell tool
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (m *Manager) String() string { return fmt.Sprintf("process manager (%d max)", m.maxProcs) }

type SessionSummary struct {
	ProcessID string `json:"process_id"`
	PID       int    `json:"pid"`
	Exited    bool   `json:"exited"`
	ExitCode  *int   `json:"exit_code,omitempty"`
}

func (m *Manager) List() []SessionSummary {
	m.mu.RLock()
	pairs := make([]struct {
		id string
		p  *process
	}, 0, len(m.procs))
	for id, p := range m.procs {
		pairs = append(pairs, struct {
			id string
			p  *process
		}{id, p})
	}
	m.mu.RUnlock()
	out := make([]SessionSummary, 0, len(pairs))
	for _, pair := range pairs {
		pair.p.mu.Lock()
		var exitCopy *int
		if pair.p.exitCode != nil {
			v := *pair.p.exitCode
			exitCopy = &v
		}
		pid := 0
		if pair.p.cmd != nil && pair.p.cmd.Process != nil {
			pid = pair.p.cmd.Process.Pid
		}
		out = append(out, SessionSummary{ProcessID: pair.id, PID: pid, Exited: pair.p.exitCode != nil, ExitCode: exitCopy})
		pair.p.mu.Unlock()
	}
	return out
}

func (m *Manager) Config() map[string]any {
	return map[string]any{"max_processes": m.maxProcs, "max_buffer_bytes": m.maxBuffer, "max_runtime": m.maxRuntime.String(), "shell_enabled": m.allowShell}
}
