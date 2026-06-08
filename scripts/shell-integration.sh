#!/usr/bin/env bash
# shell-integration.sh -- Wrapper functions for Claude Code and OpenCode
# Source this file from your .zshrc or .bashrc:
#   source /path/to/tmux-agents/scripts/shell-integration.sh
#
# These functions shadow the 'claude' and 'opencode' binaries to add
# session naming and registration with the tmux-agents server.
# Use 'command claude' or 'command opencode' to bypass the wrappers.

TMUX_AGENTS_SERVER="${TMUX_AGENTS_SERVER:-http://127.0.0.1:7077}"

# claude -- Launch Claude Code with session name and registration
#
# Usage:
#   claude <name> [args...]   Launch with name
#   claude [args...]          Prompt for name interactively
#   claude -r <name>          Resume a named session
#   claude -c                 Continue most recent session
claude() {
  local name=""

  # If first arg doesn't start with -, treat as session name.
  if [[ $# -gt 0 && "$1" != -* ]]; then
    name="$1"
    shift
  fi

  # If no name and not resuming/continuing, prompt for one.
  local needs_name=true
  for arg in "$@"; do
    case "$arg" in
      -r|--resume|-c|--continue) needs_name=false; break ;;
    esac
  done

  if [[ -z "$name" && "$needs_name" == true ]]; then
    printf "Session name: "
    read -r name
  fi

  # Get current tmux pane target for registration.
  local target=""
  if [[ -n "$TMUX" ]]; then
    target=$(tmux display-message -p '#{session_name}:#{window_index}.#{pane_index}' 2>/dev/null)
  fi

  # Pre-register with the agent server (fire-and-forget).
  if [[ -n "$name" && -n "$target" ]]; then
    curl -sf -X POST "$TMUX_AGENTS_SERVER/claude/register" \
      -H 'Content-Type: application/json' \
      -d "{\"name\":\"$name\",\"pane_target\":\"$target\",\"cwd\":\"$(pwd)\"}" \
      >/dev/null 2>&1 &
  fi

  # Build the command using 'command' to call the real binary.
  local -a cmd=(command claude)
  [[ -n "$name" ]] && cmd+=(-n "$name")
  cmd+=(--dangerously-skip-permissions --effort max)
  cmd+=("$@")

  "${cmd[@]}"
}

# opencode -- Launch OpenCode with session name and port registration
#
# Usage:
#   opencode <name> [args...]      Launch new session with name
#   opencode -s <id> [args...]     Resume existing session by ID
#   opencode -c [args...]          Continue last session
#   opencode [args...]             Prompt for name interactively
opencode() {
  local name=""
  local resuming=false

  local resume_sid=""

  # Detect resume/continue flags — skip name prompt, extract session ID.
  # Use a simple prev-arg tracker to avoid bash/zsh array indexing differences.
  local prev=""
  for arg in "$@"; do
    case "$arg" in
      -s|--session) resuming=true ;;
      -c|--continue) resuming=true ;;
      *)
        if [[ "$prev" == "-s" || "$prev" == "--session" ]]; then
          resume_sid="$arg"
        fi
        ;;
    esac
    prev="$arg"
  done

  if [[ "$resuming" == false ]]; then
    # If first arg doesn't start with -, treat as session name.
    if [[ $# -gt 0 && "$1" != -* ]]; then
      name="$1"
      shift
    fi

    if [[ -z "$name" ]]; then
      printf "Session name: "
      read -r name
    fi
  fi

  # Get current tmux pane target.
  local target=""
  if [[ -n "$TMUX" ]]; then
    target=$(tmux display-message -p '#{session_name}:#{window_index}.#{pane_index}' 2>/dev/null)
  fi

  # Request a port from the agent server, fall back to random.
  local port
  port=$(curl -sf "$TMUX_AGENTS_SERVER/opencode/port" 2>/dev/null)
  if [[ -z "$port" || ! "$port" =~ ^[0-9]+$ ]]; then
    port=$(( 10000 + (RANDOM % 55000) ))
  fi

  # Register in background after a brief delay (opencode needs time to bind).
  # Also rename the active session via the API once the server is up.
  if [[ -n "$target" ]]; then
    (
      sleep 2
      curl -sf -X POST "$TMUX_AGENTS_SERVER/opencode/register" \
        -H 'Content-Type: application/json' \
        -d "{\"port\":$port,\"name\":\"$name\",\"pane_target\":\"$target\",\"session_id\":\"$resume_sid\"}" \
        >/dev/null 2>&1

      # Rename the active session via OpenCode API if a name was provided.
      if [[ -n "$name" ]]; then
        # Get the most recent session ID.
        local sid
        sid=$(curl -sf "http://127.0.0.1:$port/session" 2>/dev/null \
          | python3 -c "import sys,json; ss=json.load(sys.stdin); print(max(ss, key=lambda s: s.get('time_updated',0))['id'])" 2>/dev/null)
        if [[ -n "$sid" ]]; then
          curl -sf -X PATCH "http://127.0.0.1:$port/session/$sid" \
            -H 'Content-Type: application/json' \
            -d "{\"title\":\"$name\"}" >/dev/null 2>&1
        fi
      fi
    ) &
  fi

  command opencode --port "$port" "$@"
}

# codex -- Launch Codex with a display name registered with the tmux-agents server
#
# Usage:
#   codex <name> [args...]   Launch with display name "<name>"
#   codex [args...]          Launch (prompts for a name)
#   codex resume [args...]   Resume; no name prompt
#
# Codex has no native session name, so the name is used only by tmux-agents for
# the navigator/pill. Codex self-reports its tmux pane via $TMUX_PANE in its
# lifecycle hooks, so no port/pane wiring is needed here. Approvals are left at
# their default so the permission pill stays meaningful.
# Use 'command codex' to bypass this wrapper.
codex() {
  local name=""
  local skip_name=false

  # 'resume'/'exec' subcommands or a flag-first invocation skip the name prompt.
  case "${1:-}" in
    resume | exec | -*) skip_name=true ;;
  esac

  if [[ "$skip_name" == false && $# -gt 0 && "$1" != -* ]]; then
    name="$1"
    shift
  fi

  if [[ -z "$name" && "$skip_name" == false ]]; then
    printf "Session name: "
    read -r name
  fi

  # Pre-register the name with the agent server, keyed by this pane
  # (fire-and-forget). The SessionStart hook claims it by pane target.
  if [[ -n "$name" && -n "${TMUX_PANE:-}" ]]; then
    curl -sf -X POST "$TMUX_AGENTS_SERVER/codex/register" \
      -H 'Content-Type: application/json' \
      -d "{\"name\":\"$name\",\"pane\":\"$TMUX_PANE\"}" \
      >/dev/null 2>&1 &
  fi

  command codex "$@"
}
