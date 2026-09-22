---
name: security-review
description: Security review checklist for auth, relay, filesystem/process execution, dependencies and deployment.
---

# Security review

Treat every MCP tool call as attacker-controlled structured input even after authentication.

Mandatory invariants:

- Prove device ownership before online-state lookup or dispatch.
- Never log bearer tokens, pairing codes, tool args, file contents, terminal output or tool results.
- Store device/pairing secrets only as hashes; compare secrets in constant time.
- Require TLS outside loopback; validate configured URLs on startup.
- Canonicalize symlinks/ancestors before allowed-root checks; refuse direct symlink overwrite.
- Bound frame/body/header sizes, list counts, reads/writes, process buffers, process count and in-flight calls.
- Prefer argv execution; shell mode is explicit and documented as privileged.
- Cloud components never execute arbitrary local commands.
- Production access tokens require subject, expiration, scope and exact resource audience.
- Database audit is metadata-only by default.

For dependency changes, prefer standard library or widely reviewed upstream packages, pin versions, run `govulncheck`, and document why the dependency is needed.
