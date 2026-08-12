// internal/collector/scanner_test.go
package collector

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScannerIncrementalEqualsFull(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "t.jsonl")
	chunk1 := todoLine(`[{"content":"a","status":"completed","activeForm":"a"},{"content":"b","status":"pending","activeForm":"b"}]`) + "\n" +
		taskLine("A", "Task A", "general-purpose") + "\n"
	chunk2 := resultLine("A") + "\n" +
		todoLine(`[{"content":"a","status":"completed","activeForm":"a"},{"content":"b","status":"completed","activeForm":"b"}]`) + "\n"

	// Incremental: write chunk1, scan; append chunk2, scan again.
	writeFile(t, path, chunk1)
	sc := NewScanner()
	sc.Scan(path)
	appendFile(t, path, chunk2)
	got := sc.Scan(path)

	// Full: parse whole file fresh.
	full := NewScanner()
	fullGot := full.Scan(path)

	if got.Done != fullGot.Done || got.Total != fullGot.Total || got.HaveTodos != fullGot.HaveTodos {
		t.Errorf("todos incremental=%+v full=%+v", got, fullGot)
	}
	if len(got.Children) != len(fullGot.Children) || len(got.Children) != 1 || !got.Children[0].IsDone() {
		t.Errorf("subs incremental=%+v full=%+v", got.Children, fullGot.Children)
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}
