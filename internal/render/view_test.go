package render

import (
	"strings"
	"testing"

	"agentmon/internal/collector"
)

func TestBodyLinesGroupsAndTree(t *testing.T) {
	sessions := []collector.Session{
		{ID: "w", Name: "work", Project: "proj", Kind: "interactive", Mode: collector.Determinate, Done: 6, Total: 10, Status: "busy",
			Children: []collector.Session{
				{ID: "c", Name: "Review Task 6", Kind: "sub:general-purpose", Mode: collector.Indeterminate, Status: "idle"},
			}},
		{ID: "b", Name: "bgjob", Project: "proj", Kind: "bg", JobState: "blocked", Blocked: true, NeedsHint: "commit now?", Mode: collector.Determinate, Done: 41, Total: 41},
	}
	out := strings.Join(BodyLines(sessions, 0, nil), "\n")

	for _, want := range []string{"▸ proj", "└─", "6/10", "busy", "⏸ blocked", "41/41", "needs: commit now?"} {
		if !strings.Contains(out, want) {
			t.Errorf("body missing %q:\n%s", want, out)
		}
	}
}

func TestBodyLinesDimsDoneRow(t *testing.T) {
	sessions := []collector.Session{
		{ID: "x", Name: "done-one", Project: "proj", Mode: collector.Indeterminate, Status: "idle"},
	}
	plain := strings.Join(BodyLines(sessions, 0, nil), "\n")
	if strings.Contains(plain, "\x1b[2m") {
		t.Errorf("nil dim set should not add faint codes:\n%q", plain)
	}
	dimmed := strings.Join(BodyLines(sessions, 0, map[string]bool{"x": true}), "\n")
	if !strings.Contains(dimmed, "\x1b[2m") {
		t.Errorf("expected faint code around dimmed row:\n%q", dimmed)
	}
	if !strings.Contains(dimmed, "done-one") {
		t.Errorf("dimmed row should still contain its content:\n%q", dimmed)
	}
}

func TestCountSessions(t *testing.T) {
	sessions := []collector.Session{
		{ID: "1", Status: "busy", Mode: collector.Indeterminate},                  // busy
		{ID: "2", Status: "idle", Mode: collector.Indeterminate},                  // done (not busy)
		{ID: "3", Kind: "bg", Blocked: true, JobState: "blocked"},                 // blocked
		{ID: "4", Status: "busy", Mode: collector.Determinate, Done: 2, Total: 5}, // busy
	}
	c := CountSessions(sessions)
	if c.Active != 4 {
		t.Errorf("Active=%d want 4", c.Active)
	}
	if c.Busy != 2 {
		t.Errorf("Busy=%d want 2", c.Busy)
	}
	if c.Blocked != 1 {
		t.Errorf("Blocked=%d want 1", c.Blocked)
	}
}
