package tmux

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// PaneInfo represents a tmux pane from list-panes.
type PaneInfo struct {
	Target  string // session:window.pane
	PaneID  string // pane_id (%N) — stable per pane, used for nav ordering
	Command string // pane_current_command
	PID     int    // pane_pid
	CWD     string // pane_current_path
}

// listPanesFormat is the -F format for ListPanes. pane_id (%N) is last so the
// parser can tolerate older/shorter rows.
const listPanesFormat = "#{session_name}:#{window_index}.#{pane_index}\t#{pane_current_command}\t#{pane_pid}\t#{pane_current_path}\t#{pane_id}"

// ListPanes returns all panes in the tmux server.
func ListPanes() ([]PaneInfo, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", listPanesFormat).Output()
	if err != nil {
		return nil, err
	}

	var panes []PaneInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if p, ok := parsePaneLine(line); ok {
			panes = append(panes, p)
		}
	}
	return panes, nil
}

// parsePaneLine parses one tmux list-panes row in listPanesFormat. The trailing
// cwd and pane_id fields are optional, so a shorter row still parses.
func parsePaneLine(line string) (PaneInfo, bool) {
	if line == "" {
		return PaneInfo{}, false
	}
	parts := strings.SplitN(line, "\t", 5)
	if len(parts) < 3 {
		return PaneInfo{}, false
	}
	pid, _ := strconv.Atoi(parts[2])
	p := PaneInfo{
		Target:  parts[0],
		Command: parts[1],
		PID:     pid,
	}
	if len(parts) >= 4 {
		p.CWD = parts[3]
	}
	if len(parts) >= 5 {
		p.PaneID = parts[4]
	}
	return p, true
}

// PaneExists checks if a tmux pane target is still alive.
func PaneExists(target string) bool {
	err := exec.Command("tmux", "display-message", "-p", "-t", target, "#{pane_id}").Run()
	return err == nil
}

// ResolvePaneTarget converts a tmux pane id (e.g. "%81" from $TMUX_PANE) into a
// session:window.pane target. Used to correlate Codex hooks, which self-report
// their pane via $TMUX_PANE, to a stable pane target.
func ResolvePaneTarget(paneID string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", paneID,
		"#{session_name}:#{window_index}.#{pane_index}").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ClaudeSessionFile is the structure of ~/.claude/sessions/<pid>.json.
type ClaudeSessionFile struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
}

// FindClaudeSessionForPaneTree walks the process tree under panePID and
// tries to find a matching ~/.claude/sessions/<pid>.json file.
func FindClaudeSessionForPaneTree(panePID int) *ClaudeSessionFile {
	// Try the pane PID directly (when claude is the pane command).
	if sf, err := ReadClaudeSessionByPID(panePID); err == nil {
		return sf
	}
	// Walk children and grandchildren.
	for _, cpid := range findChildPIDs(panePID) {
		if sf, err := ReadClaudeSessionByPID(cpid); err == nil {
			return sf
		}
	}
	return nil
}

// ReadClaudeSessionByPID reads ~/.claude/sessions/<pid>.json.
func ReadClaudeSessionByPID(pid int) (*ClaudeSessionFile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", pid))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var sf ClaudeSessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, err
	}
	return &sf, nil
}

// DiscoverOpenCodePort attempts to find the --port flag from
// /proc/<pid>/cmdline for an opencode process.
func DiscoverOpenCodePort(panePID int) (int, bool) {
	// Walk child processes looking for opencode.
	children := findChildPIDs(panePID)
	children = append(children, panePID)

	for _, cpid := range children {
		cmdline, err := readCmdline(cpid)
		if err != nil {
			continue
		}

		// Check if this is an opencode process.
		isOpenCode := false
		for _, arg := range cmdline {
			if strings.Contains(arg, "opencode") {
				isOpenCode = true
				break
			}
		}
		if !isOpenCode {
			continue
		}

		// Extract --port value.
		for i, arg := range cmdline {
			if arg == "--port" && i+1 < len(cmdline) {
				if port, err := strconv.Atoi(cmdline[i+1]); err == nil {
					return port, true
				}
			}
			if strings.HasPrefix(arg, "--port=") {
				if port, err := strconv.Atoi(strings.TrimPrefix(arg, "--port=")); err == nil {
					return port, true
				}
			}
		}
	}

	return 0, false
}

