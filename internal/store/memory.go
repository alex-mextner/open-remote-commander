package store

import (
	"context"
	"crypto/subtle"
	"errors"
	"sort"
	"sync"
	"time"
)

type memoryDevice struct {
	device    Device
	tokenHash string
}

type Memory struct {
	mu       sync.RWMutex
	devices  map[string]memoryDevice
	pairings map[string]Pairing
	userCode map[string]string
	audit    []AuditEvent
}

func NewMemory() *Memory {
	return &Memory{devices: map[string]memoryDevice{}, pairings: map[string]Pairing{}, userCode: map[string]string{}}
}

func (m *Memory) CreateDevice(_ context.Context, d Device, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.devices[d.ID]; exists {
		return errors.New("device already exists")
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now().UTC()
	}
	m.devices[d.ID] = memoryDevice{device: d, tokenHash: HashSecret(token)}
	return nil
}

func (m *Memory) GetDevice(_ context.Context, owner, id string) (Device, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[id]
	if !ok || d.device.OwnerSubject != owner || d.device.RevokedAt != nil {
		return Device{}, ErrNotFound
	}
	return d.device, nil
}

func (m *Memory) ListDevices(_ context.Context, owner string) ([]Device, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Device, 0)
	for _, d := range m.devices {
		if d.device.OwnerSubject == owner && d.device.RevokedAt == nil {
			out = append(out, d.device)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *Memory) AuthenticateDevice(_ context.Context, id, token string) (Device, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[id]
	if !ok || d.device.RevokedAt != nil || subtle.ConstantTimeCompare([]byte(d.tokenHash), []byte(HashSecret(token))) != 1 {
		return Device{}, ErrUnauthorized
	}
	return d.device, nil
}

func (m *Memory) TouchDevice(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	d.device.LastSeenAt = &now
	m.devices[id] = d
	return nil
}

func (m *Memory) RevokeDevice(_ context.Context, owner, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.devices[id]
	if !ok || d.device.OwnerSubject != owner || d.device.RevokedAt != nil {
		return ErrNotFound
	}
	now := time.Now().UTC()
	d.device.RevokedAt = &now
	m.devices[id] = d
	return nil
}

func (m *Memory) WriteAudit(_ context.Context, e AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.audit) >= 10000 {
		copy(m.audit, m.audit[len(m.audit)-5000:])
		m.audit = m.audit[:5000]
	}
	m.audit = append(m.audit, e)
	return nil
}

func (m *Memory) CreatePairing(_ context.Context, p Pairing) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pairings[p.DeviceCodeHash]; ok {
		return errors.New("pairing exists")
	}
	if _, ok := m.userCode[p.UserCodeHash]; ok {
		return errors.New("user code collision")
	}
	m.pairings[p.DeviceCodeHash] = p
	m.userCode[p.UserCodeHash] = p.DeviceCodeHash
	return nil
}

func (m *Memory) ApprovePairing(_ context.Context, userCodeHash, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	deviceHash, ok := m.userCode[userCodeHash]
	if !ok {
		return ErrNotFound
	}
	p := m.pairings[deviceHash]
	now := time.Now().UTC()
	if now.After(p.ExpiresAt) {
		return ErrPairingExpired
	}
	if p.ConsumedAt != nil {
		return ErrPairingUsed
	}
	p.OwnerSubject = owner
	p.ApprovedAt = &now
	m.pairings[deviceHash] = p
	return nil
}

func (m *Memory) ConsumePairing(_ context.Context, deviceCodeHash string, d Device, token string) (Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pairings[deviceCodeHash]
	if !ok {
		return Device{}, ErrNotFound
	}
	now := time.Now().UTC()
	if now.After(p.ExpiresAt) {
		return Device{}, ErrPairingExpired
	}
	if p.ConsumedAt != nil {
		return Device{}, ErrPairingUsed
	}
	if p.ApprovedAt == nil || p.OwnerSubject == "" {
		return Device{}, ErrPairingPending
	}
	if _, exists := m.devices[d.ID]; exists {
		return Device{}, errors.New("device id collision")
	}
	d.OwnerSubject = p.OwnerSubject
	d.Name = p.DeviceName
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	m.devices[d.ID] = memoryDevice{device: d, tokenHash: HashSecret(token)}
	p.ConsumedAt = &now
	m.pairings[deviceCodeHash] = p
	return d, nil
}
