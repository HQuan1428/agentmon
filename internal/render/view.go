package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"agentmon/internal/collector"
)

type Layout struct {
	Compact  bool
	SessionW int
	ModelW   int
	BarW     int
	TasksW   int
}

const compactStatusW = 10 // room reserved for the STATUS word (⏸ BLOCKED)

const (
	wideModelW  = 20 // MODEL column (fixed)
	wideTasksW  = 8  // TASKS column (fixed)
	maxSessionW = 50 // name column ceiling: past this, extra width feeds the bar
)

func LayoutForWidth(total int) Layout {
	cw := contentWidth(total)
	if cw < 92 {
		sessionW := cw - (12 + 4) - 1 - compactStatusW // bar[16] + gap + status
		if sessionW < 8 {
			sessionW = 8
		}
		return Layout{Compact: true, SessionW: sessionW, BarW: 12, TasksW: 7}
	}
	// Wide: MODEL/TASKS/STATUS fixed; name and bar split the rest evenly so the
	// row fills the full content width instead of clustering on the left.
	// row = name + model + (bar+4) + tasks + " " + status
	rest := cw - wideModelW - wideTasksW - compactStatusW - 1 - 4
	sessionW := rest / 2
	if sessionW > maxSessionW {
		sessionW = maxSessionW
	}
	return Layout{SessionW: sessionW, ModelW: wideModelW, BarW: rest - sessionW, TasksW: wideTasksW}
}

// Row/column styling. In a non-TTY (tests, pipes) lipgloss strips colors,
// leaving plain text — so structural assertions still see the labels. The
// done-fade dim is applied as raw ANSI (see dimIf) so it survives regardless.
var (
	projectHeadStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111")) // ▾ project group
	markerStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))  // ▸ session marker
	treeGrayStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))            // subagent rows
	modelStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))            // model on the compact 2nd line (dim gray)
	modelWideStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("231"))            // MODEL column when wide (white, roomy)
	needsStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Italic(true)
	statusBusy       = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true) // ⚡ BUSY
	statusSweep      = lipgloss.NewStyle().Foreground(lipgloss.Color("44")).Bold(true)  // 🔄 SWEEP
	statusDone       = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)  // ✓ DONE
	statusBlocked    = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true) // ⏸ BLOCKED
	statusIdle       = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true) // ● IDLE
	statusExit       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))            // ✕ EXIT
)

// agentHeadStyles color the ▾ agent header per kind, so agents read apart from
// the project header (111) and from each other.
var agentHeadStyles = map[collector.Agent]lipgloss.Style{
	collector.AgentClaude:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("215")), // warm orange
	collector.AgentCodex:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("114")), // green
	collector.AgentOpenCode: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("80")),  // cyan
	collector.AgentAider:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213")), // pink
}
var agentHeadDefault = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245"))

func agentHeadStyle(a collector.Agent) lipgloss.Style {
	if st, ok := agentHeadStyles[a]; ok {
		return st
	}
	return agentHeadDefault
}

// BodyLines renders the session table as individual lines (no column header,
// no border — the frame adds those). Sessions are grouped under a ▾ project
// header (they arrive sorted by project); each session is a ▸ row with its
// subagents nested beneath as ├─/└─ ⌁ branches. IDs in dim are drawn faint.
func BodyLines(sessions []collector.Session, phase int, dim map[string]bool, totalWidth int) []string {
	var lines []string
	layout := LayoutForWidth(totalWidth)
	cw := contentWidth(totalWidth)
	curProject := ""
	curAgent := collector.Agent("")
	for _, s := range sessions {
		newProject := s.Project != curProject
		if newProject {
			if len(lines) > 0 {
				lines = append(lines, "") // gap before each project group
			}
			lines = append(lines, projectHeadStyle.Render(truncate("▾ "+s.Project, cw)))
			curProject = s.Project
			curAgent = ""
		}
		if s.Agent != curAgent {
			if !newProject && len(lines) > 0 {
				lines = append(lines, "") // gap before an agent group within a project
			}
			lines = append(lines, agentHeadStyle(s.Agent).Render(truncate("  ▾ "+string(s.Agent), cw)))
			curAgent = s.Agent
		}
		faint := dim[s.ID]
		lines = append(lines, dimIf(sessionRow(s, phase, !faint, layout), faint))
		if layout.Compact {
			lines = append(lines, dimIf(compactModelLine(s.Model, cw), faint))
		}
		if s.Blocked && s.NeedsHint != "" {
			needs := "        needs: " + truncate(s.NeedsHint, 56)
			lines = append(lines, needsStyle.Render(truncate(needs, cw)))
		}
		for i, c := range s.Children {
			branch := "├─"
			if i == len(s.Children)-1 {
				branch = "└─"
			}
			lines = append(lines, dimIf(childRow(branch, c, phase, layout), dim[c.ID]))
		}
	}
	return lines
}

