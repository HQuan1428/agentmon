// internal/collector/collector_test.go
package collector

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCollectInteractiveWithTodosAndSubagent(t *testing.T) {
	root := t.TempDir()
	alive := os.Getpid()
	cwd := "/home/u/proj"
	writeFile(t, filepath.Join(root, "sessions", "1.json"),
		`{"pid":`+itoa(alive)+`,"sessionId":"sess1","cwd":"`+cwd+`","kind":"interactive","name":"work","status":"busy","updatedAt":5,"jobId":""}`)
	tp := TranscriptPath(root, cwd, "sess1")
	writeFile(t, tp, todoLine(`[{"content":"a","status":"completed","activeForm":"a"},{"content":"b","status":"pending","activeForm":"b"}]`)+"\n"+
		taskLine("B", "Review Task 1", "general-purpose")+"\n")

	sessions, err := Collect(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1, got %d", len(sessions))
	}
	s := sessions[0]
	if s.Mode != Determinate || s.Done != 1 || s.Total != 2 {
		t.Errorf("progress wrong: mode=%v done=%d total=%d", s.Mode, s.Done, s.Total)
	}
	if len(s.Children) != 1 || s.Children[0].Name != "Review Task 1" {
		t.Errorf("subagent not attached: %+v", s.Children)
	}
}

func TestCollectBgBlocked(t *testing.T) {
	root := t.TempDir()
	alive := os.Getpid()
	writeFile(t, filepath.Join(root, "sessions", "2.json"),
		`{"pid":`+itoa(alive)+`,"sessionId":"sess2","cwd":"/home/u/proj","kind":"bg","name":"bgjob","status":"busy","updatedAt":9,"jobId":"job1"}`)
	writeFile(t, filepath.Join(root, "jobs", "job1", "state.json"),
		`{"state":"blocked","detail":"pipeline 41/41 tasks done","needs":"decide: commit?"}`)

	sessions, _ := Collect(root)
	if len(sessions) != 1 {
		t.Fatalf("want 1, got %d", len(sessions))
	}
	s := sessions[0]
	if !s.Blocked || s.JobState != "blocked" || s.Done != 41 || s.Total != 41 || s.Mode != Determinate {
		t.Errorf("bg blocked session wrong: %+v", s)
	}
}
