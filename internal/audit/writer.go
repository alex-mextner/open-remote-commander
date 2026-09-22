package audit

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/store"
)

type Writer struct {
	store  store.Store
	log    *slog.Logger
	queue  chan store.AuditEvent
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(s store.Store, logger *slog.Logger, capacity int) *Writer {
	if capacity < 1 {
		capacity = 1024
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Writer{store: s, log: logger, queue: make(chan store.AuditEvent, capacity), cancel: cancel}
	w.wg.Add(1)
	go w.loop(ctx)
	return w
}

func (w *Writer) Record(e store.AuditEvent) {
	select {
	case w.queue <- e:
	default:
		w.log.Warn("audit queue full; dropping metadata event", "device_id", e.DeviceID, "tool", e.ToolName)
	}
}

func (w *Writer) Close() { w.cancel(); w.wg.Wait() }

func (w *Writer) loop(ctx context.Context) {
	defer w.wg.Done()
	write := func(e store.AuditEvent) {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.store.WriteAudit(c, e); err != nil {
			w.log.Warn("audit write failed", "error", err)
		}
	}
	for {
		select {
		case e := <-w.queue:
			write(e)
		case <-ctx.Done():
			for {
				select {
				case e := <-w.queue:
					write(e)
				default:
					return
				}
			}
		}
	}
}
