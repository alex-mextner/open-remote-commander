# Remote Desktop Commander compatibility

ORC targets workflow compatibility, not private implementation compatibility.

| Capability | ORC v0.1 | Notes |
|---|---|---|
| Remote MCP endpoint | ✅ | HTTP/JSON-RPC gateway supports MCP 2026-07-28 + 2025-11-25 compatibility |
| Multiple paired devices | ✅ core model | device registry + live relay hub |
| File read/write/list/info | ✅ | bounded + root policy |
| Process start/read/stdin/kill | ✅ | bounded sessions |
| Device pairing | ✅ | one-time device/user codes + persisted device credential |
| OAuth resource protection | ✅ verifier | dev HMAC + production introspection |
| Device dashboard | ✅ | dependency-free Vercel app; pair/list/revoke |
| File/content search | ✅ | bounded async sessions with pagination; no ripgrep dependency |
| Read multiple files | ✅ | bounded total/per-file reads |
| Exact edit block | ✅ | expected-replacement safety check |
| AST/symbol-aware code search | ⏳ | planned |
| Process/session inspection | ✅ | agent sessions + bounded OS process list; arbitrary kill is opt-in |
| Non-secret agent config | ✅ | read-only policy/platform snapshot |
| GUI/screenshot control | ⏳ | separate higher-risk capability |
| Private relay | ✅ | loopback relay + OpenAI Secure MCP Tunnel profile; container deployment also supported |

Remote Desktop Commander documentation and its open-source local engine are used only as behavioral/reference inputs. ORC's hosted relay, protocol, types and implementation are independent and open source.
