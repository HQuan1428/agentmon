package collector

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
)

type CodexRolloutSnapshot struct {
	NativeID  string
	ParentID  string
	Cwd       string
	Model     string
	Busy      bool
	Done      bool
	UpdatedAt int64
}

type codexRolloutState struct {
	offset      int64
	fileInfo    os.FileInfo
	meta        CodexRolloutSnapshot
	openTurns   map[string]bool
	hasTerminal bool
}

type CodexScanner struct {
	states map[string]*codexRolloutState
}

func NewCodexScanner() *CodexScanner {
	return &CodexScanner{states: map[string]*codexRolloutState{}}
}

func (sc *CodexScanner) Scan(path string) CodexRolloutSnapshot {
	st := sc.states[path]
	if st == nil {
		st = newCodexRolloutState()
		sc.states[path] = st
	}

	f, err := os.Open(path)
	if err != nil {
		return st.snapshot()
	}
	defer f.Close()

	if info, err := f.Stat(); err == nil {
		if info.Size() < st.offset || (st.fileInfo != nil && !os.SameFile(st.fileInfo, info)) {
			*st = *newCodexRolloutState()
		}
		st.fileInfo = info
	}
	if _, err := f.Seek(st.offset, io.SeekStart); err != nil {
		return st.snapshot()
	}

	reader := bufio.NewReader(f)
	for {
		raw, err := reader.ReadBytes('\n')
		if err == nil {
			st.offset += int64(len(raw))
			st.apply(raw)
		}
		if err != nil {
			break
		}
	}

	return st.snapshot()
}

func newCodexRolloutState() *codexRolloutState {
	return &codexRolloutState{openTurns: map[string]bool{}}
}

func (st *codexRolloutState) snapshot() CodexRolloutSnapshot {
	snapshot := st.meta
	snapshot.Busy = len(st.openTurns) > 0
	snapshot.Done = st.hasTerminal && !snapshot.Busy
	return snapshot
}

func (st *codexRolloutState) apply(raw []byte) {
	var line struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &line) != nil || len(line.Payload) == 0 {
		return
	}

	switch line.Type {
	case "session_meta":
		var payload struct {
			ID       *string `json:"id"`
			Cwd      *string `json:"cwd"`
			ParentID *string `json:"parent_thread_id"`
		}
		if json.Unmarshal(line.Payload, &payload) != nil || payload.ID == nil || payload.Cwd == nil || strings.TrimSpace(*payload.ID) == "" || strings.TrimSpace(*payload.Cwd) == "" {
			return
		}
		st.meta.NativeID = *payload.ID
		st.meta.Cwd = *payload.Cwd
		if payload.ParentID == nil {
			st.meta.ParentID = ""
		} else {
			st.meta.ParentID = *payload.ParentID
		}
	case "turn_context":
		var payload struct {
			TurnID *string `json:"turn_id"`
			Model  *string `json:"model"`
		}
		if json.Unmarshal(line.Payload, &payload) != nil || payload.TurnID == nil || payload.Model == nil || strings.TrimSpace(*payload.TurnID) == "" || strings.TrimSpace(*payload.Model) == "" {
			return
		}
		st.meta.Model = *payload.Model
	case "event_msg":
		var payload struct {
			Type   *string `json:"type"`
			TurnID *string `json:"turn_id"`
		}
		if json.Unmarshal(line.Payload, &payload) != nil || payload.Type == nil || payload.TurnID == nil || strings.TrimSpace(*payload.TurnID) == "" {
			return
		}
		switch *payload.Type {
		case "task_started":
			st.openTurns[*payload.TurnID] = true
		case "task_complete", "task_aborted":
			if !st.openTurns[*payload.TurnID] {
				return
			}
			delete(st.openTurns, *payload.TurnID)
			st.hasTerminal = true
		}
	}
}
