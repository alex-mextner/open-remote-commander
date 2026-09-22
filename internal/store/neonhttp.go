package store

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type NeonHTTP struct {
	connectionString string
	endpoint         string
	client           *http.Client
}

type neonQuery struct {
	Query  string `json:"query"`
	Params []any  `json:"params"`
}

type neonResult struct {
	Fields []struct {
		Name string `json:"name"`
	} `json:"fields"`
	Rows     [][]any `json:"rows"`
	RowCount int     `json:"rowCount"`
}

func NewNeonHTTP(connectionString, endpointOverride string) (*NeonHTTP, error) {
	u, err := url.Parse(connectionString)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.Hostname() == "" || strings.TrimPrefix(u.Path, "/") == "" {
		return nil, errors.New("invalid Neon Postgres connection string")
	}
	endpoint := strings.TrimSpace(endpointOverride)
	if endpoint == "" {
		parts := strings.Split(u.Hostname(), ".")
		if len(parts) < 2 {
			return nil, errors.New("cannot derive Neon HTTP endpoint")
		}
		parts[0] = "api"
		endpoint = "https://" + strings.Join(parts, ".") + "/sql"
	}
	eu, err := url.Parse(endpoint)
	if err != nil || eu.Host == "" || (eu.Scheme != "https" && !(eu.Scheme == "http" && (eu.Hostname() == "127.0.0.1" || eu.Hostname() == "localhost"))) {
		return nil, errors.New("Neon HTTP endpoint must use https outside loopback")
	}
	return &NeonHTTP{connectionString: connectionString, endpoint: endpoint, client: &http.Client{Timeout: 8 * time.Second}}, nil
}

func (n *NeonHTTP) query(ctx context.Context, q string, params ...any) (neonResult, error) {
	body, err := json.Marshal(neonQuery{Query: q, Params: params})
	if err != nil {
		return neonResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint, bytes.NewReader(body))
	if err != nil {
		return neonResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Neon-Connection-String", n.connectionString)
	req.Header.Set("Neon-Raw-Text-Output", "true")
	req.Header.Set("Neon-Array-Mode", "true")
	resp, err := n.client.Do(req)
	if err != nil {
		return neonResult{}, fmt.Errorf("neon http query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return neonResult{}, fmt.Errorf("neon http query status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out neonResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return neonResult{}, fmt.Errorf("decode neon response: %w", err)
	}
	return out, nil
}

func rowMap(r neonResult, row []any) map[string]any {
	m := make(map[string]any, len(r.Fields))
	for i, f := range r.Fields {
		if i < len(row) {
			m[f.Name] = row[i]
		}
	}
	return m
}

func textVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func timePtr(v any) *time.Time {
	s := textVal(v)
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	return &t
}

func deviceFromMap(m map[string]any) Device {
	return Device{
		ID: textVal(m["id"]), OwnerSubject: textVal(m["owner_subject"]), Name: textVal(m["name"]),
		CreatedAt: derefTime(timePtr(m["created_at"])), LastSeenAt: timePtr(m["last_seen_at"]), RevokedAt: timePtr(m["revoked_at"]),
	}
}

func derefTime(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}

func (n *NeonHTTP) CreateDevice(ctx context.Context, d Device, token string) error {
	_, err := n.query(ctx, `INSERT INTO devices(id, owner_subject, name, token_hash) VALUES ($1,$2,$3,$4)`, d.ID, d.OwnerSubject, d.Name, HashSecret(token))
	return err
}

func (n *NeonHTTP) GetDevice(ctx context.Context, owner, id string) (Device, error) {
	r, err := n.query(ctx, `SELECT id, owner_subject, name, created_at, last_seen_at, revoked_at FROM devices WHERE id=$1 AND owner_subject=$2 AND revoked_at IS NULL`, id, owner)
	if err != nil {
		return Device{}, err
	}
	if len(r.Rows) != 1 {
		return Device{}, ErrNotFound
	}
	return deviceFromMap(rowMap(r, r.Rows[0])), nil
}

func (n *NeonHTTP) ListDevices(ctx context.Context, owner string) ([]Device, error) {
	r, err := n.query(ctx, `SELECT id, owner_subject, name, created_at, last_seen_at, revoked_at FROM devices WHERE owner_subject=$1 AND revoked_at IS NULL ORDER BY created_at`, owner)
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(r.Rows))
	for _, row := range r.Rows {
		out = append(out, deviceFromMap(rowMap(r, row)))
	}
	return out, nil
}

func (n *NeonHTTP) AuthenticateDevice(ctx context.Context, id, token string) (Device, error) {
	r, err := n.query(ctx, `SELECT id, owner_subject, name, token_hash, created_at, last_seen_at, revoked_at FROM devices WHERE id=$1 AND revoked_at IS NULL`, id)
	if err != nil {
		return Device{}, err
	}
	if len(r.Rows) != 1 {
		return Device{}, ErrUnauthorized
	}
	m := rowMap(r, r.Rows[0])
	want, got := textVal(m["token_hash"]), HashSecret(token)
	if len(want) != len(got) || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		return Device{}, ErrUnauthorized
	}
	return deviceFromMap(m), nil
}

