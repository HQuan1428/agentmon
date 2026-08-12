// internal/collector/transcript.go
package collector

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func EncodeCwd(cwd string) string {
	return strings.ReplaceAll(cwd, "/", "-")
}

func TranscriptPath(root, cwd, sessionID string) string {
	return filepath.Join(root, "projects", EncodeCwd(cwd), sessionID+".jsonl")
}

// transcriptLine is the minimal shape we read from each JSONL line.
type transcriptLine struct {
	Type    string `json:"type"`
	Message struct {
		Role    string `json:"role"`
		Model   string `json:"model"`
		Content []struct {
			Type      string          `json:"type"`
			Name      string          `json:"name"`
			ID        string          `json:"id"`
			ToolUseID string          `json:"tool_use_id"`
			Input     json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

type todoItem struct {
	Status string `json:"status"`
}

func ParseTodos(path string) (done, total int, found bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var line transcriptLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		for _, c := range line.Message.Content {
			if c.Type == "tool_use" && c.Name == "TodoWrite" {
				var in struct {
					Todos []todoItem `json:"todos"`
				}
				if json.Unmarshal(c.Input, &in) != nil {
					continue
				}
				d := 0
				for _, td := range in.Todos {
					if td.Status == "completed" {
						d++
					}
				}
				done, total, found = d, len(in.Todos), true
			}
		}
	}
	return done, total, found
}

func ParseSubagents(path string) []Session {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	type spawn struct {
		name, subtype string
	}
	order := []string{} // tool_use ids in first-seen order
	spawns := map[string]spawn{}
	done := map[string]bool{}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var line transcriptLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		for _, c := range line.Message.Content {
			switch {
			case c.Type == "tool_use" && (c.Name == "Task" || c.Name == "Agent"):
				var in struct {
					Description  string `json:"description"`
					SubagentType string `json:"subagent_type"`
				}
				_ = json.Unmarshal(c.Input, &in)
				if _, seen := spawns[c.ID]; !seen {
					order = append(order, c.ID)
				}
				spawns[c.ID] = spawn{name: in.Description, subtype: in.SubagentType}
			case c.Type == "tool_result" && c.ToolUseID != "":
				done[c.ToolUseID] = true
			}
		}
	}

	var out []Session
	for _, id := range order {
		sp := spawns[id]
		s := Session{
			ID:     id,
			Name:   sp.name,
			Kind:   "sub:" + sp.subtype,
			Mode:   Indeterminate,
			Status: "busy",
		}
		if done[id] {
			s.Status = "idle"
		}
		out = append(out, s)
	}
	return out
}

type scanState struct {
	offset    int64
	haveTodos bool
	model     string
	done      int
	total     int
	order     []string
	spawns    map[string]struct{ name, subtype string }
	doneSub   map[string]bool
}

type Scanner struct {
	states map[string]*scanState
}

func NewScanner() *Scanner { return &Scanner{states: map[string]*scanState{}} }

func (sc *Scanner) Prune(live map[string]struct{}) {
	for path := range sc.states {
		if _, ok := live[path]; !ok {
			delete(sc.states, path)
		}
	}
}

type TranscriptSnapshot struct {
	Done      int
	Total     int
	HaveTodos bool
	Model     string
	Children  []Session
}

func (sc *Scanner) Scan(path string) TranscriptSnapshot {
	st := sc.states[path]
	if st == nil {
		st = &scanState{spawns: map[string]struct{ name, subtype string }{}, doneSub: map[string]bool{}}
		sc.states[path] = st
	}
	f, err := os.Open(path)
	if err != nil {
		return st.snapshot()
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() < st.offset {
		*st = scanState{spawns: map[string]struct{ name, subtype string }{}, doneSub: map[string]bool{}} // rotated
	}
	if _, err := f.Seek(st.offset, io.SeekStart); err != nil {
		return st.snapshot()
	}
	reader := bufio.NewReader(f)
	for {
		raw, err := reader.ReadBytes('\n')
		if len(raw) > 0 && (err == nil) { // only consume complete lines
			st.offset += int64(len(raw))
			st.apply(raw)
		}
		if err != nil {
			break
		}
	}
	return st.snapshot()
}

func (st *scanState) snapshot() TranscriptSnapshot {
	return TranscriptSnapshot{
		Done:      st.done,
		Total:     st.total,
		HaveTodos: st.haveTodos,
		Model:     ModelOrUnknown(st.model),
		Children:  st.buildSubs(),
	}
}

func (st *scanState) apply(raw []byte) {
	var line transcriptLine
	if json.Unmarshal(raw, &line) != nil {
		return
	}
	if model := strings.TrimSpace(line.Message.Model); line.Type == "assistant" && line.Message.Role == "assistant" && model != "" {
		st.model = model
	}
	for _, c := range line.Message.Content {
		switch {
		case c.Type == "tool_use" && c.Name == "TodoWrite":
			var in struct {
				Todos []todoItem `json:"todos"`
			}
			if json.Unmarshal(c.Input, &in) == nil {
				d := 0
				for _, td := range in.Todos {
					if td.Status == "completed" {
						d++
					}
				}
				st.done, st.total, st.haveTodos = d, len(in.Todos), true
			}
		case c.Type == "tool_use" && (c.Name == "Task" || c.Name == "Agent"):
			var in struct {
				Description  string `json:"description"`
				SubagentType string `json:"subagent_type"`
			}
			_ = json.Unmarshal(c.Input, &in)
			if _, seen := st.spawns[c.ID]; !seen {
				st.order = append(st.order, c.ID)
			}
			st.spawns[c.ID] = struct{ name, subtype string }{in.Description, in.SubagentType}
		case c.Type == "tool_result" && c.ToolUseID != "":
			st.doneSub[c.ToolUseID] = true
		}
	}
}

func (st *scanState) buildSubs() []Session {
	var out []Session
	for _, id := range st.order {
		sp := st.spawns[id]
		s := Session{ID: id, Name: sp.name, Kind: "sub:" + sp.subtype, Mode: Indeterminate, Status: "busy"}
		if st.doneSub[id] {
			s.Status = "idle"
		}
		out = append(out, s)
	}
	return out
}
