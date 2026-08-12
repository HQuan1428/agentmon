package model

import (
	"testing"

	"agentmon/internal/collector"
)

func TestDiffEvents(t *testing.T) {
	prev := []collector.Session{
		{ID: "a", Mode: collector.Determinate, Done: 6, Total: 10}, // not done
		{ID: "b", Kind: "bg", JobState: "running"},                 // not blocked
	}
	cur := []collector.Session{
		{ID: "a", Mode: collector.Determinate, Done: 10, Total: 10}, // -> done
		{ID: "b", Kind: "bg", JobState: "blocked", Blocked: true},   // -> blocked
	}
	evs := DiffEvents(prev, cur)
	got := map[string]EventKind{}
	for _, e := range evs {
		got[e.SessionID] = e.Kind
	}
	if got["a"] != DoneEvent {
		t.Errorf("a: want DoneEvent, got %v", got["a"])
	}
	if got["b"] != ApprovalEvent {
		t.Errorf("b: want ApprovalEvent, got %v", got["b"])
	}
}

func TestDiffEventsNoRepeat(t *testing.T) {
	done := []collector.Session{{ID: "a", Mode: collector.Determinate, Done: 10, Total: 10}}
	if evs := DiffEvents(done, done); len(evs) != 0 {
		t.Errorf("stable done should emit nothing, got %v", evs)
	}
}

func TestDiffEventsChildDone(t *testing.T) {
	prev := []collector.Session{{ID: "p", Status: "busy", Mode: collector.Indeterminate,
		Children: []collector.Session{{ID: "c", Mode: collector.Indeterminate, Status: "busy"}}}}
	cur := []collector.Session{{ID: "p", Status: "busy", Mode: collector.Indeterminate,
		Children: []collector.Session{{ID: "c", Mode: collector.Indeterminate, Status: "idle"}}}}
	evs := DiffEvents(prev, cur)
	if len(evs) != 1 || evs[0].SessionID != "c" || evs[0].Kind != DoneEvent {
		t.Errorf("want child DoneEvent, got %v", evs)
	}
}

func TestDiffEventsNamespacedIDsDoNotCollide(t *testing.T) {
	prev := []collector.Session{
		{ID: "same", Agent: collector.AgentClaude, Mode: collector.Indeterminate, Status: "busy"},
		{ID: "same", Agent: collector.AgentCodex, Mode: collector.Indeterminate, Status: "busy"},
	}
	cur := []collector.Session{
		{ID: "same", Agent: collector.AgentClaude, Mode: collector.Indeterminate, Status: "busy"},
		{ID: "same", Agent: collector.AgentCodex, Mode: collector.Indeterminate, Status: "idle"},
	}
	evs := DiffEvents(prev, cur)
	if len(evs) != 1 || evs[0].SessionID != "codex:same" || evs[0].Kind != DoneEvent {
		t.Fatalf("want one codex:same DoneEvent, got %v", evs)
	}
}
