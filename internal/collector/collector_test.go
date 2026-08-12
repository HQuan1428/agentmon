package collector

import "testing"

func TestNewDefaultIncludesFourAdapters(t *testing.T) {
	c := NewDefault("/home/test", fakeSource{})
	want := []Agent{AgentClaude, AgentCodex, AgentOpenCode, AgentAider}
	if len(c.adapters) != len(want) {
		t.Fatalf("adapters=%d want %d", len(c.adapters), len(want))
	}
	for i, adapter := range c.adapters {
		if got := adapter.Agent(); got != want[i] {
			t.Fatalf("adapter[%d]=%q want %q", i, got, want[i])
		}
	}
}
