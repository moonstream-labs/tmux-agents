# Implementation Notes

## 1. Runtime Topology

`tmux-agents` has four components:

1. **`agents.tmux`** — TPM entrypoint. Installs keybinding, ensures server is running, optionally appends status modules.

2. **Go status server** (`server/`) — Background HTTP server on `127.0.0.1:7077`. Receives push events from all three tools, maintains state DB, publishes tmux signaling options. Runs as systemd user service.

3. **`scripts/_navigator_picker.sh`** — Renders Active/Recent views from state DB. Hosts interactive fzf picker. Routes selection actions per tool.

4. **Status pill scripts** — `status_claude.sh` (󰚩), `status_opencode.sh` (), and `status_codex.sh` (✦). Read per-tool pill options.

## 2. Go Server Architecture

### Goroutine Layout

```
main
 ├─ net/http.ListenAndServe
 │   POST /claude/hook         (Claude Code hook receiver)
 │   POST /claude/register     (pre-registration from claude wrapper)
 │   POST /opencode/register   (instance registration from opencode wrapper)
 │   GET  /opencode/port       (port assignment)
 │   POST /codex/hook          (Codex hook receiver, via shim)
 │   POST /codex/register      (pre-registration from codex wrapper)
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

1. Collects `ActivePanes()` from all registered providers (Claude, OpenCode, Codex stores)
2. Collects recent (ended) sessions from the shared `Recents` ring
3. Computes per-tool pill values: `permission|N` > `running|N` > `active|N` > `idle|0`
4. Compares pills with previous — if changed: writes DB snapshot, bumps `@agents-gen`, sets pill options, refreshes tmux clients

### State DB Schema

SQLite at `/tmp/tmux-agents-<uid>/state.db`, WAL mode:

```sql
CREATE TABLE panes (
  target     TEXT PRIMARY KEY,
  tool       TEXT NOT NULL,      -- 'claude' | 'opencode' | 'codex'
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
- `@agents-codex-pill` — format: `state|count`
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

## 5. Codex Integration

### State via command hooks

Codex lifecycle hooks (in `~/.codex/hooks.json` or `~/.codex/config.toml`, enabled
by default) are **command** hooks — Codex has no HTTP hook type — so each event
runs `scripts/codex-hook.sh`, which forwards the event JSON (received on stdin) to
`POST /codex/hook` and adds the pane id via an `X-Tmux-Pane` header. The shim
detaches the curl and returns immediately because Codex runs hooks synchronously,
and emits no stdout (Codex warns on non-JSON hook output). The payload schema
mirrors Claude Code's (`session_id`, `cwd`, `hook_event_name`, `transcript_path`, …).

| Hook Event | State Transition |
|---|---|
| `SessionStart` | Register → idle |
| `UserPromptSubmit` | → running |
| `PreToolUse` | → running |
| `PostToolUse` | → running |
| `PermissionRequest` | → permission |
| `Stop` | → idle |
| `PreCompact` / `PostCompact` / `SubagentStart` / `SubagentStop` | tracked, no state change |

Two Codex limitations shape the rest of the design: hooks fire **only on a turn**
(not on launch, resume, or while idle), and there is no `SessionEnd`. So hooks alone
can't see an idle or just-resumed session, and a finished session is removed by the
pane scanner when its pane disappears.

### Discovery, titles & correlation

Codex's on-disk session index is the `threads` table in `~/.codex/state_<N>.sqlite`
(the store behind `codex resume`): `id` (= the session id and the uuid embedded in
the rollout filename), `title`, `cwd`, `updated_at`. `codex.ThreadReader` reads it
**read-only and best-effort** — globbing the highest schema version, tolerating a
missing/changed DB, and degrading to "no title" rather than erroring (the schema is
undocumented and version-numbered).

Two sources combine, mirroring Claude's *(session file + hooks)*:

- **Pane scanner (every 5s)** finds each `codex` pane by `pane_current_command`,
  walks the pane's process tree to the `codex` process, and reads its **open rollout
  file descriptor** (`/proc/<pid>/fd/* → …/rollout-<ts>-<uuid>.jsonl`). The uuid is
  the exact session id, so the scanner registers the (idle) session and titles it via
  `ThreadReader.ByID(id)` — no cwd guessing. This makes a launched- or
  resumed-but-idle session visible and correctly named before its first turn, and
  rebinds the pane if a different session is resumed into it. (cwd matching was
  dropped because sessions sharing a directory would borrow each other's titles.) If
  the rollout fd can't be read, the scanner falls back to a bare, unnamed placeholder
  rather than risk a wrong title.
- **Hooks** self-report the pane via `$TMUX_PANE` (resolved to a target with
  `tmux display-message`) and drive state transitions; the handler upserts on every
  event, self-healing after a server restart.

`ActivePanes()` dedupes by pane target (a hook binding retires any placeholder for
that pane). Title/rename changes propagate within one scan (~5s).

- The display name is Codex's live session title from `threads.title` (the value
  `codex resume` shows), refreshed each scan by exact session id — so auto-titles and
  renames are reflected. A `codex` wrapper name is the initial label until Codex sets a
  title (the scanner only overwrites with a non-empty title). The pill therefore always
  matches what Codex itself shows for the session, including after a resume.
- Recent resume uses `codex resume <session_id>`.

### Trust

Non-managed Codex command hooks must be reviewed once via `/hooks` (or bypassed per
invocation with `--dangerously-bypass-hook-trust`); trust is keyed to the hook's
hash, so editing the shim command requires re-trusting.

## 6. Pane Scanner Fallback

Background goroutine (5s interval) runs `tmux list-panes -a`. For each pane:

- Running `claude`: walks `/proc` tree to find `~/.claude/sessions/<pid>.json`, assigns pane target to existing session or registers new one
- Running `opencode`: discovers `--port` flag from `/proc/<pid>/cmdline`, registers SSE connection
- Running `codex`: resolves the exact session id from the codex process's open rollout fd, registers the (idle) session, and titles it from the threads DB by id; state comes from hooks; the session is pruned when the pane disappears (Codex has no `SessionEnd`)

Handles sessions started without wrappers and server restarts while sessions are active.

## 7. Picker Rendering

`_navigator_picker.sh` reads from state DB. Rows include a tool glyph column (󰚩 /  / ✦) between the state dot and session name.

View switching via fzf `--listen` + background watcher polling `@agents-gen`.

Selection routing by tool:
- Active: navigate to tmux pane (all tools)
- Recent Claude: `claude -r <session_id>`
- Recent OpenCode: `opencode -s <session_id>` in session directory
- Recent Codex: `codex resume <session_id>` in session directory

### Recent sessions

When a session is removed — Claude `SessionEnd` (or scanner prune), OpenCode SSE-drop or pane prune, Codex pane-close prune — the call site invokes `Reconciler.AddRecent`, which records it in a shared in-memory `Recents` ring (`state/recent.go`): most-recent first, deduped by `tool+session_id+host`, capped at 50, with empty name/dir filled from the `session_names` cache. `Reconcile()` sources `allRecent` from the ring and `WriteSnapshot` writes it to the `recent` table each pass.

Because `WriteSnapshot` rewrites the `recent` table on every reconcile, the ring is seeded from the DB at startup (`db.LoadRecent`) so the list survives a server restart (the first reconcile re-persists the reloaded rows rather than wiping them). For OpenCode the representative session of a removed instance is captured (via `Store.RecentInfo`, read under lock before removal); Codex placeholders (no session id) are skipped.

## 8. Process Lifecycle

Server runs as `tmux-agents.service` (systemd user unit, `Type=exec`, `Restart=on-failure`).

`agents.tmux` checks `GET /healthz` on plugin load. If unreachable, starts the service via `systemctl --user start`.

Graceful shutdown on SIGTERM: stops HTTP listener, cancels all SSE goroutines, writes final state, closes DB.

## 9. Last-Window Toggle

A small convenience that is **independent of the Go server** — pure tmux hooks +
shell (`scripts/last-window.sh`), so it works even when the status server is down.
It tracks the previously-focused window *across sessions* (tmux's built-in
`last-window` is per-session only).

- **`track`** — invoked by the `session-window-changed` and `client-session-changed`
  hooks. Records the current and previous focus as `session:window.pane` in two
  global options, `@agents-lastwin-cur` / `@agents-lastwin-prev`. Redundant
  same-window fires are ignored so `prev` is never clobbered with `cur`.
- **`jump`** — bound to `@agents-last-window-key` (opt-in; unset = no binding).
  Switches to `@agents-lastwin-prev` via `select-window` → `select-pane` →
  `switch-client` (mirroring the picker's navigation), guarded so a since-closed
  target is a silent no-op. Because the jump itself fires the tracking hooks, the
  binding ping-pongs between the two most recent windows.

`agents.tmux` registers the hooks once per server, guarded by the
`@agents-lastwin-hooked` sentinel option: this file is re-sourced on every
tmux/TPM reload, so the guard prevents duplicate appended hooks; it clears on
server restart, exactly when the hooks themselves reset. Hooks are appended with
`set-hook -ga`, so the tracker coexists with any future consumer of those hooks.
The feature touches none of the server, DB, reconciler, pills, or picker.
