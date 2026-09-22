# Connect Open Remote Commander to ChatGPT

This runbook was validated on 2026-09-22 with OpenAI Secure MCP Tunnel and
tunnel-client v0.0.14.

## Availability

ChatGPT custom MCP apps are configured on ChatGPT web in Developer Mode.
As of 2026-09-22, full MCP including write/modify actions is available to
Business, Enterprise, and Edu workspaces. Pro can connect developer-mode MCP
apps with read/fetch permissions, but full write MCP is not enabled there.

Official references:

- https://developers.openai.com/api/docs/guides/secure-mcp-tunnels
- https://help.openai.com/en/articles/12584461-developer-mode-and-mcp-apps-in-chatgpt

## Why use Secure MCP Tunnel

For a developer machine, keep orc-server bound to loopback. Secure MCP Tunnel
opens an outbound connection to OpenAI and does not expose an inbound port.

## One-time OpenAI Platform setup

1. In Platform tunnel settings, create a tunnel and associate it with the
   ChatGPT workspace that will use the app.
2. Ensure the operator/runtime principal has Tunnels Read + Use.
   Creating/editing the tunnel additionally requires Tunnels Read + Manage.
3. Create a separate runtime API key. Do not use an admin key for the daemon.
4. Keep the returned tunnel_... identifier and runtime key local.

Platform pages:

- https://platform.openai.com/settings/organization/tunnels
- https://platform.openai.com/settings/organization/api-keys

On the Mac, run:

    orc-tunnel-setup

The helper opens the two Platform pages, prompts for the tunnel ID, reads the
runtime key without echoing it, stores the key in a 0600 file, and starts a
managed tunnel-client runtime against http://127.0.0.1:8765/mcp.

## Verify the tunnel

    tunnel-client runtimes status open-remote-commander --json
    curl -fsS http://127.0.0.1:8765/readyz

Do not consider the tunnel ready until runtime status reports the process
running and both health/readiness are true.

## Create the ChatGPT app

1. Open ChatGPT web. The tunnel-client guide links directly to
   https://chatgpt.com/#settings/Connectors for tunnel selection.
2. Ensure Developer Mode is enabled for your account/workspace. Business
   admins/owners can use Workspace settings → Apps → Create; Enterprise/Edu
   can also grant developer access by RBAC and enabled users can use
   Settings → Apps → Advanced Settings.
3. Choose Apps → Create for a developer-mode custom app.
4. Set Connection = Tunnel.
5. Select the Open Remote Commander tunnel or paste its tunnel_... ID.
6. Click Scan Tools and review the discovered actions.
7. Create/enable the app.
8. In a chat, select the app for the message or @mention it when a new tool
   call is needed.

Custom MCP apps are web-only today. Agent mode does not invoke custom apps;
deep research only uses read/fetch actions. After a published app's tool
definitions change, an admin must refresh/review the actions before ChatGPT
uses the updated snapshot.

## ORC tunnel trust boundary

ORC_MCP_TRUST_LOOPBACK=true is intentionally narrow:

- orc-server refuses it unless ORC_LISTEN_ADDR explicitly names a loopback host;
- only the MCP route gets the generated in-memory trusted bearer;
- control-plane device/pairing APIs keep their normal bearer authentication;
- OAuth Protected Resource Metadata is not published in this mode, matching the
  no-auth local MCP profile expected by tunnel-client;
- the trusted subject is configured with ORC_MCP_TRUSTED_SUBJECT.

For a publicly reachable relay, disable this mode and use production OAuth
introspection instead.
