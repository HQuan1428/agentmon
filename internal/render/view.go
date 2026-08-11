package render

import (
	"fmt"
	"strings"

	"agentmon/internal/collector"
)

// RenderView renders the grouped session view. IDs present in dim (may be
// nil) have their row rendered faint — used to fade a session that just
// completed before it disappears.
func RenderView(sessions []collector.Session, barWidth, phase int, dim map[string]bool) string {
	var b strings.Builder
	curProject := ""
	for _, s := range sessions {
		if s.Project != curProject {
			if curProject != "" {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "▸ %s\n", s.Project)
			curProject = s.Project
		}
		line := fmt.Sprintf("   %-22s %s  %s", truncate(s.Name, 22), RenderBar(s, barWidth, phase), Label(s))
		b.WriteString(dimIf(line, dim[s.ID]) + "\n")
		if s.Blocked && s.NeedsHint != "" {
			fmt.Fprintf(&b, "   %-22s needs: %s\n", "", truncate(s.NeedsHint, 60))
		}
		for i, c := range s.Children {
			branch := "├─"
			if i == len(s.Children)-1 {
				branch = "└─"
			}
			cl := fmt.Sprintf("      %s %-16s %s  %s", branch, truncate(c.Name, 16), RenderBar(c, barWidth, phase), Label(c))
			b.WriteString(dimIf(cl, dim[c.ID]) + "\n")
		}
	}
	return b.String()
}

// dimIf wraps a line in the ANSI faint attribute when d is true. Terminals
// offer a single faint level, so this is a one-step dim, not a gradual fade.
func dimIf(s string, d bool) string {
	if d {
		return "\x1b[2m" + s + "\x1b[0m"
	}
	return s
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
