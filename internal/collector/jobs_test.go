// internal/collector/jobs_test.go
package collector

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanJobsLivenessAndReaping(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	write := func(id, body string, age time.Duration) {
		p := filepath.Join(root, "jobs", id, "state.json")
		writeFile(t, p, body)
		at := now.Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write("run", `{"state":"running","detail":"3/8 tasks done","name":"build","cwd":"/home/u/proj"}`, 0)
	write("blk", `{"state":"blocked","detail":"5/8 tasks done","needs":"commit?","name":"blk","cwd":"/x/y"}`, 0)
	write("don", `{"state":"done","detail":"8/8 tasks done","name":"don","cwd":"/x/y"}`, 0)
	write("stalerun", `{"state":"running","detail":"1/2 tasks done","name":"old","cwd":"/x/y"}`, 30*time.Minute)
	write("donegone", `{"state":"done","detail":"2/2 tasks done","name":"dg","cwd":"/x/y"}`, time.Minute)

	by := map[string]Session{}
	for _, s := range ScanJobs(root, now) {
		by[s.jobID] = s
	}
	for _, want := range []string{"run", "blk", "don"} {
		if _, ok := by[want]; !ok {
			t.Errorf("job %q should be visible", want)
		}
	}
	for _, gone := range []string{"stalerun", "donegone"} {
		if _, ok := by[gone]; ok {
			t.Errorf("job %q should be dropped", gone)
		}
	}
	if s := by["run"]; s.Kind != "bg" || s.Status != "busy" || s.Mode != Determinate || s.Done != 3 || s.Total != 8 || s.Name != "build" || s.Project != "proj" {
		t.Fatalf("run=%+v", s)
	}
	if s := by["blk"]; !s.Blocked || s.JobState != "blocked" || s.NeedsHint != "commit?" {
		t.Fatalf("blk=%+v", s)
	}
}

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
