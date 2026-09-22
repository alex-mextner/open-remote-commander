# Agent instructions

Before changing Go code, read `.agents/skills/go-engineering/SKILL.md`. Before touching auth, relay, filesystem/process execution, or dependencies, also read `.agents/skills/security-review/SKILL.md`. For MCP/device protocol work read `.agents/skills/remote-mcp/SKILL.md`.

Keep changes atomic. Add or update tests before fixing security- or behavior-sensitive code. Do not weaken path containment, size/concurrency limits, TLS requirements, authorization checks, or content-retention defaults to make a test pass.

The hosted relay and device agent are separate trust boundaries. Never move arbitrary command execution into the cloud service.
