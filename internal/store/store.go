package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrUnauthorized   = errors.New("unauthorized")
	ErrPairingPending = errors.New("pairing pending")
	ErrPairingExpired = errors.New("pairing expired")
	ErrPairingUsed    = errors.New("pairing already used")
)

type Device struct {
	ID           string     `json:"id"`
	OwnerSubject string     `json:"-"`
	Name         string     `json:"name"`
	CreatedAt    time.Time  `json:"created_at"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	Online       bool       `json:"online"`
}

type AuditEvent struct {
	OwnerSubject string
	DeviceID     string
	ToolName     string
	Status       string
	Duration     time.Duration
	ErrorCode    string
}

type Pairing struct {
	DeviceCodeHash string
	UserCodeHash   string
	DeviceName     string
	ExpiresAt      time.Time
	OwnerSubject   string
	ApprovedAt     *time.Time
	ConsumedAt     *time.Time
}

type Store interface {
	CreateDevice(context.Context, Device, string) error
	GetDevice(context.Context, string, string) (Device, error)
	ListDevices(context.Context, string) ([]Device, error)
	AuthenticateDevice(context.Context, string, string) (Device, error)
	TouchDevice(context.Context, string) error
	RevokeDevice(context.Context, string, string) error
	WriteAudit(context.Context, AuditEvent) error

	CreatePairing(context.Context, Pairing) error
	ApprovePairing(context.Context, string, string) error
	ConsumePairing(context.Context, string, Device, string) (Device, error)
}

func HashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}
