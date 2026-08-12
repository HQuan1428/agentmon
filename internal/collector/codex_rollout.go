package collector

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

const codexRolloutAnchorSize = 4096

type CodexRolloutSnapshot struct {
	NativeID  string
	ParentID  string
	Cwd       string
	Source    string
	Model     string
	Busy      bool
	Done      bool
	UpdatedAt int64
}

type codexRolloutState struct {
	offset      int64
	fileInfo    os.FileInfo
	anchor      []byte
	tailAnchor  []byte
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
		if st.mustReset(f, info) {
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
			st.addAnchor(raw)
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

func (st *codexRolloutState) mustReset(f *os.File, info os.FileInfo) bool {
	if info.Size() < st.offset || (st.fileInfo != nil && !os.SameFile(st.fileInfo, info)) {
		return true
	}
	if len(st.anchor) == 0 {
		return false
	}
	return !matchesCodexAnchor(f, 0, st.anchor) ||
		!matchesCodexAnchor(f, st.offset-int64(len(st.tailAnchor)), st.tailAnchor)
}

func (st *codexRolloutState) addAnchor(raw []byte) {
	remaining := codexRolloutAnchorSize - len(st.anchor)
	if remaining > 0 {
		prefix := raw
		if len(prefix) > remaining {
			prefix = prefix[:remaining]
		}
		st.anchor = append(st.anchor, prefix...)
	}

	if len(raw) >= codexRolloutAnchorSize {
		st.tailAnchor = append(st.tailAnchor[:0], raw[len(raw)-codexRolloutAnchorSize:]...)
		return
	}
	if len(st.tailAnchor)+len(raw) > codexRolloutAnchorSize {
		st.tailAnchor = append(st.tailAnchor[len(st.tailAnchor)+len(raw)-codexRolloutAnchorSize:], raw...)
		return
	}
	st.tailAnchor = append(st.tailAnchor, raw...)
}

func matchesCodexAnchor(f *os.File, offset int64, anchor []byte) bool {
	current := make([]byte, len(anchor))
	if _, err := f.ReadAt(current, offset); err != nil {
		return false
	}
	return bytes.Equal(anchor, current)
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
			ID        *string         `json:"id"`
			Cwd       *string         `json:"cwd"`
			ParentID  *string         `json:"parent_thread_id"`
			Source    json.RawMessage `json:"source"`
			Timestamp *string         `json:"timestamp"`
		}
		if json.Unmarshal(line.Payload, &payload) != nil || payload.ID == nil || payload.Cwd == nil || strings.TrimSpace(*payload.ID) == "" || strings.TrimSpace(*payload.Cwd) == "" {
			return
		}
		source, updatedAt := st.meta.Source, st.meta.UpdatedAt
		if len(payload.Source) > 0 {
			var ok bool
			source, ok = codexSource(payload.Source)
			if !ok {
				return
			}
		}
		if payload.Timestamp != nil {
			var ok bool
			updatedAt, ok = codexUpdatedAt(*payload.Timestamp)
			if !ok {
				return
			}
		}
		st.meta.NativeID = *payload.ID
		st.meta.Cwd = *payload.Cwd
		st.meta.Source = source
		st.meta.UpdatedAt = updatedAt
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

func codexSource(raw json.RawMessage) (string, bool) {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return name, strings.TrimSpace(name) != ""
	}

	var source map[string]json.RawMessage
	if json.Unmarshal(raw, &source) != nil || len(source) != 1 {
		return "", false
	}
	for name := range source {
		return name, strings.TrimSpace(name) != ""
	}
	return "", false
}

func codexUpdatedAt(timestamp string) (int64, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return 0, false
	}
	return parsed.UnixMilli(), true
}
