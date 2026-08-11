package collector

import "sort"

func Collect(root string) ([]Session, error) {
	sessions, err := ScanSessions(root)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		s := &sessions[i]
		if s.Kind == "bg" && s.jobID != "" {
			if state, blocked, needs, done, total, ok := ParseJob(root, s.jobID); ok {
				s.JobState, s.Blocked, s.NeedsHint, s.Done, s.Total = state, blocked, needs, done, total
				if total > 0 {
					s.Mode = Determinate
				} else {
					s.Mode = Indeterminate
				}
				continue
			}
		}
		// interactive (or bg without a job file): use transcript
		tp := TranscriptPath(root, s.Cwd, s.ID)
		if done, total, found := ParseTodos(tp); found {
			s.Mode, s.Done, s.Total = Determinate, done, total
		} else {
			s.Mode = Indeterminate
		}
		s.Children = ParseSubagents(tp)
	}
	sort.Slice(sessions, func(a, b int) bool {
		if sessions[a].Project != sessions[b].Project {
			return sessions[a].Project < sessions[b].Project
		}
		return sessions[a].Name < sessions[b].Name
	})
	return sessions, nil
}
