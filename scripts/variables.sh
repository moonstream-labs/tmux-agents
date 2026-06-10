#!/usr/bin/env bash
# variables.sh -- Option names and defaults for tmux-agents

# Double-source guard
[[ -n "${_AGENTS_VARIABLES_LOADED:-}" ]] && return
_AGENTS_VARIABLES_LOADED=1

# --- Keybind ---
AGENTS_POPUP_KEY_OPTION="@agents-popup-key"
AGENTS_POPUP_KEY_DEFAULT="o"

# --- Last-window toggle key (opt-in: empty = no binding) ---
AGENTS_LAST_WINDOW_KEY_OPTION="@agents-last-window-key"
AGENTS_LAST_WINDOW_KEY_DEFAULT=""

# --- Popup styling ---
AGENTS_POPUP_WIDTH_OPTION="@agents-popup-width"
AGENTS_POPUP_WIDTH_DEFAULT="70%"

AGENTS_POPUP_HEIGHT_OPTION="@agents-popup-height"
AGENTS_POPUP_HEIGHT_DEFAULT="50%"

AGENTS_POPUP_BORDER_OPTION="@agents-popup-border"
AGENTS_POPUP_BORDER_DEFAULT="rounded"

AGENTS_POPUP_BG_OPTION="@agents-popup-bg"
AGENTS_POPUP_BG_DEFAULT="#080909"

AGENTS_POPUP_FG_OPTION="@agents-popup-fg"
AGENTS_POPUP_FG_DEFAULT="#dadada"

AGENTS_AUTO_STATUS_RIGHT_OPTION="@agents-auto-status-right"
AGENTS_AUTO_STATUS_RIGHT_DEFAULT="off"

# --- Generation counter (shared across both tools) ---
AGENTS_GEN_OPTION="@agents-gen"

# --- Per-tool pill options ---
AGENTS_CLAUDE_PILL_OPTION="@agents-claude-pill"
AGENTS_OPENCODE_PILL_OPTION="@agents-opencode-pill"

# --- Server ---
AGENTS_SERVER_URL="${TMUX_AGENTS_SERVER:-http://127.0.0.1:7077}"

# --- Paths ---
AGENTS_STATE_DIR="${AGENTS_STATE_DIR:-/tmp/tmux-agents-$(id -u)}"
AGENTS_STATE_DB_PATH="${AGENTS_STATE_DB_PATH:-$AGENTS_STATE_DIR/state.db}"
