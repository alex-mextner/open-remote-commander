package osproc

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxProcessOutput = 4 << 20

type Process struct {
	PID     int    `json:"pid"`
	PPID    int    `json:"ppid,omitempty"`
	Command string `json:"command"`
	Args    string `json:"args,omitempty"`
}

func List(ctx context.Context, max int) ([]Process, error) {
	if max <= 0 {
		max = 1000
	}
	if max > 5000 {
		max = 5000
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if runtime.GOOS == "windows" {
		return listWindows(ctx, max)
	}
	cmd := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,comm=,args=")
	out, err := limitedOutput(cmd)
	if err != nil {
		return nil, err
	}
	var result []Process
	s := bufio.NewScanner(strings.NewReader(string(out)))
	s.Buffer(make([]byte, 64<<10), 1<<20)
	for s.Scan() && len(result) < max {
		fields := strings.Fields(s.Text())
		if len(fields) < 3 {
			continue
		}
		pid, e1 := strconv.Atoi(fields[0])
		ppid, e2 := strconv.Atoi(fields[1])
		if e1 != nil || e2 != nil {
			continue
		}
		command := fields[2]
		args := ""
		if len(fields) > 3 {
			args = strings.Join(fields[3:], " ")
		}
		result = append(result, Process{PID: pid, PPID: ppid, Command: command, Args: args})
	}
	return result, s.Err()
}

func listWindows(ctx context.Context, max int) ([]Process, error) {
	cmd := exec.CommandContext(ctx, "tasklist", "/FO", "CSV", "/NH")
	out, err := limitedOutput(cmd)
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(strings.NewReader(string(out)))
	var result []Process
	for len(result) < max {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rec) < 2 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(rec[1]))
		if err != nil {
			continue
		}
		result = append(result, Process{PID: pid, Command: rec[0]})
	}
	return result, nil
}

func Kill(pid int) error {
	if pid <= 0 {
		return errors.New("pid must be positive")
	}
	if pid == os.Getpid() {
		return errors.New("refusing to kill the agent process")
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return p.Kill()
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal process %d: %w", pid, err)
	}
	return nil
}

func limitedOutput(cmd *exec.Cmd) ([]byte, error) {
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(pipe, maxProcessOutput+1))
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if len(b) > maxProcessOutput {
		return nil, errors.New("process list output exceeds limit")
	}
	if waitErr != nil {
		return nil, fmt.Errorf("process listing failed: %v: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return b, nil
}
