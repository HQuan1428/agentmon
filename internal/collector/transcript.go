// internal/collector/transcript.go
package collector

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// subStaleTTL reaps a background subagent whose agent-<id>.jsonl stopped growing
// without a clean end_turn (crashed or abandoned-awaiting-continuation), so it
// eventually shows DONE and fades instead of pinning the tree forever.
// ponytail: mtime-idle heuristic; a long TTL avoids false-positives on think pauses.
const subStaleTTL = 10 * time.Minute

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
		Role       string `json:"role"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type      string          `json:"type"`
			Name      string          `json:"name"`
			ID        string          `json:"id"`
			ToolUseID string          `json:"tool_use_id"`
			Input     json.RawMessage `json:"input"`
			Content   json.RawMessage `json:"content"` // tool_result payload (string or [{text}])
		} `json:"content"`
	} `json:"message"`
}

// launchAckRe pulls the agentId out of an async-agent launch acknowledgement.
// Current Claude Code dispatches Task/Agent in the background: the inline
// tool_result returns "Async agent launched successfully … agentId: <hex>"
// within milliseconds of the spawn — it is NOT completion. The real subagent
// runs on in projects/<enc>/<sessionID>/subagents/agent-<agentId>.jsonl.
var launchAckRe = regexp.MustCompile(`agentId:\s*([0-9a-fA-F]+)`)

// launchAckAgentID returns the async agentId when a tool_result is a background
// launch ack, or "" for a plain (synchronous) result.
func launchAckAgentID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	text := resultText(raw)
	if !strings.Contains(text, "Async agent launched") {
		return ""
	}
	if m := launchAckRe.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// resultText flattens a tool_result payload (a bare string or a list of
// {type,text} blocks) into one string.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, blk := range blocks {
			b.WriteString(blk.Text)
		}
		return b.String()
	}
	return ""
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
	agentIDs  map[string]string // toolUseID -> async agentId (background subagents)
}

func newScanState() *scanState {
	return &scanState{
		spawns:   map[string]struct{ name, subtype string }{},
		doneSub:  map[string]bool{},
		agentIDs: map[string]string{},
	}
}

type Scanner struct {
	states map[string]*scanState
	now    func() time.Time
}

func NewScanner() *Scanner {
	return &Scanner{states: map[string]*scanState{}, now: time.Now}
}

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
		st = newScanState()
		sc.states[path] = st
	}
	f, err := os.Open(path)
	if err != nil {
		return st.snapshot()
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() < st.offset {
		*st = *newScanState() // rotated
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
	snap := st.snapshot()
	sc.resolveSubs(path, &snap)
	return snap
}

// resolveSubs decides the live state of each background subagent from its own
// agent-<id>.jsonl (busy while it grows, DONE at end_turn or once stale). The
// inline launch-ack tool_result only means "dispatched", never "finished".
func (sc *Scanner) resolveSubs(path string, snap *TranscriptSnapshot) {
	dir := strings.TrimSuffix(path, ".jsonl")
	now := sc.now()
	for i := range snap.Children {
		c := &snap.Children[i]
		if c.agentID == "" {
			continue // synchronous/legacy child: already resolved in buildSubs
		}
		file := filepath.Join(dir, "subagents", "agent-"+c.agentID+".jsonl")
		if subagentDone(file, now) {
			markSubDone(c)
		}
	}
}

// subagentDone reports whether a background subagent's transcript shows it has
// finished: its last assistant turn ended with stop_reason "end_turn", or the
// file has gone stale (no writes for subStaleTTL). A missing/not-yet-created
// file counts as still running.
func subagentDone(file string, now time.Time) bool {
	f, err := os.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && now.Sub(info.ModTime()) >= subStaleTTL {
		return true
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	lastStop := ""
	for sc.Scan() {
		var line transcriptLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.Message.Role == "assistant" && line.Message.StopReason != "" {
			lastStop = line.Message.StopReason
		}
	}
	return lastStop == "end_turn"
}

// markSubDone renders a subagent as DONE: a full determinate bar so StateOf
// returns StateDone (not the idle pulse) and the model fades it after grace.
func markSubDone(s *Session) {
	s.Status = "idle"
	s.Mode = Determinate
	s.Done, s.Total = 1, 1
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
			if aid := launchAckAgentID(c.Content); aid != "" {
				st.agentIDs[c.ToolUseID] = aid // background async: dispatched, not finished
			} else {
				st.doneSub[c.ToolUseID] = true // synchronous result = completion
			}
		}
	}
}

func (st *scanState) buildSubs() []Session {
	var out []Session
	for _, id := range st.order {
		sp := st.spawns[id]
		s := Session{ID: id, Name: sp.name, Kind: "sub:" + sp.subtype, Mode: Indeterminate, Status: "busy"}
		s.agentID = st.agentIDs[id]
		if st.doneSub[id] {
			markSubDone(&s) // synchronous/legacy Task: inline result is the completion
		}
		out = append(out, s)
	}
	return out
}
