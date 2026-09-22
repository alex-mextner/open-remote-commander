# Device relay wire protocol v1

The protocol is intentionally smaller than MCP. MCP terminates at `orc-server`; the device connection carries only authenticated remote tool calls.

## Transport

- WebSocket, one active connection per `device_id` in v1.
- `wss://` required outside loopback.
- JSON text messages.
- Maximum logical message size: 4 MiB.
- Unknown frame types or malformed required fields close the connection.

## Frames

### `call`
Required: `type`, `id`, `tool`, `args`.

### `result`
Required: `type`, `id`. If `ok=false`, `error` is required.

### `ping` / `pong`
May include an `id`; no tool payload.

## Compatibility

The protocol version is negotiated out of band in v1 by the HTTP path (`/agent/v1/connect`). Breaking changes use a new path/version. Additive JSON fields MUST be ignored by newer tolerant decoders only when explicitly specified; security-sensitive agent decoding otherwise rejects unknown tool arguments.
