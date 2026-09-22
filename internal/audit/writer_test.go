package audit

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/alex-mextner/open-remote-commander/internal/store"
)

type auditStore struct {
	store.Store
	mu     sync.Mutex
	events []store.AuditEvent
}

func (s *auditStore) WriteAudit(_ context.Context, e store.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func TestCloseDrainsQueuedEvents(t *testing.T) {
	s := &auditStore{}
	w := New(s, slog.New(slog.NewTextHandler(io.Discard, nil)), 8)
	for i := 0; i < 5; i++ {
		w.Record(store.AuditEvent{DeviceID: "d", ToolName: "ping"})
	}
	w.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) != 5 {
		t.Fatalf("events=%d want 5", len(s.events))
	}
}
