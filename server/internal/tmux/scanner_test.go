package tmux

import "testing"

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
