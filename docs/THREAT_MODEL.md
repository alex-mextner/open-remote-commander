# Threat model

## Assets

- local files and credentials reachable by the OS user
- process execution authority
- device bearer tokens
- MCP/OAuth access tokens
- ownership mapping between users and devices
- audit metadata

## Primary threats and controls

| Threat | Control |
|---|---|
| Cross-tenant device invocation | ownership check before online check/dispatch; stable OAuth subject |
| Device-token database leak | store only SHA-256 token hash; high-entropy random token |
| Symlink path escape | canonicalize existing paths and existing ancestors before containment check |
| Malicious huge payload | HTTP/WebSocket/read/write/process limits; no WS compression |
| Goroutine/memory exhaustion | bounded in-flight semaphore, process count and ring buffers |
| Credential leakage in logs | structured metadata logs; never log Authorization headers or tool payloads |
| Replay of pairing code | expiry + one-time consume + hashed codes |
| Stolen browser/MCP session | rely on authorization server MFA/session security; narrow audience/scope |
| Relay compromise | server has no local shell; payload persistence disabled by design |
| Agent compromise | equivalent to local user compromise; mitigate with OS account/container/VM |

## Deliberate non-boundaries

Allowed roots and shell disabling reduce accidents and blast radius but are not a sandbox against a hostile process already running as the same user. Strong isolation requires a separate OS user, container, sandbox or VM.
