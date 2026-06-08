#!/usr/bin/env bash
set -euo pipefail

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$CURRENT_DIR/helpers.sh"

MODE=""
RENDER_COLS=""
while (($# > 0)); do
  case "$1" in
  --render-active | --render-recent)
    MODE="$1"
    ;;
  --cols)
    if (($# > 1)); then
      RENDER_COLS="$2"
      shift
    fi
    ;;
  esac
  shift
done

# --- Color setup ---
get_opt() {
  local val
  val=$(tmux show-option -gqv "$1" 2>/dev/null)
  echo "${val:-$2}"
}

CLR_YELLOW=$(get_opt "@thm_yellow" "#f9e2af")
CLR_GREEN=$(get_opt "@thm_green" "#a6e3a1")
CLR_WHITE="#dadada"
CLR_DIM=$(get_opt "@thm_overlay_0" "#6c7086")
CLR_TEXT=$(get_opt "@thm_fg" "#cdd6f4")

ansi_fg() { printf '\033[38;2;%d;%d;%dm' "0x${1:1:2}" "0x${1:3:2}" "0x${1:5:2}"; }

FG_YELLOW=$(ansi_fg "$CLR_YELLOW")
FG_GREEN=$(ansi_fg "$CLR_GREEN")
FG_WHITE=$(ansi_fg "$CLR_WHITE")
FG_DIM=$(ansi_fg "$CLR_DIM")
FG_TEXT=$(ansi_fg "$CLR_TEXT")
RST="\033[0m"
ROW_FS=$'\x1f'

# Tool glyphs
GLYPH_CLAUDE="󰚩 "
GLYPH_OPENCODE=" "

GLYPH_CODEX="✦ "

# --- Layout: fixed budgeted column widths ---
get_cols() {
  if [[ -n "$RENDER_COLS" && "$RENDER_COLS" =~ ^[0-9]+$ ]]; then
    echo "$RENDER_COLS"
    return
  fi

  local cols
  cols=$(tmux display-message -p '#{pane_width}' 2>/dev/null || true)
  if [[ -n "$cols" && "$cols" =~ ^[0-9]+$ ]]; then
    echo "$cols"
    return
  fi

  tput cols 2>/dev/null || echo 80
}

COLS=$(get_cols)

# Visible overhead: dot(1) + glyph(2) + 2-space gaps before 5 columns (10) = 13
OVERHEAD=13
SAFETY_COLS=2
PAD_TARGET=$((COLS - SAFETY_COLS))
((PAD_TARGET < 20)) && PAD_TARGET=20

COL_BUDGET=$((PAD_TARGET - OVERHEAD))
((COL_BUDGET < 20)) && COL_BUDGET=20

# Desired widths
C_SESSION=30
C_DIR=30
C_HOST=10
C_TMUX=20
C_AGE=10

# Minimum widths
MIN_SESSION=10
MIN_DIR=10
MIN_HOST=6
MIN_TMUX=8
MIN_AGE=6

total_cols=$((C_SESSION + C_DIR + C_HOST + C_TMUX + C_AGE))
deficit=$((total_cols - COL_BUDGET))

if ((deficit > 0)); then
  reducible=$((C_SESSION - MIN_SESSION))
  ((reducible < 0)) && reducible=0
  dec=$((deficit < reducible ? deficit : reducible))
  C_SESSION=$((C_SESSION - dec))
  deficit=$((deficit - dec))
fi

if ((deficit > 0)); then
  reducible=$((C_DIR - MIN_DIR))
  ((reducible < 0)) && reducible=0
  dec=$((deficit < reducible ? deficit : reducible))
  C_DIR=$((C_DIR - dec))
  deficit=$((deficit - dec))
fi

if ((deficit > 0)); then
  reducible=$((C_TMUX - MIN_TMUX))
  ((reducible < 0)) && reducible=0
  dec=$((deficit < reducible ? deficit : reducible))
  C_TMUX=$((C_TMUX - dec))
  deficit=$((deficit - dec))
fi

if ((deficit > 0)); then
  reducible=$((C_HOST - MIN_HOST))
  ((reducible < 0)) && reducible=0
  dec=$((deficit < reducible ? deficit : reducible))
  C_HOST=$((C_HOST - dec))
  deficit=$((deficit - dec))
fi

if ((deficit > 0)); then
  reducible=$((C_AGE - MIN_AGE))
  ((reducible < 0)) && reducible=0
  dec=$((deficit < reducible ? deficit : reducible))
  C_AGE=$((C_AGE - dec))
  deficit=$((deficit - dec))
fi

if ((deficit > 0)); then
  while ((deficit > 0 && C_SESSION > 3)); do
    C_SESSION=$((C_SESSION - 1))
    deficit=$((deficit - 1))
  done
fi

if ((deficit > 0)); then
  while ((deficit > 0 && C_DIR > 3)); do
    C_DIR=$((C_DIR - 1))
    deficit=$((deficit - 1))
  done
fi

# --- Truncation helpers ---
truncate_str() {
  local str="$1" max="$2"
  if ((${#str} > max)); then
    echo "${str:0:$((max - 3))}..."
  else
    echo "$str"
  fi
}

shell_quote() {
  local s="${1:-}"
  s=${s//\'/\'"\'"\'}
  printf "'%s'" "$s"
}

b64enc() {
  printf '%s' "${1:-}" | base64 | tr -d '\n'
}

b64dec() {
  printf '%s' "${1:-}" | base64 -d 2>/dev/null || true
}

tool_glyph() {
  case "$1" in
  claude) printf '%s' "$GLYPH_CLAUDE" ;;
  opencode) printf '%s' "$GLYPH_OPENCODE" ;;
  codex) printf '%s' "$GLYPH_CODEX" ;;
  *) printf '?' ;;
  esac
}

load_active_rows() {
  local db_path
  db_path=$(get_state_db_path)

  [[ -f "$db_path" ]] || return 0
  sqlite3 -separator "$ROW_FS" "$db_path" \
    "SELECT target, tool, state, host, IFNULL(session_id,''), replace(replace(replace(IFNULL(name,''), char(9), ' '), char(10), ' '), char(13), ''), replace(replace(replace(IFNULL(dir,''), char(9), ' '), char(10), ' '), char(13), ''), IFNULL(updated, '') FROM panes" \
    2>/dev/null || true
}

load_recent_rows() {
  local db_path
  db_path=$(get_state_db_path)

  [[ -f "$db_path" ]] || return 0
  sqlite3 -separator "$ROW_FS" "$db_path" \
    "SELECT tool, host, session_id, replace(replace(replace(IFNULL(name,''), char(9), ' '), char(10), ' '), char(13), ''), replace(replace(replace(IFNULL(dir,''), char(9), ' '), char(10), ' '), char(13), ''), IFNULL(updated, ''), IFNULL(tmux_session, '') FROM recent WHERE NOT EXISTS (SELECT 1 FROM panes p WHERE p.tool = recent.tool AND p.session_id = recent.session_id) ORDER BY updated DESC LIMIT 20" \
    2>/dev/null || true
}

lookup_recent_dir() {
  local tool="$1"
  local host="$2"
  local sid="$3"

  local db_path
  db_path=$(get_state_db_path)
  [[ -f "$db_path" ]] || return 0

  sqlite3 "$db_path" "SELECT IFNULL(dir, '') FROM recent WHERE tool = '${tool//\'/\'\'}' AND host = '${host//\'/\'\'}' AND session_id = '${sid//\'/\'\'}' LIMIT 1" 2>/dev/null || true
}

# Returns the active pane target (session:window.pane) for a session, if it is
# currently open, else empty.
lookup_active_target() {
  local tool="$1"
  local sid="$2"

  local db_path
  db_path=$(get_state_db_path)
  [[ -f "$db_path" ]] || return 0

  sqlite3 "$db_path" "SELECT target FROM panes WHERE tool = '${tool//\'/\'\'}' AND session_id = '${sid//\'/\'\'}' LIMIT 1" 2>/dev/null || true
}

normalize_path() {
  local p="${1:-}"
  [[ -z "$p" ]] && return
  p="${p%/}"
  [[ -z "$p" ]] && p="/"
  readlink -f -- "$p" 2>/dev/null || printf '%s' "$p"
}

short_dir() {
  local dir="$1"
  dir="${dir/#$HOME/~}"
  local parts
  parts=$(awk -F'/' '{if(NF>2) print $(NF-1)"/"$NF; else print $0}' <<<"$dir")
  truncate_str "$parts" "$C_DIR"
}

pad_line() {
  local line="$1"
  local visible
  visible=$(sed 's/\x1b\[[0-9;]*m//g' <<<"$line")
  local vlen=${#visible}
  local padding=$((PAD_TARGET - vlen))
  ((padding > 0)) && printf '%s%*s' "$line" "$padding" "" || printf '%s' "$line"
}

state_priority() {
  case "$1" in
  permission) echo 0 ;;
  running) echo 1 ;;
  *) echo 2 ;;
  esac
}

