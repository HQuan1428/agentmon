// internal/collector/jobs_test.go
package collector

import (
	"path/filepath"
	"testing"
)

func TestParseJobBlockedWithProgress(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "jobs", "job1", "state.json"),
		`{"state":"blocked","detail":"pipeline 41/41 tasks done","needs":"decide: commit now?","inFlight":{"tasks":0,"queued":0}}`)

	state, blocked, needs, done, total, ok := ParseJob(root, "job1")
	if !ok || state != "blocked" || !blocked || needs != "decide: commit now?" || done != 41 || total != 41 {
		t.Fatalf("ParseJob=(%q,%v,%q,%d,%d,%v)", state, blocked, needs, done, total, ok)
	}
}

func TestParseJobRunningNoProgress(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "jobs", "job2", "state.json"), `{"state":"running","detail":"working","needs":""}`)
	state, blocked, _, done, total, ok := ParseJob(root, "job2")
	if !ok || state != "running" || blocked || done != 0 || total != 0 {
		t.Fatalf("ParseJob running=(%q,%v,%d,%d,%v)", state, blocked, done, total, ok)
	}
}

func TestParseJobMissing(t *testing.T) {
	if _, _, _, _, _, ok := ParseJob(t.TempDir(), "nope"); ok {
		t.Error("expected ok=false for missing job")
	}
}
