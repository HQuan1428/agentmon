package collector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

var tasksRe = regexp.MustCompile(`(\d+)/(\d+)\s+tasks`)

// jobStaleTTL reaps a bg job whose state.json stopped being written (crashed
// daemon) so it doesn't pin the UI forever.
// ponytail: mtime-TTL heuristic; upgrade to real daemon-pid detection if it misfires.
const jobStaleTTL = 15 * time.Minute

// jobDoneGrace is how long a finished (state=="done") job keeps rendering DONE
// after its last state.json write before it drops out.
const jobDoneGrace = 5 * time.Second

type jobState struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
	Needs  string `json:"needs"`
	Name   string `json:"name"`
	Cwd    string `json:"cwd"`
}

func readJobState(path string) (jobState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return jobState{}, false
	}
	var j jobState
	if json.Unmarshal(data, &j) != nil {
		return jobState{}, false
	}
	return j, true
}

func jobProgress(detail string) (done, total int) {
	if m := tasksRe.FindStringSubmatch(detail); m != nil {
		done, _ = strconv.Atoi(m[1])
		total, _ = strconv.Atoi(m[2])
	}
	return done, total
}

func ParseJob(root, jobID string) (state string, blocked bool, needs string, done, total int, ok bool) {
	j, ok := readJobState(filepath.Join(root, "jobs", jobID, "state.json"))
	if !ok {
		return "", false, "", 0, 0, false
	}
	done, total = jobProgress(j.Detail)
	return j.State, j.State == "blocked", j.Needs, done, total, true
}

// ScanJobs builds a bg Session for every ~/.claude/jobs/<id>/state.json that is
// still live. bg jobs are daemon-backed: the pid recorded in sessions/*.json is
// the transient CLI launcher (dead once the terminal closes) while the daemon
// keeps running, so liveness comes from the job state, never a pid. A job shows
// while running/blocked/idle and its state.json is fresh; a "done" job renders
// DONE briefly (jobDoneGrace) then drops; a stale one is reaped (jobStaleTTL).
func ScanJobs(root string, now time.Time) []Session {
	dir := filepath.Join(root, "jobs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		jobID := e.Name()
		statePath := filepath.Join(dir, jobID, "state.json")
		j, ok := readJobState(statePath)
		if !ok {
			continue
		}
		info, err := os.Stat(statePath)
		if err != nil {
			continue
		}
		age := now.Sub(info.ModTime())
		if j.State == "done" {
			if age >= jobDoneGrace {
				continue
			}
		} else if age >= jobStaleTTL {
			continue
		}

		done, total := jobProgress(j.Detail)
		mode := Indeterminate
		if total > 0 {
			mode = Determinate
		}
		status := "idle"
		if j.State == "running" {
			status = "busy"
		}
		out = append(out, Session{
			ID:        jobID,
			NativeID:  jobID,
			Name:      jobName(j, jobID),
			Project:   filepath.Base(j.Cwd),
			Cwd:       j.Cwd,
			Kind:      "bg",
			Status:    status,
			JobState:  j.State,
			Blocked:   j.State == "blocked",
			NeedsHint: j.Needs,
			Mode:      mode,
			Done:      done,
			Total:     total,
			jobID:     jobID,
		})
	}
	return out
}

func jobName(j jobState, jobID string) string {
	if j.Name != "" {
		return j.Name
	}
	return jobID
}
