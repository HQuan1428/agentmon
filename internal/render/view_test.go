package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"agentmon/internal/collector"
)

func TestBodyLinesGroupedSessionsAndTree(t *testing.T) {
	sessions := []collector.Session{
		{ID: "w", Agent: collector.AgentClaude, Name: "improve-finbert", Project: "proj", Kind: "interactive", Mode: collector.Determinate, Done: 6, Total: 10, Status: "busy",
			Children: []collector.Session{
				{ID: "c", Name: "task-6-review", Kind: "sub:general-purpose", Mode: collector.Indeterminate, Status: "idle"},
			}},
		{ID: "b", Agent: collector.AgentClaude, Name: "da_cnm", Project: "proj", Kind: "bg", JobState: "blocked", Blocked: true, NeedsHint: "commit now?", Mode: collector.Determinate, Done: 41, Total: 41},
	}
	out := strings.Join(BodyLines(sessions, 0, nil, 120), "\n")

	for _, want := range []string{
		"▾ proj",             // project group header
		"▸ improve-finbert",  // session marker
		"└─ ⌁ task-6-review", // subagent branch + icon
		"[", "]",             // bracketed bar
		"6/10", "41/41", "DONE", // TASKS column (uppercase DONE)
		"⚡ BUSY", "✓ DONE", "⏸ BLOCKED", // STATUS icons + uppercase
		"(bg)", "needs: commit now?",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("body missing %q:\n%s", want, out)
		}
	}
}

func TestBodyLinesDimsDoneRow(t *testing.T) {
	sessions := []collector.Session{
		{ID: "x", Name: "done-one", Project: "proj", Mode: collector.Indeterminate, Status: "idle"},
	}
	plain := strings.Join(BodyLines(sessions, 0, nil, 120), "\n")
	if strings.Contains(plain, "\x1b[2m") {
		t.Errorf("nil dim set should not add faint codes:\n%q", plain)
	}
	dimmed := strings.Join(BodyLines(sessions, 0, map[string]bool{"x": true}, 120), "\n")
	if !strings.Contains(dimmed, "\x1b[2m") {
		t.Errorf("expected faint code around dimmed row:\n%q", dimmed)
	}
	if !strings.Contains(dimmed, "done-one") {
		t.Errorf("dimmed row should still contain its content:\n%q", dimmed)
	}
}

func TestBodyLinesAgentHierarchy(t *testing.T) {
	sessions := []collector.Session{
		{ID: "claude:1", Agent: collector.AgentClaude, Model: "claude-opus-4-6", Name: "claude-work", Project: "proj", Status: "busy", Mode: collector.Indeterminate,
			Children: []collector.Session{{ID: "claude:child", Agent: collector.AgentClaude, Model: "claude-opus-4-6", Name: "review", Status: "busy", Mode: collector.Indeterminate}}},
		{ID: "codex:1", Agent: collector.AgentCodex, Model: "gpt-5.6-sol", Name: "codex-work", Project: "proj", Status: "busy", Mode: collector.Indeterminate},
	}
	out := stripANSI(strings.Join(BodyLines(sessions, 0, nil, 120), "\n"))
	ordered := []string{"▾ proj", "  ▾ Claude", "    ▸ claude-work", "      └─ ⌁ review", "  ▾ Codex", "    ▸ codex-work"}
	last := -1
	for _, want := range ordered {
		idx := strings.Index(out, want)
		if idx <= last {
			t.Fatalf("%q out of order:\n%s", want, out)
		}
		last = idx
	}
	if !strings.Contains(out, "claude-opus-4-6") || !strings.Contains(out, "gpt-5.6-sol") {
		t.Fatalf("session models missing:\n%s", out)
	}
	childLine := lineContaining(out, "review")
	if strings.Contains(childLine, "claude-opus-4-6") || strings.Contains(childLine, "gpt-5.6-sol") {
		t.Fatalf("subagent leaked model: %s", childLine)
	}
}

func TestLayoutForWidthAndCompactModel(t *testing.T) {
	wide := LayoutForWidth(96)
	if wide.Compact || wide.SessionW != 32 || wide.ModelW != 20 || wide.BarW != 15 || wide.TasksW != 8 {
		t.Fatalf("wide layout=%+v", wide)
	}
	compact := LayoutForWidth(95)
	if !compact.Compact || compact.ModelW != 0 || compact.SessionW != contentWidth(95) || compact.BarW != 12 || compact.TasksW != 7 {
		t.Fatalf("compact layout=%+v", compact)
	}
	out := stripANSI(strings.Join(BodyLines([]collector.Session{{
		ID: "codex:1", Agent: collector.AgentCodex, Model: "gpt-5.6-sol", Name: "work", Project: "proj", Status: "busy", Mode: collector.Indeterminate,
	}}, 0, nil, 80), "\n"))
	if !strings.Contains(out, "gpt-5.6-sol") {
		t.Fatalf("compact body should continue model:\n%s", out)
	}
}

