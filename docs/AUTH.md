# Authentication and identity

ORC deliberately separates three credentials:

1. **User/MCP access token** — identifies the human/account and authorizes MCP/control-plane calls.
2. **Pairing device code + user code** — short-lived one-time bootstrap credentials.
3. **Device token** — long-lived random bearer secret for one paired machine.

Reusing one credential class for another is forbidden.

## Development mode

`ORC_AUTH_MODE=dev` uses `cmd/orc-token` and an HMAC key. It exists so the complete resource/relay path can be exercised locally without setting up an authorization server.

Requirements:

- server binds to loopback unless you intentionally put TLS in front of it;
- `ORC_HMAC_SECRET` is at least 32 random bytes;
- access tokens are audience-bound to `ORC_AUTH_AUDIENCE` and carry `orc:tools`.

Development HMAC tokens are not a production identity system.

## Production mode: token introspection

Set:

```text
ORC_AUTH_MODE=introspection
ORC_AUTH_ISSUER=https://auth.example.com
ORC_AUTH_AUDIENCE=https://orc.example.com/mcp
ORC_INTROSPECTION_URL=https://auth.example.com/oauth2/introspect
ORC_INTROSPECTION_CLIENT_ID=...
ORC_INTROSPECTION_CLIENT_SECRET=...
```

The verifier requires:

- HTTPS introspection endpoint;
- `active: true`;
- non-empty stable `sub`;
- future `exp`;
- exact configured resource audience in `aud`;
- `orc:tools` scope for MCP/control-plane operations.

The server publishes OAuth Protected Resource Metadata for MCP discovery. The configured authorization server remains responsible for authorization-code/PKCE login, consent, client registration policy, MFA, refresh tokens and session security.

## Why ORC does not roll its own OAuth server

OAuth authorization servers are a separate high-risk security product. ORC is compatible with standards-compliant providers rather than shipping a lightly reviewed password/session implementation inside the relay. A self-hosted deployment can pair ORC with an open-source OAuth/OIDC server; hosted deployments may use a managed one.

## Neon Auth

The reference Neon `dev` branch can host **dashboard identity** through Neon Auth. That does not automatically make Neon Auth the MCP authorization server or guarantee the MCP resource audience/scopes expected above. Treat dashboard identity and MCP authorization as separate until an explicit, tested subject-mapping and token-audience design is configured.

## Pairing secret

`ORC_PAIRING_SECRET` is a separate random server secret. Human pairing codes have low entropy by design, so the database stores `HMAC-SHA256(pairing_secret, normalized_user_code)` rather than a plain SHA-256 hash. Device codes and device tokens are high entropy and are stored as SHA-256 hashes.

## Private OpenAI tunnel mode

For a developer machine connected through OpenAI Secure MCP Tunnel, the MCP
listener can remain private on loopback and avoid a separate browser OAuth
flow for the MCP endpoint:

    ORC_LISTEN_ADDR=127.0.0.1:8765
    ORC_PUBLIC_BASE_URL=http://127.0.0.1:8765
    ORC_MCP_TRUST_LOOPBACK=true
    ORC_MCP_TRUSTED_SUBJECT=<stable-local-subject>

This mode is rejected at configuration load time if the listener is not an
explicit loopback address. At startup ORC creates a random in-memory bearer.
Only unauthenticated loopback requests to /mcp receive that bearer through a
local middleware; it is never written to disk.

The pairing/device control APIs continue to use the configured dev or
introspection verifier. Protected Resource Metadata is not published in trusted
loopback mode so tunnel-client treats the local MCP target as intentionally
no-auth.

Do not use trusted loopback mode behind a reverse proxy or a non-loopback
listener. Public deployments must use normal resource-server authentication.
