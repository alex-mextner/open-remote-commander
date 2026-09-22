---
name: remote-mcp
description: MCP and device-relay compatibility rules for Open Remote Commander.
---

# Remote MCP rules

- MCP terminates at `orc-server`; the agent protocol is a smaller independent wire protocol.
- Prefer the official `modelcontextprotocol/go-sdk` for the public MCP endpoint.
- Support Streamable HTTP and Protected Resource Metadata required by current MCP authorization guidance.
- MCP access tokens identify users; device tokens identify machines. Never reuse one token class for the other.
- Device WebSockets are outbound from the controlled machine, one current owner connection per device ID.
- Calls use random IDs and bounded pending maps. Closing a socket must immediately fail all pending calls.
- Breaking device-protocol changes get a new `/agent/vN/` endpoint; do not silently reinterpret old frames.
- New tools require: public JSON type, MCP schema/handler, executor implementation, tests, security review and compatibility docs.