func TestCompactModelContinuation(t *testing.T) {
	sessions := []collector.Session{{
		ID: "codex:1", Agent: collector.AgentCodex, Model: "gpt-5.6-sol", Name: "codex-work", Project: "proj",
		Status: "busy", Mode: collector.Determinate, Done: 2, Total: 5,
		Children: []collector.Session{{
			ID: "codex:child", Agent: collector.AgentCodex, Model: "child-model", Name: "review",
			Status: "busy", Mode: collector.Indeterminate,
		}},
	}}

	lines := BodyLines(sessions, 0, nil, 70)
	nameIdx := indexLine(lines, "codex-work")
	modelIdx := indexLine(lines, "gpt-5.6-sol")
	if nameIdx < 0 || modelIdx != nameIdx+1 {
		t.Fatalf("compact rows=%q", lines)
	}
	detail := stripANSI(lines[modelIdx])
	for _, want := range []string{"model:", "[", "]", "2/5", "BUSY"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("compact detail missing %q: %q", want, detail)
		}
	}
	child := lineContaining(stripANSI(strings.Join(lines, "\n")), "review")
	if strings.Contains(child, "gpt-5.6-sol") || strings.Contains(child, "child-model") {
		t.Fatalf("subagent leaked model: %q", child)
	}

	narrow := stripANSI(strings.Join(BodyLines(sessions, 0, nil, 40), "\n"))
	if strings.Contains(narrow, "unknown") || strings.Contains(narrow, "child-model") {
		t.Fatalf("narrow rows replaced or leaked model: %q", narrow)
	}
	if !strings.Contains(narrow, "model: …") {
		t.Fatalf("narrow rows should truncate the evidenced model: %q", narrow)
	}
}

func TestBodyLinesUnicodeColumnsAndCompactWidth(t *testing.T) {
	sessions := []collector.Session{
		{ID: "codex:ascii", Agent: collector.AgentCodex, Model: "model-a", Name: "ascii", Project: "proj", Status: "busy", Mode: collector.Indeterminate},
		{ID: "codex:cjk", Agent: collector.AgentCodex, Model: "模型-v2", Name: "会議修正", Project: "proj", Status: "busy", Mode: collector.Indeterminate},
		{ID: "codex:emoji", Agent: collector.AgentCodex, Model: "🧠-v3", Name: "🧑‍💻 review", Project: "proj", Status: "busy", Mode: collector.Indeterminate},
	}
	layout := LayoutForWidth(120)
	out := stripANSI(strings.Join(BodyLines(sessions, 0, nil, 120), "\n"))
	for _, model := range []string{"model-a", "模型-v2", "🧠-v3"} {
		line := lineContaining(out, model)
		if got := cellStart(line, model); got != layout.SessionW {
			t.Errorf("model %q starts at cell %d, want %d:\n%s", model, got, layout.SessionW, line)
		}
		if got := cellStart(line, "["); got != layout.SessionW+layout.ModelW {
			t.Errorf("progress after %q starts at cell %d, want %d:\n%s", model, got, layout.SessionW+layout.ModelW, line)
		}
	}

	compactWidth := 80
	compactSessions := []collector.Session{{
		ID: "claude:long", Agent: collector.AgentClaude, Name: strings.Repeat("🧑‍💻", 30), Project: strings.Repeat("專案", 30),
		Model: "模型", Status: "busy", Mode: collector.Indeterminate, Blocked: true, NeedsHint: strings.Repeat("需要核准", 30),
	}}
	for _, line := range BodyLines(compactSessions, 0, nil, compactWidth) {
		if got, max := lipgloss.Width(stripANSI(line)), contentWidth(compactWidth); got > max {
			t.Errorf("compact line width=%d exceeds content width=%d:\n%s", got, max, stripANSI(line))
		}
	}
	header := lineContaining(stripANSI(columnHeader(contentWidth(compactWidth), LayoutForWidth(compactWidth))), "PROJECT")
	if got, max := lipgloss.Width(header), contentWidth(compactWidth); got > max {
		t.Errorf("compact header width=%d exceeds content width=%d:\n%s", got, max, header)
	}
}

func cellStart(line, needle string) int {
	i := strings.Index(line, needle)
	if i < 0 {
		return -1
	}
	return lipgloss.Width(line[:i])
}

func lineContaining(text, needle string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func indexLine(lines []string, needle string) int {
	for i, line := range lines {
		if strings.Contains(stripANSI(line), needle) {
			return i
		}
	}
	return -1
}

func TestCountSessions(t *testing.T) {
	sessions := []collector.Session{
		{ID: "1", Status: "busy", Mode: collector.Indeterminate},                  // busy
		{ID: "2", Status: "idle", Mode: collector.Indeterminate},                  // done (not busy)
		{ID: "3", Kind: "bg", Blocked: true, JobState: "blocked"},                 // blocked
		{ID: "4", Status: "busy", Mode: collector.Determinate, Done: 2, Total: 5}, // busy
	}
	c := CountSessions(sessions)
	if c.Active != 4 {
		t.Errorf("Active=%d want 4", c.Active)
	}
	if c.Busy != 2 {
		t.Errorf("Busy=%d want 2", c.Busy)
	}
	if c.Blocked != 1 {
		t.Errorf("Blocked=%d want 1", c.Blocked)
	}
}
