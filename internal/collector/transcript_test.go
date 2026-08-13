// internal/collector/transcript_test.go
package collector

import (
	"path/filepath"
	"testing"
)

func TestEncodeCwd(t *testing.T) {
	if got := EncodeCwd("/home/u/proj"); got != "-home-u-proj" {
		t.Errorf("EncodeCwd=%q", got)
	}
}

// One assistant line carrying a TodoWrite tool_use with the given todos JSON array.
func todoLine(todos string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"TodoWrite","input":{"todos":` + todos + `}}]}}`
}

func modelLine(model string) string {
	return `{"type":"assistant","message":{"role":"assistant","model":"` + model + `","content":[]}}`
}

func TestScannerCapturesModelFromCompleteLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.jsonl")
	writeFile(t, path, modelLine("claude-opus-4-6")+"\n"+
		`{"type":"user","message":{"role":"user","model":"not-a-runtime-model","content":[]}}`+"\n"+
		modelLine("claude-sonnet-4-5"))

	sc := NewScanner()
	if got := sc.Scan(path); got.Model != "claude-opus-4-6" {
		t.Fatalf("model after partial line=%q", got.Model)
	}
	appendFile(t, path, "\n")
	if got := sc.Scan(path); got.Model != "claude-sonnet-4-5" {
		t.Fatalf("model after completed line=%q", got.Model)
	}
}

func TestScannerIgnoresWhitespaceOnlyAssistantModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.jsonl")
	writeFile(t, path, modelLine("claude-opus-4-6")+"\n"+
		`{"type":"assistant","message":{"role":"assistant","model":" \t ","content":[]}}`+"\n")

	if got := NewScanner().Scan(path); got.Model != "claude-opus-4-6" {
		t.Fatalf("model after whitespace-only update=%q", got.Model)
	}
}

func TestParseTodosLastWins(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "x.jsonl")
	first := todoLine(`[{"content":"a","status":"completed","activeForm":"a"},{"content":"b","status":"pending","activeForm":"b"}]`)
	last := todoLine(`[{"content":"a","status":"completed","activeForm":"a"},{"content":"b","status":"completed","activeForm":"b"},{"content":"c","status":"in_progress","activeForm":"c"}]`)
	writeFile(t, path, first+"\n"+`{"type":"user","message":{"role":"user","content":[]}}`+"\n"+last+"\n")

	done, total, found := ParseTodos(path)
	if !found || total != 3 || done != 2 {
		t.Fatalf("ParseTodos=(%d,%d,%v) want (2,3,true)", done, total, found)
	}
}

func TestParseTodosNoneFound(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "x.jsonl")
	writeFile(t, path, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`+"\n")
	if _, _, found := ParseTodos(path); found {
		t.Error("expected found=false when no TodoWrite present")
	}
	if _, _, found := ParseTodos(filepath.Join(root, "missing.jsonl")); found {
		t.Error("expected found=false for missing file")
	}
}

func taskLine(id, desc, subtype string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"Task","input":{"description":"` + desc + `","subagent_type":"` + subtype + `"}}]}}`
}
func resultLine(useID string) string {
	return `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + useID + `"}]}}`
}

func agentLine(id, desc, subtype string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"Agent","input":{"description":"` + desc + `","subagent_type":"` + subtype + `"}}]}}`
}

// launchAckLine is the inline tool_result current Claude Code returns milliseconds
// after dispatching a background Agent — a launch ack carrying the agentId, NOT
// completion.
func launchAckLine(useID, agentID string) string {
	return `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + useID +
		`","content":[{"type":"text","text":"Async agent launched successfully. agentId: ` + agentID + ` (internal ID)"}]}]}}`
}

func TestScannerBackgroundSubagentLiveness(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sess.jsonl")
	writeFile(t, path,
		agentLine("tu1", "Investigate", "investigator")+"\n"+launchAckLine("tu1", "aaa1")+"\n"+
			agentLine("tu2", "Build", "builder")+"\n"+launchAckLine("tu2", "bbb2")+"\n")
	subDir := filepath.Join(root, "sess", "subagents")
	// aaa1 finished cleanly; bbb2 is mid tool call (still running)
	writeFile(t, filepath.Join(subDir, "agent-aaa1.jsonl"),
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`+"\n")
	writeFile(t, filepath.Join(subDir, "agent-bbb2.jsonl"),
		`{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","id":"z","name":"Bash"}]}}`+"\n")

	snap := NewScanner().Scan(path)
	if len(snap.Children) != 2 {
		t.Fatalf("children=%+v", snap.Children)
	}
	by := map[string]Session{}
	for _, c := range snap.Children {
		by[c.Name] = c
	}
	if !by["Investigate"].IsDone() {
		t.Errorf("finished (end_turn) subagent should be DONE: %+v", by["Investigate"])
	}
	if by["Build"].IsDone() {
		t.Errorf("running subagent should not be DONE: %+v", by["Build"])
	}
}

func TestParseSubagents(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "x.jsonl")
	// task A completed, task B still running
	writeFile(t, path, taskLine("A", "Implement Task 1", "general-purpose")+"\n"+
		resultLine("A")+"\n"+
		taskLine("B", "Review Task 1", "general-purpose")+"\n")

	subs := ParseSubagents(path)
	if len(subs) != 2 {
		t.Fatalf("want 2 subagents, got %d", len(subs))
	}
	byName := map[string]Session{}
	for _, s := range subs {
		byName[s.Name] = s
	}
	if !byName["Implement Task 1"].IsDone() {
		t.Error("task A should be done")
	}
	if byName["Review Task 1"].IsDone() {
		t.Error("task B should still be running")
	}
	if byName["Review Task 1"].Kind != "sub:general-purpose" {
		t.Errorf("kind=%q", byName["Review Task 1"].Kind)
	}
}
