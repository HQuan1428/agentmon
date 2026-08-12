// internal/render/bar_test.go
package render

import (
	"strings"
	"testing"

	"agentmon/internal/collector"
)

func runeLen(s string) int { return len([]rune(s)) }

func TestRenderBarDeterminate(t *testing.T) {
	s := collector.Session{Mode: collector.Determinate, Done: 6, Total: 10, Status: "busy"}
	bar := RenderBar(s, 10, 0)
	if runeLen(bar) != 10 {
		t.Fatalf("width=%d want 10 (%q)", runeLen(bar), bar)
	}
	if strings.Count(bar, "█") != 6 {
		t.Errorf("filled=%d want 6 (%q)", strings.Count(bar, "█"), bar)
	}
	if !strings.Contains(bar, "▓") {
		t.Errorf("expected wavefront in %q", bar)
	}
}

func TestRenderBarDone(t *testing.T) {
	s := collector.Session{Mode: collector.Indeterminate, Status: "idle"}
	bar := RenderBar(s, 8, 3)
	if bar != strings.Repeat("█", 8) {
		t.Errorf("done bar=%q", bar)
	}
}

func TestRenderBarSweepWidthStable(t *testing.T) {
	s := collector.Session{Mode: collector.Indeterminate, Status: "busy"}
	block := runeLen(string(sweepGrad)) // gradient block cells
	for phase := 0; phase < 20; phase++ {
		bar := RenderBar(s, 15, phase)
		if runeLen(bar) != 15 {
			t.Fatalf("phase %d width=%d want 15 (%q)", phase, runeLen(bar), bar)
		}
		// The empty track shrinks by exactly the gradient block width.
		if got := strings.Count(bar, empty); got != 15-block {
			t.Errorf("phase %d empty cells=%d want %d (%q)", phase, got, 15-block, bar)
		}
	}
}
