package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"agentmon/internal/collector"
)

const (
	barW         = 16 // progress bar width in cells
	sessionColW  = 30 // "PROJECT / SESSION" column
	tasksColW    = 9  // "TASKS" column
	nameMax      = 24 // session name truncation
	childNameMax = 20 // subagent name truncation
)

// Column-styling. In a non-TTY (tests, pipes) lipgloss strips colors, leaving
// plain text — so structural assertions still see the labels. The done-fade
// dim is applied as raw ANSI (see dimIf) so it survives regardless.
var (
	projectHeadStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))    // blue
	treeGrayStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))              // soft gray
	needsStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Italic(true) // amber
	statusBusy       = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))              // yellow
	statusDone       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))               // green
	statusBlocked    = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)   // magenta
	statusRun        = lipgloss.NewStyle().Foreground(lipgloss.Color("44"))               // cyan
)

// BodyLines renders the session table as individual lines (no column header,
// no border — the frame adds those). IDs in dim are drawn faint. It groups by
// project and nests subagents as a tree.
func BodyLines(sessions []collector.Session, phase int, dim map[string]bool) []string {
	var lines []string
	curProject := ""
	for _, s := range sessions {
		if s.Project != curProject {
			lines = append(lines, projectHeadStyle.Render("▸ "+s.Project))
			curProject = s.Project
		}
		faint := dim[s.ID]
		lines = append(lines, dimIf(sessionRow(s, phase, !faint), faint))
		if s.Blocked && s.NeedsHint != "" {
			lines = append(lines, needsStyle.Render("     needs: "+truncate(s.NeedsHint, 56)))
		}
		for i, c := range s.Children {
			branch := "├─"
			if i == len(s.Children)-1 {
				branch = "└─"
			}
			lines = append(lines, dimIf(childRow(branch, c, phase), dim[c.ID]))
		}
	}
	return lines
}

// sessionRow builds one top-level row: name | bar | tasks | status.
func sessionRow(s collector.Session, phase int, colored bool) string {
	name := padRight("  "+truncate(s.Name, nameMax), sessionColW)
	bar := RenderBar(s, barW, phase)
	tasks := padRight(tasksText(s), tasksColW)
	status := statusText(s)
	if colored {
		status = statusStyle(s).Render(status)
	}
	return name + bar + "  " + tasks + " " + status
}

// childRow builds a subagent row, drawn entirely in soft gray.
func childRow(branch string, c collector.Session, phase int) string {
	name := padRight("     "+branch+" "+truncate(c.Name, childNameMax), sessionColW)
	bar := RenderBar(c, barW, phase)
	tasks := padRight(tasksText(c), tasksColW)
	line := name + bar + "  " + tasks + " " + statusText(c)
	return treeGrayStyle.Render(line)
}

// tasksText is the TASKS column: a fraction when known, else an em dash.
func tasksText(s collector.Session) string {
	if s.Mode == collector.Determinate && s.Total > 0 {
		if s.IsDone() {
			return "done"
		}
		return fmt.Sprintf("%d/%d", s.Done, s.Total)
	}
	if s.IsDone() {
		return "done"
	}
	return "—"
}

// statusText is the STATUS column word.
func statusText(s collector.Session) string {
	switch {
	case s.Blocked:
		return "⏸ blocked"
	case s.IsDone():
		return "done"
	case s.Status == "busy":
		return "busy"
	default:
		return "running"
	}
}

func statusStyle(s collector.Session) lipgloss.Style {
	switch {
	case s.Blocked:
		return statusBlocked
	case s.IsDone():
		return statusDone
	case s.Status == "busy":
		return statusBusy
	default:
		return statusRun
	}
}

// dimIf wraps a line in the ANSI faint attribute (raw, so it survives in tests
// and pipes) when d is true. Terminals offer one faint level, so this is a
// one-step dim, not a gradual fade.
func dimIf(s string, d bool) string {
	if d {
		return "\x1b[2m" + s + "\x1b[0m"
	}
	return s
}

// padRight pads s with spaces to n visible runes (or truncates), ANSI-free
// inputs only — callers pass plain text for padded columns.
func padRight(s string, n int) string {
	w := len([]rune(s))
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
