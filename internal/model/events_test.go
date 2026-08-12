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

func TestDiffEventsNewDoneIDDoesNotComplete(t *testing.T) {
	prev := []collector.Session{{
		ID: "codex:pid:42", NativeID: "pid:42", Agent: collector.AgentCodex,
		Mode: collector.Indeterminate, Status: "busy",
	}}
	cur := []collector.Session{{
		ID: "codex:session-1", NativeID: "session-1", Agent: collector.AgentCodex,
		Mode: collector.Indeterminate, Status: "idle",
	}}

	if events := DiffEvents(prev, cur); len(events) != 0 {
		t.Fatalf("new already-done identity must not emit completion: %v", events)
	}
}

func TestDiffEventsSubagentSilent(t *testing.T) {
	// A subagent finishing must NOT ring the chime; only top-level sessions do.
	prev := []collector.Session{{ID: "p", Status: "busy", Mode: collector.Indeterminate,
		Children: []collector.Session{{ID: "c", Mode: collector.Indeterminate, Status: "busy"}}}}
	cur := []collector.Session{{ID: "p", Status: "busy", Mode: collector.Indeterminate,
		Children: []collector.Session{{ID: "c", Mode: collector.Indeterminate, Status: "idle"}}}}
	if evs := DiffEvents(prev, cur); len(evs) != 0 {
		t.Errorf("subagent finishing should be silent, got %v", evs)
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

func TestDiffEventsSubagentSilentAcrossAgents(t *testing.T) {
	// Even with per-agent namespacing, a finishing subagent stays silent.
	prev := []collector.Session{
		{ID: "parent", Agent: collector.AgentCodex, Mode: collector.Indeterminate, Status: "busy",
			Children: []collector.Session{{ID: "child", Mode: collector.Indeterminate, Status: "busy"}}},
	}
	cur := []collector.Session{
		{ID: "parent", Agent: collector.AgentCodex, Mode: collector.Indeterminate, Status: "busy",
			Children: []collector.Session{{ID: "child", Mode: collector.Indeterminate, Status: "idle"}}},
	}
	if evs := DiffEvents(prev, cur); len(evs) != 0 {
		t.Fatalf("subagent finishing should be silent, got %v", evs)
	}
}
