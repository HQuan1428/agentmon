// internal/collector/types.go
package collector

type ProgressMode int

const (
	Determinate ProgressMode = iota
	Indeterminate
)

type Session struct {
	ID        string
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
