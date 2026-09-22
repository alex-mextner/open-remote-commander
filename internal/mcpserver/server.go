package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alex-mextner/open-remote-commander/internal/audit"
	"github.com/alex-mextner/open-remote-commander/internal/authn"
	"github.com/alex-mextner/open-remote-commander/internal/relay"
	"github.com/alex-mextner/open-remote-commander/internal/store"
)

const (
	modernVersion = "2026-07-28"
	legacyVersion = "2025-11-25"
	serverVersion = "0.1.0"
	toolScope     = "orc:tools"
	maxBody       = 4 << 20
)

type Server struct {
	store               store.Store
	hub                 *relay.Hub
	audit               *audit.Writer
	verifier            authn.Verifier
	resourceMetadataURL string
}

func New(st store.Store, hub *relay.Hub, aw *audit.Writer, verifier authn.Verifier, resourceMetadataURL string) *Server {
	return &Server{store: st, hub: hub, audit: aw, verifier: verifier, resourceMetadataURL: resourceMetadataURL}
}

func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if ct := strings.ToLower(r.Header.Get("Content-Type")); !strings.HasPrefix(ct, "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	identity, ok := s.identity(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		rpcWrite(w, http.StatusRequestEntityTooLarge, nil, nil, &rpcError{Code: -32600, Message: "request too large"})
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		rpcWrite(w, http.StatusBadRequest, nil, nil, &rpcError{Code: -32700, Message: "parse error"})
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		rpcWrite(w, http.StatusBadRequest, req.ID, nil, &rpcError{Code: -32600, Message: "invalid request"})
		return
	}
	modern, err := validateProtocolHeaders(r, req)
	if err != nil {
		rpcWrite(w, http.StatusBadRequest, req.ID, nil, &rpcError{Code: -32020, Message: err.Error()})
		return
	}

	if len(req.ID) == 0 || string(req.ID) == "null" {
		// MCP notifications are accepted and have no response body.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "server/discover":
		rpcWrite(w, http.StatusOK, req.ID, map[string]any{
			"supportedVersions": []string{modernVersion, legacyVersion},
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"instructions":      "Remote filesystem and process operations run only on paired devices.",
			"ttlMs":             60000,
			"cacheScope":        "public",
			"resultType":        "complete",
			"_meta": map[string]any{
				"io.modelcontextprotocol/serverInfo": map[string]any{"name": "open-remote-commander", "version": serverVersion},
			},
		}, nil)
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := legacyVersion
		if p.ProtocolVersion == modernVersion {
			version = modernVersion
		}
		rpcWrite(w, http.StatusOK, req.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "open-remote-commander", "version": serverVersion},
		}, nil)
	case "ping":
		result := map[string]any{}
		if modern {
			result["resultType"] = "complete"
		}
		rpcWrite(w, http.StatusOK, req.ID, result, nil)
	case "tools/list":
		result := map[string]any{"tools": toolDefinitions()}
		if modern {
			result["ttlMs"] = 60000
			result["cacheScope"] = "public"
			result["resultType"] = "complete"
		}
		rpcWrite(w, http.StatusOK, req.ID, result, nil)
	case "tools/call":
		s.callTool(w, r, req, identity, modern)
	default:
		rpcWrite(w, http.StatusNotFound, req.ID, nil, &rpcError{Code: -32601, Message: "method not found"})
	}
}

func (s *Server) identity(w http.ResponseWriter, r *http.Request) (authn.Identity, bool) {
	token, ok := authn.BearerToken(r)
	if !ok {
		s.unauthorized(w)
		return authn.Identity{}, false
	}
	id, err := authn.Require(r.Context(), s.verifier, token, toolScope)
	if err != nil {
		s.unauthorized(w)
		return authn.Identity{}, false
	}
	return id, true
}

func (s *Server) unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, s.resourceMetadataURL))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
}

