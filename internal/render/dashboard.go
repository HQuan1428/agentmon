package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"agentmon/internal/collector"
)

// Version is shown as a badge in the header. main may override it.
var Version = "0.1.0"

// eyeArt is a compact ASCII rendering of the monitoring "eye" icon
// (noun-monitoring-3554650.svg): almond lids around a round iris.
var eyeArt = []string{
	" ,---. ",
	"( (◉) )",
	" `---' ",
}

const (
	chromeLines  = 9 // border(2) + header(3) + separator(1) + column header(2) + footer(1)
	minWidth     = 32
	defWidth     = 80
	smallHeaderW = 52 // below this content width the header drops to icons + counts
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("51"))
	eyeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("44"))
	verBadge     = lipgloss.NewStyle().Foreground(lipgloss.Color("189")).Background(lipgloss.Color("60")).Padding(0, 1)
	colHeadStyle = lipgloss.NewStyle().Bold(true).Faint(true)
	ruleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sepStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("62")) // header/table divider, frame color
	heroDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	heroTip      = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	keyCap       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("62")).Padding(0, 1)
	keyLabel     = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	frameStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(0, 1)
	dotActive    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	dotBusy      = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	dotBlocked   = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	bellOn       = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	bellOff      = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

// Counts summarizes the visible sessions for the header dashboard.
type Counts struct {
	Active, Busy, Blocked int
}

// CountSessions counts top-level sessions: Active is how many are shown, Busy
// how many are actively working, Blocked how many bg jobs await a decision.
func CountSessions(sessions []collector.Session) Counts {
	var c Counts
	for _, s := range sessions {
		c.Active++
		switch {
		case s.Blocked:
			c.Blocked++
		case s.Status == "busy" && !s.IsDone():
			c.Busy++
		}
	}
	return c
}

// contentWidth is the usable width inside the border + padding.
func contentWidth(w int) int {
	if w <= 0 {
		w = defWidth
	}
	if w < minWidth {
		w = minWidth
	}
	return w - 4 // border (2) + padding (2)
}

// BodyBudget is how many body lines fit for a given total height (0 = show all).
func BodyBudget(height int) int {
	if height <= 0 {
		return 0
	}
	if b := height - chromeLines; b > 0 {
		return b
	}
	return 1
}

// header builds the 3-line dashboard header: the ASCII eye on the left with
// the title/version on its middle line, and the session counter + bell badge
// right-aligned on that same middle line. It degrades responsively — on a
// narrow frame it drops the title/version and shows just the status icons
// with their counts (fixed gaps) plus the bell icon, so it never wraps.
func header(cw int, c Counts, soundOn bool) string {
	// emojiSlack reserves columns for double-width emoji (⚡ and the 🔊/🔇
	// bell) that lipgloss.Width undercounts by one cell each — without it the
	// right block renders ~2 cells wider than measured and the bell wraps.
	const emojiSlack = 2

	var left, right string
	if cw < smallHeaderW {
		// Narrow: eye only on the left; icons + counts + bell icon on the right.
		left = eyeStyle.Render(eyeArt[1])
		right = counterCompact(c) + "  " + bellIcon(soundOn)
	} else {
		left = eyeStyle.Render(eyeArt[1]) + "  " + titleStyle.Render("agentmon") + " " + verBadge.Render("v"+Version)
		right = counterBadge(c) + "   " + bellBadge(soundOn)
		if lipgloss.Width(right) > cw-lipgloss.Width(left)-emojiSlack {
			right = counterCompact(c) + "  " + bellBadge(soundOn)
		}
	}
	avail := cw - lipgloss.Width(left) - emojiSlack
	if avail < 1 {
		avail = 1
	}
	// PlaceHorizontal right-aligns within `avail` using lipgloss's own width
	// measure, so the middle line lands at ≤ cw and the frame won't wrap it
	// (manual gap math desyncs on double-width emoji like ⚡/🔊).
	mid := left + lipgloss.PlaceHorizontal(avail, lipgloss.Right, right)
	return eyeStyle.Render(eyeArt[0]) + "\n" + mid + "\n" + eyeStyle.Render(eyeArt[2])
}