# --- Render active sessions from state DB ---
render_active() {
  local active_rows
  active_rows=$(load_active_rows)
  if [[ -z "$active_rows" ]]; then
    return
  fi

  # Header
  local hdr
  hdr=$(printf '  %b  %-*s  %-*s  %-*s  %-*s  %-*s%b' \
    "$FG_DIM" \
    "$C_SESSION" "session" \
    "$C_DIR" "directory" \
    "$C_HOST" "host" \
    "$C_TMUX" "tmux-session" \
    "$C_AGE" "active" \
    "$RST")
  printf '%s\t%s\n' "__HEADER__" "$(pad_line "$hdr")"

  # Collect rows with sort keys
  local -a sortable=()
  while IFS="$ROW_FS" read -r target tool state host sid title dir updated; do
    [[ -z "$target" ]] && continue

    local pri
    pri=$(state_priority "$state")
    local ts="${updated:-0}"

    # State circle color
    local circle_color
    case "$state" in
    permission) circle_color="$FG_YELLOW" ;;
    running) circle_color="$FG_GREEN" ;;
    *) circle_color="$FG_WHITE" ;;
    esac

    # Tool glyph
    local glyph
    glyph=$(tool_glyph "$tool")

    # Format fields
    local s_title s_dir s_host age tmux_ses
    s_title=$(truncate_str "${title:-untitled}" "$C_SESSION")
    if [[ "$dir" == "-" || -z "$dir" ]]; then
      s_dir=$(truncate_str "${dir:--}" "$C_DIR")
    else
      s_dir=$(short_dir "$dir")
    fi
    s_host=$(truncate_str "$host" "$C_HOST")
    tmux_ses=$(truncate_str "${target%%:*}" "$C_TMUX")
    age=""
    if [[ -n "$updated" && "$updated" =~ ^[0-9]+$ ]]; then
      age=$(truncate_str "$(format_age "$((updated / 1000))")" "$C_AGE")
    fi

    local row
    row=$(printf '%b●%b %s  %b%-*s%b  %b%-*s%b  %b%-*s%b  %b%-*s%b  %b%-*s%b' \
      "$circle_color" "$RST" "$glyph" \
      "$FG_TEXT" "$C_SESSION" "$s_title" "$RST" \
      "$FG_DIM" "$C_DIR" "$s_dir" "$RST" \
      "$FG_TEXT" "$C_HOST" "$s_host" "$RST" \
      "$FG_DIM" "$C_TMUX" "$tmux_ses" "$RST" \
      "$FG_DIM" "$C_AGE" "$age" "$RST")

    sortable+=("${pri}|${ts}|${target}"$'\t'"$(pad_line "$row")")
  done <<<"$active_rows"

  # Sort: by priority asc, then timestamp desc
  printf '%s\n' "${sortable[@]}" | sort -t'|' -k1,1n -k2,2rn | while IFS='|' read -r _pri _ts rest; do
    echo "$rest"
  done
}

