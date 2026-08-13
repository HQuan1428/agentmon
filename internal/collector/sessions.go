// internal/collector/sessions.go
package collector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

func itoa(i int) string { return strconv.Itoa(i) }

type rawSession struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	JobID     string `json:"jobId"`
	UpdatedAt int64  `json:"updatedAt"`
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Signal 0 probes existence without affecting the process.
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func ScanSessions(root string) ([]Session, error) {
	return scanSessions(root, pidAlive)
}

func scanSessions(root string, alive func(int) bool) ([]Session, error) {
	dir := filepath.Join(root, "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var r rawSession
		if json.Unmarshal(data, &r) != nil {
			continue
		}
		if r.Kind == "bg" {
			continue // bg jobs come from the jobs/ store (ScanJobs), not pid-gated here
		}
		if !alive(r.PID) {
			continue
		}
		out = append(out, Session{
			ID:        r.SessionID,
			Name:      r.Name,
			Project:   filepath.Base(r.Cwd),
			Cwd:       r.Cwd,
			Kind:      r.Kind,
			Status:    r.Status,
			PID:       r.PID,
			jobID:     r.JobID,
			UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}
