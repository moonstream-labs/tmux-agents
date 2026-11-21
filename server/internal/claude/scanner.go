package claude

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"

	"github.com/moonstream-labs/tmux-agents/internal/state"
)

// sessionFile is the structure of ~/.claude/sessions/<pid>.json
type sessionFile struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	StartedAt int64  `json:"startedAt"`
	Kind      string `json:"kind"`
}

// ScanActiveSessions reads ~/.claude/sessions/*.json to discover
// already-running Claude Code instances. Called once at server startup.
func ScanActiveSessions(store *Store) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	pattern := filepath.Join(home, ".claude", "sessions", "*.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}

	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var sf sessionFile
		if err := json.Unmarshal(data, &sf); err != nil {
			continue
		}

		if sf.SessionID == "" || sf.PID == 0 {
			continue
		}

		// Verify process is still alive.
		if err := syscall.Kill(sf.PID, 0); err != nil {
			continue
		}

		// Register with unknown state — will be updated by hooks.
		if _, exists := store.Get(sf.SessionID); exists {
			continue
		}

		sess := store.Register(sf.SessionID, sf.CWD)
		sess.State = state.StateUnknown
		sess.PID = sf.PID
		log.Printf("claude: recovered session id=%s cwd=%s pid=%d", sf.SessionID, sf.CWD, sf.PID)
	}
}

// HasTranscript reports whether a session has a transcript on disk. The
// throwaway id `claude --resume` runs under while its picker is open never gets
// one, which is how a displaced placeholder is told apart from a real session
// that ended.
func HasTranscript(sessionID string) bool {
	home, err := os.UserHomeDir()
	if err != nil || sessionID == "" {
		return false
	}
	matches, err := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl"))
	return err == nil && len(matches) > 0
}

// ScanSessionTitle reads the JSONL file for a session and extracts
// the most recent custom-title entry.
func ScanSessionTitle(sessionID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	// Claude Code stores session JSONLs under project directories.
	// We need to search all project dirs for this session ID.
	pattern := filepath.Join(home, ".claude", "projects", "*", sessionID+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return "", fmt.Errorf("no JSONL found for session %s", sessionID)
	}

	// Read the last match (most likely the active one).
	data, err := os.ReadFile(matches[len(matches)-1])
	if err != nil {
		return "", err
	}

	// Scan for the last custom-title entry.
	var lastTitle string
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var entry struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.Type == "custom-title" && entry.CustomTitle != "" {
			lastTitle = entry.CustomTitle
		}
	}

	return lastTitle, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
