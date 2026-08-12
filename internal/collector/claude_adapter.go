package collector

import (
	"agentmon/internal/procscan"
)

type ClaudeAdapter struct {
	root    string
	scanner *Scanner
}

func NewClaudeAdapter(root string) *ClaudeAdapter {
	return &ClaudeAdapter{root: root, scanner: NewScanner()}
}

func (a *ClaudeAdapter) Agent() Agent { return AgentClaude }

func (a *ClaudeAdapter) Discover(snapshot procscan.Snapshot) ([]Session, error) {
	sessions, err := scanSessions(a.root, snapshot.HasPID)
	if err != nil {
		return nil, err
	}
	live := make(map[string]struct{}, len(sessions))
	for i := range sessions {
		s := &sessions[i]
		path := TranscriptPath(a.root, s.Cwd, s.ID)
		live[path] = struct{}{}
		transcript := a.scanner.Scan(path)
		s.Model = transcript.Model

		if s.Kind == "bg" && s.jobID != "" {
			if state, blocked, needs, done, total, ok := ParseJob(a.root, s.jobID); ok {
				s.JobState, s.Blocked, s.NeedsHint, s.Done, s.Total = state, blocked, needs, done, total
				if total > 0 {
					s.Mode = Determinate
				} else {
					s.Mode = Indeterminate
				}
				normalizeClaudeSession(s)
				continue
			}
		}

		if done, total, ok := ParseTasksDir(a.root, s.ID); ok {
			// Current Claude Code (TaskCreate/TaskUpdate store) takes priority.
			s.Mode, s.Done, s.Total = Determinate, done, total
		} else if transcript.HaveTodos {
			// Older sessions expose the list via TodoWrite in the transcript.
			s.Mode, s.Done, s.Total = Determinate, transcript.Done, transcript.Total
		} else {
			s.Mode = Indeterminate
		}
		s.Children = transcript.Children
		normalizeClaudeSession(s)
	}
	a.scanner.Prune(live)
	return sessions, nil
}

func normalizeClaudeSession(session *Session) {
	session.NativeID = session.ID
	session.ID = GlobalID(AgentClaude, session.NativeID)
	session.Agent = AgentClaude
	for i := range session.Children {
		child := &session.Children[i]
		child.NativeID = child.ID
		child.ID = session.ID + "/" + child.NativeID
		child.Agent = AgentClaude
		child.Model = ""
	}
}
