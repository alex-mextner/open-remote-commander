#!/bin/bash
set -euo pipefail

TC="${TUNNEL_CLIENT_BIN:-$HOME/.local/bin/tunnel-client}"
MCP_URL="${ORC_MCP_URL:-http://127.0.0.1:8765/mcp}"
READY_URL="${ORC_READY_URL:-http://127.0.0.1:8765/readyz}"
ALIAS="${ORC_TUNNEL_ALIAS:-open-remote-commander}"
PROFILE="${ORC_TUNNEL_PROFILE:-open-remote-commander}"
CONFIG_DIR="$HOME/.config/open-remote-commander"
KEY_FILE="$CONFIG_DIR/openai_tunnel_runtime_key"

if [[ ! -x "$TC" ]]; then
  echo "tunnel-client not found at $TC" >&2
  exit 1
fi
if ! curl -fsS "$READY_URL" >/dev/null; then
  echo "Open Remote Commander is not ready at $READY_URL" >&2
  exit 1
fi

tunnel_id="${1:-}"
if [[ -z "$tunnel_id" ]]; then
  open "https://platform.openai.com/settings/organization/tunnels" >/dev/null 2>&1 || true
  open "https://platform.openai.com/settings/organization/api-keys" >/dev/null 2>&1 || true
  printf "Tunnel ID (tunnel_...): "
  IFS= read -r tunnel_id
fi
if [[ ! "$tunnel_id" =~ ^tunnel_[A-Za-z0-9_-]+$ ]]; then
  echo "Invalid tunnel ID" >&2
  exit 1
fi

runtime_key="${CONTROL_PLANE_API_KEY:-}"
if [[ -z "$runtime_key" ]]; then
  printf "Runtime API key (input hidden): "
  IFS= read -r -s runtime_key
  echo
fi
if [[ -z "$runtime_key" ]]; then
  echo "Runtime API key is required" >&2
  exit 1
fi

install -d -m 700 "$CONFIG_DIR"
umask 077
printf '%s\n' "$runtime_key" > "$KEY_FILE"
chmod 600 "$KEY_FILE"
unset runtime_key

"$TC" runtimes connect \
  --alias "$ALIAS" \
  --profile "$PROFILE" \
  --tunnel-id "$tunnel_id" \
  --mcp-server-url "$MCP_URL" \
  --runtime-api-key "file:$KEY_FILE"

status="$("$TC" runtimes status "$ALIAS" --json)"
printf '%s\n' "$status"
STATUS_JSON="$status" python3 - <<'PY'
import json, os, sys
data=json.loads(os.environ["STATUS_JSON"])
flat=json.dumps(data, separators=(",", ":")).lower()
required=('"process_running":true', '"healthy":true', '"ready":true')
missing=[x for x in required if x not in flat]
if missing:
    print("Tunnel runtime did not report running+healthy+ready.", file=sys.stderr)
    sys.exit(1)
print("Tunnel runtime is running, healthy, and ready.")
PY

echo
echo "Tunnel ID: $tunnel_id"
echo "Next: in ChatGPT web create a Developer Mode app, choose Connection = Tunnel,"
echo "select/paste this tunnel ID, Scan Tools, then Create."
