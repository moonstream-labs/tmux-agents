package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexSessionIDFromRollout(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{
			"/home/u/.codex/sessions/2026/06/07/rollout-2026-06-07T22-30-32-019ea511-44dd-7212-ac9b-f07bdc3aa218.jsonl",
			"019ea511-44dd-7212-ac9b-f07bdc3aa218",
		},
		{"/tmp/notes.txt", ""},
		{"/home/u/.codex/log/codex-tui.log", ""},
		{"rollout-2026-06-08T00-49-09-019ea590-2c89-7923-a78c-6a1f2052a029.jsonl", "019ea590-2c89-7923-a78c-6a1f2052a029"},
	}
	for _, c := range cases {
		if got := CodexSessionIDFromRollout(c.path); got != c.want {
			t.Errorf("CodexSessionIDFromRollout(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestParsePaneLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		ok   bool
		want PaneInfo
	}{
		{
			name: "full row with pane_id",
			line: "main:1.0\tclaude\t1234\t/home/u/proj\t%81",
			ok:   true,
			want: PaneInfo{Target: "main:1.0", Command: "claude", PID: 1234, CWD: "/home/u/proj", PaneID: "%81"},
		},
		{
			name: "legacy row without pane_id",
			line: "main:2.1\topencode\t999\t/tmp",
			ok:   true,
			want: PaneInfo{Target: "main:2.1", Command: "opencode", PID: 999, CWD: "/tmp"},
		},
		{name: "empty line", line: "", ok: false},
		{name: "too few fields", line: "main:1.0\tclaude", ok: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parsePaneLine(c.line)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if ok && got != c.want {
				t.Errorf("parsePaneLine(%q) = %+v, want %+v", c.line, got, c.want)
			}
		})
	}
}

func TestIsDescendant(t *testing.T) {
	self, parent := os.Getpid(), os.Getppid()
	if !isDescendant(self, self) {
		t.Error("a process counts as its own descendant")
	}
	if !isDescendant(self, parent) {
		t.Error("this process descends from its parent")
	}
	if isDescendant(parent, self) {
		t.Error("the parent does not descend from this process")
	}
	if isDescendant(0x7ffffff0, parent) {
		t.Error("a pid with no /proc entry is not a descendant")
	}
}

// A claude process rewrites ~/.claude/sessions/<pid>.json when it switches
// session (`--resume` picker, /clear). The scanner must see the new id on its
// next pass, through the known-pid fast path as well as the tree walk.
func TestClaudeSessionForPaneRereadsChangedSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	self := os.Getpid()
	file := filepath.Join(dir, fmt.Sprintf("%d.json", self))
	write := func(id string) {
		t.Helper()
		body := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"cwd":"/x"}`, self, id)
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// First sight: nothing known yet; claude is the pane's own process.
	write("placeholder")
	sf := ClaudeSessionForPane(self, 0)
	if sf == nil || sf.SessionID != "placeholder" || sf.PID != self {
		t.Fatalf("first sight = %+v, want placeholder / pid %d", sf, self)
	}

	// The process switches session. Re-check via the fast path: the known pid
	// is alive under the pane (our parent stands in for the pane's shell).
	write("real")
	sf = ClaudeSessionForPane(os.Getppid(), self)
	if sf == nil || sf.SessionID != "real" {
		t.Fatalf("fast-path re-check = %+v, want real", sf)
	}

	// A known pid that is not a live process under the pane is ignored and the
	// tree walk is used instead.
	sf = ClaudeSessionForPane(self, 0x7ffffff0)
	if sf == nil || sf.SessionID != "real" {
		t.Fatalf("stale known pid = %+v, want real via the tree walk", sf)
	}

	// No session file (yet): nothing to bind.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if sf := ClaudeSessionForPane(self, self); sf != nil {
		t.Fatalf("no session file should yield nil, got %+v", sf)
	}
}
