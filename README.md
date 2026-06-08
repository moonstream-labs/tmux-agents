# tmux-agents

A tmux plugin for navigating active and recent Claude Code and OpenCode coding agent sessions.

Provides:

- a popup navigator (`prefix + o`)
- dual status pills for Claude Code (󰚩) and OpenCode ()
- push-based session tracking via a Go background server

## Features

- **Active view**: shows currently attached agent panes with live state (running, permission, idle)
- **Recent view** *(planned)*: the picker tab and resume routing exist, but the recent list is not yet populated
- **Dual status pills**: independent indicators per tool in the tmux status line
- **Push-based state**: Claude Code hooks and OpenCode SSE — no screen scraping
- **Shell wrappers**: `claude` and `opencode` shell functions shadow the real binaries for named session launch with automatic registration (`command claude` / `command opencode` bypass them)
- **Fallback discovery**: pane scanner finds sessions started without wrappers

## Requirements

| Dependency | Version | Purpose |
|---|---|---|
| `tmux` | >= 3.3 | popup UI |
| `bash` | >= 4 | plugin scripts |
| `fzf` | any | popup picker |
| `sqlite3` | any | state DB reads from picker |
| `go` | >= 1.26 | build server binary |
| `curl` | any | fzf live reload, wrapper registration |
| `claude` | any | Claude Code sessions |
| `opencode` | any | OpenCode sessions |
| `python3` | any | OpenCode wrapper auto-rename (optional) |

Linux is currently required (`/proc` for process inspection, `flock`).

## Installation

### 1. Build and install the server

```bash
git clone https://github.com/moonstream-labs/tmux-agents.git \
  ~/.local/share/tmux/plugins/tmux-agents

# Build, install binary, set up systemd service
~/.local/share/tmux/plugins/tmux-agents/scripts/install.sh
```

### 2. Add Claude Code hooks

Merge the contents of `.claude/hooks.json` into your `~/.claude/settings.json`. These hooks allow the server to track Claude Code session state in real time.

### 3. Source shell integration

Add to your `.zshrc` or `.bashrc`:

```bash
source ~/.local/share/tmux/plugins/tmux-agents/scripts/shell-integration.sh
```

This provides the `claude` and `opencode` wrapper functions.

### 4. Configure tmux

Add to `tmux.conf`:

```tmux
# With TPM
set -g @plugin 'moonstream-labs/tmux-agents'

# Or manual
run-shell ~/.local/share/tmux/plugins/tmux-agents/agents.tmux
```

### Status modules (manual composition, recommended)

```tmux
set -ag status-right "#($HOME/.local/share/tmux/plugins/tmux-agents/scripts/status_opencode.sh)"
set -ag status-right "#($HOME/.local/share/tmux/plugins/tmux-agents/scripts/status_claude.sh)"
```

### Status modules (automatic append)

```tmux
set -g @agents-auto-status-right 'on'
```

## Usage

### Shell wrappers

The shell integration defines `claude` and `opencode` functions that shadow the
real binaries, adding session naming and server registration. Use `command claude`
or `command opencode` to bypass them.

```bash
claude my-feature       # Launch Claude Code with name "my-feature"
claude                  # Prompt for name, then launch
claude -r auth-refactor # Resume a named Claude Code session

opencode trawl-dev      # Launch OpenCode with name on auto-assigned port
opencode                # Prompt for name, then launch
```

The `claude` wrapper also injects `--dangerously-skip-permissions --effort max`
into every launch. Both wrappers register the session with the background server
automatically. Sessions started without the wrappers are discovered by the pane
scanner within ~5 seconds.

### Navigator picker

- Open: `prefix + o`
- Toggle views: left (Active) / right (Recent)
- Select: Enter
- Abort: Esc or Ctrl-c

Active rows show a tool glyph (󰚩 or ), state indicator, session name, directory, and tmux session. Selecting navigates to the pane.

Recent rows are intended to show past sessions from both tools, with selection resuming the session (Claude Code: `claude -r <session_id>`; OpenCode: `opencode -s <session_id>`). The view and this routing are implemented, but the recent list is not yet populated — see `docs/IMPLEMENTATION.md`.

### Session naming

- At launch: `claude my-name` or `opencode my-name` passes the name to the tool
- Mid-session: use `/rename` in either tool — the server detects changes automatically
  - Claude Code: via fsnotify on JSONL files
  - OpenCode: via SSE `session.updated` events

## Configuration

Set options before TPM initialization.

```tmux
set -g @agents-popup-key 'o'
set -g @agents-popup-width '70%'
set -g @agents-popup-height '50%'
set -g @agents-popup-border 'rounded'
set -g @agents-popup-bg '#080909'
set -g @agents-popup-fg '#dadada'
set -g @agents-auto-status-right 'off'
```

| Option | Default | Description |
|---|---|---|
| `@agents-popup-key` | `o` | Popup launcher key (with prefix) |
| `@agents-popup-width` | `70%` | Popup width |
| `@agents-popup-height` | `50%` | Popup height |
| `@agents-popup-border` | `rounded` | Popup border style |
| `@agents-popup-bg` | `#080909` | Popup background |
| `@agents-popup-fg` | `#dadada` | Popup foreground |
| `@agents-auto-status-right` | `off` | Auto-append status modules |

Internal options (managed by the server):

- `@agents-claude-pill` — Claude Code state and count
- `@agents-opencode-pill` — OpenCode state and count
- `@agents-gen` — generation counter for picker reload
- `@agents-server-ts` — server heartbeat

Environment variables:

- `TMUX_AGENTS_SERVER` — server URL (default: `http://127.0.0.1:7077`)
- `AGENTS_STATE_DIR` — state directory (default: `/tmp/tmux-agents-<uid>`)

## Architecture

See `docs/IMPLEMENTATION.md` for full details.

## Troubleshooting

- **Popup doesn't open**: verify keybind with `tmux show-options -g | grep @agents-popup-key`
- **No rows in picker**: check server health with `curl http://127.0.0.1:7077/healthz`
- **Pills missing**: ensure status modules are in `status-right`
- **Sessions not appearing**: check `systemctl --user status tmux-agents` and server logs via `journalctl --user -u tmux-agents`
- **Claude sessions unnamed**: sessions started without the `claude` wrapper have no name until `/rename` is used

## License

MIT (see `LICENSE`).
