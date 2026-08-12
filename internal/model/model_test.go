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
	m.sessions = []collector.Session{{ID: "a", Status: "busy", Mode: collector.Determinate, Done: 6, Total: 10}}
	m.seeded = true
	m2, evs := m.applyPoll([]collector.Session{{ID: "a", Status: "idle", Mode: collector.Determinate, Done: 10, Total: 10}})
	if len(evs) != 1 || evs[0].Kind != DoneEvent {
		t.Fatalf("want one DoneEvent, got %v", evs)
	}
	if len(m2.sessions) != 1 || !m2.sessions[0].IsDone() {
		t.Errorf("snapshot not updated: %+v", m2.sessions)
	}
}

func TestApplyPollOpenCodeIdleWithPendingTodosCompletes(t *testing.T) {
	m := New(fakeCollectionSource{}, nil, time.Second)
	m.seeded = true
	m.sessions = []collector.Session{{
		ID: "opencode:ses_1", NativeID: "ses_1", Agent: collector.AgentOpenCode, Status: "busy",
		Mode: collector.Determinate, Done: 1, Total: 1,
	}}

	next := []collector.Session{{
		ID: "opencode:ses_1", NativeID: "ses_1", Agent: collector.AgentOpenCode, Status: "idle",
		Mode: collector.Determinate, Done: 0, Total: 1,
	}}
	m, events := m.applyPoll(next)
	if len(events) != 1 || events[0].Kind != DoneEvent || events[0].SessionID != "opencode:ses_1" {
		t.Fatalf("idle lifecycle events=%v", events)
	}
	if !m.sessions[0].IsDone() {
		t.Fatalf("idle session not done: %+v", m.sessions[0])
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

func TestOpenIdleStaysVisibleNotDimmed(t *testing.T) {
	t0 := time.Unix(1000, 0)
	m := seededModelAt(t0)
	m.sessions = []collector.Session{busy("a")}
	m, evs := m.applyPoll([]collector.Session{idle("a")}) // stopped, still open
	if len(evs) != 1 || evs[0].Kind != DoneEvent {
		t.Fatalf("busy→idle should chime once: %v", evs)
	}
	m.nowFn = func() time.Time { return t0.Add(10 * time.Second) } // long after old grace
	out := m.View()
	if !strings.Contains(out, "worker") {
		t.Fatalf("open idle session must stay visible:\n%s", out)
	}
	if strings.Contains(out, "\x1b[2m") {
		t.Fatalf("open session must not be dimmed:\n%q", out)
	}
}

func TestExitGhostShowsThenDisappears(t *testing.T) {
	t0 := time.Unix(1000, 0)
	m := seededModelAt(t0)
	m.sessions = []collector.Session{busy("a")}
	m, _ = m.applyPoll([]collector.Session{}) // session gone from poll → EXIT ghost
	out := m.View()
	if !strings.Contains(out, "worker") || !strings.Contains(out, "EXIT") {
		t.Fatalf("exited session should show EXIT within 3s:\n%s", out)
	}
	if !strings.Contains(out, "\x1b[2m") {
		t.Fatalf("exit ghost should be dimmed:\n%q", out)
	}
	m.nowFn = func() time.Time { return t0.Add(graceDuration + time.Second) }
	m, _ = m.applyPoll([]collector.Session{})
	if strings.Contains(m.View(), "worker") {
		t.Fatalf("exited session should disappear after 3s:\n%s", m.View())
	}
}

func TestExitReappearCancelsGhost(t *testing.T) {
	t0 := time.Unix(1000, 0)
	m := seededModelAt(t0)
	m.sessions = []collector.Session{busy("a")}
	m, _ = m.applyPoll([]collector.Session{})          // gone → ghost
	m, _ = m.applyPoll([]collector.Session{busy("a")}) // back
	if len(m.exiting) != 0 {
		t.Fatalf("reappeared session should clear its ghost: %v", m.exiting)
	}
}

func TestAttentionChimeEdge(t *testing.T) {
	t0 := time.Unix(1000, 0)
	m := seededModelAt(t0)
	m.sessions = []collector.Session{busy("a")}
	m, evs := m.applyPoll([]collector.Session{idle("a")}) // busy→idle
	if len(evs) != 1 {
		t.Fatalf("enter idle: want 1 chime, got %v", evs)
	}
	m, evs = m.applyPoll([]collector.Session{idle("a")}) // staying idle
	if len(evs) != 0 {
		t.Fatalf("staying idle: want 0, got %v", evs)
	}
	m, _ = m.applyPoll([]collector.Session{busy("a")})   // back to busy
	m, evs = m.applyPoll([]collector.Session{idle("a")}) // idle again
	if len(evs) != 1 {
		t.Fatalf("re-idle: want 1, got %v", evs)
	}
}

func TestSubagentFadesOutWhenDone(t *testing.T) {
	t0 := time.Unix(1000, 0)
	m := seededModelAt(t0)
	parent := func(childStatus string) []collector.Session {
		return []collector.Session{{ID: "p", Name: "parent", Project: "x", Status: "busy", Mode: collector.Indeterminate,
			Children: []collector.Session{{ID: "c", Name: "kid-task", Status: childStatus, Mode: collector.Indeterminate}}}}
	}
	m.sessions = parent("busy")
	m, evs := m.applyPoll(parent("idle")) // subagent finished at t0
	if len(evs) != 0 {
		t.Fatalf("subagent finishing must be silent, got %v", evs)
	}
	if !strings.Contains(m.View(), "kid-task") {
		t.Fatalf("finished subagent should show within grace:\n%s", m.View())
	}
	m.nowFn = func() time.Time { return t0.Add(graceDuration + time.Second) }
	if strings.Contains(m.View(), "kid-task") {
		t.Fatalf("finished subagent should fade out after grace:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "parent") {
		t.Fatalf("parent session must stay visible:\n%s", m.View())
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

func TestCollapseKeepsAgentHierarchy(t *testing.T) {
	m := New(fakeCollectionSource{}, nil, time.Second)
	m, _ = m.applyPoll([]collector.Session{{
		ID: "codex:1", Agent: collector.AgentCodex, Model: "gpt-5.6-sol", Name: "codex-work", Project: "proj",
		Status: "busy", Mode: collector.Indeterminate,
		Children: []collector.Session{{ID: "codex:child", Agent: collector.AgentCodex, Name: "review", Status: "busy", Mode: collector.Indeterminate}},
	}})
	m.width = 70

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	out := updated.(Model).View()
	for _, want := range []string{"proj", "Codex", "codex-work", "gpt-5.6-sol"} {
		if !strings.Contains(out, want) {
			t.Fatalf("collapsed view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "review") {
		t.Fatalf("collapsed view kept subagent:\n%s", out)
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
