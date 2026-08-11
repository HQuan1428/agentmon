// internal/collector/transcript.go
package collector

import (
	"bufio"
	"encoding/json"
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
	Message struct {
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
	order := []string{}         // tool_use ids in first-seen order
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
			ID:      id,
			Name:    sp.name,
			Kind:    "sub:" + sp.subtype,
			Mode:    Indeterminate,
			Status:  "busy",
		}
		if done[id] {
			s.Status = "idle"
		}
		out = append(out, s)
	}
	return out
}
