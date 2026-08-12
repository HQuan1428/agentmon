package collector

import (
	"errors"
	"testing"

	"agentmon/internal/procscan"
)

type fakeSource struct {
	snap procscan.Snapshot
	err  error
}

func (f fakeSource) Snapshot() (procscan.Snapshot, error) {
	return f.snap, f.err
}

type fakeAdapter struct {
	agent           Agent
	rows            []Session
	err             error
	panicValue      any
	agentPanicValue any
}

func (f fakeAdapter) Agent() Agent {
	if f.agentPanicValue != nil {
		panic(f.agentPanicValue)
	}
	return f.agent
}

func (f fakeAdapter) Discover(procscan.Snapshot) ([]Session, error) {
	if f.panicValue != nil {
		panic(f.panicValue)
	}
	return f.rows, f.err
}

func TestCoordinatorKeepsHealthyAdapterResults(t *testing.T) {
	c := NewCoordinator(fakeSource{},
		fakeAdapter{agent: AgentClaude, err: errors.New("bad schema")},
		fakeAdapter{agent: AgentCodex, rows: []Session{{ID: "codex:1", Agent: AgentCodex, Project: "p"}}},
		fakeAdapter{agent: AgentAider, panicValue: "boom"},
	)

	got := c.Collect()
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "codex:1" {
		t.Fatalf("sessions=%+v", got.Sessions)
	}
	if len(got.Errors) != 2 {
		t.Fatalf("errors=%+v", got.Errors)
	}
	if got.Errors[0].Agent != AgentClaude || got.Errors[1].Agent != AgentAider {
		t.Fatalf("error agents=%+v", got.Errors)
	}
}

func TestCoordinatorIsolatesNilAdapter(t *testing.T) {
	c := NewCoordinator(fakeSource{}, nil,
		fakeAdapter{agent: AgentCodex, rows: []Session{{ID: "codex:1", Agent: AgentCodex}}},
	)

	got := c.Collect()
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "codex:1" {
		t.Fatalf("sessions=%+v", got.Sessions)
	}
	if len(got.Errors) != 1 || got.Errors[0].Agent != "" {
		t.Fatalf("errors=%+v", got.Errors)
	}
}

func TestCoordinatorIsolatesAgentPanic(t *testing.T) {
	c := NewCoordinator(fakeSource{},
		fakeAdapter{agentPanicValue: "agent boom"},
		fakeAdapter{agent: AgentCodex, rows: []Session{{ID: "codex:1", Agent: AgentCodex}}},
	)

	got := c.Collect()
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "codex:1" {
		t.Fatalf("sessions=%+v", got.Sessions)
	}
	if len(got.Errors) != 1 || got.Errors[0].Agent != "" {
		t.Fatalf("errors=%+v", got.Errors)
	}
}

func TestCoordinatorRejectsDuplicateGlobalIDsAndKeepsFirst(t *testing.T) {
	c := NewCoordinator(fakeSource{},
		fakeAdapter{agent: AgentCodex, rows: []Session{{ID: "codex:1", Agent: AgentCodex, Name: "first"}}},
		fakeAdapter{agent: AgentCodex, rows: []Session{{ID: "codex:1", Agent: AgentCodex, Name: "second"}}},
	)

	got := c.Collect()
	if len(got.Sessions) != 1 || got.Sessions[0].Name != "first" {
		t.Fatalf("sessions=%+v", got.Sessions)
	}
	if len(got.Errors) != 1 || got.Errors[0].Agent != AgentCodex {
		t.Fatalf("errors=%+v", got.Errors)
	}
}

func TestCoordinatorRejectsSessionsOutsideAdapterNamespace(t *testing.T) {
	c := NewCoordinator(fakeSource{}, fakeAdapter{
		agent: AgentCodex,
		rows:  []Session{{ID: "claude:1", Agent: AgentClaude, Name: "wrong agent"}},
	})

	got := c.Collect()
	if len(got.Sessions) != 0 {
		t.Fatalf("sessions=%+v", got.Sessions)
	}
	if len(got.Errors) != 1 || got.Errors[0].Agent != AgentCodex {
		t.Fatalf("errors=%+v", got.Errors)
	}
}