var codexRolloutRe = regexp.MustCompile(`([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)

// CodexSessionIDFromRollout extracts the session (thread) id from a Codex
// rollout file path (…/rollout-<timestamp>-<uuid>.jsonl). Returns "" otherwise.
func CodexSessionIDFromRollout(path string) string {
	if !strings.Contains(path, "rollout-") {
		return ""
	}
	if m := codexRolloutRe.FindStringSubmatch(path); m != nil {
		return m[1]
	}
	return ""
}

// FindCodexSessionForPaneTree walks the process tree under panePID, finds the
// codex process, and returns its session id from the open rollout file
// descriptor. This gives an exact pane→session mapping independent of cwd
// (Codex keeps the rollout JSONL open for the session's lifetime). Returns "".
func FindCodexSessionForPaneTree(panePID int) string {
	pids := append([]int{panePID}, findChildPIDs(panePID)...)
	for _, pid := range pids {
		if sid := codexSessionFromFDs(pid); sid != "" {
			return sid
		}
	}
	return ""
}

func codexSessionFromFDs(pid int) string {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if sid := CodexSessionIDFromRollout(target); sid != "" {
			return sid
		}
	}
	return ""
}

func findChildPIDs(parentPID int) []int {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(parentPID)).Output()
	if err != nil {
		return nil
	}

	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			pids = append(pids, pid)
			// Also check grandchildren.
			pids = append(pids, findChildPIDs(pid)...)
		}
	}
	return pids
}

func readCmdline(pid int) ([]string, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty cmdline")
	}

	// cmdline is null-delimited.
	var args []string
	for _, arg := range strings.Split(string(data), "\x00") {
		if arg != "" {
			args = append(args, arg)
		}
	}
	return args, nil
}

// ScannerCallbacks defines how the scanner notifies the main server
// about discovered and departed panes.
type ScannerCallbacks struct {
	// OnPanesListed is called once per scan with a target→pane-id (%N) map built
	// from the full list-panes pass, before any discovery/prune callback fires.
	// The reconciler stores it and stamps PaneRow.PaneID with no per-event
	// shell-out. It covers every pane (agent or not, tracked or not), so it has
	// no equivalent of the discovery "already tracked → skip" gap.
	OnPanesListed func(paneIDByTarget map[string]string)

	// OnClaudeDiscovered is called when a claude pane is found that is
	// not yet tracked. Returns true if the session was registered.
	OnClaudeDiscovered func(target string, pid int) bool

	// OnOpenCodeDiscovered is called when an opencode pane with a known
	// port is found that is not yet tracked.
	OnOpenCodeDiscovered func(target string, port int) bool

	// OnCodexDiscovered is called for each codex pane found, with its cwd and
	// pane pid. Codex hooks fire only on a turn (not on launch/resume/idle) and
	// never carry the session title, so the server uses this to resolve the
	// session id from the codex process's open rollout file and refresh the
	// title from Codex's threads DB.
	OnCodexDiscovered func(target, cwd string, pid int)

	// IsClaudeTracked returns true if the given pane target is already
	// tracked as a Claude Code session.
	IsClaudeTracked func(target string) bool

	// IsOpenCodeTracked returns true if the given pane target is already
	// tracked as an OpenCode session.
	IsOpenCodeTracked func(target string) bool

	// OnPaneGone is called when a previously tracked pane no longer runs an agent.
	OnPaneGone func(target string)

	// GetTrackedTargets returns all pane targets currently tracked by any store.
	GetTrackedTargets func() []string
}

// RunScanner periodically scans tmux panes to discover untracked sessions
// and prune dead panes. Runs until ctx is cancelled.
func RunScanner(ctx context.Context, interval time.Duration, cb ScannerCallbacks) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scanOnce(cb)
		}
	}
}

func scanOnce(cb ScannerCallbacks) {
	panes, err := ListPanes()
	if err != nil {
		return
	}

	// Publish the target→pane-id map first, so any pane discovered below already
	// sees its own %N when its discovery triggers a reconcile.
	if cb.OnPanesListed != nil {
		ids := make(map[string]string, len(panes))
		for _, p := range panes {
			if p.PaneID != "" {
				ids[p.Target] = p.PaneID
			}
		}
		cb.OnPanesListed(ids)
	}

	// Build set of panes running claude or opencode.
	agentPanes := make(map[string]bool)

	for _, p := range panes {
		switch p.Command {
		case "claude":
			agentPanes[p.Target] = true
			if cb.IsClaudeTracked != nil && cb.IsClaudeTracked(p.Target) {
				continue
			}
			if cb.OnClaudeDiscovered != nil {
				cb.OnClaudeDiscovered(p.Target, p.PID)
			}

		case "opencode":
			agentPanes[p.Target] = true
			if cb.IsOpenCodeTracked != nil && cb.IsOpenCodeTracked(p.Target) {
				continue
			}
			port, found := DiscoverOpenCodePort(p.PID)
			if found && cb.OnOpenCodeDiscovered != nil {
				cb.OnOpenCodeDiscovered(p.Target, port)
			}

		case "codex":
			// Codex hooks self-report state ($TMUX_PANE), but they fire only on a
			// turn — not on launch/resume/idle — and never carry the title. So the
			// scanner discovers each codex pane here (placeholder + title from the
			// threads DB); hooks refine state. Pruning is handled below.
			agentPanes[p.Target] = true
			if cb.OnCodexDiscovered != nil {
				cb.OnCodexDiscovered(p.Target, p.CWD, p.PID)
			}
		}
	}

	// Prune tracked sessions whose pane no longer runs an agent process.
	if cb.OnPaneGone != nil {
		if cb.GetTrackedTargets != nil {
			for _, target := range cb.GetTrackedTargets() {
				if !agentPanes[target] {
					cb.OnPaneGone(target)
				}
			}
		}
	}
}
