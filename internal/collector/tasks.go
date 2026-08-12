package collector

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// ParseTasksDir reads a Claude session's task list at <root>/tasks/<sessionID>/,
// where each task is a <n>.json file with a "status" field (Claude Code's
// TaskCreate/TaskUpdate store). It returns completed/total (deleted tasks
// excluded) and whether any task exists.
func ParseTasksDir(root, sessionID string) (done, total int, found bool) {
	entries, err := os.ReadDir(filepath.Join(root, "tasks", sessionID))
	if err != nil {
		return 0, 0, false
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, "tasks", sessionID, e.Name()))
		if err != nil {
			continue
		}
		var t struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(data, &t) != nil || t.Status == "deleted" {
			continue
		}
		total++
		if t.Status == "completed" {
			done++
		}
	}
	return done, total, total > 0
}
