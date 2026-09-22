package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryDeviceOwnershipAndToken(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	d := Device{ID: "d1", OwnerSubject: "alice", Name: "laptop"}
	if err := m.CreateDevice(ctx, d, "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetDevice(ctx, "bob", "d1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-owner GetDevice = %v", err)
	}
	if _, err := m.AuthenticateDevice(ctx, "d1", "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token = %v", err)
	}
	got, err := m.AuthenticateDevice(ctx, "d1", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerSubject != "alice" {
		t.Fatalf("owner=%q", got.OwnerSubject)
	}
}

func TestMemoryPairingLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	p := Pairing{DeviceCodeHash: HashSecret("device-code"), UserCodeHash: HashSecret("ABCD1234"), DeviceName: "Mac", ExpiresAt: time.Now().Add(time.Minute)}
	if err := m.CreatePairing(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ConsumePairing(ctx, p.DeviceCodeHash, Device{ID: "d1"}, "tok"); !errors.Is(err, ErrPairingPending) {
		t.Fatalf("consume pending=%v", err)
	}
	if err := m.ApprovePairing(ctx, p.UserCodeHash, "alice"); err != nil {
		t.Fatal(err)
	}
	d, err := m.ConsumePairing(ctx, p.DeviceCodeHash, Device{ID: "d1"}, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if d.OwnerSubject != "alice" || d.Name != "Mac" {
		t.Fatalf("unexpected device: %+v", d)
	}
	if _, err := m.ConsumePairing(ctx, p.DeviceCodeHash, Device{ID: "d2"}, "tok2"); !errors.Is(err, ErrPairingUsed) {
		t.Fatalf("second consume=%v", err)
	}
}
