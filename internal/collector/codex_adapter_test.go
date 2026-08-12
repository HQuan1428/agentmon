package collector

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentmon/internal/procscan"
)

func TestCodexAdapterBuildsSessionAndSubagentTree(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".codex")
	parentPath := filepath.Join(root, "sessions", "2026", "08", "12", "rollout-parent.jsonl")
	childPath := filepath.Join(root, "sessions", "2026", "08", "12", "rollout-child.jsonl")
	writeFile(t, parentPath, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"parent","cwd":"/work/project","source":"cli"}}`,
		`{"type":"turn_context","payload":{"turn_id":"turn-parent","model":"gpt-5.6-sol"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-parent"}}`,
	}, "\n")+"\n")
	writeFile(t, childPath, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"child","cwd":"/work/project","source":{"subagent":{}},"parent_thread_id":"parent"}}`,
		`{"type":"turn_context","payload":{"turn_id":"turn-child","model":"gpt-5.6-mini"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-child"}}`,
	}, "\n")+"\n")

	rows, err := NewCodexAdapter(root).Discover(procscan.Snapshot{Processes: []procscan.Process{
		{PID: 42, UID: 1000, Comm: "codex", Cwd: "/work/project", Files: []procscan.OpenFile{{FD: 7, Path: parentPath}}},
		{PID: 43, UID: 1000, Exe: "/usr/bin/codex", Cwd: "/work/project", Files: []procscan.OpenFile{{FD: 8, Path: childPath}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("sessions=%+v", rows)
	}
	parent := rows[0]
	if parent.ID != "codex:parent" || parent.NativeID != "parent" || parent.Agent != AgentCodex || parent.Model != "gpt-5.6-sol" || parent.Project != "project" || parent.Status != "busy" || parent.Mode != Indeterminate {
		t.Fatalf("parent=%+v", parent)
	}
	if len(parent.Children) != 1 {
		t.Fatalf("children=%+v", parent.Children)
	}
	child := parent.Children[0]
	if child.ID != "codex:parent/child" || child.NativeID != "child" || child.Agent != AgentCodex || child.Model != "" || child.Status != "busy" {
		t.Fatalf("child=%+v", child)
	}
}

func TestCodexAdapterDoesNotGuessUnmatchedRollout(t *testing.T) {
	a := NewCodexAdapter(filepath.Join(t.TempDir(), ".codex"))
	rows, err := a.Discover(procscan.Snapshot{Processes: []procscan.Process{{
		PID: 42, UID: 1000, Comm: "codex", Exe: "/usr/bin/codex", Cwd: "/work/p",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "codex:pid:42" || rows[0].Model != "unknown" || rows[0].Status != "" || rows[0].IsDone() {
		t.Fatalf("observation=%+v", rows)
	}
}

func TestCodexAdapterRecognizesNodeWrapperOnly(t *testing.T) {
	rows, err := NewCodexAdapter(filepath.Join(t.TempDir(), ".codex")).Discover(procscan.Snapshot{Processes: []procscan.Process{
		{PID: 42, Comm: "node", Exe: "/usr/bin/node", Args: []string{"node", "/opt/node_modules/@openai/codex/bin/codex.js"}, Cwd: "/work/p"},
		{PID: 43, Comm: "node", Exe: "/usr/bin/node", Args: []string{"node", "/opt/node_modules/other/bin/other.js"}, Cwd: "/work/q"},
		{PID: 44, Comm: "node", Exe: "/usr/bin/node", Args: []string{"node", "/opt/unrelated/codex"}, Cwd: "/work/r"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "codex:pid:42" {
		t.Fatalf("sessions=%+v", rows)
	}
}

func TestCodexAdapterRejectsRolloutOutsideSessionTree(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, ".codex")
	outside := filepath.Join(base, "sessions-backup", "rollout-outside.jsonl")
	wrongName := filepath.Join(root, "sessions", "2026", "session.jsonl")
	writeFile(t, outside, `{"type":"session_meta","payload":{"id":"outside","cwd":"/work/p"}}`+"\n")
	writeFile(t, wrongName, `{"type":"session_meta","payload":{"id":"wrong-name","cwd":"/work/p"}}`+"\n")

	rows, err := NewCodexAdapter(root).Discover(procscan.Snapshot{Processes: []procscan.Process{{
		PID: 42, Comm: "codex", Cwd: "/work/p",
		Files: []procscan.OpenFile{{FD: 7, Path: outside}, {FD: 8, Path: wrongName}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "codex:pid:42" {
		t.Fatalf("sessions=%+v", rows)
	}
}

func TestCodexAdapterRetainsCompletedSessionForFourSeconds(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".codex")
	path := filepath.Join(root, "sessions", "2026", "08", "12", "rollout-done.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"done-session","cwd":"/work/project"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
	}, "\n")+"\n")
	a := NewCodexAdapter(root)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	withFile := procscan.Snapshot{Processes: []procscan.Process{{
		PID: 42, Comm: "codex", Cwd: "/work/project", Files: []procscan.OpenFile{{FD: 7, Path: path}},
	}}}
	withoutFile := procscan.Snapshot{Processes: []procscan.Process{{PID: 42, Comm: "codex", Cwd: "/work/project"}}}

	rows, err := a.Discover(withFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "codex:done-session" || rows[0].Status != "idle" || !rows[0].IsDone() {
		t.Fatalf("completed=%+v", rows)
	}
	now = now.Add(4 * time.Second)
	rows, err = a.Discover(withoutFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "codex:done-session" || !rows[0].IsDone() {
		t.Fatalf("at four seconds=%+v", rows)
	}
	now = now.Add(time.Nanosecond)
	rows, err = a.Discover(withoutFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "codex:pid:42" || rows[0].IsDone() {
		t.Fatalf("after expiry=%+v", rows)
	}
}

func TestCodexAdapterPrunesRolloutStateAfterExitGrace(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".codex")
	path := filepath.Join(root, "sessions", "2026", "08", "12", "rollout-done.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"done-session","cwd":"/work/project"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
	}, "\n")+"\n")
	adapter := NewCodexAdapter(root)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	adapter.now = func() time.Time { return now }
	live := procscan.Snapshot{Processes: []procscan.Process{{
		PID: 42, StartTicks: 100, Comm: "codex", Cwd: "/work/project",
		Files: []procscan.OpenFile{{FD: 7, Path: path}},
	}}}

	if rows, err := adapter.Discover(live); err != nil || len(rows) != 1 || !rows[0].IsDone() {
		t.Fatalf("completed rows=%+v err=%v", rows, err)
	}
	if len(adapter.scanner.states) != 1 {
		t.Fatalf("scanner states after completion=%d", len(adapter.scanner.states))
	}
	now = now.Add(4 * time.Second)
	if rows, err := adapter.Discover(procscan.Snapshot{}); err != nil || len(rows) != 1 || !rows[0].IsDone() {
		t.Fatalf("exit grace rows=%+v err=%v", rows, err)
	}
	if len(adapter.scanner.states) != 1 {
		t.Fatalf("scanner pruned before grace elapsed: %d", len(adapter.scanner.states))
	}
	now = now.Add(time.Nanosecond)
	if rows, err := adapter.Discover(procscan.Snapshot{}); err != nil || len(rows) != 0 {
		t.Fatalf("after exit grace rows=%+v err=%v", rows, err)
	}
	if len(adapter.scanner.states) != 0 {
		t.Fatalf("scanner retained expired rollout %q", path)
	}
}

func TestCodexAdapterPrunesCompletedSessionWhenPIDIsReused(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".codex")
	path := filepath.Join(root, "sessions", "2026", "08", "12", "rollout-old.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"old-session","cwd":"/work/old"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
	}, "\n")+"\n")
	a := NewCodexAdapter(root)

	rows, err := a.Discover(procscan.Snapshot{Processes: []procscan.Process{{
		PID: 42, StartTicks: 100, Comm: "codex", Cwd: "/work/old",
		Files: []procscan.OpenFile{{FD: 7, Path: path}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "codex:old-session" {
		t.Fatalf("completed=%+v", rows)
	}

	rows, err = a.Discover(procscan.Snapshot{Processes: []procscan.Process{{
		PID: 42, StartTicks: 200, Comm: "codex", Cwd: "/work/new",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "codex:pid:42" || rows[0].Cwd != "/work/new" || rows[0].IsDone() {
		t.Fatalf("reused PID observation=%+v", rows)
	}
}
