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
POPUP_KEY=$(get_tmux_option "$AGENTS_POPUP_KEY_OPTION" "$AGENTS_POPUP_KEY_DEFAULT")
tmux bind-key "$POPUP_KEY" run-shell -b "$SCRIPTS_DIR/navigator.sh"

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
