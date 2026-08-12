package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"agentmon/internal/collector"
)

const (
	barW        = 15 // progress bar width in cells (inside the [ ] brackets)
	sessionColW = 30 // "PROJECT / SESSION" column
	tasksColW   = 8  // "TASKS" column
)

// Row/column styling. In a non-TTY (tests, pipes) lipgloss strips colors,
// leaving plain text — so structural assertions still see the labels. The
// done-fade dim is applied as raw ANSI (see dimIf) so it survives regardless.
var (
	projectHeadStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111")) // ▾ project group
	markerStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))  // ▸ session marker
	treeGrayStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))            // subagent rows
	needsStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Italic(true)
	statusBusy       = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true) // ⚡ BUSY
	statusSweep      = lipgloss.NewStyle().Foreground(lipgloss.Color("44")).Bold(true)  // 🔄 SWEEP
	statusDone       = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)  // ✓ DONE
	statusBlocked    = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true) // ⏸ BLOCKED
)

// BodyLines renders the session table as individual lines (no column header,
// no border — the frame adds those). Sessions are grouped under a ▾ project
// header (they arrive sorted by project); each session is a ▸ row with its
// subagents nested beneath as ├─/└─ ⌁ branches. IDs in dim are drawn faint.
func BodyLines(sessions []collector.Session, phase int, dim map[string]bool) []string {
	var lines []string
	curProject := ""
	for _, s := range sessions {
		if s.Project != curProject {
			lines = append(lines, projectHeadStyle.Render("▾ "+s.Project))
			curProject = s.Project
		}
		faint := dim[s.ID]
		lines = append(lines, dimIf(sessionRow(s, phase, !faint), faint))
		if s.Blocked && s.NeedsHint != "" {
			lines = append(lines, needsStyle.Render("    needs: "+truncate(s.NeedsHint, 56)))
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

const (
	sessionIndent = "  "     // sessions sit one level under their ▾ project
	childIndent   = "      " // subagents sit one level under their ▸ session
)

// nameCell builds the left "PROJECT / SESSION" cell: a fixed-width column of
// prefix + name, truncating the name to whatever room the prefix leaves so the
// PROGRESS column always starts at the same offset.
func nameCell(prefix, name string) string {
	avail := sessionColW - len([]rune(prefix))
	if avail < 1 {
		avail = 1
	}
	return padRight(prefix+truncate(name, avail), sessionColW)
}

// sessionRow builds one session row, indented under its project:
// ▸ name | [bar] | tasks | status.
func sessionRow(s collector.Session, phase int, colored bool) string {
	label := s.Name
	if s.Kind == "bg" {
		label += " (bg)"
	}
	name := nameCell(sessionIndent+"▸ ", label)
	if colored {
		name = strings.Replace(name, "▸", markerStyle.Render("▸"), 1)
	}
	status := statusText(s)
	if colored {
		status = statusStyle(s).Render(status)
	}
	return name + bracketBar(s, phase) + "  " + padRight(tasksText(s), tasksColW) + " " + status
}

// childRow builds a subagent row, indented under its session, in soft gray.
func childRow(branch string, c collector.Session, phase int) string {
	name := nameCell(childIndent+branch+" ⌁ ", c.Name)
	line := name + bracketBar(c, phase) + "  " + padRight(tasksText(c), tasksColW) + " " + statusText(c)
	return treeGrayStyle.Render(line)
}

func bracketBar(s collector.Session, phase int) string {
	return "[" + RenderBar(s, barW, phase) + "]"
}

// tasksText is the TASKS column: a fraction when known, DONE when finished,
// or a dash for indeterminate work with no task count.
func tasksText(s collector.Session) string {
	if s.Mode == collector.Determinate && s.Total > 0 {
		if s.IsDone() {
			return "DONE"
		}
		return fmt.Sprintf("%d/%d", s.Done, s.Total)
	}
	if s.IsDone() {
		return "DONE"
	}
	return "--"
}

// statusText is the STATUS column: an icon plus an uppercase word.
func statusText(s collector.Session) string {
	switch {
	case s.Blocked:
		return "⏸ BLOCKED"
	case s.IsDone():
		return "✓ DONE"
	case s.Mode == collector.Determinate:
		return "⚡ BUSY"
	default:
		return "🔄 SWEEP"
	}
}

func statusStyle(s collector.Session) lipgloss.Style {
	switch {
	case s.Blocked:
		return statusBlocked
	case s.IsDone():
		return statusDone
	case s.Mode == collector.Determinate:
		return statusBusy
	default:
		return statusSweep
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