func validateProtocolHeaders(r *http.Request, req rpcRequest) (bool, error) {
	headerVersion := strings.TrimSpace(r.Header.Get("MCP-Protocol-Version"))
	var metaVersion string
	if len(req.Params) > 0 && string(req.Params) != "null" {
		var p struct {
			Meta map[string]any `json:"_meta"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			metaVersion, _ = p.Meta["io.modelcontextprotocol/protocolVersion"].(string)
		}
	}
	modern := req.Method == "server/discover" || headerVersion == modernVersion || metaVersion == modernVersion
	if headerVersion != "" && headerVersion != modernVersion && headerVersion != legacyVersion {
		return false, fmt.Errorf("unsupported protocol version %q", headerVersion)
	}
	if modern && req.Method != "server/discover" {
		if headerVersion == "" {
			return false, errors.New("MCP-Protocol-Version header is required for modern requests")
		}
		if metaVersion == "" {
			return false, errors.New("modern requests require _meta io.modelcontextprotocol/protocolVersion")
		}
		if headerVersion != metaVersion {
			return false, errors.New("protocol version header does not match request metadata")
		}
	}
	if hm := strings.TrimSpace(r.Header.Get("Mcp-Method")); hm != "" && hm != req.Method {
		return false, fmt.Errorf("Mcp-Method %q does not match request method %q", hm, req.Method)
	}
	if req.Method == "tools/call" {
		var p struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if hn := strings.TrimSpace(r.Header.Get("Mcp-Name")); hn != "" && hn != p.Name {
			return false, fmt.Errorf("Mcp-Name %q does not match tool %q", hn, p.Name)
		}
	}
	return modern, nil
}

func (s *Server) callTool(w http.ResponseWriter, r *http.Request, req rpcRequest, identity authn.Identity, modern bool) {
	var p struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
		rpcWrite(w, http.StatusBadRequest, req.ID, nil, &rpcError{Code: -32602, Message: "invalid tool call parameters"})
		return
	}
	if p.Arguments == nil {
		p.Arguments = map[string]json.RawMessage{}
	}
	if p.Name == "list_devices" {
		devices, err := s.store.ListDevices(r.Context(), identity.Subject)
		if err != nil {
			rpcWrite(w, http.StatusInternalServerError, req.ID, nil, &rpcError{Code: -32603, Message: "internal error"})
			return
		}
		for i := range devices {
			devices[i].Online = s.hub.IsOnline(identity.Subject, devices[i].ID)
		}
		s.toolResult(w, req.ID, map[string]any{"devices": devices}, nil, modern)
		return
	}
	if !knownRemoteTool(p.Name) {
		rpcWrite(w, http.StatusNotFound, req.ID, nil, &rpcError{Code: -32602, Message: "unknown tool"})
		return
	}
	var deviceID string
	if raw := p.Arguments["device_id"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &deviceID)
	}
	if strings.TrimSpace(deviceID) == "" {
		rpcWrite(w, http.StatusBadRequest, req.ID, nil, &rpcError{Code: -32602, Message: "device_id is required"})
		return
	}
	// Prove ownership before consulting live relay state, avoiding an ID oracle.
	if _, err := s.store.GetDevice(r.Context(), identity.Subject, deviceID); err != nil {
		s.toolResult(w, req.ID, nil, errors.New("device offline or unavailable"), modern)
		return
	}
	delete(p.Arguments, "device_id")
	payload := make(map[string]any, len(p.Arguments))
	for k, raw := range p.Arguments {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			rpcWrite(w, http.StatusBadRequest, req.ID, nil, &rpcError{Code: -32602, Message: "invalid arguments"})
			return
		}
		payload[k] = v
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	started := time.Now()
	res, err := s.hub.Call(ctx, identity.Subject, deviceID, p.Name, payload)
	status, errorCode := "ok", ""
	if err != nil {
		status = "error"
		switch {
		case errors.Is(err, relay.ErrOffline):
			status, errorCode = "offline", "device_offline"
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			status, errorCode = "timeout", "timeout"
		}
		s.audit.Record(store.AuditEvent{OwnerSubject: identity.Subject, DeviceID: deviceID, ToolName: p.Name, Status: status, Duration: time.Since(started), ErrorCode: errorCode})
		s.toolResult(w, req.ID, nil, err, modern)
		return
	}
	if res.Error != nil {
		status, errorCode = "error", res.Error.Code
		s.audit.Record(store.AuditEvent{OwnerSubject: identity.Subject, DeviceID: deviceID, ToolName: p.Name, Status: status, Duration: time.Since(started), ErrorCode: errorCode})
		s.toolResult(w, req.ID, nil, fmt.Errorf("%s: %s", res.Error.Code, res.Error.Message), modern)
		return
	}
	var out any = map[string]any{"ok": true}
	if len(res.Value) > 0 && string(res.Value) != "null" {
		if err := json.Unmarshal(res.Value, &out); err != nil {
			s.toolResult(w, req.ID, nil, errors.New("invalid response from device"), modern)
			return
		}
	}
	s.audit.Record(store.AuditEvent{OwnerSubject: identity.Subject, DeviceID: deviceID, ToolName: p.Name, Status: status, Duration: time.Since(started)})
	s.toolResult(w, req.ID, out, nil, modern)
}

func (s *Server) toolResult(w http.ResponseWriter, id json.RawMessage, value any, toolErr error, modern bool) {
	result := map[string]any{}
	if modern {
		result["resultType"] = "complete"
	}
	if toolErr != nil {
		result["content"] = []any{map[string]any{"type": "text", "text": toolErr.Error()}}
		result["isError"] = true
	} else {
		b, _ := json.Marshal(value)
		result["content"] = []any{map[string]any{"type": "text", "text": string(b)}}
		result["structuredContent"] = value
	}
	rpcWrite(w, http.StatusOK, id, result, nil)
}

func rpcWrite(w http.ResponseWriter, status int, id json.RawMessage, result any, e *rpcError) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: e})
}

func knownRemoteTool(name string) bool {
	switch name {
	case "ping", "read_file", "write_file", "list_directory", "create_directory", "move_file", "get_file_info", "start_process", "read_process_output", "interact_with_process", "force_terminate":
		return true
	default:
		return false
	}
}

func toolDefinitions() []map[string]any {
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	integer := func(desc string) map[string]any { return map[string]any{"type": "integer", "description": desc} }
	boolean := func(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	dev := str("paired device identifier")
	return []map[string]any{
		{"name": "list_devices", "description": "List paired computers and online status.", "inputSchema": obj(map[string]any{})},
		{"name": "ping", "description": "Check that a paired computer is reachable.", "inputSchema": obj(map[string]any{"device_id": dev}, "device_id")},
		{"name": "read_file", "description": "Read a bounded chunk of a file.", "inputSchema": obj(map[string]any{"device_id": dev, "path": str("path on the paired device"), "offset": integer("byte offset"), "max_bytes": integer("maximum bytes")}, "device_id", "path")},
		{"name": "write_file", "description": "Atomically write a bounded file.", "inputSchema": obj(map[string]any{"device_id": dev, "path": str("destination path"), "content": str("file content"), "encoding": str("utf-8 or base64"), "mode": integer("optional POSIX mode")}, "device_id", "path", "content")},
		{"name": "list_directory", "description": "List a bounded number of directory entries.", "inputSchema": obj(map[string]any{"device_id": dev, "path": str("directory path")}, "device_id", "path")},
		{"name": "create_directory", "description": "Create a directory inside allowed roots.", "inputSchema": obj(map[string]any{"device_id": dev, "path": str("directory path"), "mode": integer("optional POSIX mode")}, "device_id", "path")},
		{"name": "move_file", "description": "Move or rename a file. Existing destinations are preserved unless overwrite=true.", "inputSchema": obj(map[string]any{"device_id": dev, "source": str("source path"), "destination": str("destination path"), "overwrite": boolean("replace an existing non-symlink destination")}, "device_id", "source", "destination")},
		{"name": "get_file_info", "description": "Get file or directory metadata.", "inputSchema": obj(map[string]any{"device_id": dev, "path": str("path")}, "device_id", "path")},
		{"name": "start_process", "description": "Start a bounded process session; argv is preferred and shell is disabled by default.", "inputSchema": obj(map[string]any{"device_id": dev, "argv": map[string]any{"type": "array", "items": str("argument")}, "command": str("shell command, only if enabled on the agent"), "cwd": str("working directory"), "env": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}}, "device_id")},
		{"name": "read_process_output", "description": "Read process output from an absolute cursor.", "inputSchema": obj(map[string]any{"device_id": dev, "process_id": str("process session ID"), "cursor": integer("absolute output cursor"), "max_bytes": integer("maximum bytes")}, "device_id", "process_id")},
		{"name": "interact_with_process", "description": "Write data to process stdin.", "inputSchema": obj(map[string]any{"device_id": dev, "process_id": str("process session ID"), "data": str("stdin data"), "encoding": str("utf-8 or base64")}, "device_id", "process_id", "data")},
		{"name": "force_terminate", "description": "Terminate a process session.", "inputSchema": obj(map[string]any{"device_id": dev, "process_id": str("process session ID")}, "device_id", "process_id")},
	}
}
