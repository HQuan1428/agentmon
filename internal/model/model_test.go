package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"agentmon/internal/collector"
)

func TestApplyPollFiresEvents(t *testing.T) {
	m := New("/fake", nil, time.Second)
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
	m := New("/fake", nil, time.Second)
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

func TestToggleSound(t *testing.T) {
	m := New("/fake", nil, time.Second)
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
	m := New("/fake", nil, time.Second)
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
	m := New("/fake", nil, time.Second)
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