# --- Render recent sessions from state DB ---
render_recent() {
  local recent_rows
  recent_rows=$(load_recent_rows)
  if [[ -z "$recent_rows" ]]; then
    printf '%s\t%s\n' "__HEADER__" "$(pad_line "  No recent sessions found.")"
    return
  fi

  # Header
  local hdr
  hdr=$(printf '  %b  %-*s  %-*s  %-*s  %-*s  %-*s%b' \
    "$FG_DIM" \
    "$C_SESSION" "session" \
    "$C_DIR" "directory" \
    "$C_HOST" "host" \
    "$C_TMUX" "tmux-session" \
    "$C_AGE" "active" \
    "$RST")
  printf '%s\t%s\n' "__HEADER__" "$(pad_line "$hdr")"

  # Data rows: tool|host|sid|title|dir|updated|tmux_session
  while IFS="$ROW_FS" read -r tool host sid title dir updated remote_tmux_session; do
    [[ -z "$sid" ]] && continue

    local glyph
    glyph=$(tool_glyph "$tool")

    local s_title s_dir s_host age tmux_ses
    s_title=$(truncate_str "$title" "$C_SESSION")
    s_dir=$(short_dir "$dir")
    s_host=$(truncate_str "$host" "$C_HOST")
    tmux_ses="-"
    if [[ -n "${remote_tmux_session:-}" ]]; then
      tmux_ses=$(truncate_str "$remote_tmux_session" "$C_TMUX")
    fi
    age=""
    if [[ -n "$updated" && "$updated" =~ ^[0-9]+$ ]]; then
      age=$(truncate_str "$(format_age "$((updated / 1000))")" "$C_AGE")
    fi

    local row
    row=$(printf '%b●%b %s  %b%-*s%b  %b%-*s%b  %b%-*s%b  %b%-*s%b  %b%-*s%b' \
      "$FG_DIM" "$RST" "$glyph" \
      "$FG_TEXT" "$C_SESSION" "$s_title" "$RST" \
      "$FG_DIM" "$C_DIR" "$s_dir" "$RST" \
      "$FG_TEXT" "$C_HOST" "$s_host" "$RST" \
      "$FG_DIM" "$C_TMUX" "$tmux_ses" "$RST" \
      "$FG_DIM" "$C_AGE" "$age" "$RST")

    printf 'recent|%s|%s|%s|%s\t%s\n' \
      "$tool" \
      "$(b64enc "$host")" \
      "$(b64enc "$sid")" \
      "$(b64enc "${remote_tmux_session:-}")" \
      "$(pad_line "$row")"
  done <<<"$recent_rows"
}