func (n *NeonHTTP) TouchDevice(ctx context.Context, id string) error {
	_, err := n.query(ctx, `UPDATE devices SET last_seen_at=now() WHERE id=$1 AND revoked_at IS NULL`, id)
	return err
}

func (n *NeonHTTP) RevokeDevice(ctx context.Context, owner, id string) error {
	r, err := n.query(ctx, `UPDATE devices SET revoked_at=now() WHERE id=$1 AND owner_subject=$2 AND revoked_at IS NULL RETURNING id`, id, owner)
	if err != nil {
		return err
	}
	if len(r.Rows) != 1 {
		return ErrNotFound
	}
	return nil
}

func (n *NeonHTTP) WriteAudit(ctx context.Context, e AuditEvent) error {
	_, err := n.query(ctx, `INSERT INTO call_audit(owner_subject, device_id, tool_name, status, duration_ms, error_code) VALUES ($1,$2,$3,$4,$5,NULLIF($6,''))`, e.OwnerSubject, e.DeviceID, e.ToolName, e.Status, strconv.FormatInt(e.Duration.Milliseconds(), 10), e.ErrorCode)
	return err
}

func (n *NeonHTTP) CreatePairing(ctx context.Context, p Pairing) error {
	_, err := n.query(ctx, `INSERT INTO pairings(device_code_hash,user_code_hash,device_name,expires_at) VALUES ($1,$2,$3,$4)`, p.DeviceCodeHash, p.UserCodeHash, p.DeviceName, p.ExpiresAt.UTC().Format(time.RFC3339Nano))
	return err
}

func (n *NeonHTTP) ApprovePairing(ctx context.Context, userCodeHash, owner string) error {
	r, err := n.query(ctx, `UPDATE pairings SET owner_subject=$2, approved_at=now() WHERE user_code_hash=$1 AND consumed_at IS NULL AND expires_at>now() RETURNING device_code_hash`, userCodeHash, owner)
	if err != nil {
		return err
	}
	if len(r.Rows) != 1 {
		return ErrNotFound
	}
	return nil
}

func (n *NeonHTTP) ConsumePairing(ctx context.Context, deviceCodeHash string, d Device, token string) (Device, error) {
	q := `WITH p AS (
  UPDATE pairings SET consumed_at=now()
  WHERE device_code_hash=$1 AND approved_at IS NOT NULL AND owner_subject IS NOT NULL AND consumed_at IS NULL AND expires_at>now()
  RETURNING owner_subject, device_name
), ins AS (
  INSERT INTO devices(id,owner_subject,name,token_hash)
  SELECT $2,owner_subject,device_name,$3 FROM p
  RETURNING id,owner_subject,name,created_at,last_seen_at,revoked_at
)
SELECT id,owner_subject,name,created_at,last_seen_at,revoked_at FROM ins`
	r, err := n.query(ctx, q, deviceCodeHash, d.ID, HashSecret(token))
	if err != nil {
		return Device{}, err
	}
	if len(r.Rows) == 1 {
		return deviceFromMap(rowMap(r, r.Rows[0])), nil
	}
	status, err := n.query(ctx, `SELECT approved_at,consumed_at,expires_at FROM pairings WHERE device_code_hash=$1`, deviceCodeHash)
	if err != nil {
		return Device{}, err
	}
	if len(status.Rows) != 1 {
		return Device{}, ErrNotFound
	}
	m := rowMap(status, status.Rows[0])
	if timePtr(m["consumed_at"]) != nil {
		return Device{}, ErrPairingUsed
	}
	if exp := timePtr(m["expires_at"]); exp != nil && time.Now().After(*exp) {
		return Device{}, ErrPairingExpired
	}
	if timePtr(m["approved_at"]) == nil {
		return Device{}, ErrPairingPending
	}
	return Device{}, ErrNotFound
}
