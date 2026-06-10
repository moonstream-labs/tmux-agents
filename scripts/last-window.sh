#!/usr/bin/env bash
# last-window.sh -- toggle to the previously-focused tmux window, across sessions.
#
# tmux's built-in `last-window` is per-session; this tracks the last-focused
# window globally (in whatever session it lives). Hook-driven and entirely
# independent of the Go status server -- it works even when the server is down.
#
#   track  -- invoked by the session-window-changed / client-session-changed
#             hooks; records current + previous focus as session:window.pane.
#   jump   -- invoked by the keybinding; switches to the previous focus.
#
# State (internal global tmux options):
#   @agents-lastwin-cur   most recently focused target (session:window.pane)
#   @agents-lastwin-prev  the one before it -- the jump destination
#
# Self-contained (no sourcing): `track` runs on every focus change, so the hot
# path avoids the fork that sourcing variables.sh would incur.

set -u

CUR_OPT="@agents-lastwin-cur"
PREV_OPT="@agents-lastwin-prev"

track() {
    local new cur
    new=$(tmux display-message -p '#{session_name}:#{window_index}.#{pane_index}' 2>/dev/null) || return 0
    [[ -z "$new" ]] && return 0
    cur=$(tmux show-option -gqv "$CUR_OPT" 2>/dev/null)
    # Ignore redundant fires for the same window so prev isn't clobbered with cur.
    [[ "$new" == "$cur" ]] && return 0
    [[ -n "$cur" ]] && tmux set-option -g "$PREV_OPT" "$cur"
    tmux set-option -g "$CUR_OPT" "$new"
}

jump() {
    local prev sess winpane win
    prev=$(tmux show-option -gqv "$PREV_OPT" 2>/dev/null)
    [[ -z "$prev" ]] && return 0
    sess="${prev%%:*}"
    winpane="${prev#*:}"
    win="${winpane%%.*}"
    # Guard: the remembered target may have been closed since it was recorded.
    tmux has-session -t "$sess" 2>/dev/null || return 0
    tmux select-window -t "${sess}:${win}" 2>/dev/null || return 0
    tmux select-pane -t "$prev" 2>/dev/null || true
    tmux switch-client -t "$sess" 2>/dev/null || true
}

case "${1:-}" in
    track) track ;;
    jump) jump ;;
    *)
        echo "usage: last-window.sh {track|jump}" >&2
        exit 2
        ;;
esac
