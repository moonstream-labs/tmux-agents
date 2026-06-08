#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"
SERVER_DIR="$REPO_DIR/server"

echo "=== Building tmux-agent-server ==="
cd "$SERVER_DIR"
go build -o tmux-agent-server ./cmd/tmux-agent-server
echo "  Built: $SERVER_DIR/tmux-agent-server"

echo "=== Installing binary ==="
install -m 755 "$SERVER_DIR/tmux-agent-server" "$HOME/.local/bin/tmux-agent-server"
echo "  Installed: ~/.local/bin/tmux-agent-server"

echo "=== Installing systemd service ==="
mkdir -p "$HOME/.config/systemd/user"
install -m 644 "$REPO_DIR/tmux-agents.service" "$HOME/.config/systemd/user/tmux-agents.service"
systemctl --user daemon-reload
systemctl --user enable --now tmux-agents.service
echo "  Service enabled and started"

echo ""
echo "=== Next steps ==="
echo ""
echo "1. Add Claude Code hooks to ~/.claude/settings.json:"
echo '   (merge into existing "hooks" object if present)'
echo ""
cat << 'HOOKS'
{
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "http", "url": "http://127.0.0.1:7077/claude/hook", "async": true}]}],
    "UserPromptSubmit": [{"hooks": [{"type": "http", "url": "http://127.0.0.1:7077/claude/hook", "async": true}]}],
    "PreToolUse": [{"hooks": [{"type": "http", "url": "http://127.0.0.1:7077/claude/hook", "async": true}]}],
    "PermissionRequest": [{"hooks": [{"type": "http", "url": "http://127.0.0.1:7077/claude/hook", "async": true}]}],
    "Notification": [{"matcher": "permission_prompt", "hooks": [{"type": "http", "url": "http://127.0.0.1:7077/claude/hook", "async": true}]}],
    "Stop": [{"hooks": [{"type": "http", "url": "http://127.0.0.1:7077/claude/hook", "async": true}]}],
    "SessionEnd": [{"hooks": [{"type": "http", "url": "http://127.0.0.1:7077/claude/hook", "async": true}]}]
  }
}
HOOKS
echo ""
echo "1b. (Codex) Add Codex lifecycle hooks to ~/.codex/hooks.json:"
echo '   (merge into existing "hooks" object if present)'
echo ""
CODEX_SHIM="$REPO_DIR/scripts/codex-hook.sh"
cat <<CODEXHOOKS
{
  "hooks": {
    "SessionStart":      [{ "hooks": [{ "type": "command", "command": "$CODEX_SHIM" }] }],
    "UserPromptSubmit":  [{ "hooks": [{ "type": "command", "command": "$CODEX_SHIM" }] }],
    "PreToolUse":        [{ "hooks": [{ "type": "command", "command": "$CODEX_SHIM" }] }],
    "PermissionRequest": [{ "hooks": [{ "type": "command", "command": "$CODEX_SHIM" }] }],
    "PostToolUse":       [{ "hooks": [{ "type": "command", "command": "$CODEX_SHIM" }] }],
    "Stop":              [{ "hooks": [{ "type": "command", "command": "$CODEX_SHIM" }] }]
  }
}
CODEXHOOKS
echo ""
echo "   Then start Codex and run /hooks to review and trust the new hook"
echo "   (or launch once with --dangerously-bypass-hook-trust)."
echo ""
echo "2. Source the shell integration in your .zshrc or .bashrc:"
echo "   source $REPO_DIR/scripts/shell-integration.sh"
echo ""
echo "3. Update tmux.conf:"
echo "   set -g @plugin 'moonstream-labs/tmux-agents'"
echo "   # Replace status module paths:"
echo "   #   order (left->right): status_opencode.sh, status_codex.sh, status_claude.sh"
echo ""
echo "4. Reload tmux config: tmux source-file ~/.config/tmux/tmux.conf"
echo ""
