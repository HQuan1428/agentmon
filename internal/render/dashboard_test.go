package render

import (
	"strings"
	"testing"

	"agentmon/internal/collector"
)

func TestComposeChrome(t *testing.T) {
	body := BodyLines([]collector.Session{
		{ID: "w", Name: "work", Project: "proj", Mode: collector.Determinate, Done: 6, Total: 10, Status: "busy"},
	}, 0, nil)
	out := Compose(90, 24, Counts{Active: 1, Busy: 1}, true, body, 0, false)

	// Rounded border corners.
	for _, want := range []string{"╭", "╮", "╰", "╯"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing border corner %q", want)
		}
	}
	// Header identity + version badge + column headers + footer + counter + bell.
	for _, want := range []string{"agentmon", "v0.1.0", "PROJECT / SESSION", "PROGRESS", "TASKS", "STATUS", "quit", "sound", "scroll", "active", "🔊 ON"} {
		if !strings.Contains(out, want) {
			t.Errorf("compose missing %q", want)
		}
	}
	if strings.Contains(out, "🔇 OFF") {
		t.Error("bell should read ON when soundOn=true")
	}
}

func TestComposeBellOff(t *testing.T) {
	out := Compose(90, 24, Counts{}, false, nil, 0, true)
	if !strings.Contains(out, "🔇 OFF") {
		t.Errorf("bell should read OFF when soundOn=false:\n%s", out)
	}
}

func TestComposeEmptyStateHero(t *testing.T) {
	out := Compose(90, 24, Counts{}, true, nil, 0, true)
	if !strings.Contains(out, "💤") {
		t.Errorf("empty state should show the 💤 hero:\n%s", out)
	}
	if !strings.Contains(out, "Tip:") {
		t.Errorf("empty state should show a launch tip:\n%s", out)
	}
	if strings.Contains(out, "PROGRESS") {
		t.Error("empty state should not render the column header")
	}
}

func TestComposeCapsToHeight(t *testing.T) {
	// More body than fits: output must never exceed the given height.
	var many []collector.Session
	for i := 0; i < 40; i++ {
		many = append(many, collector.Session{ID: string(rune('a' + i)), Name: "s", Project: "p", Mode: collector.Indeterminate, Status: "busy"})
	}
	out := Compose(90, 20, Counts{Active: 40}, true, BodyLines(many, 0, nil), 0, false)
	if n := strings.Count(out, "\n") + 1; n > 20 {
		t.Errorf("compose produced %d lines, exceeds height 20", n)
	}
}
