#!/usr/bin/env bash
# codex-hook.sh -- forward a Codex lifecycle hook event to the tmux-agents server.
#
# Wire this as a Codex "command" hook (see .codex/hooks.json). Codex passes the
# event JSON on stdin; this script forwards it to POST /codex/hook and adds the
# pane id from $TMUX_PANE so the server can correlate the session to its pane.
#
# Notes:
#   - Codex command hooks run synchronously, so the curl is detached and the
#     script returns immediately to avoid blocking the agent's turn.
#   - stdout is kept empty: Codex warns when a hook prints non-JSON.

server="${TMUX_AGENTS_SERVER:-http://127.0.0.1:7077}"
payload="$(cat)"

(
  curl -s --connect-timeout 1 --max-time 3 \
    -X POST "$server/codex/hook" \
    -H 'Content-Type: application/json' \
    -H "X-Tmux-Pane: ${TMUX_PANE:-}" \
    --data-raw "$payload" >/dev/null 2>&1 &
)

exit 0
