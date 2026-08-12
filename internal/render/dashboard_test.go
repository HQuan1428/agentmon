package render

import (
	"regexp"
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

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestComposeHasHeaderSeparator(t *testing.T) {
	body := BodyLines([]collector.Session{
		{ID: "w", Name: "work", Project: "proj", Mode: collector.Determinate, Done: 6, Total: 10, Status: "busy"},
	}, 0, nil)
	out := stripANSI(Compose(90, 24, Counts{Active: 1, Busy: 1}, true, body, 0, false))

	// Rule-like content lines (inside the side borders, mostly ─): the header
	// separator plus the column-header rule → at least two.
	rules := 0
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "│") && strings.Count(ln, "─") >= 20 {
			rules++
		}
	}
	if rules < 2 {
		t.Errorf("expected ≥2 rule lines (header separator + column rule), got %d:\n%s", rules, out)
	}
}

func TestSmallHeaderResponsive(t *testing.T) {
	// A narrow frame drops the title/version and shows only icons + counts.
	out := stripANSI(Compose(40, 20, Counts{Active: 2, Busy: 1}, true, nil, 0, true))
	if strings.Contains(out, "agentmon") || strings.Contains(out, "v0.1.0") {
		t.Errorf("small header should drop title/version:\n%s", out)
	}
	if strings.Contains(out, "active") || strings.Contains(out, "busy") {
		t.Errorf("small header should use icon+count, not words:\n%s", out)
	}
	if !strings.Contains(out, "●") || !strings.Contains(out, "2") {
		t.Errorf("small header should still show the status icons and counts:\n%s", out)
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
