# Deployment runbook

This document describes the reference deployment shape. It is intentionally split because the dashboard and stateful relay have different runtime needs.

## 1. Neon Postgres

Create a Neon project in the region nearest the relay. Use a preview/development branch first.

- Application traffic: pooled `DATABASE_URL` is acceptable because ORC's reference store uses Neon HTTP SQL.
- Manual/schema migrations: use a direct/unpooled database connection.
- Apply `migrations/001_init.sql` to a preview branch and run the integration/smoke checks before production.
- Protect production branch/database access according to your deployment network model.

The durable application schema stores devices, pairing state and metadata-only audit rows. Tool arguments/results are not columns in that schema.

## 2. Go relay/server

The v0.1 relay has an in-memory live-connection hub, so run **one relay replica**. A generic container platform/VM is the reference runtime:

```bash
docker build -t open-remote-commander .
docker run --rm -p 8080:8080 --env-file .env open-remote-commander
```

For internet exposure:

- terminate TLS at the platform/load balancer;
- set `ORC_PUBLIC_BASE_URL=https://...`;
- set production OAuth/introspection variables from `docs/AUTH.md`;
- set a high-entropy `ORC_PAIRING_SECRET`;
- use a Neon `DATABASE_URL`;
- health check `/healthz`; readiness `/readyz`;
- preserve WebSocket upgrade support and a connection idle timeout comfortably above the agent heartbeat.

Do not scale the current relay horizontally behind a round-robin balancer: an MCP request could land on an instance that does not own the target device socket. Horizontal scaling requires the distributed connection-ownership/dispatch design described in `ARCHITECTURE.md`.

## 3. Vercel dashboard

Deploy the **`web/` directory as its own Vercel project**. It is static and has no build dependencies.

After Vercel assigns the dashboard origin, add that exact origin to the relay's `ORC_ALLOWED_ORIGINS` and restart/redeploy the relay. The dashboard sends bearer-authenticated API requests directly to the relay.

The engineering-preview dashboard intentionally keeps the bearer access token in JavaScript memory only; refresh/close clears it. A first-class OAuth login flow is a follow-up, not a reason to persist tokens in localStorage.

## 4. Agent

On a target machine:

```bash
ORC_SERVER_URL=https://orc.example.com \
ORC_ALLOWED_ROOTS="$HOME" \
./orc-agent
```

With no saved device credential the agent starts pairing, displays a code and verification URL, polls until approval, stores the issued device token in its user config directory, then opens the outbound WebSocket connection.

For higher-risk automation, run the agent under a dedicated OS account, container or VM. `ORC_ALLOW_SHELL=false` is the safe default; prefer `argv` process execution.

## 5. Release checklist

1. `make check`, `make vuln`, `make lint`, `make fuzz`.
2. Run MCP protocol smoke test against a real official SDK/client.
3. Run agent → relay → filesystem/process e2e.
4. Validate migration on a disposable Neon branch.
5. Scan the repository and built artifacts for secrets.
6. Tag only from a clean `main` commit.
7. Verify release checksums/artifacts and smoke-test at least macOS arm64 + Linux amd64 before announcing.
