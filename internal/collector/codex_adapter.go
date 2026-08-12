package collector

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentmon/internal/procscan"
)

const codexRecentDuration = 4 * time.Second

type CodexAdapter struct {
	root    string
	scanner *CodexScanner
	now     func() time.Time
	recent  map[string]recentSession
}

type recentSession struct {
	Row        Session
	ParentID   string
	SourcePath string
	StartTicks uint64
	ExpiresAt  time.Time
}

type codexObservedSession struct {
	Row      Session
	ParentID string
}

func NewCodexAdapter(root string) *CodexAdapter {
	return &CodexAdapter{
		root: root, scanner: NewCodexScanner(), now: time.Now,
		recent: map[string]recentSession{},
	}
}

func (a *CodexAdapter) Agent() Agent { return AgentCodex }

func (a *CodexAdapter) Discover(snapshot procscan.Snapshot) ([]Session, error) {
	now := a.now()
	instances := make(map[int]uint64)
	attributed := make(map[int]bool)
	emitted := make(map[int]bool)
	livePaths := make(map[string]struct{})
	observed := make(map[string]codexObservedSession)
	var processes []procscan.Process

	for _, process := range snapshot.Processes {
		if !isCodexProcess(process) {
			continue
		}
		processes = append(processes, process)
		instances[process.PID] = process.StartTicks
		for _, file := range process.Files {
			if !a.isRolloutPath(file.Path) {
				continue
			}
			livePaths[file.Path] = struct{}{}
			rollout := a.scanner.Scan(file.Path)
			if strings.TrimSpace(rollout.NativeID) == "" {
				continue
			}
			attributed[process.PID] = true
			row := codexSession(process, rollout)
			entry := codexObservedSession{Row: row, ParentID: strings.TrimSpace(rollout.ParentID)}

			switch {
			case rollout.Busy:
				delete(a.recent, row.ID)
			case rollout.Done:
				recent, ok := a.recent[row.ID]
				if !ok || recent.StartTicks != process.StartTicks {
					recent.ExpiresAt = now.Add(codexRecentDuration)
				}
				recent.Row, recent.ParentID, recent.SourcePath, recent.StartTicks = row, entry.ParentID, file.Path, process.StartTicks
				a.recent[row.ID] = recent
				if now.After(recent.ExpiresAt) {
					continue
				}
			}

			if _, exists := observed[row.NativeID]; !exists {
				observed[row.NativeID] = entry
				emitted[process.PID] = true
			}
		}
	}

	for id, recent := range a.recent {
		startTicks, alive := instances[recent.Row.PID]
		if (alive && startTicks != recent.StartTicks) || now.After(recent.ExpiresAt) {
			delete(a.recent, id)
			continue
		}
		livePaths[recent.SourcePath] = struct{}{}
		if _, exists := observed[recent.Row.NativeID]; !exists {
			observed[recent.Row.NativeID] = codexObservedSession{Row: recent.Row, ParentID: recent.ParentID}
			emitted[recent.Row.PID] = true
		}
	}
	a.scanner.Prune(livePaths)

	rows := buildCodexTree(observed)
	for _, process := range processes {
		if attributed[process.PID] || emitted[process.PID] {
			continue
		}
		rows = append(rows, codexProcessObservation(process))
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

func isCodexProcess(process procscan.Process) bool {
	if codexExecutable(process.Comm) || codexExecutable(process.Exe) {
		return true
	}
	wrapper := strings.ToLower(filepath.Base(process.Exe))
	if wrapper == "." || wrapper == "" {
		wrapper = strings.ToLower(filepath.Base(process.Comm))
	}
	if wrapper != "node" && wrapper != "nodejs" {
		return false
	}
	for _, arg := range process.Args {
		path := filepath.ToSlash(arg)
		base := strings.ToLower(filepath.Base(arg))
		if base == "codex.js" && strings.Contains(path, "/@openai/codex/") {
			return true
		}
	}
	return false
}

func codexExecutable(name string) bool {
	return strings.ToLower(filepath.Base(strings.TrimSpace(name))) == "codex"
}

func (a *CodexAdapter) isRolloutPath(path string) bool {
	rel, err := filepath.Rel(filepath.Join(a.root, "sessions"), path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	name := filepath.Base(path)
	return strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, ".jsonl")
}

func codexSession(process procscan.Process, rollout CodexRolloutSnapshot) Session {
	status := ""
	if rollout.Busy {
		status = "busy"
	} else if rollout.Done {
		status = "idle"
	}
	return Session{
		ID:        GlobalID(AgentCodex, rollout.NativeID),
		NativeID:  rollout.NativeID,
		Agent:     AgentCodex,
		Model:     ModelOrUnknown(rollout.Model),
		Name:      shortCodexID(rollout.NativeID),
		Project:   filepath.Base(rollout.Cwd),
		Cwd:       rollout.Cwd,
		PID:       process.PID,
		Status:    status,
		Mode:      Indeterminate,
		UpdatedAt: rollout.UpdatedAt,
	}
}

func codexProcessObservation(process procscan.Process) Session {
	nativeID := fmt.Sprintf("pid:%d", process.PID)
	return Session{
		ID:       GlobalID(AgentCodex, nativeID),
		NativeID: nativeID,
		Agent:    AgentCodex,
		Model:    "unknown",
		Name:     fmt.Sprintf("PID %d", process.PID),
		Project:  filepath.Base(process.Cwd),
		Cwd:      process.Cwd,
		PID:      process.PID,
		Mode:     Indeterminate,
	}
}

func shortCodexID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func buildCodexTree(observed map[string]codexObservedSession) []Session {
	children := make(map[string][]string)
	var roots []string
	for nativeID, entry := range observed {
		if entry.ParentID != "" && entry.ParentID != nativeID {
			if _, ok := observed[entry.ParentID]; ok {
				children[entry.ParentID] = append(children[entry.ParentID], nativeID)
				continue
			}
		}
		roots = append(roots, nativeID)
	}
	sort.Strings(roots)
	for parentID := range children {
		sort.Strings(children[parentID])
	}

	visited := make(map[string]bool)
	var build func(string, string) Session
	build = func(nativeID, parentGlobalID string) Session {
		visited[nativeID] = true
		row := observed[nativeID].Row
		if parentGlobalID == "" {
			row.ID = GlobalID(AgentCodex, nativeID)
		} else {
			row.ID = parentGlobalID + "/" + nativeID
			row.Model = ""
		}
		for _, childID := range children[nativeID] {
			if !visited[childID] {
				row.Children = append(row.Children, build(childID, row.ID))
			}
		}
		return row
	}

	rows := make([]Session, 0, len(roots))
	for _, nativeID := range roots {
		rows = append(rows, build(nativeID, ""))
	}
	for nativeID := range observed {
		if !visited[nativeID] {
			rows = append(rows, build(nativeID, ""))
		}
	}
	return rows
}
