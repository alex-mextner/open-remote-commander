# Open Remote Commander — product and protocol specification

Status: **v0.1 engineering specification**  
Normative language: **MUST**, **SHOULD**, **MAY** is used in the RFC 2119 sense.

## 1. Goal

Open Remote Commander (ORC) is an open-source remote MCP system that lets an authorized AI client invoke filesystem and process tools on computers owned by the same user. The target UX is the useful part of Remote Desktop Commander, but every runtime component needed for self-hosting is open source.

The product has four separable components:

1. **`orc-agent`** — a small Go process running on the controlled machine.
2. **`orc-server`** — MCP resource server, device registry, pairing API, and relay gateway.
3. **Control-plane UI** — device/pairing/revocation dashboard; deployable independently on Vercel.
4. **Postgres state** — durable device identity, pairing state, and metadata-only audit records; Neon is the reference deployment.

## 2. Non-goals for v0.1

- The cloud service is **not** a generic shell host.
- Allowed roots and command policy are **not** a sandbox boundary.
- ORC does not store terminal transcripts, file contents, MCP tool arguments, or tool results in its database by default.
- Screen-control / mouse / keyboard automation is not in v0.1. It can be added as a separate capability with stronger consent and OS permissions.

## 3. User journeys

### 3.1 Pair a machine

1. User launches `orc-agent pair --server https://…`.
2. Agent requests a short-lived `device_code` and human `user_code`.
3. User opens the verification URI, signs in to the same identity used by the MCP client, verifies the code, and approves the device.
4. Agent polls the token endpoint until approval.
5. Server creates a new device identity and returns a random device bearer token exactly once.
6. Agent stores the token in an OS-appropriate private credential store/file and connects outbound to `/agent/v1/connect` over WSS.

### 3.2 Call a tool

1. MCP client calls a tool with `device_id` and tool-specific arguments.
2. `orc-server` validates the MCP access token, audience, expiry, and scope.
3. Server confirms that `device_id` belongs to the authenticated subject **before checking online state**.
4. Relay sends a typed call frame to the existing device WebSocket.
5. Agent validates frame shape and tool arguments, applies local policy, executes, and returns a typed result/error.
6. Server returns the result to MCP and writes only metadata audit fields.

### 3.3 Revoke a machine

1. User revokes device in the dashboard/API.
2. Device token is invalid immediately for new connections.
3. Any live connection is disconnected by the relay when revocation is observed.
4. Future MCP calls return a non-enumerating unavailable/not-found response.

## 4. Trust boundaries

### AI client / MCP client
Trusted to act as the signed-in user. A compromised AI session is high impact.

### Hosted server
Trusted for authentication, authorization, routing and availability. It MUST NOT gain local OS execution powers beyond calls explicitly proxied to an authenticated device.

### Device agent
Runs with the OS user's permissions. It is the only component allowed to touch the user's filesystem or spawn local processes.

### Database
Contains identity/routing/audit metadata. It SHOULD be treated as sensitive, but compromise of the DB alone SHOULD NOT reveal plaintext device bearer tokens or historical file/process content.

## 5. Authentication and authorization

### 5.1 MCP access

Production mode uses a standards-compliant OAuth authorization server. `orc-server` is an OAuth protected resource.

Required token properties:

- `active = true` (for introspection mode)
- stable user subject (`sub`)
- unexpired `exp`
- audience containing the configured MCP resource URI
- required scope, initially `orc:tools`

The server exposes Protected Resource Metadata at both canonical MCP-compatible locations:

- `/.well-known/oauth-protected-resource`
- `/.well-known/oauth-protected-resource/mcp`

`ORC_AUTH_MODE=dev` uses locally signed HMAC tokens and MUST only be used on loopback/private development deployments.

### 5.2 Agent authentication

Each device receives a random token of at least 256 bits. The server stores only `SHA-256(token)` and uses constant-time comparison. Device tokens are independent from MCP/user access tokens.

### 5.3 Authorization ordering

Ownership checks MUST occur before online-state disclosure or dispatch. A caller must not be able to distinguish “device does not exist”, “device belongs to another user”, and “device is offline” by probing arbitrary IDs.

## 6. Pairing

Pairing follows the semantics of OAuth Device Authorization (RFC 8628): short-lived device code, short human user code, verification URI, polling interval, pending/expired/used states.

The v0.1 endpoints are ORC-owned control-plane endpoints so an external OAuth AS can remain independent:

- `POST /api/v1/pairings` — unauthenticated agent starts pairing.
- `POST /api/v1/pairings/approve` — authenticated user approves a user code.
- `POST /api/v1/pairings/token` — agent polls with device code and receives device credentials once.

Codes MUST be stored as hashes. Pairing rows MUST expire and MUST be one-time consumable.

