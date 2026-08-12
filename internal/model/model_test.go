package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agentmon/internal/collector"
)

type fakeCollectionSource struct{ result collector.Collection }

func (f fakeCollectionSource) Collect() collector.Collection { return f.result }

func TestPollCmdUsesNormalizedSource(t *testing.T) {
	source := fakeCollectionSource{result: collector.Collection{Sessions: []collector.Session{{ID: "codex:1"}}}}
	m := New(source, nil, time.Second)
	msg := m.pollCmd()().(pollMsg)
	if len(msg) != 1 || msg[0].ID != "codex:1" {
		t.Fatalf("msg=%+v", msg)
	}
}

func TestApplyPollFiresEvents(t *testing.T) {
	m := New(fakeCollectionSource{}, nil, time.Second)
	m.sessions = []collector.Session{{ID: "a", Mode: collector.Determinate, Done: 6, Total: 10}}
	m.seeded = true
	m2, evs := m.applyPoll([]collector.Session{{ID: "a", Mode: collector.Determinate, Done: 10, Total: 10}})
	if len(evs) != 1 || evs[0].Kind != DoneEvent {
		t.Fatalf("want one DoneEvent, got %v", evs)
	}
	if len(m2.sessions) != 1 || !m2.sessions[0].IsDone() {
		t.Errorf("snapshot not updated: %+v", m2.sessions)
	}
}

func TestApplyPollFirstPollNoChime(t *testing.T) {
	m := New(fakeCollectionSource{}, nil, time.Second)
	sessions := []collector.Session{
		{ID: "a", Mode: collector.Determinate, Done: 10, Total: 10}, // already done
		{ID: "b", Kind: "bg", Blocked: true, NeedsHint: "approve pls"},
	}
	m2, evs := m.applyPoll(sessions)
	if len(evs) != 0 {
		t.Fatalf("want zero events on first poll, got %v", evs)
	}
	if len(m2.sessions) != 2 {
		t.Errorf("snapshot not stored on first poll: %+v", m2.sessions)
	}
	if !m2.seeded {
		t.Error("expected seeded=true after first poll")
	}
}

// seededModelAt returns a model with a fixed clock, already past the first
// poll so applyPoll takes the diffing path.
func seededModelAt(now time.Time) Model {
	m := New(fakeCollectionSource{}, nil, time.Second)
	m.nowFn = func() time.Time { return now }
	m.seeded = true
	return m
}

func busy(id string) collector.Session {
	return collector.Session{ID: id, Name: "worker", Project: "p", Mode: collector.Indeterminate, Status: "busy"}
}
func idle(id string) collector.Session {
	return collector.Session{ID: id, Name: "worker", Project: "p", Mode: collector.Indeterminate, Status: "idle"}
}

func TestDoneWithinGraceVisibleAndDimmed(t *testing.T) {
	t0 := time.Unix(1000, 0)
	m := seededModelAt(t0)
	m.sessions = []collector.Session{busy("a")}
	m, _ = m.applyPoll([]collector.Session{idle("a")}) // just went done at t0
	out := m.View()
	if !strings.Contains(out, "worker") {
		t.Fatalf("done session should stay visible within grace:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[2m") {
		t.Fatalf("done-within-grace row should be dimmed:\n%q", out)
	}
}

func TestDonePastGraceHidden(t *testing.T) {
	t0 := time.Unix(1000, 0)
	m := seededModelAt(t0)
	m.sessions = []collector.Session{busy("a")}
	m, _ = m.applyPoll([]collector.Session{idle("a")})
	m.nowFn = func() time.Time { return t0.Add(graceDuration + time.Second) }
	out := m.View()
	if strings.Contains(out, "worker") {
		t.Fatalf("done session past grace should be hidden:\n%s", out)
	}
}

func TestDoneThenBusyAgainVisibleNotDimmed(t *testing.T) {
	t0 := time.Unix(1000, 0)
	m := seededModelAt(t0)
	m.sessions = []collector.Session{busy("a")}
	m, _ = m.applyPoll([]collector.Session{idle("a")}) // done
	m, _ = m.applyPoll([]collector.Session{busy("a")}) // working again
	out := m.View()
	if !strings.Contains(out, "worker") {
		t.Fatalf("re-busy session should be visible:\n%s", out)
	}
	if strings.Contains(out, "\x1b[2m") {
		t.Fatalf("re-busy session should not be dimmed:\n%q", out)
	}
}

func TestFirstPollDoneHiddenImmediately(t *testing.T) {
	t0 := time.Unix(1000, 0)
	m := New(fakeCollectionSource{}, nil, time.Second)
	m.nowFn = func() time.Time { return t0 }
	m, evs := m.applyPoll([]collector.Session{idle("a")})
	if len(evs) != 0 {
		t.Fatalf("first poll must not fire events, got %v", evs)
	}
	out := m.View()
	if strings.Contains(out, "worker") {
		t.Fatalf("already-done session at startup should be hidden, not granted grace:\n%s", out)
	}
}

func TestToggleSound(t *testing.T) {
	m := New(fakeCollectionSource{}, nil, time.Second)
	if !m.soundOn {
		t.Fatal("sound should default on")
	}
	m = m.toggleSound()
	if m.soundOn {
		t.Error("sound should be off after toggle")
	}
}

func manySessions(n int) []collector.Session {
	sessions := make([]collector.Session, 0, n)
	for i := 0; i < n; i++ {
		sessions = append(sessions, collector.Session{
			ID:      fmt.Sprintf("s%d", i),
			Name:    fmt.Sprintf("sess-%d", i),
			Project: "proj",
			Mode:    collector.Determinate,
			Done:    1,
			Total:   10,
		})
	}
	return sessions
}

func TestViewWindowsToHeight(t *testing.T) {
	m := New(fakeCollectionSource{}, nil, time.Second)
	m, _ = m.applyPoll(manySessions(10))

	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 5})
	m = mm.(Model)

	out := m.View()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) > 5 {
		t.Fatalf("want at most 5 lines, got %d:\n%s", len(lines), out)
	}
}

func TestScrollClampsAtBottom(t *testing.T) {
	m := New(fakeCollectionSource{}, nil, time.Second)
	m, _ = m.applyPoll(manySessions(10))

	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 5})
	m = mm.(Model)

	max := m.maxScroll()
	if max <= 0 {
		t.Fatalf("expected positive maxScroll for content taller than viewport, got %d", max)
	}

	for i := 0; i < max+20; i++ {
		mm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = mm.(Model)
	}
	if m.scroll != max {
		t.Fatalf("scroll should clamp at maxScroll=%d, got %d", max, m.scroll)
	}

	out := m.View()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) > 5 {
		t.Fatalf("windowed content out of bounds: %d lines:\n%s", len(lines), out)
	}
}