const (
	sessionIndent = "    "   // sessions sit under project and agent
	childIndent   = "      " // subagents sit under their session
)

// nameCell builds the left "PROJECT / SESSION" cell: a fixed-width column of
// prefix + name, truncating the name to whatever room the prefix leaves so the
// PROGRESS column always starts at the same offset.
func nameCell(prefix, name string, width int) string {
	avail := width - lipgloss.Width(prefix) - 1 // -1 keeps a gap before the next column
	if avail < 1 {
		avail = 1
	}
	return padRight(prefix+truncate(name, avail), width)
}

// sessionRow builds one session row, indented under its project:
// ▸ name | [bar] | tasks | status.
func sessionRow(s collector.Session, phase int, colored bool, layout Layout) string {
	label := s.Name
	if s.Kind == "bg" {
		label += " (bg)"
	}
	name := nameCell(sessionIndent+"▸ ", label, layout.SessionW)
	if colored {
		name = strings.Replace(name, "▸", markerStyle.Render("▸"), 1)
	}
	status := statusText(s)
	if colored {
		status = statusStyle(s).Render(status)
	}
	if layout.Compact {
		return name + progressCell(s, phase, layout) + " " + status
	}
	return name + modelCell(s.Model, layout) + progressCell(s, phase, layout) + padRight(tasksText(s), layout.TasksW) + " " + status
}

const modelIndent = "        " // 8 spaces: deeper than the name (▸ at col 4, name at col 6)

// compactModelLine is the dim second line under a compact session row: just the
// model, indented past the name so it reads as that session's detail.
func compactModelLine(model string, width int) string {
	return modelStyle.Render(truncate(modelIndent+collector.ModelOrUnknown(model), width))
}

// childRow builds a subagent row, indented under its session, in soft gray.
func childRow(branch string, c collector.Session, phase int, layout Layout) string {
	name := nameCell(childIndent+branch+" ⌁ ", c.Name, layout.SessionW)
	if layout.Compact {
		return treeGrayStyle.Render(name)
	}
	line := name + blankModelCell(layout) + progressCell(c, phase, layout) + padRight(tasksText(c), layout.TasksW) + " " + statusText(c)
	return treeGrayStyle.Render(line)
}

func modelCell(model string, layout Layout) string {
	if layout.Compact {
		return ""
	}
	return modelWideStyle.Render(padRight(truncate(collector.ModelOrUnknown(model), layout.ModelW), layout.ModelW))
}

func blankModelCell(layout Layout) string {
	if layout.Compact {
		return ""
	}
	return strings.Repeat(" ", layout.ModelW)
}

func progressCell(s collector.Session, phase int, layout Layout) string {
	bar := "[" + RenderBar(s, layout.BarW, phase) + "]"
	return padRight(bar, layout.BarW+4)
}

// tasksText is the TASKS column: a fraction when known, DONE when finished,
// or a dash for indeterminate work with no task count.
func tasksText(s collector.Session) string {
	if StateOf(s) == StateDone {
		return "DONE"
	}
	if s.Mode == collector.Determinate && s.Total > 0 {
		return fmt.Sprintf("%d/%d", s.Done, s.Total)
	}
	return "--"
}

// statusText is the STATUS column: an icon plus an uppercase word.
func statusText(s collector.Session) string {
	switch StateOf(s) {
	case StateBlocked:
		return "⏸ BLOCKED"
	case StateExit:
		return "✕ EXIT"
	case StateDone:
		return "✓ DONE"
	case StateIdle:
		return "● IDLE"
	case StateBusy:
		return "⚡ BUSY"
	default:
		return "🔄 SWEEP"
	}
}

func statusStyle(s collector.Session) lipgloss.Style {
	switch StateOf(s) {
	case StateBlocked:
		return statusBlocked
	case StateExit:
		return statusExit
	case StateDone:
		return statusDone
	case StateIdle:
		return statusIdle
	case StateBusy:
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

// padRight pads s with spaces to n terminal cells (or truncates), ANSI-free
// inputs only — callers pass plain text for padded columns.
func padRight(s string, n int) string {
	s = truncate(s, n)
	w := lipgloss.Width(s)
	return s + strings.Repeat(" ", n-w)
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return ansi.Truncate(s, n, "…")
}
