#!/usr/bin/env bash
# navigate.sh -- step to the previous/next active agent session, jumping focus
# directly (no popup). The traversal is a STABLE ring: the active set (the same
# sessions the picker's Active view shows) ordered by tmux pane id (%N), which is
# monotonic by pane creation and never renumbered or reused for a pane's life --
# so a session keeps its slot until its pane dies, independent of the Active
# view's priority/recency display sort.
#
#   navigate.sh <prev|next> [client_name]
#
# $2 is the invoking client (tmux expands #{client_name} in the keybinding), so
# the jump lands on the right terminal with multiple clients attached -- the same
# discipline the picker uses.
#
# Cursor: @agents-nav-cursor holds the %N of the last session jumped to. If the
# focused pane is itself an active session, that's the current position;
# otherwise the cursor supplies it ("resume from last agent"). The ring wraps at
# both ends; an empty/stale cursor means next -> first, prev -> last.
#
# Independent of the popup and deliberately fork-light: it reads the state DB
# directly and does NOT ensure_server_running (no cold-start stall on a keypress).
# Server down / empty DB -> silent no-op.

set -u

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$CURRENT_DIR/helpers.sh"

DIR="${1:-}"
CLIENT="${2:-}"
case "$DIR" in
prev | next) ;;
*)
    echo "usage: navigate.sh {prev|next} [client]" >&2
    exit 2
    ;;
esac

# Resolve the invoking client (fall back to tmux's current client).
[[ -z "$CLIENT" ]] && CLIENT=$(tmux display-message -p '#{client_name}' 2>/dev/null)
cflag=()
[[ -n "$CLIENT" ]] && cflag=(-c "$CLIENT")

db_path=$(get_state_db_path)
[[ -f "$db_path" ]] || exit 0

# --- Build the ring: local active panes ordered by numeric %N --------------
# A row whose pane_id is still empty (a session registered since the last scan
# published the map) is resolved live so it isn't dropped. Lines: "%N<TAB>target".
ordered=$(
    sqlite3 -separator $'\x1f' "$db_path" \
        "SELECT IFNULL(pane_id,''), target FROM panes WHERE host = 'local'" 2>/dev/null |
        while IFS=$'\x1f' read -r pid target; do
            [[ -n "$target" ]] || continue
            if [[ -z "$pid" ]]; then
                pid=$(tmux display-message -t "$target" -p '#{pane_id}' 2>/dev/null)
            fi
            [[ "$pid" =~ ^%[0-9]+$ ]] || continue
            printf '%d\t%s\t%s\n' "${pid#%}" "$pid" "$target"
        done | sort -n -k1,1
)
[[ -n "$ordered" ]] || exit 0

ids=()
targets=()
while IFS=$'\t' read -r _num pid target; do
    ids+=("$pid")
    targets+=("$target")
done <<<"$ordered"

n=${#ids[@]}
((n > 0)) || exit 0

# --- Locate the current position in the ring -------------------------------
cur=$(tmux display-message "${cflag[@]}" -p '#{pane_id}' 2>/dev/null)

idx=-1
for i in "${!ids[@]}"; do
    [[ "${ids[$i]}" == "$cur" ]] && {
        idx=$i
        break
    }
done

# Off any agent pane: resume from the stored cursor if it's still in the ring.
if ((idx < 0)); then
    saved=$(tmux show-option -gqv "$AGENTS_NAV_CURSOR_OPTION" 2>/dev/null)
    if [[ -n "$saved" ]]; then
        for i in "${!ids[@]}"; do
            [[ "${ids[$i]}" == "$saved" ]] && {
                idx=$i
                break
            }
        done
    fi
fi

# --- Target index (wrap) ---------------------------------------------------
if ((idx < 0)); then
    # No anchor: next -> first, prev -> last.
    if [[ "$DIR" == next ]]; then tidx=0; else tidx=$((n - 1)); fi
elif [[ "$DIR" == next ]]; then
    tidx=$(((idx + 1) % n))
else
    tidx=$(((idx - 1 + n) % n))
fi

target="${targets[$tidx]}"
agents_switch_to_pane "$target" "$CLIENT"
tmux set-option -g "$AGENTS_NAV_CURSOR_OPTION" "${ids[$tidx]}"

# --- Optional indicator: "name (i/n)" --------------------------------------
indicator=$(get_tmux_option "$AGENTS_NAV_INDICATOR_OPTION" "$AGENTS_NAV_INDICATOR_DEFAULT")
case "$indicator" in
on | yes | true)
    esc="${target//\'/\'\'}"
    name=$(sqlite3 "$db_path" "SELECT IFNULL(NULLIF(name,''),'') FROM panes WHERE target = '$esc' LIMIT 1" 2>/dev/null)
    [[ -n "$name" ]] || name="${target%%:*}"
    name="${name//#/##}" # don't let a name expand as a tmux format
    tmux display-message "${cflag[@]}" "agents: ${name} ($((tidx + 1))/${n})"
    ;;
esac
