package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/protocol"
	"github.com/alex-mextner/open-remote-commander/internal/store"
	"github.com/alex-mextner/open-remote-commander/internal/ws"
)

var (
	ErrOffline = errors.New("device offline")
	ErrClosed  = errors.New("device connection closed")
)

type Result struct {
	Value json.RawMessage
	Error *protocol.RemoteError
}

type pendingResult struct {
	result Result
	err    error
}

type Hub struct {
	mu          sync.RWMutex
	devices     map[string]*deviceConn
	maxInFlight int
}

type deviceConn struct {
	hub     *Hub
	device  store.Device
	conn    *ws.Conn
	sem     chan struct{}
	mu      sync.Mutex
	pending map[string]chan pendingResult
	done    chan struct{}
	once    sync.Once
}

func NewHub(maxInFlight int) *Hub {
	if maxInFlight < 1 {
		maxInFlight = 16
	}
	return &Hub{devices: map[string]*deviceConn{}, maxInFlight: maxInFlight}
}

func (h *Hub) Register(device store.Device, conn *ws.Conn) {
	d := &deviceConn{hub: h, device: device, conn: conn, sem: make(chan struct{}, h.maxInFlight), pending: map[string]chan pendingResult{}, done: make(chan struct{})}
	h.mu.Lock()
	old := h.devices[device.ID]
	h.devices[device.ID] = d
	h.mu.Unlock()
	if old != nil {
		old.close(ErrClosed)
	}
	go d.readLoop()
}

func (h *Hub) IsOnline(owner, deviceID string) bool {
	h.mu.RLock()
	d := h.devices[deviceID]
	h.mu.RUnlock()
	return d != nil && d.device.OwnerSubject == owner
}

func (h *Hub) Disconnect(deviceID string) {
	h.mu.RLock()
	d := h.devices[deviceID]
	h.mu.RUnlock()
	if d != nil {
		d.close(ErrClosed)
	}
}

func (h *Hub) Call(ctx context.Context, owner, deviceID, tool string, args any) (Result, error) {
	h.mu.RLock()
	d := h.devices[deviceID]
	h.mu.RUnlock()
	// Deliberately return the same error for a missing device and a device owned
	// by another subject. This avoids turning online state into an ID oracle.
	if d == nil || d.device.OwnerSubject != owner {
		return Result{}, ErrOffline
	}
	select {
	case d.sem <- struct{}{}:
		defer func() { <-d.sem }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-d.done:
		return Result{}, ErrOffline
	}
	id, err := randomID()
	if err != nil {
		return Result{}, err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return Result{}, err
	}
	frame := protocol.Frame{Type: protocol.TypeCall, ID: id, Tool: tool, Args: raw}
	payload, err := json.Marshal(frame)
	if err != nil {
		return Result{}, err
	}
	ch := make(chan pendingResult, 1)
	d.mu.Lock()
	select {
	case <-d.done:
		d.mu.Unlock()
		return Result{}, ErrOffline
	default:
	}
	d.pending[id] = ch
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.pending, id); d.mu.Unlock() }()
	_ = d.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := d.conn.WriteMessage(payload); err != nil {
		d.close(err)
		return Result{}, ErrOffline
	}
	select {
	case p := <-ch:
		return p.result, p.err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-d.done:
		return Result{}, ErrOffline
	}
}

func (d *deviceConn) readLoop() {
	defer d.close(ErrClosed)
	for {
		_ = d.conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		b, err := d.conn.ReadMessage()
		if err != nil {
			return
		}
		var f protocol.Frame
		if err := json.Unmarshal(b, &f); err != nil {
			return
		}
		if err := f.Validate(); err != nil {
			return
		}
		switch f.Type {
		case protocol.TypeResult:
			d.mu.Lock()
			ch := d.pending[f.ID]
			d.mu.Unlock()
			if ch != nil {
				select {
				case ch <- pendingResult{result: Result{Value: f.Result, Error: f.Error}}:
				default:
				}
			}
		case protocol.TypePing:
			payload, _ := json.Marshal(protocol.Frame{Type: protocol.TypePong, ID: f.ID})
			_ = d.conn.WriteMessage(payload)
		case protocol.TypePong:
		default:
			return
		}
	}
}

func (d *deviceConn) close(err error) {
	d.once.Do(func() {
		_ = d.conn.Close()
		close(d.done)
		d.hub.mu.Lock()
		if d.hub.devices[d.device.ID] == d {
			delete(d.hub.devices, d.device.ID)
		}
		d.hub.mu.Unlock()
		d.mu.Lock()
		for id, ch := range d.pending {
			select {
			case ch <- pendingResult{err: err}:
			default:
			}
			delete(d.pending, id)
		}
		d.mu.Unlock()
	})
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("random id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
