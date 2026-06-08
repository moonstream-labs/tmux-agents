# Implementation Notes

## 1. Runtime Topology

`tmux-agents` has four components:

1. **`agents.tmux`** — TPM entrypoint. Installs keybinding, ensures server is running, optionally appends status modules.

2. **Go status server** (`server/`) — Background HTTP server on `127.0.0.1:7077`. Receives push events from both tools, maintains state DB, publishes tmux signaling options. Runs as systemd user service.

3. **`scripts/_navigator_picker.sh`** — Renders Active/Recent views from state DB. Hosts interactive fzf picker. Routes selection actions per tool.

4. **Status pill scripts** — `status_claude.sh` (glyph: 󰚩) and `status_opencode.sh` (glyph: ). Read per-tool pill options.

## 2. Go Server Architecture

### Goroutine Layout

```
main
 ├─ net/http.ListenAndServe
 │   POST /claude/hook         (Claude Code hook receiver)
 │   POST /claude/register     (pre-registration from claude wrapper)
 │   POST /opencode/register   (instance registration from opencode wrapper)
 │   GET  /opencode/port       (port assignment)
 │   GET  /healthz
 │
 ├─ claude.WatchTitles         (fsnotify on ~/.claude/projects/*/*.jsonl)
 ├─ claude.ScanActiveSessions  (startup: scan ~/.claude/sessions/*.json)
 ├─ tmux.RunScanner            (periodic 5s: pane discovery + target assignment)
 ├─ [per OC instance] sse.Client (GET /event on OpenCode server)
 ├─ heartbeat ticker           (1s: write @agents-server-ts)
 └─ pending prune ticker       (30s: remove stale pre-registrations)
```

### State Flow

All state mutations from any goroutine call `state.Reconciler.Reconcile()`, which holds a `sync.Mutex` and:

1. Collects `ActivePanes()` from all registered providers (Claude store, OpenCode store)
2. Collects `RecentSessions()` from all providers
3. Computes per-tool pill values: `permission|N` > `running|N` > `active|N` > `idle|0`
4. Compares pills with previous — if changed: writes DB snapshot, bumps `@agents-gen`, sets pill options, refreshes tmux clients

### State DB Schema

SQLite at `/tmp/tmux-agents-<uid>/state.db`, WAL mode:

```sql
CREATE TABLE panes (
  target     TEXT PRIMARY KEY,
  tool       TEXT NOT NULL,      -- 'claude' | 'opencode'
  state      TEXT NOT NULL,      -- 'idle' | 'running' | 'permission' | 'unknown'
  session_id TEXT,
  name       TEXT,
  dir        TEXT,
  updated    INTEGER,
  host       TEXT NOT NULL DEFAULT 'local'
);

CREATE TABLE recent (
  tool         TEXT NOT NULL,
  session_id   TEXT NOT NULL,
  name         TEXT,
  dir          TEXT,
  updated      INTEGER,
  host         TEXT NOT NULL DEFAULT 'local',
  tmux_session TEXT,
  PRIMARY KEY(tool, session_id, host)
);

CREATE TABLE session_names (   -- persistent name cache; survives restarts
  tool       TEXT NOT NULL,
  session_id TEXT NOT NULL,
  name       TEXT NOT NULL,
  dir        TEXT,
  updated    INTEGER,
  PRIMARY KEY(tool, session_id)
);
```

### tmux Signaling Options

- `@agents-claude-pill` — format: `state|count`
- `@agents-opencode-pill` — format: `state|count`
- `@agents-gen` — monotonic counter, incremented on any state change
- `@agents-server-ts` — unix epoch heartbeat

## 3. Claude Code Integration

### State via HTTP Hooks

Claude Code hooks (`type: "http"`, `async: true`) POST JSON to `/claude/hook`. Configured in `~/.claude/settings.json`.