# --- Handle render-only modes for fzf reload ---
case "$MODE" in
--render-active)
  render_active
  exit 0
  ;;
--render-recent)
  render_recent
  exit 0
  ;;
esac

# --- Initial render ---
initial_rows=$(render_active)
if [[ -z "$initial_rows" ]]; then
  echo "No agent sessions found."
  echo "Press Enter to close."
  read -r
  exit 0
fi

# --- fzf listen port ---
FZF_PORT=$((16384 + $(id -u) % 10000))
ensure_state_dir
MODE_FILE="$AGENTS_STATE_DIR/picker-mode-$$"
printf 'active' >"$MODE_FILE"

# --- Background watcher: auto-reload on state changes ---
_state_watcher() {
  local last_gen
  last_gen=$(tmux show-option -gqv "$AGENTS_GEN_OPTION" 2>/dev/null || true)
  [[ "$last_gen" =~ ^[0-9]+$ ]] || last_gen=0

  while true; do
    sleep 0.1
    local current_gen mode
    current_gen=$(tmux show-option -gqv "$AGENTS_GEN_OPTION" 2>/dev/null || true)
    [[ "$current_gen" =~ ^[0-9]+$ ]] || current_gen="$last_gen"

    if [[ "$current_gen" != "$last_gen" ]]; then
      last_gen="$current_gen"
      read -r mode <"$MODE_FILE" 2>/dev/null || mode="active"
      if [[ "$mode" == "recent" ]]; then
        reload_cmd="bash $CURRENT_DIR/_navigator_picker.sh --render-recent --cols $COLS"
      else
        reload_cmd="bash $CURRENT_DIR/_navigator_picker.sh --render-active --cols $COLS"
      fi
      curl -sf -XPOST "localhost:$FZF_PORT" \
        -d "reload($reload_cmd)" >/dev/null 2>&1 || true
    fi
  done
}

_state_watcher &
watcher_pid=$!

cleanup() {
  kill "$watcher_pid" 2>/dev/null || true
  rm -f "$MODE_FILE"
}
trap cleanup EXIT

# --- Launch fzf with arrow key view toggle ---
selection=$(
  echo "$initial_rows" | fzf \
    --listen="$FZF_PORT" \
    --ansi \
    --with-nth='2..' \
    --delimiter=$'\t' \
    --no-sort \
    --reverse \
    --no-info \
    --prompt='  ' \
    --marker='' \
    --pointer='' \
    --no-scrollbar \
    --border=none \
    --header-lines=1 \
    --bind='ctrl-c:abort,esc:abort' \
    --bind="right:execute-silent(printf recent > $MODE_FILE)+reload(bash $CURRENT_DIR/_navigator_picker.sh --render-recent --cols $COLS)" \
    --bind="left:execute-silent(printf active > $MODE_FILE)+reload(bash $CURRENT_DIR/_navigator_picker.sh --render-active --cols $COLS)"
) || exit 0

# --- Handle selection ---
key=$(cut -f1 <<<"$selection")
[[ "$key" == "__HEADER__" ]] && exit 0

# --- Active session: navigate to tmux pane ---
if [[ "$key" != recent\|* ]]; then
  local_target="$key"
  tmux_session="${local_target%%:*}"
  window_pane="${local_target#*:}"
  tmux select-window -t "${tmux_session}:${window_pane%%.*}" 2>/dev/null || true
  tmux select-pane -t "$local_target" 2>/dev/null || true
  tmux switch-client -t "$tmux_session" 2>/dev/null || true
  exit 0
