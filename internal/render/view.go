package render

import (
	"fmt"
	"strings"

	"agentmon/internal/collector"
)

func RenderView(sessions []collector.Session, barWidth, phase int) string {
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
		fmt.Fprintf(&b, "   %-22s %s  %s\n", truncate(s.Name, 22), RenderBar(s, barWidth, phase), Label(s))
		if s.Blocked && s.NeedsHint != "" {
			fmt.Fprintf(&b, "   %-22s needs: %s\n", "", truncate(s.NeedsHint, 60))
		}
		for i, c := range s.Children {
			branch := "├─"
			if i == len(s.Children)-1 {
				branch = "└─"
			}
			fmt.Fprintf(&b, "      %s %-16s %s  %s\n", branch, truncate(c.Name, 16), RenderBar(c, barWidth, phase), Label(c))
		}
	}
	return b.String()
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
