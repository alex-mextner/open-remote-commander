# HTTP API v1

All JSON responses use UTF-8. Error bodies use `{ "error": { "code": "...", "message": "..." } }`.

## Health

- `GET /healthz` — process liveness.
- `GET /readyz` — dependency readiness.

## Pairing

### `POST /api/v1/pairings`
Unauthenticated device bootstrap. Body: `PairingStartRequest`.

### `POST /api/v1/pairings/approve`
Requires user OAuth bearer token. Body: `PairingApproveRequest`.

### `POST /api/v1/pairings/token`
Unauthenticated polling endpoint. Body: `PairingTokenRequest`. Returns 428/pending semantics until approved, 410 when expired/used, and the device token once on success.

## Agent connection

`GET /agent/v1/connect?device_id=...` with `Authorization: Bearer <device-token>` upgrades to WebSocket.

## MCP

`POST /mcp` (and the streamable GET form supported by the SDK) is the MCP resource endpoint and requires OAuth bearer authentication.
