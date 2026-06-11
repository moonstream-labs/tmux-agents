#!/usr/bin/env bash
# agents.tmux -- TPM entry point for tmux-agents
# Registers keybindings and ensures the Go status server is running.

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPTS_DIR="$CURRENT_DIR/scripts"

source "$SCRIPTS_DIR/helpers.sh"

# --- Cleanup stale options from tmux-opencode ---
tmux set-option -gu "@opencode-hosts-json" 2>/dev/null || true
tmux set-option -gu "@opencode-panes" 2>/dev/null || true
tmux set-option -gu "@opencode-recent" 2>/dev/null || true
tmux set-option -gu "@opencode-pill" 2>/dev/null || true
tmux set-option -gu "@opencode-gen" 2>/dev/null || true
tmux set-option -gu "@opencode-daemon-ts" 2>/dev/null || true

# --- Stop old bash daemon if running ---
OLD_STATE_DIR="/tmp/tmux-opencode-$(id -u)"
if [[ -f "$OLD_STATE_DIR/daemon.pid" ]]; then
    OLD_PID=$(cat "$OLD_STATE_DIR/daemon.pid" 2>/dev/null)
    if [[ -n "$OLD_PID" && "$OLD_PID" =~ ^[0-9]+$ ]]; then
        kill "$OLD_PID" 2>/dev/null || true
    fi
    rm -f "$OLD_STATE_DIR/daemon.pid"
fi

# --- Keybinding ---
# Pass the triggering client to the launcher. run-shell expands #{...} formats in
# its command, so the picker can act on the exact client/session the popup was
# opened from instead of tmux's ambiguous "current" client (which misroutes when
# multiple clients are attached). client_name is a pty path with no spaces.
POPUP_KEY=$(get_tmux_option "$AGENTS_POPUP_KEY_OPTION" "$AGENTS_POPUP_KEY_DEFAULT")
tmux bind-key "$POPUP_KEY" run-shell -b "$SCRIPTS_DIR/navigator.sh '#{client_name}'"

# --- Last-window toggle (opt-in; hook-driven, independent of the server) ---
# Binds a "jump to the previously-focused window across sessions" toggle and
# registers the focus-tracking hooks. The bind is idempotent (overwrite); the
# hooks are appended (-ga) once per server, guarded by a sentinel option so a
# config reload (which re-sources this file) cannot stack duplicate hooks. The
# sentinel clears on server restart, exactly when the hooks themselves reset.
LAST_WINDOW_KEY=$(get_tmux_option "$AGENTS_LAST_WINDOW_KEY_OPTION" "$AGENTS_LAST_WINDOW_KEY_DEFAULT")
if [[ -n "$LAST_WINDOW_KEY" ]]; then
    tmux bind-key "$LAST_WINDOW_KEY" run-shell -b "$SCRIPTS_DIR/last-window.sh jump"
    if [[ "$(tmux show-option -gqv @agents-lastwin-hooked)" != "1" ]]; then
        tmux set-hook -ga session-window-changed "run-shell -b '$SCRIPTS_DIR/last-window.sh track'"
        tmux set-hook -ga client-session-changed "run-shell -b '$SCRIPTS_DIR/last-window.sh track'"
        tmux set-option -g @agents-lastwin-hooked 1
    fi
fi

# --- Active-session navigation (opt-in) ------------------------------------
# prev/next step through the active agent sessions in stable %N order. Binds are
# idempotent (overwrite). Default key table is "root" (-n): no prefix, intercepts
# before the focused pane -- required for the dev1 Cmd+[/] case, where the keys
# arrive as C-S-,/. and must beat any in-pane consumer. Set @agents-nav-key-table
# to "prefix" where a dedicated no-prefix key can't be emitted.
NAV_PREV_KEY=$(get_tmux_option "$AGENTS_NAV_PREV_KEY_OPTION" "$AGENTS_NAV_PREV_KEY_DEFAULT")
NAV_NEXT_KEY=$(get_tmux_option "$AGENTS_NAV_NEXT_KEY_OPTION" "$AGENTS_NAV_NEXT_KEY_DEFAULT")
NAV_KEY_TABLE=$(get_tmux_option "$AGENTS_NAV_KEY_TABLE_OPTION" "$AGENTS_NAV_KEY_TABLE_DEFAULT")
nav_root=()
[[ "$NAV_KEY_TABLE" == "root" ]] && nav_root=(-n)
if [[ -n "$NAV_PREV_KEY" ]]; then
    tmux bind-key "${nav_root[@]}" "$NAV_PREV_KEY" run-shell -b "$SCRIPTS_DIR/navigate.sh prev '#{client_name}'"
fi
if [[ -n "$NAV_NEXT_KEY" ]]; then
    tmux bind-key "${nav_root[@]}" "$NAV_NEXT_KEY" run-shell -b "$SCRIPTS_DIR/navigate.sh next '#{client_name}'"
fi

# --- Ensure Go server is running ---
ensure_server_running || true

# --- Status line (optional auto-append mode) ---
AUTO_STATUS_RIGHT=$(get_tmux_option "$AGENTS_AUTO_STATUS_RIGHT_OPTION" "$AGENTS_AUTO_STATUS_RIGHT_DEFAULT")
if [[ "$AUTO_STATUS_RIGHT" == "on" || "$AUTO_STATUS_RIGHT" == "yes" || "$AUTO_STATUS_RIGHT" == "true" ]]; then
    CURRENT_STATUS_RIGHT=$(tmux show-option -gqv "status-right")

    OC_FRAGMENT="#($SCRIPTS_DIR/status_opencode.sh)"
    CC_FRAGMENT="#($SCRIPTS_DIR/status_claude.sh)"
    CX_FRAGMENT="#($SCRIPTS_DIR/status_codex.sh)"

    if [[ "$CURRENT_STATUS_RIGHT" != *"$OC_FRAGMENT"* ]]; then
        CURRENT_STATUS_RIGHT="${CURRENT_STATUS_RIGHT} ${OC_FRAGMENT}"
    fi
    if [[ "$CURRENT_STATUS_RIGHT" != *"$CX_FRAGMENT"* ]]; then
        CURRENT_STATUS_RIGHT="${CURRENT_STATUS_RIGHT} ${CX_FRAGMENT}"
    fi
    if [[ "$CURRENT_STATUS_RIGHT" != *"$CC_FRAGMENT"* ]]; then
        CURRENT_STATUS_RIGHT="${CURRENT_STATUS_RIGHT} ${CC_FRAGMENT}"
    fi

    tmux set-option -g status-right "$CURRENT_STATUS_RIGHT"
fi