## 7. Device relay protocol

Transport: WebSocket over TLS (`wss://`) except loopback development. Client-to-server WebSocket frames are masked per RFC 6455. Compression is disabled in v0.1.

Frame limit: 16 MiB. Default concurrent in-flight calls per device: 16, configurable up to 128.

### Call frame

```json
{
  "type": "call",
  "id": "128-bit-random-hex",
  "tool": "read_file",
  "args": {"path": "/home/alex/file.txt"}
}
```

### Success result

```json
{
  "type": "result",
  "id": "same-call-id",
  "ok": true,
  "result": {"encoding": "utf-8", "content": "..."}
}
```

### Error result

```json
{
  "type": "result",
  "id": "same-call-id",
  "ok": false,
  "error": {"code": "execution_error", "message": "..."}
}
```

Ping/pong frames are application-level liveness frames in addition to WebSocket control frames.

## 8. MCP tools in v0.1

| Tool | Purpose | Mutating |
|---|---|---:|
| `list_devices` | list caller-owned devices and online state | no |
| `ping` | confirm selected device is reachable | no |
| `read_file` | bounded file read with offset | no |
| `read_multiple_files` | bounded bulk reads with per-file errors | no |
| `write_file` | atomic bounded write | yes |
| `edit_block` | exact-string edit with expected replacement count | yes |
| `list_directory` | bounded directory listing | no |
| `create_directory` | create directory tree | yes |
| `move_file` | rename/move within allowed roots | yes |
| `get_file_info` | stat file/directory | no |
| `start_search` / `get_more_search_results` / `stop_search` / `list_searches` | bounded async filename/content search sessions | no |
| `start_process` | start argv or optional shell command | yes |
| `read_process_output` | cursor-based bounded process output | no |
| `interact_with_process` | write stdin | yes |
| `force_terminate` | terminate process session | yes |
| `list_sessions` | inspect agent-owned process sessions | no |
| `list_processes` | bounded OS process listing | no |
| `kill_process` | terminate arbitrary OS PID when explicitly enabled locally | yes |
| `get_config` | non-secret local policy/platform snapshot | no |

Future compatibility tools: AST/symbol-aware code search, structured-document readers/editors, configuration mutation, desktop screenshots, and explicit GUI control.

## 9. Local filesystem policy

- Agent has an explicit list of allowed roots.
- Existing paths are canonicalized with symlink evaluation before root containment checks.
- New paths are checked through their nearest existing ancestor so a symlinked parent cannot escape the allowed root.
- Writes refuse to replace a symlink directly.
- Single-file reads/writes are bounded to 4 MiB per call; multi-read responses are bounded to 8 MiB raw before encoding.
- Directory listing is bounded to 2,000 entries per call.

## 10. Process policy

- `argv` mode is preferred because it avoids a shell parser.
- Shell command mode is separately configurable (`ORC_ALLOW_SHELL`).
- Concurrent process sessions are bounded.
- Output is stored only in an in-memory ring buffer with an absolute cursor.
- Buffer size and maximum process runtime are bounded.
- Environment overrides are count/size validated.

## 11. Data model

### devices
`id`, `owner_subject`, `name`, `token_hash`, `created_at`, `last_seen_at`, `revoked_at`.

### pairings
Hashed device/user codes, device name, expiry, owner assignment, approval and consumption timestamps.

### call_audit
`owner_subject`, `device_id`, `tool_name`, `status`, `duration_ms`, `error_code`, timestamp. No args/results.

## 12. Availability and scaling

v0.1 relay ownership is in-process. A device WebSocket is owned by one server instance. MCP is stateless. The horizontal-scaling design adds a connection-ownership registry and dispatch bus while keeping payload retention transient.

The server MUST implement graceful shutdown: stop accepting new calls, fail/finish pending relay calls, close device connections, and flush metadata audit work within a bounded deadline.

## 13. Observability

Structured logs contain request IDs, tool names, duration, status, and device ID where appropriate. Secrets and tool payloads MUST be redacted/omitted. Metrics SHOULD include active devices, in-flight calls, latency histogram, call error classes, pairing outcomes, and audit-queue drops.

## 14. Performance targets

Engineering targets for a same-region server/agent connection excluding tool execution time:

- p50 relay overhead < 25 ms
- p95 relay overhead < 100 ms
- no unbounded goroutine/channel growth under disconnected clients
- bounded memory per device and per process session

These are targets, not claims, until the benchmark/load-test suite is checked in and published.

## 15. Release gates

A release cannot be tagged unless:

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `govulncheck ./...`
- lint succeeds
- MCP smoke test succeeds
- agent↔server↔tool e2e succeeds
- migration was tested on a disposable/preview Neon branch
- no secrets are present in git history
