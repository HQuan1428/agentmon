package render

import (
	"fmt"
	"math"
	"strings"

	"agentmon/internal/collector"
)

const (
	empty = "⋮"
	full  = "█"
	wave  = "▓"
)

func RenderBar(s collector.Session, width, phase int) string {
	if width < 1 {
		width = 1
	}
	if s.IsDone() {
		return strings.Repeat(full, width)
	}
	if s.Mode == collector.Determinate {
		filled := int(math.Round(s.Fraction() * float64(width)))
		if filled > width {
			filled = width
		}
		b := strings.Repeat(full, filled)
		rest := width - filled
		if rest > 0 {
			b += wave
			rest--
		}
		return b + strings.Repeat(empty, rest)
	}
	// Indeterminate running: bounce a 3-wide block.
	block := 3
	if block > width {
		block = width
	}
	span := width - block
	pos := 0
	if span > 0 {
		cycle := 2 * span
		p := phase % cycle
		if p <= span {
			pos = p
		} else {
			pos = cycle - p
		}
	}
	runes := make([]string, width)
	for i := range runes {
		runes[i] = empty
	}
	for i := 0; i < block; i++ {
		runes[pos+i] = wave
	}
	return strings.Join(runes, "")
}

func Label(s collector.Session) string {
	if s.Blocked {
		return "⏸ blocked"
	}
	if s.IsDone() {
		return "done"
	}
	if s.Mode == collector.Determinate {
		return fmt.Sprintf("%d/%d", s.Done, s.Total)
	}
	return "sweep"
}
