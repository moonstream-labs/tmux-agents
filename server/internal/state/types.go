package state

type Tool string

const (
	ToolClaude   Tool = "claude"
	ToolOpenCode Tool = "opencode"
	ToolCodex    Tool = "codex"
)

type SessionState string

const (
	StateIdle       SessionState = "idle"
	StateRunning    SessionState = "running"
	StatePermission SessionState = "permission"
	StateUnknown    SessionState = "unknown"
)

type PaneRow struct {
	Target    string
	PaneID    string // tmux pane id (%N); stable per pane, drives nav ordering
	Tool      Tool
	State     SessionState
	SessionID string
	Name      string
	Dir       string
	Updated   int64
	Host      string
}

type RecentRow struct {
	Tool        Tool
	SessionID   string
	Name        string
	Dir         string
	Updated     int64
	Host        string
	TmuxSession string
}
