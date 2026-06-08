#!/usr/bin/env bash
# shell-integration.sh -- Wrapper functions for Claude Code and OpenCode
# Source this file from your .zshrc or .bashrc:
#   source /path/to/tmux-agents/scripts/shell-integration.sh
#
# These functions shadow the 'claude', 'opencode', and 'codex' binaries to add
# server registration and an optional session name. The name is optional — a
# bare invocation starts immediately and the session is named from its own
# title. Use 'command <tool>' to bypass a wrapper.

TMUX_AGENTS_SERVER="${TMUX_AGENTS_SERVER:-http://127.0.0.1:7077}"

# claude -- Launch Claude Code with an optional session name + registration
#
# Usage:
#   claude <name> [args...]   Launch with an explicit name
#   claude [args...]          Launch immediately (named from its title / /rename)
#   claude -r <id>            Resume a session
#   claude -c                 Continue most recent session
claude() {
  local name=""

  # Optional leading session name: `claude my-feature`. A bare `claude` starts
  # immediately; its name then comes from the session title (custom-title via
  # /rename), which the title watcher and pane scanner pick up.
  if [[ $# -gt 0 && "$1" != -* ]]; then
    name="$1"
    shift
  fi

  # If named, pre-register the name + pane so the server binds it instantly
  # (otherwise the scanner binds the pane within a few seconds via /proc).
  if [[ -n "$name" && -n "$TMUX" ]]; then
    local target
    target=$(tmux display-message -p '#{session_name}:#{window_index}.#{pane_index}' 2>/dev/null)
    if [[ -n "$target" ]]; then
      curl -sf -X POST "$TMUX_AGENTS_SERVER/claude/register" \
        -H 'Content-Type: application/json' \
        -d "{\"name\":\"$name\",\"pane_target\":\"$target\",\"cwd\":\"$(pwd)\"}" \
        >/dev/null 2>&1 &
    fi
  fi

  # Build the command using 'command' to call the real binary.
  local -a cmd=(command claude)
  [[ -n "$name" ]] && cmd+=(-n "$name")
  cmd+=(--dangerously-skip-permissions)
  cmd+=("$@")

  "${cmd[@]}"
}

# opencode -- Launch OpenCode with an optional session name and port registration
#
# The --port wiring and server registration are essential (the server discovers
# the OpenCode instance via the --port flag in /proc); the name is optional.
#
# Usage:
#   opencode <name> [args...]      Launch new session with an explicit name
#   opencode -s <id> [args...]     Resume existing session by ID
#   opencode -c [args...]          Continue last session
#   opencode [args...]             Launch immediately (named from its title)
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

  # Optional leading session name: `opencode my-feature`. A bare `opencode`
  # starts immediately; its name then comes from the session title (which the
  # SSE watcher picks up). Resume/continue invocations never take a name.
  if [[ "$resuming" == false && $# -gt 0 && "$1" != -* ]]; then
    name="$1"
    shift
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

# codex -- Launch Codex with an optional display name registered with the server
#
# Usage:
#   codex <name> [args...]   Launch with an explicit display name "<name>"
#   codex [args...]          Launch immediately (named from its own title)
#   codex resume [args...]   Resume an existing session
#
# Codex has no native session name, so the name is used only by tmux-agents for
# the navigator/pill. Codex self-reports its tmux pane via $TMUX_PANE in its
# lifecycle hooks, so no port/pane wiring is needed here. Approvals are left at
# their default so the permission pill stays meaningful.
# Use 'command codex' to bypass this wrapper.
codex() {
  local name=""

  # Optional leading display name: `codex my-feature`. A bare `codex`, the
  # 'resume'/'exec' subcommands, or a flag-first invocation start immediately;
  # the name is then taken from Codex's own thread title.
  case "${1:-}" in
    "" | resume | exec | -*) ;;
    *)
      name="$1"
      shift
      ;;
  esac

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
