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
