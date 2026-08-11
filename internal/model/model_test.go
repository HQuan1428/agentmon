package model

import (
	"testing"
	"time"

	"agentmon/internal/collector"
)

func TestApplyPollFiresEvents(t *testing.T) {
	m := New("/fake", nil, time.Second)
	m.sessions = []collector.Session{{ID: "a", Mode: collector.Determinate, Done: 6, Total: 10}}
	m2, evs := m.applyPoll([]collector.Session{{ID: "a", Mode: collector.Determinate, Done: 10, Total: 10}})
	if len(evs) != 1 || evs[0].Kind != DoneEvent {
		t.Fatalf("want one DoneEvent, got %v", evs)
	}
	if len(m2.sessions) != 1 || !m2.sessions[0].IsDone() {
		t.Errorf("snapshot not updated: %+v", m2.sessions)
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
