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

// comet is the forward-flowing shimmer over a Busy fill: head → fading tail.
var comet = []string{"▓", "▒", "░"} // index 0 = head (brightest)

// pulseGlyphs breathe a single dot for an idle session (~500ms at 100ms/tick).
var pulseGlyphs = []string{"·", "•", "●", "•", "·"}

// State is the visual state a session is in.
type State int

const (
	StateBusy    State = iota // alive, working, determinate
	StateSweep                // alive, working, indeterminate
	StateIdle                 // alive, stopped, waiting for the user
	StateDone                 // alive, determinate task finished
	StateBlocked              // bg job waiting on a decision
	StateExit                 // process gone (model-set), lingering briefly
)

// StateOf derives the visual state from a session.
func StateOf(s collector.Session) State {
	switch {
	case s.Exited:
		return StateExit
	case s.Blocked:
		return StateBlocked
	case s.Status == "busy":
		if s.Mode == collector.Determinate {
			return StateBusy
		}
		return StateSweep
	default: // idle / stopped
		if s.Mode == collector.Determinate && s.Total > 0 && s.Done >= s.Total {
			return StateDone
		}
		return StateIdle
	}
}

// RenderBar renders the inner progress bar (no brackets) of the given width,
// animated per the session's state.
func RenderBar(s collector.Session, width, phase int) string {
	if width < 1 {
		width = 1
	}
	switch StateOf(s) {
	case StateDone:
		return strings.Repeat(full, width)
	case StateExit:
		return strings.Repeat(empty, width)
	case StateIdle:
		return idleBar(phase)
	case StateSweep:
		return sweepBar(width, phase)
	case StateBlocked:
		return fillBar(s, width, -1) // static progress, no shimmer
	default: // StateBusy
		return fillBar(s, width, phase)
	}
}

// fillBar fills to the progress fraction. With phase >= 0 a shimmer cell slides
// back and forth across the filled region (Busy); with phase < 0 it renders a
// static wavefront at the fill boundary (Blocked).
func fillBar(s collector.Session, width, phase int) string {
	filled := int(math.Round(s.Fraction() * float64(width)))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	runes := make([]string, width)
	for i := range runes {
		if i < filled {
			runes[i] = full
		} else {
			runes[i] = empty
		}
	}
	if phase >= 0 {
		if filled > 0 {
			head := phase % filled
			// draw tail first, head last, so head wins when filled < len(comet)
			for i := len(comet) - 1; i >= 0; i-- {
				pos := ((head-i)%filled + filled) % filled
				runes[pos] = comet[i]
			}
		}
	} else if filled < width {
		runes[filled] = wave // static wavefront (blocked)
	}
	return strings.Join(runes, "")
}

// idleBar is a slow pulsing dot flanked by 3 faint dots each side: "⋮⋮⋮·⋮⋮⋮".
// It is a fixed 7 cells (not the full bar width); progressCell pads the column.
func idleBar(phase int) string {
	flank := strings.Repeat(empty, 3)
	return flank + pulseGlyphs[phase%len(pulseGlyphs)] + flank
}

// sweepBar bounces the gradient block along an empty track (indeterminate work).
func sweepBar(width, phase int) string {
	grad := sweepGrad
	block := len(grad)
	if block > width {
		block = width
		grad = sweepGrad[:block]
	}
	runes := make([]string, width)
	for i := range runes {
		runes[i] = empty
	}
	pos := bounce(phase, width-block)
	for i := 0; i < block; i++ {
		runes[pos+i] = string(grad[i])
	}
	return strings.Join(runes, "")
}

// bounce maps phase to a triangle wave in [0, span].
func bounce(phase, span int) int {
	if span <= 0 {
		return 0
	}
	cycle := 2 * span
	p := phase % cycle
	if p <= span {
		return p
	}
	return cycle - p
}