| Hook Event | State Transition |
|---|---|
| `SessionStart` | Register → idle |
| `UserPromptSubmit` | → running |
| `PreToolUse` | → running (reinforces) |
| `PermissionRequest` | → permission |
| `Notification` (permission_prompt) | → permission |
| `Stop` | → idle |
| `SessionEnd` | Remove → recent |

### Session Identity

- Hook payloads include `session_id` (UUID) and `cwd`
- Active sessions discoverable from `~/.claude/sessions/<pid>.json`
- Session names stored as `custom-title` entries in `~/.claude/projects/<project>/<sessionId>.jsonl`

### Pane Correlation

1. The `claude` shell wrapper (which shadows the real binary and injects `--dangerously-skip-permissions --effort max`) pre-registers `{name, pane_target, cwd}` via `POST /claude/register`
2. When `SessionStart` hook fires, server matches by pane target verification or CWD
3. Pane scanner fallback: walks process tree from pane PID, finds `~/.claude/sessions/<pid>.json`

### Title Propagation

fsnotify watches `~/.claude/projects/*/` for JSONL writes. On write, tails last lines for `custom-title` entries and updates session name.

## 4. OpenCode Integration

### State via SSE

Each OpenCode TUI instance runs an embedded HTTP server. The `opencode` shell wrapper passes `--port <N>` and registers with the Go server.

Per-instance SSE goroutine connects to `GET /event`:

| SSE Event | State Transition |
|---|---|
| `session.idle` | → idle |
| `session.status` | → idle/running (maps OpenCode status idle/busy/retry) |
| `message.part.updated` | → running (only when not already idle — avoids racing a late idle) |
| `permission.asked` | → permission |
| `permission.replied` | → re-check /session/status |
| `session.created` | Register session |
| `session.updated` | Re-fetch metadata (catches renames) |
| `session.deleted` | Remove session |
| `session.error` | → idle |
| `server.connected` | Fetch all sessions |

Connection loss triggers exponential backoff retry (1s → 30s max).

### Instance Lifecycle

- One instance = one tmux pane = one SSE goroutine
- Multiple sessions per instance, but `ActivePanes()` returns only the most recently active session per instance
- Instance removed when SSE drops and process is dead, or when pane vanishes from tmux

### Session Metadata

- `GET /session` — list all sessions (title, directory)
- `GET /session/status` — per-session status (idle, busy, retry)
- `PATCH /session/:id` — rename (propagated via `session.updated` SSE event)

## 5. Pane Scanner Fallback

Background goroutine (5s interval) runs `tmux list-panes -a`. For each pane:

- Running `claude`: walks `/proc` tree to find `~/.claude/sessions/<pid>.json`, assigns pane target to existing session or registers new one
- Running `opencode`: discovers `--port` flag from `/proc/<pid>/cmdline`, registers SSE connection

Handles sessions started without wrappers and server restarts while sessions are active.

## 6. Picker Rendering

`_navigator_picker.sh` reads from state DB. Rows include a tool glyph column (󰚩 / ) between the state dot and session name.

View switching via fzf `--listen` + background watcher polling `@agents-gen`.

Selection routing by tool:
- Active: navigate to tmux pane (both tools)
- Recent Claude: `claude -r <session_id>`
- Recent OpenCode: `opencode -s <session_id>` in session directory

> **Current status:** Both stores' `RecentSessions()` return `nil`, and `Reconcile()` rewrites the `recent` table from those providers on every pass — so the Recent tab and its selection routing are wired, but the list is not yet populated. Populating it is planned work: an in-memory recent ring per store surfaced via `RecentSessions()`, since side-channel writes directly to the `recent` table would be truncated by the next reconcile.

## 7. Process Lifecycle

Server runs as `tmux-agents.service` (systemd user unit, `Type=exec`, `Restart=on-failure`).

`agents.tmux` checks `GET /healthz` on plugin load. If unreachable, starts the service via `systemctl --user start`.

Graceful shutdown on SIGTERM: stops HTTP listener, cancels all SSE goroutines, writes final state, closes DB.
