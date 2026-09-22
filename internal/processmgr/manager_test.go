package processmgr

import (
	"runtime"
	"testing"
	"time"
)

func TestRingAbsoluteCursorAfterTruncation(t *testing.T) {
	r := &ring{max: 5}
	r.write([]byte("abcdef"))
	data, next, truncated := r.read(0, 10)
	if string(data) != "bcdef" || next != 6 || !truncated {
		t.Fatalf("data=%q next=%d truncated=%v base=%d", data, next, truncated, r.base)
	}
}

func TestStartReadProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh-free argv available differently on windows")
	}
	m := New(2, 64<<10, 5*time.Second, false)
	res, err := m.Start(StartRequest{Argv: []string{"/bin/echo", "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		out, err := m.Read(res.ProcessID, 0, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if out.Exited {
			if out.Data != "hello\n" {
				t.Fatalf("output=%q", out.Data)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("process did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExitedProcessDoesNotConsumeConcurrencySlotForever(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX executable paths")
	}
	m := New(1, 64<<10, 5*time.Second, false)
	first, err := m.Start(StartRequest{Argv: []string{"/bin/echo", "one"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		out, err := m.Read(first.ProcessID, 0, 1024)
		if err != nil {
			t.Fatal(err)
		}
		if out.Exited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first process did not exit")
		}
		time.Sleep(5 * time.Millisecond)
	}
	second, err := m.Start(StartRequest{Argv: []string{"/bin/echo", "two"}})
	if err != nil {
		t.Fatalf("second process should reuse active slot: %v", err)
	}
	if second.ProcessID == "" {
		t.Fatal("empty second process id")
	}
}
