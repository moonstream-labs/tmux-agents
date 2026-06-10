# tmux-agents

A tmux plugin for navigating active and recent Claude Code, OpenCode, and Codex coding agent sessions.

Provides:

- a popup navigator (`prefix + o`)
- per-tool status pills for Claude Code (󰚩), OpenCode (), and Codex (✦)
- push-based session tracking via a Go background server

## Features

- **Active view**: shows currently attached agent panes with live state (running, permission, idle)
- **Recent view**: ended sessions from all three tools, newest first, with one-key resume (`claude -r` / `opencode -s` / `codex resume`)
- **Per-tool status pills**: independent indicators for each tool in the tmux status line
- **Push-based state**: Claude Code hooks, OpenCode SSE, and Codex hooks — no screen scraping
- **Shell wrappers**: `claude`, `opencode`, and `codex` shell functions shadow the real binaries for named session launch with automatic registration (`command <tool>` bypasses them)
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
| `codex` | >= 0.130 | Codex sessions (lifecycle hooks; enabled by default) |
| `python3` | any | OpenCode wrapper auto-rename (optional) |

Linux is currently required (`/proc` for process inspection).

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

### 2b. Add Codex hooks

`scripts/install.sh` prints a ready-to-merge `~/.codex/hooks.json` that points every
Codex lifecycle event at `scripts/codex-hook.sh`. Codex only supports command hooks,
so the shim forwards each event (received on stdin, plus the pane via `$TMUX_PANE`)
to the server. After merging, start Codex and run `/hooks` once to review and trust
the hook (or launch with `--dangerously-bypass-hook-trust`).

### 3. Source shell integration

Add to your `.zshrc` or `.bashrc`:

```bash
source ~/.local/share/tmux/plugins/tmux-agents/scripts/shell-integration.sh
```

This provides the `claude`, `opencode`, and `codex` wrapper functions.

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
set -ag status-right "#($HOME/.local/share/tmux/plugins/tmux-agents/scripts/status_codex.sh)"
set -ag status-right "#($HOME/.local/share/tmux/plugins/tmux-agents/scripts/status_claude.sh)"
```

### Status modules (automatic append)

```tmux
set -g @agents-auto-status-right 'on'
```

## Usage

### Shell wrappers

The shell integration defines `claude`, `opencode`, and `codex` functions that
shadow the real binaries, adding server registration and an optional session
name. The name is optional — a bare invocation launches immediately and the
session is named from its own title (and any later `/rename`). Use
`command <tool>` to bypass a wrapper.

```bash
claude my-feature       # Launch Claude Code with name "my-feature"
claude                  # Launch immediately (named from its title)
claude -r auth-refactor # Resume a named Claude Code session

opencode trawl-dev      # Launch OpenCode with name on auto-assigned port
opencode                # Launch immediately (named from its title)

codex my-fix            # Launch Codex with display name "my-fix"
codex                   # Launch immediately (named from its title)
codex resume            # Resume a Codex session
```

The `claude` wrapper also injects `--dangerously-skip-permissions`
into every launch; the `codex` wrapper leaves approvals at their default so the
permission pill stays meaningful (the pill shows Codex's own live session title; a
`codex <name>` wrapper name is used until Codex assigns one). All wrappers register
the session with the
background server automatically. Sessions started without the wrappers are
discovered by the pane scanner within ~5 seconds.

### Navigator picker

- Open: `prefix + o`
- Toggle views: left (Active) / right (Recent)
- Select: Enter
- Abort: Esc or Ctrl-c

Active rows show a tool glyph (󰚩, , or ✦), state indicator, session name, directory, and tmux session. Selecting navigates to the pane.

Recent rows show ended sessions from all tools (newest first) with their directory and age. Selecting one resumes it — Claude Code `claude -r <session_id>`, OpenCode `opencode -s <session_id>`, Codex `codex resume <session_id>` — in a **new window**, created in the tmux session you launched the picker from and opened in the session's recorded directory (falling back to your current directory if that path is gone). Navigation and resume target the client/session the popup was opened from, so they stay correct with multiple clients attached.

### Last-window toggle

`@agents-last-window-key` binds a "jump to the previously-focused window" toggle
that works **across sessions** — unlike tmux's built-in `last-window`, which only
remembers the last window *within* the current session. Opt-in (unset by default);
set it to a key to enable:

```tmux
set -g @agents-last-window-key 'a'   # prefix + a toggles to the last window, anywhere
```

It tracks focus via tmux hooks and ping-pongs between your two most recent windows
regardless of which session each lives in. Independent of the status server — no
server, DB, or pill involvement.

### Session naming

- At launch (optional): `claude my-name`, `opencode my-name`, or `codex my-name`
  sets an initial name. Omit it and the session is named from its own title.
- Mid-session renames/auto-titles are reflected for all three: Claude Code via
  fsnotify on the session JSONL, OpenCode via SSE `session.updated`, and Codex by
  reading its live session title from `~/.codex/state_*.sqlite` — the same value
  `codex resume` shows — refreshed ~every 5s. A `codex <name>` wrapper name is used as
  the label until Codex assigns its own title.

## Configuration

Set options before TPM initialization.

```tmux
set -g @agents-popup-key 'o'
set -g @agents-last-window-key 'a'   # toggle to last-focused window across sessions (default: unset)
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
| `@agents-last-window-key` | _(unset)_ | Toggle to the previously-focused window across sessions (with prefix); empty = no binding |
| `@agents-popup-width` | `70%` | Popup width |
| `@agents-popup-height` | `50%` | Popup height |
| `@agents-popup-border` | `rounded` | Popup border style |
| `@agents-popup-bg` | `#080909` | Popup background |
| `@agents-popup-fg` | `#dadada` | Popup foreground |
| `@agents-auto-status-right` | `off` | Auto-append status modules |

Internal options (managed by the server):

- `@agents-claude-pill` — Claude Code state and count
- `@agents-opencode-pill` — OpenCode state and count
- `@agents-codex-pill` — Codex state and count
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
