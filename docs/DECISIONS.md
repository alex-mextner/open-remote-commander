# Architecture decision log

## ADR-001 — Go for agent and server
Accepted. One memory-safe, static-binary-friendly language keeps agent and relay operationally simple. Go's standard library covers HTTP/TLS/process/filesystem primitives well, and race/fuzz tooling is first-class.

## ADR-002 — Keep execution exclusively on the agent
Accepted. The cloud service routes typed calls only. This is the most important blast-radius boundary in the system.

## ADR-003 — Neon for durable state
Accepted for reference deployment. Postgres is used for identity, pairing, revocation and metadata audit. Preview branches make migration testing cheap and isolated.

## ADR-004 — Vercel only for control-plane UI/stateless pieces
Accepted. Long-lived device sockets remain on a normal Go service/container where ownership, draining and backpressure are explicit. The dashboard can deploy on Vercel independently.

## ADR-005 — OAuth protected-resource model
Accepted. Production MCP tokens are externally issued and audience-bound. v0.1 supports introspection; local HMAC auth exists only for development.
