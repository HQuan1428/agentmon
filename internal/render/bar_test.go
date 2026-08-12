// internal/render/bar_test.go
package render

import (
	"strings"
	"testing"

	"agentmon/internal/collector"
)

func runeLen(s string) int  { return len([]rune(s)) }
func nonEmpty(s string) int { return runeLen(s) - strings.Count(s, empty) }

func TestRenderBarBusyShimmer(t *testing.T) {
	s := collector.Session{Mode: collector.Determinate, Done: 6, Total: 10, Status: "busy"}
	bar := RenderBar(s, 10, 0)
	if runeLen(bar) != 10 {
		t.Fatalf("width=%d want 10 (%q)", runeLen(bar), bar)
	}
	if nonEmpty(bar) != 6 {
		t.Errorf("filled=%d want 6 (%q)", nonEmpty(bar), bar)
	}
	if !strings.Contains(bar, "▓") {
		t.Errorf("expected shimmer ▓ in %q", bar)
	}
	if RenderBar(s, 10, 0) == RenderBar(s, 10, 3) {
		t.Error("shimmer should move across phases")
	}
}

func TestRenderBarDone(t *testing.T) {
	s := collector.Session{Mode: collector.Determinate, Done: 5, Total: 5, Status: "idle"}
	if bar := RenderBar(s, 8, 3); bar != strings.Repeat("█", 8) {
		t.Errorf("done bar=%q want solid", bar)
	}
}

func TestRenderBarIdlePulse(t *testing.T) {
	s := collector.Session{Mode: collector.Indeterminate, Status: "idle"}
	seen := map[string]bool{}
	for ph := 0; ph < len(pulseGlyphs); ph++ {
		bar := RenderBar(s, 11, ph)
		if runeLen(bar) != 11 {
			t.Fatalf("width=%d want 11 (%q)", runeLen(bar), bar)
		}
		if nonEmpty(bar) != 1 {
			t.Errorf("idle should have exactly one pulse cell (%q)", bar)
		}
		seen[bar] = true
	}
	if len(seen) < 2 {
		t.Error("pulse glyph should change across phases")
	}
}

func TestRenderBarExit(t *testing.T) {
	s := collector.Session{Exited: true, Mode: collector.Determinate, Done: 5, Total: 10, Status: "idle"}
	if bar := RenderBar(s, 8, 0); bar != strings.Repeat(empty, 8) {
		t.Errorf("exit bar=%q want empty track", bar)
	}
}

func TestRenderBarSweepWidthStable(t *testing.T) {
	s := collector.Session{Mode: collector.Indeterminate, Status: "busy"}
	block := runeLen(string(sweepGrad))
	for phase := 0; phase < 20; phase++ {
		bar := RenderBar(s, 15, phase)
		if runeLen(bar) != 15 {
			t.Fatalf("phase %d width=%d want 15 (%q)", phase, runeLen(bar), bar)
		}
		if got := strings.Count(bar, empty); got != 15-block {
			t.Errorf("phase %d empty cells=%d want %d (%q)", phase, got, 15-block, bar)
		}
	}
}

func TestStateOf(t *testing.T) {
	cases := []struct {
		s    collector.Session
		want State
	}{
		{collector.Session{Status: "busy", Mode: collector.Determinate}, StateBusy},
		{collector.Session{Status: "busy", Mode: collector.Indeterminate}, StateSweep},
		{collector.Session{Status: "idle", Mode: collector.Indeterminate}, StateIdle},
		{collector.Session{Status: "idle", Mode: collector.Determinate, Done: 5, Total: 5}, StateDone},
		{collector.Session{Status: "idle", Mode: collector.Determinate, Done: 2, Total: 5}, StateIdle},
		{collector.Session{Blocked: true}, StateBlocked},
		{collector.Session{Exited: true, Status: "idle"}, StateExit},
	}
	for i, c := range cases {
		if got := StateOf(c.s); got != c.want {
			t.Errorf("case %d: StateOf=%d want %d", i, got, c.want)
		}
	}
}
