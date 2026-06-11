#!/usr/bin/env bash
# helpers.sh -- Shared utilities for tmux-agents

[[ -n "${_AGENTS_HELPERS_LOADED:-}" ]] && return
_AGENTS_HELPERS_LOADED=1

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$CURRENT_DIR/variables.sh"

get_tmux_option() {
    local option="$1"
    local default_value="$2"
    local value
    value=$(tmux show-option -gqv "$option" 2>/dev/null)
    echo "${value:-$default_value}"
}

ensure_state_dir() {
    mkdir -p "$AGENTS_STATE_DIR"
}

get_state_db_path() {
    echo "$AGENTS_STATE_DB_PATH"
}

format_age() {
    local epoch="$1"
    local now
    now=$(date +%s)
    local diff=$(( now - epoch ))

    if (( diff < 60 )); then
        echo "${diff}s ago"
    elif (( diff < 3600 )); then
        echo "$(( diff / 60 ))m ago"
    elif (( diff < 86400 )); then
        echo "$(( diff / 3600 ))h ago"
    else
        echo "$(( diff / 86400 ))d ago"
    fi
}

server_is_healthy() {
    curl -sf "$AGENTS_SERVER_URL/healthz" >/dev/null 2>&1
}

ensure_server_running() {
    if server_is_healthy; then
        return 0
    fi

    # Try to start via systemd.
    systemctl --user start tmux-agents.service 2>/dev/null || true

    # Wait up to 3 seconds for healthz.
    local i
    for i in {1..6}; do
        sleep 0.5
        if server_is_healthy; then
            return 0
        fi
    done

    return 1
}

# agents_switch_to_pane <pane-target> [client]
# Switch <client> (or the current client when empty) to an existing pane in one
# step: switch-client -t accepts a pane target (one containing ':', '.', or '%')
# and changes session+window+pane together, moving only that client -- no
# preparatory global window/pane selection in the destination. -Z preserves a
# zoomed destination so the jump matches what the popup shows.
#
# Both nav paths -- the popup's selection (navigate_to_pane in
# _navigator_picker.sh) and the prev/next keys (navigate.sh) -- MUST route
# through here so the two cannot diverge.
agents_switch_to_pane() {
    local target="$1" client="${2:-}"
    local cflag=()
    [[ -n "$client" ]] && cflag=(-c "$client")
    tmux switch-client "${cflag[@]}" -Z -t "$target" 2>/dev/null || true
}
