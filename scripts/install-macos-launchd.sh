#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN_DIR="$HOME/.local/bin"
CFG_DIR="$HOME/.config/open-remote-commander"
LOG_DIR="$HOME/Library/Logs/OpenRemoteCommander"
LAUNCH_DIR="$HOME/Library/LaunchAgents"
LABEL_PREFIX="${ORC_LAUNCHD_PREFIX:-app.openremotecommander}"

for f in server.env agent.env database_url; do
  if [[ ! -s "$CFG_DIR/$f" ]]; then
    echo "Missing $CFG_DIR/$f; configure ORC before installing launchd services." >&2
    exit 1
  fi
done

install -d -m 700 "$BIN_DIR" "$CFG_DIR" "$LOG_DIR" "$LAUNCH_DIR"
chmod 600 "$CFG_DIR/server.env" "$CFG_DIR/agent.env" "$CFG_DIR/database_url"

mkdir -p "$ROOT/.artifacts/bin"
cd "$ROOT"
go build -trimpath -o .artifacts/bin/orc-server ./cmd/orc-server
go build -trimpath -o .artifacts/bin/orc-agent ./cmd/orc-agent
go build -trimpath -o .artifacts/bin/orc-token ./cmd/orc-token
install -m 0755 .artifacts/bin/orc-server "$BIN_DIR/orc-server"
install -m 0755 .artifacts/bin/orc-agent "$BIN_DIR/orc-agent"
install -m 0755 .artifacts/bin/orc-token "$BIN_DIR/orc-token"

cat > "$BIN_DIR/orc-server-run" <<'EOF'
#!/bin/bash
set -euo pipefail
set -a
source "$HOME/.config/open-remote-commander/server.env"
set +a
export DATABASE_URL="$(cat "$HOME/.config/open-remote-commander/database_url")"
exec "$HOME/.local/bin/orc-server"
EOF

cat > "$BIN_DIR/orc-agent-run" <<'EOF'
#!/bin/bash
set -euo pipefail
set -a
source "$HOME/.config/open-remote-commander/agent.env"
set +a
exec "$HOME/.local/bin/orc-agent"
EOF
chmod 700 "$BIN_DIR/orc-server-run" "$BIN_DIR/orc-agent-run"

server_label="$LABEL_PREFIX.server"
agent_label="$LABEL_PREFIX.agent"
server_plist="$LAUNCH_DIR/$server_label.plist"
agent_plist="$LAUNCH_DIR/$agent_label.plist"
write_plist() {
  local label="$1" program="$2" out="$3" stem="$4"
  cat > "$out" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key>
  <array><string>$program</string></array>
  <key>WorkingDirectory</key><string>$ROOT</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>5</integer>
  <key>StandardOutPath</key><string>$LOG_DIR/$stem.out.log</string>
  <key>StandardErrorPath</key><string>$LOG_DIR/$stem.err.log</string>
</dict>
</plist>
EOF
  chmod 600 "$out"
  plutil -lint "$out" >/dev/null
}

write_plist "$server_label" "$BIN_DIR/orc-server-run" "$server_plist" server
write_plist "$agent_label" "$BIN_DIR/orc-agent-run" "$agent_plist" agent

uid="$(id -u)"
launchctl bootout "gui/$uid/$agent_label" >/dev/null 2>&1 || true
launchctl bootout "gui/$uid/$server_label" >/dev/null 2>&1 || true
launchctl bootstrap "gui/$uid" "$server_plist"
for _ in {1..50}; do
  if curl -fsS http://127.0.0.1:8765/readyz >/dev/null 2>&1; then
    break
  fi
  sleep .2
done
curl -fsS http://127.0.0.1:8765/readyz >/dev/null
launchctl bootstrap "gui/$uid" "$agent_plist"

echo "Installed:"
echo "  $server_label"
echo "  $agent_label"
echo "Logs: $LOG_DIR"
echo "Status:"
launchctl print "gui/$uid/$server_label" | grep -E 'state =|pid =' | head -2 || true
launchctl print "gui/$uid/$agent_label" | grep -E 'state =|pid =' | head -2 || true