fi

# --- Recent session: open session ---
# key format: "recent|<tool>|base64(host)|base64(session_id)|base64(tmux_session)"
IFS='|' read -r _recent_tag recent_tool host_b64 sid_b64 tmux_b64 <<<"$key"
recent_host=$(b64dec "$host_b64")
recent_sid=$(b64dec "$sid_b64")
recent_tmux_session=$(b64dec "$tmux_b64")
[[ -n "$recent_host" && -n "$recent_sid" ]] || exit 0

# If this session is already open as an active pane, navigate to it rather than
# launching a second instance on the same backing session.
if [[ "$recent_host" == "local" ]]; then
  active_target=$(lookup_active_target "$recent_tool" "$recent_sid")
  if [[ -n "$active_target" ]]; then
    nav_session="${active_target%%:*}"
    nav_window_pane="${active_target#*:}"
    tmux select-window -t "${nav_session}:${nav_window_pane%%.*}" 2>/dev/null || true
    tmux select-pane -t "$active_target" 2>/dev/null || true
    tmux switch-client -t "$nav_session" 2>/dev/null || true
    exit 0
  fi
fi

current_cmd=$(tmux display-message -p '#{pane_current_command}' 2>/dev/null || true)
current_path=$(tmux display-message -p '#{pane_current_path}' 2>/dev/null || true)

recent_dir=$(lookup_recent_dir "$recent_tool" "$recent_host" "$recent_sid")

if [[ "$recent_tool" == "claude" && "$recent_host" == "local" ]]; then
  # --- Claude Code recent: resume session ---
  resume_cmd="claude -r $(shell_quote "$recent_sid") --dangerously-skip-permissions --effort max"

  current_path_norm=$(normalize_path "$current_path")
  recent_path_norm=$(normalize_path "$recent_dir")

  if [[ "$current_cmd" =~ ^(zsh|bash|fish|sh)$ ]] &&
    [[ -n "$current_path_norm" && -n "$recent_path_norm" ]] &&
    [[ "$current_path_norm" == "$recent_path_norm" ]]; then
    tmux send-keys "$resume_cmd" Enter
  elif [[ -n "$recent_dir" && -d "$recent_dir" ]]; then
    tmux new-window -c "$recent_dir" "$resume_cmd"
  else
    tmux new-window "$resume_cmd"
  fi

elif [[ "$recent_tool" == "opencode" && "$recent_host" == "local" ]]; then
  # --- OpenCode recent: resume session ---
  recent_cmd="opencode -s $(shell_quote "$recent_sid")"
  if [[ -n "$recent_dir" ]]; then
    recent_cmd="cd -- $(shell_quote "$recent_dir") && $recent_cmd"
  fi

  current_path_norm=$(normalize_path "$current_path")
  recent_path_norm=$(normalize_path "$recent_dir")

  if [[ "$current_cmd" =~ ^(zsh|bash|fish|sh)$ ]] &&
    [[ -n "$current_path_norm" && -n "$recent_path_norm" ]] &&
    [[ "$current_path_norm" == "$recent_path_norm" ]]; then
    tmux send-keys "$recent_cmd" Enter
  elif [[ -n "$recent_dir" && -d "$recent_dir" ]]; then
    tmux new-window -c "$recent_dir" "opencode -s '$recent_sid'"
  else
    tmux new-window "opencode -s '$recent_sid'"
  fi

elif [[ "$recent_tool" == "codex" && "$recent_host" == "local" ]]; then
  # --- Codex recent: resume session ---
  recent_cmd="codex resume $(shell_quote "$recent_sid")"
  if [[ -n "$recent_dir" ]]; then
    recent_cmd="cd -- $(shell_quote "$recent_dir") && $recent_cmd"
  fi

  current_path_norm=$(normalize_path "$current_path")
  recent_path_norm=$(normalize_path "$recent_dir")

  if [[ "$current_cmd" =~ ^(zsh|bash|fish|sh)$ ]] &&
    [[ -n "$current_path_norm" && -n "$recent_path_norm" ]] &&
    [[ "$current_path_norm" == "$recent_path_norm" ]]; then
    tmux send-keys "$recent_cmd" Enter
  elif [[ -n "$recent_dir" && -d "$recent_dir" ]]; then
    tmux new-window -c "$recent_dir" "codex resume '$recent_sid'"
  else
    tmux new-window "codex resume '$recent_sid'"
  fi
fi
