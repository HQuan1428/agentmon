// internal/collector/types.go
package collector

import "strings"

type Agent string

const (
	AgentClaude   Agent = "Claude"
	AgentCodex    Agent = "Codex"
	AgentOpenCode Agent = "OpenCode"
	AgentAider    Agent = "Aider"
)

func (a Agent) Rank() int {
	switch a {
	case AgentClaude:
		return 0
	case AgentCodex:
		return 1
	case AgentOpenCode:
		return 2
	case AgentAider:
		return 3
	default:
		return 99
	}
}

func GlobalID(agent Agent, native string) string {
	return strings.ToLower(string(agent)) + ":" + native
}

func ModelOrUnknown(model string) string {
	if strings.TrimSpace(model) == "" {
		return "unknown"
	}
	return model
}

type ProgressMode int

const (
	Determinate ProgressMode = iota
	Indeterminate
)

type Session struct {
	ID        string
	NativeID  string
	Agent     Agent
	Model     string
	Name      string
	Project   string
	Cwd       string
	Kind      string
	Status    string
	JobState  string
	PID       int
	Mode      ProgressMode
	Done      int
	Total     int
	Blocked   bool
	NeedsHint string
	jobID     string // bg jobId, used to locate jobs/<jobID>/state.json
	Children  []Session
	UpdatedAt int64
}

func (s Session) IsDone() bool {
	switch {
	case s.Blocked:
		return false
	case s.JobState == "done" || s.JobState == "idle":
		return true
	case s.Mode == Determinate:
		return s.Total > 0 && s.Done >= s.Total
	default:
		return s.Status == "idle"
	}
}

func (s Session) Fraction() float64 {
	if s.Mode == Determinate && s.Total > 0 {
		f := float64(s.Done) / float64(s.Total)
		if f > 1 {
			return 1
		}
		return f
	}
	if s.IsDone() {
		return 1
	}
	return 0
}
