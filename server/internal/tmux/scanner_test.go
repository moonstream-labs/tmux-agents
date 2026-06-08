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
