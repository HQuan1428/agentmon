package collector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

var tasksRe = regexp.MustCompile(`(\d+)/(\d+)\s+tasks`)

func ParseJob(root, jobID string) (state string, blocked bool, needs string, done, total int, ok bool) {
	data, err := os.ReadFile(filepath.Join(root, "jobs", jobID, "state.json"))
	if err != nil {
		return "", false, "", 0, 0, false
	}
	var j struct {
		State  string `json:"state"`
		Detail string `json:"detail"`
		Needs  string `json:"needs"`
	}
	if json.Unmarshal(data, &j) != nil {
		return "", false, "", 0, 0, false
	}
	if m := tasksRe.FindStringSubmatch(j.Detail); m != nil {
		done, _ = strconv.Atoi(m[1])
		total, _ = strconv.Atoi(m[2])
	}
	return j.State, j.State == "blocked", j.Needs, done, total, true
}
