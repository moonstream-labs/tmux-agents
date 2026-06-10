#!/usr/bin/env bash
# navigator.sh -- Popup launcher for the agent session navigator.
#
# $1 is the triggering client name, expanded by tmux from #{client_name} in the
# run-shell keybinding. The picker uses the captured client/session/cwd so it
# acts on the exact context the popup was launched from -- not tmux's ambiguous
# "current" target, which misroutes when several clients are attached.

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$CURRENT_DIR/helpers.sh"

# --- Capture the invoking context up front, before ensure_server_running can
# sleep on a cold start (which would let focus drift). Prefer the client passed
# by the keybinding; fall back to tmux's current-client resolution. Session and
# cwd are derived *from that client* so they stay consistent with it. ---
SRC_CLIENT="${1:-}"
[[ -z "$SRC_CLIENT" ]] && SRC_CLIENT=$(tmux display-message -p '#{client_name}' 2>/dev/null)

src_q=()
[[ -n "$SRC_CLIENT" ]] && src_q=(-c "$SRC_CLIENT")
SRC_SESSION=$(tmux display-message "${src_q[@]}" -p '#{session_name}' 2>/dev/null)
SRC_PATH=$(tmux display-message "${src_q[@]}" -p '#{pane_current_path}' 2>/dev/null)

ensure_server_running

# Read popup options
POPUP_WIDTH=$(get_tmux_option "$AGENTS_POPUP_WIDTH_OPTION" "$AGENTS_POPUP_WIDTH_DEFAULT")
POPUP_HEIGHT=$(get_tmux_option "$AGENTS_POPUP_HEIGHT_OPTION" "$AGENTS_POPUP_HEIGHT_DEFAULT")
POPUP_BORDER=$(get_tmux_option "$AGENTS_POPUP_BORDER_OPTION" "$AGENTS_POPUP_BORDER_DEFAULT")
POPUP_BG=$(get_tmux_option "$AGENTS_POPUP_BG_OPTION" "$AGENTS_POPUP_BG_DEFAULT")
POPUP_FG=$(get_tmux_option "$AGENTS_POPUP_FG_OPTION" "$AGENTS_POPUP_FG_DEFAULT")

# Forward the invoking context to the picker as environment variables (no
# command-string quoting of session names / paths required).
tmux display-popup -E \
    -w "$POPUP_WIDTH" \
    -h "$POPUP_HEIGHT" \
    -b "$POPUP_BORDER" \
    -S "fg=$POPUP_FG" \
    -s "bg=$POPUP_BG,fg=$POPUP_FG" \
    -e "AGENTS_SRC_CLIENT=$SRC_CLIENT" \
    -e "AGENTS_SRC_SESSION=$SRC_SESSION" \
    -e "AGENTS_SRC_PATH=$SRC_PATH" \
    "$CURRENT_DIR/_navigator_picker.sh"