func TestCoordinatorRejectsChildOutsideAdapterNamespace(t *testing.T) {
	c := NewCoordinator(fakeSource{}, fakeAdapter{
		agent: AgentCodex,
		rows: []Session{{
			ID: "codex:parent", Agent: AgentCodex, Name: "parent",
			Children: []Session{
				{ID: "codex:child", Agent: AgentCodex, Name: "valid child"},
				{ID: "claude:child", Agent: AgentClaude, Name: "wrong child"},
			},
		}},
	})

	got := c.Collect()
	if len(got.Sessions) != 1 || len(got.Sessions[0].Children) != 1 || got.Sessions[0].Children[0].ID != "codex:child" {
		t.Fatalf("sessions=%+v", got.Sessions)
	}
	if len(got.Errors) != 1 || got.Errors[0].Agent != AgentCodex {
		t.Fatalf("errors=%+v", got.Errors)
	}
}

func TestCoordinatorRejectsDuplicateChildGlobalIDsAndKeepsFirst(t *testing.T) {
	c := NewCoordinator(fakeSource{}, fakeAdapter{
		agent: AgentCodex,
		rows: []Session{{
			ID: "codex:parent", Agent: AgentCodex, Name: "parent",
			Children: []Session{
				{ID: "codex:child", Agent: AgentCodex, Name: "first"},
				{ID: "codex:child", Agent: AgentCodex, Name: "second"},
			},
		}},
	})

	got := c.Collect()
	if len(got.Sessions) != 1 || len(got.Sessions[0].Children) != 1 || got.Sessions[0].Children[0].Name != "first" {
		t.Fatalf("sessions=%+v", got.Sessions)
	}
	if len(got.Errors) != 1 || got.Errors[0].Agent != AgentCodex {
		t.Fatalf("errors=%+v", got.Errors)
	}
}

func TestCoordinatorSortsByProjectAgentActivityAndName(t *testing.T) {
	c := NewCoordinator(fakeSource{},
		fakeAdapter{agent: AgentAider, rows: []Session{{
			ID: "aider:1", Agent: AgentAider, Project: "beta", Name: "first",
		}}},
		fakeAdapter{agent: AgentCodex, rows: []Session{{
			ID: "codex:1", Agent: AgentCodex, Project: "alpha", Name: "alpha",
		}}},
		fakeAdapter{agent: AgentClaude, rows: []Session{
			{ID: "claude:1", Agent: AgentClaude, Project: "alpha", Name: "zeta", Status: "idle", Mode: Indeterminate},
			{ID: "claude:2", Agent: AgentClaude, Project: "alpha", Name: "omega", Status: "busy", Mode: Indeterminate},
		}},
	)

	got := c.Collect()
	want := []string{"claude:2", "claude:1", "codex:1", "aider:1"}
	if len(got.Sessions) != len(want) {
		t.Fatalf("len(sessions)=%d want %d; sessions=%+v", len(got.Sessions), len(want), got.Sessions)
	}
	for i, id := range want {
		if got.Sessions[i].ID != id {
			t.Fatalf("sessions[%d]=%q want %q; sessions=%+v", i, got.Sessions[i].ID, id, got.Sessions)
		}
	}
}

func TestCoordinatorReportsSourceFailureAsSystemError(t *testing.T) {
	c := NewCoordinator(fakeSource{err: errors.New("proc unavailable")}, fakeAdapter{agent: AgentCodex})

	got := c.Collect()
	if len(got.Sessions) != 0 {
		t.Fatalf("sessions=%+v", got.Sessions)
	}
	if len(got.Errors) != 1 || got.Errors[0].Agent != "" {
		t.Fatalf("errors=%+v", got.Errors)
	}
}
