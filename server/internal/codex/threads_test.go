package codex

import "testing"

func TestIsAutoTitle(t *testing.T) {
	cases := []struct {
		name  string
		title string
		fum   string
		want  bool
	}{
		{"empty title", "", "anything", true},
		{"exact first message", "Plase read the doc", "Plase read the doc", true},
		{"munged prefix of first message", "with exactly: original-task", "Reply with exactly: original-task", true},
		{"truncated with ellipsis", "Plase read and carefully…", "Plase read and carefully assess the doc", true},
		{"explicit rename", "trawl-review", "Plase read and carefully assess the doc", false},
		{"codex summary", "Parser fix", "Help me fix the parser bug", false},
		{"rename with no first message", "my-feature", "", false},
	}
	for _, c := range cases {
		got := Thread{Title: c.title, FirstUserMessage: c.fum}.IsAutoTitle()
		if got != c.want {
			t.Errorf("%s: IsAutoTitle(title=%q, fum=%q) = %v, want %v", c.name, c.title, c.fum, got, c.want)
		}
	}
}
