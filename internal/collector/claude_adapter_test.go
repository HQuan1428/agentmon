package collector

import (
	"path/filepath"
	"testing"

	"agentmon/internal/procscan"
)

func TestClaudeAdapterDiscoversNormalizedSession(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/u/proj"
	writeFile(t, filepath.Join(root, "sessions", "1.json"),
		`{"pid":77,"sessionId":"sess1","cwd":"`+cwd+`","kind":"interactive","name":"work","status":"busy","updatedAt":5,"jobId":""}`)
	writeFile(t, TranscriptPath(root, cwd, "sess1"), modelLine("claude-opus-4-6")+"\n"+taskLine("tool-B", "Review", "general-purpose")+"\n")

	got, err := NewClaudeAdapter(root).Discover(procscan.Snapshot{
		UID:       1000,
		Processes: []procscan.Process{{PID: 77, UID: 1000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("sessions=%+v", got)
	}
	s := got[0]
	if s.ID != "claude:sess1" || s.NativeID != "sess1" || s.Agent != AgentClaude || s.Model != "claude-opus-4-6" {
		t.Fatalf("identity=%+v", s)
	}
	if len(s.Children) != 1 {
		t.Fatalf("children=%+v", s.Children)
	}
	if s.Children[0].ID != "claude:sess1/tool-B" || s.Children[0].Agent != AgentClaude || s.Children[0].Model != "" {
		t.Fatalf("child=%+v", s.Children[0])
	}
}

func TestClaudeAdapterUsesUnknownForMissingModel(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/u/proj"
	writeFile(t, filepath.Join(root, "sessions", "1.json"),
		`{"pid":77,"sessionId":"sess1","cwd":"`+cwd+`","kind":"interactive","name":"work","status":"busy","updatedAt":5,"jobId":""}`)
	writeFile(t, TranscriptPath(root, cwd, "sess1"), todoLine(`[]`)+"\n")

	got, err := NewClaudeAdapter(root).Discover(procscan.Snapshot{Processes: []procscan.Process{{PID: 77}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Model != "unknown" {
		t.Fatalf("sessions=%+v", got)
	}
}

func TestClaudeAdapterOmitsDeadPID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sessions", "1.json"),
		`{"pid":88,"sessionId":"dead","cwd":"/home/u/proj","kind":"interactive","name":"work","status":"busy","updatedAt":5,"jobId":""}`)

	got, err := NewClaudeAdapter(root).Discover(procscan.Snapshot{Processes: []procscan.Process{{PID: 77}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("sessions=%+v", got)
	}
}

func TestClaudeAdapterPrunesTranscriptStateAfterSessionExit(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/u/proj"
	writeFile(t, filepath.Join(root, "sessions", "1.json"),
		`{"pid":77,"sessionId":"sess1","cwd":"`+cwd+`","kind":"interactive","name":"work","status":"busy","updatedAt":5}`)
	path := TranscriptPath(root, cwd, "sess1")
	writeFile(t, path, modelLine("claude-opus-4-6")+"\n")
	adapter := NewClaudeAdapter(root)

	if _, err := adapter.Discover(procscan.Snapshot{Processes: []procscan.Process{{PID: 77}}}); err != nil {
		t.Fatal(err)
	}
	if len(adapter.scanner.states) != 1 {
		t.Fatalf("scanner states after live session=%d", len(adapter.scanner.states))
	}
	if _, err := adapter.Discover(procscan.Snapshot{}); err != nil {
		t.Fatal(err)
	}
	if len(adapter.scanner.states) != 0 {
		t.Fatalf("scanner retained exited transcript %q", path)
	}
}

func TestClaudeAdapterPreservesBlockedBackgroundState(t *testing.T) {
	root := t.TempDir()
	cwd := "/home/u/proj"
	writeFile(t, filepath.Join(root, "sessions", "1.json"),
		`{"pid":77,"sessionId":"sess1","cwd":"`+cwd+`","kind":"bg","name":"work","status":"busy","updatedAt":5,"jobId":"job1"}`)
	writeFile(t, TranscriptPath(root, cwd, "sess1"), modelLine("claude-sonnet-4-5")+"\n")
	writeFile(t, filepath.Join(root, "jobs", "job1", "state.json"),
		`{"state":"blocked","detail":"pipeline 2/3 tasks done","needs":"approve"}`)

	got, err := NewClaudeAdapter(root).Discover(procscan.Snapshot{Processes: []procscan.Process{{PID: 77}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("sessions=%+v", got)
	}
	s := got[0]
	if !s.Blocked || s.JobState != "blocked" || s.NeedsHint != "approve" || s.Done != 2 || s.Total != 3 || s.Mode != Determinate || s.Model != "claude-sonnet-4-5" {
		t.Fatalf("session=%+v", s)
	}
}
