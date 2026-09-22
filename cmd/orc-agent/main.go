package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/config"
	"github.com/alex-mextner/open-remote-commander/internal/executor"
	"github.com/alex-mextner/open-remote-commander/internal/pathpolicy"
	"github.com/alex-mextner/open-remote-commander/internal/processmgr"
	"github.com/alex-mextner/open-remote-commander/internal/protocol"
	"github.com/alex-mextner/open-remote-commander/internal/ws"
	apiv1 "github.com/alex-mextner/open-remote-commander/pkg/api/v1"
)

type credentials struct {
	DeviceID string `json:"device_id"`
	Token    string `json:"token"`
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.LoadAgent()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	creds := credentials{DeviceID: cfg.DeviceID, Token: cfg.AgentToken}
	credPath, _ := credentialsPath()
	if creds.DeviceID == "" || creds.Token == "" {
		if saved, err := loadCredentials(credPath); err == nil {
			creds = saved
		}
	}
	if creds.DeviceID == "" || creds.Token == "" {
		creds, err = pair(ctx, cfg.ServerURL, cfg.DeviceName, logger)
		if err != nil {
			logger.Error("pairing failed", "error", err)
			os.Exit(1)
		}
		if err := saveCredentials(credPath, creds); err != nil {
			logger.Warn("could not persist credentials", "error", err)
		}
	}

	policy, err := pathpolicy.New(cfg.AllowedRoots)
	if err != nil {
		logger.Error("path policy", "error", err)
		os.Exit(2)
	}
	pm := processmgr.New(cfg.MaxProcesses, cfg.MaxProcessBytes, cfg.MaxRuntime, cfg.AllowShell)
	exec := executor.NewWithOptions(policy, pm, executor.Options{AllowKillProcess: cfg.AllowKillProcess})

	backoff := time.Second
	for ctx.Err() == nil {
		err = connectAndServe(ctx, cfg.ServerURL, creds, exec, logger)
		if ctx.Err() != nil {
			break
		}
		logger.Warn("connection lost", "error", err, "retry_in", backoff)
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			break
		case <-t.C:
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func connectAndServe(ctx context.Context, server string, creds credentials, exec *executor.Executor, logger *slog.Logger) error {
	u, err := url.Parse(server)
	if err != nil {
		return err
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/agent/v1/connect"
	q := u.Query()
	q.Set("device_id", creds.DeviceID)
	u.RawQuery = q.Encode()
	conn, err := ws.Dial(ctx, u.String(), creds.Token, protocol.MaxFrameBytes)
	if err != nil {
		return err
	}
	defer conn.Close()
	logger.Info("connected", "device_id", creds.DeviceID)

	errCh := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b, _ := json.Marshal(protocol.Frame{Type: protocol.TypePing})
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(b); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}
	}()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		b, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var f protocol.Frame
		if err := json.Unmarshal(b, &f); err != nil {
			return err
		}
		if err := f.Validate(); err != nil {
			return err
		}
		switch f.Type {
		case protocol.TypeCall:
			go handleCall(ctx, conn, exec, f, logger)
		case protocol.TypePing:
			payload, _ := json.Marshal(protocol.Frame{Type: protocol.TypePong, ID: f.ID})
			_ = conn.WriteMessage(payload)
		case protocol.TypePong:
		default:
			return errors.New("unexpected relay frame")
		}
		select {
		case err := <-errCh:
			return err
		default:
		}
	}
}

func handleCall(ctx context.Context, conn *ws.Conn, exec *executor.Executor, f protocol.Frame, logger *slog.Logger) {
	value, err := exec.Execute(ctx, f.Tool, f.Args)
	out := protocol.Frame{Type: protocol.TypeResult, ID: f.ID, OK: err == nil}
	if err != nil {
		out.Error = &protocol.RemoteError{Code: classify(err), Message: err.Error()}
	} else {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			out.OK = false
			out.Error = &protocol.RemoteError{Code: apiv1.ErrorInternal, Message: "could not encode result"}
		} else {
			out.Result = raw
		}
	}
	payload, marshalErr := json.Marshal(out)
	if marshalErr != nil {
		logger.Warn("encode result", "call_id", f.ID, "error", marshalErr)
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := conn.WriteMessage(payload); err != nil {
		logger.Debug("write result failed", "call_id", f.ID, "error", err)
	}
}

func classify(err error) string {
	if errors.Is(err, pathpolicy.ErrOutsideRoots) {
		return apiv1.ErrorForbidden
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, processmgr.ErrNotFound) {
		return apiv1.ErrorNotFound
	}
	return apiv1.ErrorExecution
}

func pair(ctx context.Context, server, deviceName string, logger *slog.Logger) (credentials, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	body, _ := json.Marshal(apiv1.PairingStartRequest{DeviceName: deviceName})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/v1/pairings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return credentials{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return credentials{}, fmt.Errorf("start pairing: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var start apiv1.PairingStartResponse
	if err := json.NewDecoder(resp.Body).Decode(&start); err != nil {
		return credentials{}, err
	}
	fmt.Fprintf(os.Stderr, "\nPair this device:\n  Code: %s\n  Open: %s\n\n", start.UserCode, start.VerificationURIComplete)
	logger.Info("waiting for pairing approval")
	interval := time.Duration(start.Interval) * time.Second
	if interval < time.Second {
		interval = 3 * time.Second
	}
	deadline := time.NewTimer(time.Duration(start.ExpiresIn) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return credentials{}, ctx.Err()
		case <-deadline.C:
			return credentials{}, errors.New("pairing expired")
		case <-ticker.C:
			payload, _ := json.Marshal(apiv1.PairingTokenRequest{DeviceCode: start.DeviceCode})
			r, _ := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/v1/pairings/token", bytes.NewReader(payload))
			r.Header.Set("Content-Type", "application/json")
			res, err := client.Do(r)
			if err != nil {
				continue
			}
			if res.StatusCode == http.StatusOK {
				var tok apiv1.PairingTokenResponse
				err = json.NewDecoder(res.Body).Decode(&tok)
				_ = res.Body.Close()
				if err != nil {
					return credentials{}, err
				}
				return credentials{DeviceID: string(tok.DeviceID), Token: tok.AccessToken}, nil
			}
			_ = res.Body.Close()
			if res.StatusCode != 428 {
				return credentials{}, fmt.Errorf("pairing token endpoint returned %s", res.Status)
			}
		}
	}
}

func credentialsPath() (string, error) {
	if runtime.GOOS == "windows" {
		base := os.Getenv("APPDATA")
		if base == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(base, "OpenRemoteCommander", "device.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "open-remote-commander", "device.json"), nil
}

func loadCredentials(path string) (credentials, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return credentials{}, err
	}
	var c credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return credentials{}, err
	}
	if c.DeviceID == "" || c.Token == "" {
		return credentials{}, errors.New("invalid credential file")
	}
	return c, nil
}

func saveCredentials(path string, c credentials) error {
	if path == "" {
		return errors.New("credential path unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
