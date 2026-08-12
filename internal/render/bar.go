package render

import (
	"math"
	"strings"

	"agentmon/internal/collector"
)

const (
	empty = "⋮"
	full  = "█"
	wave  = "▓"
)

// sweepGrad is the gradient block that slides along an indeterminate bar.
var sweepGrad = []rune("░▒▓█▓▒░")

// RenderBar renders the inner progress bar (no brackets) of the given width.
// Determinate bars fill left-to-right with a wavefront; indeterminate ones
// bounce a soft gradient block; done bars are solid.
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
	// Indeterminate running: bounce the gradient block along an empty track.
	grad := sweepGrad
	block := len(grad)
	if block > width {
		block = width
		grad = sweepGrad[:block]
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
		runes[pos+i] = string(grad[i])
	}
	return strings.Join(runes, "")
}
