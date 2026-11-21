#!/usr/bin/env bash
set -euo pipefail

# Combined agents pill -- one pill for Claude Code, OpenCode and Codex together.
#
# The server publishes one "state|count" option per tool. This folds them into a
# single pill: the state is the most urgent one present across all tools
# (permission > running > active) and the count is the number of sessions in
# that state -- the same rule each per-tool pill applies to its own tool, so the
# result equals what the server would compute over every pane at once.
#
# All three options are read in ONE tmux invocation (three chained commands).
# An unset option prints nothing, so a tool the server hasn't reported on yet
# simply contributes no line. Rendering below mirrors status_claude.sh.

pills=$(tmux show-option -gqv "@agents-claude-pill" \; \
  show-option -gqv "@agents-opencode-pill" \; \
  show-option -gqv "@agents-codex-pill" 2>/dev/null || true)

state="idle"
count=0
best=0
while IFS='|' read -r s c; do
  [[ "$c" =~ ^[1-9][0-9]*$ ]] || continue
  case "$s" in
  permission) rank=3 ;;
  running) rank=2 ;;
  active) rank=1 ;;
  *) continue ;;
  esac
  if ((rank > best)); then
    best=$rank
    state="$s"
    count="$c"
  elif ((rank == best)); then
    count=$((count + c))
  fi
done <<<"$pills"

get_opt() {
  local val
  val=$(tmux show-option -gqv "$1" 2>/dev/null)
  echo "${val:-$2}"
}

color_yellow=$(get_opt "@thm_yellow" "#f9e2af")
color_green=$(get_opt "@thm_green" "#a6e3a1")
color_crust=$(get_opt "@thm_crust" "#1e1e2e")
color_white="#dadada"

if [[ "$count" == "0" ]]; then
  printf '#[fg=%s,bg=default] 󰚩  %s #[default] ' "$color_white" "$count"
  exit 0
fi

case "$state" in
permission) bg="$color_yellow" ;;
running)    bg="$color_green" ;;
*)          bg="$color_white" ;;
esac

printf '#[fg=%s,bg=default]#[fg=%s,bg=%s,bold]󰚩  %s#[fg=%s,bg=default]#[default] ' \
  "$bg" "$color_crust" "$bg" "$count" "$bg"