func counterBadge(c Counts) string {
	return fmt.Sprintf("%s %s  %s %s  %s %s",
		dotActive.Render("●"), fmt.Sprintf("%d active", c.Active),
		dotBusy.Render("⚡"), fmt.Sprintf("%d busy", c.Busy),
		dotBlocked.Render("⏸"), fmt.Sprintf("%d blocked", c.Blocked),
	)
}

// counterCompact shows just the icons and counts with a fixed gap — the
// friendly small-screen form.
func counterCompact(c Counts) string {
	return fmt.Sprintf("%s %d   %s %d   %s %d",
		dotActive.Render("●"), c.Active,
		dotBusy.Render("⚡"), c.Busy,
		dotBlocked.Render("⏸"), c.Blocked,
	)
}

func bellIcon(on bool) string {
	if on {
		return bellOn.Render("🔊")
	}
	return bellOff.Render("🔇")
}

func bellBadge(on bool) string {
	if on {
		return bellOn.Render("🔊 ON")
	}
	return bellOff.Render("🔇 OFF")
}

func columnHeader(cw int) string {
	labels := padRight("PROJECT / SESSION", sessionColW) +
		padRight("PROGRESS", barW+2) +
		padRight("TASKS", tasksColW) + "STATUS"
	rule := ruleStyle.Render(strings.Repeat("─", cw))
	return colHeadStyle.Render(labels) + "\n" + rule
}

func footer() string {
	seg := func(k, label string) string { return keyCap.Render(k) + " " + keyLabel.Render(label) }
	return strings.Join([]string{
		seg("q", "quit"),
		seg("s", "sound"),
		seg("c", "tree"),
		seg("↑/↓", "scroll"),
	}, "   ")
}

func emptyState(cw, bodyH int) string {
	if bodyH < 3 {
		bodyH = 3
	}
	card := lipgloss.JoinVertical(lipgloss.Center,
		"💤",
		heroDim.Render("Chưa có agent nào đang làm việc"),
		heroTip.Render("Tip: mở một phiên Claude Code, nó sẽ hiện ở đây ngay"),
	)
	return lipgloss.Place(cw, bodyH, lipgloss.Center, lipgloss.Center, card)
}

// Compose assembles the full bordered dashboard. body is the pre-rendered
// table lines (ignored when empty is true); scroll windows the body to fit.
func Compose(width, height int, c Counts, soundOn bool, body []string, scroll int, empty bool) string {
	cw := contentWidth(width)
	head := header(cw, c, soundOn)
	foot := footer()

	var mid string
	if empty {
		bodyH := BodyBudget(height)
		if bodyH <= 0 {
			bodyH = 3
		}
		mid = emptyState(cw, bodyH)
	} else {
		lines := body
		if b := BodyBudget(height); b > 0 {
			lines = windowLines(body, scroll, b)
		}
		mid = columnHeader(cw) + "\n" + strings.Join(lines, "\n")
	}

	// Separator dividing the header (name + status counts) from the table.
	sep := sepStyle.Render(strings.Repeat("─", cw))
	inner := lipgloss.JoinVertical(lipgloss.Left, head, sep, mid, foot)
	// Clip each line to the content width so a narrow frame degrades by
	// truncating rows on the right rather than wrapping them into a mess.
	inner = lipgloss.NewStyle().MaxWidth(cw).Render(inner)
	// lipgloss's Width includes horizontal padding, so the text area is
	// Width-2; set Width = cw+2 to give the content exactly cw columns.
	out := frameStyle.Width(cw + 2).Render(inner)
	if height > 0 {
		out = capLines(out, height)
	}
	return out
}

func windowLines(lines []string, scroll, budget int) []string {
	if len(lines) <= budget {
		return lines
	}
	max := len(lines) - budget
	if scroll < 0 {
		scroll = 0
	} else if scroll > max {
		scroll = max
	}
	return lines[scroll : scroll+budget]
}

func capLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}
