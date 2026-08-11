// internal/render/view_test.go
package render

import (
	"strings"
	"testing"

	"agentmon/internal/collector"
)

func TestRenderViewGroupsAndTree(t *testing.T) {
	sessions := []collector.Session{
		{Name: "work", Project: "proj", Kind: "interactive", Mode: collector.Determinate, Done: 6, Total: 10, Status: "busy",
			Children: []collector.Session{
				{Name: "Review Task 6", Kind: "sub:general-purpose", Mode: collector.Indeterminate, Status: "idle"},
			}},
		{Name: "bgjob", Project: "proj", Kind: "bg", JobState: "blocked", Blocked: true, NeedsHint: "commit now?", Mode: collector.Determinate, Done: 41, Total: 41},
	}
	out := RenderView(sessions, 12, 0)

	if !strings.Contains(out, "▸ proj") {
		t.Errorf("missing project header:\n%s", out)
	}
	if !strings.Contains(out, "└─") {
		t.Errorf("missing tree branch:\n%s", out)
	}
	if !strings.Contains(out, "6/10") || !strings.Contains(out, "⏸ blocked") {
		t.Errorf("missing labels:\n%s", out)
	}
	if !strings.Contains(out, "needs: commit now?") {
		t.Errorf("missing needs hint:\n%s", out)
	}
}
