package collector

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"agentmon/internal/procscan"
)

func TestAiderAdapterAttributesOpenHistoryAndRuntimeModel(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "project")
	input := filepath.Join(cwd, ".aider.input.history")
	chat := filepath.Join(cwd, ".aider.chat.history.md")
	writeFile(t, input, "# now\n+/model sonnet\n\n# now\n+write tests\n\n")
	writeFile(t, chat, "")
	writeFile(t, filepath.Join(home, ".aider.conf.yml"), "model: home-model\n")
	writeFile(t, filepath.Join(cwd, ".aider.conf.yml"), "model: repo-model\n")

	adapter := NewAiderAdapter(home)
	if adapter.Agent() != AgentAider {
		t.Fatalf("agent=%q", adapter.Agent())
	}
	rows, err := adapter.Discover(procscan.Snapshot{UID: 1000, Processes: []procscan.Process{{
		PID: 55, UID: 1000, StartTicks: 10, Comm: "aider", Exe: "/usr/bin/aider", Cwd: cwd,
		Args: []string{"aider", "--model", "argv-model"}, Env: map[string]string{"AIDER_MODEL": "env-model"},
		Files: []procscan.OpenFile{{FD: 7, Path: input}, {FD: 8, Path: chat}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%+v", rows)
	}
	row := rows[0]
	if row.ID != "aider:pid:55" || row.NativeID != "pid:55" || row.Agent != AgentAider || row.Name != "PID 55" || row.Project != "project" || row.Cwd != cwd || row.PID != 55 {
		t.Fatalf("row=%+v", row)
	}
	if row.Model != "sonnet" || row.Mode != Indeterminate || row.Status != "busy" || row.IsDone() || len(row.Children) != 0 {
		t.Fatalf("runtime=%+v", row)
	}
}

func TestAiderAdapterRecognizesExactLaunchShapesAndCurrentUID(t *testing.T) {
	adapter := NewAiderAdapter(t.TempDir())
	rows, err := adapter.Discover(procscan.Snapshot{UID: 1000, Processes: []procscan.Process{
		{PID: 1, UID: 1000, Comm: "aider", Exe: "/usr/local/bin/aider", Cwd: "/work/direct"},
		{PID: 2, UID: 1000, Comm: "python3", Exe: "/usr/bin/python3", Args: []string{"python3", "-m", "aider"}, Cwd: "/work/module"},
		{PID: 3, UID: 1000, Comm: "python3", Exe: "/usr/bin/python3", Args: []string{"/venv/bin/python3", "/venv/bin/aider"}, Cwd: "/work/entry"},
		{PID: 4, UID: 1000, Comm: "python3", Exe: "/usr/bin/python3", Args: []string{"python3", "script.py", "aider"}, Cwd: "/work/unrelated"},
		{PID: 5, UID: 2000, Comm: "aider", Exe: "/usr/bin/aider", Cwd: "/work/foreign"},
		{PID: 6, UID: 1000, Comm: "aider", Exe: "/usr/bin/python3", Args: []string{"python3", "script.py"}, Cwd: "/work/spoofed-comm"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
		if row.Model != "unknown" || row.Status != "" || row.IsDone() {
			t.Fatalf("observation=%+v", row)
		}
	}
	sort.Strings(ids)
	want := []string{"aider:pid:1", "aider:pid:2", "aider:pid:3"}
	if len(ids) != len(want) {
		t.Fatalf("ids=%v", ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids=%v", ids)
		}
	}
}

func TestAiderAdapterFailsClosedForSharedDefaultHistory(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "shared")
	input := filepath.Join(cwd, ".aider.input.history")
	chat := filepath.Join(cwd, ".aider.chat.history.md")
	writeFile(t, input, "# old\n+write code\n\n")
	writeFile(t, chat, "#### write code\n\ndone\n\n")

	rows, err := NewAiderAdapter(home).Discover(procscan.Snapshot{UID: 1000, Processes: []procscan.Process{
		{PID: 10, UID: 1000, StartTicks: 10, Comm: "aider", Exe: "/usr/bin/aider", Args: []string{"aider", "--model=sonnet"}, Cwd: cwd},
		{PID: 11, UID: 1000, StartTicks: 11, Comm: "aider", Exe: "/usr/bin/aider", Args: []string{"aider", "--model", "opus"}, Cwd: cwd},
	}})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	for _, row := range rows {
		if row.Model != "unknown" || row.Status != "" || row.IsDone() {
			t.Fatalf("ambiguous row=%+v", row)
		}
	}
}

func TestAiderAdapterFailsClosedWhenProcessesShareOpenHistory(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "shared")
	input := filepath.Join(cwd, ".aider.input.history")
	chat := filepath.Join(cwd, ".aider.chat.history.md")
	writeFile(t, input, "# old\n+write code\n\n")
	writeFile(t, chat, "#### write code\n\ndone\n\n")
	files := []procscan.OpenFile{{FD: 7, Path: input}, {FD: 8, Path: chat}}

	rows, err := NewAiderAdapter(home).Discover(procscan.Snapshot{UID: 1000, Processes: []procscan.Process{
		{PID: 10, UID: 1000, StartTicks: 10, Comm: "aider", Exe: "/usr/bin/aider", Args: []string{"aider", "--model=sonnet"}, Cwd: cwd, Files: files},
		{PID: 11, UID: 1000, StartTicks: 11, Comm: "aider", Exe: "/usr/bin/aider", Args: []string{"aider", "--model=opus"}, Cwd: cwd, Files: files},
	}})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	for _, row := range rows {
		if row.Model != "unknown" || row.Status != "" || row.IsDone() {
			t.Fatalf("shared open history row=%+v", row)
		}
	}
}

func TestAiderAdapterUsesLaunchModelEvidence(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	configCwd := filepath.Join(root, "config")
	argCwd := filepath.Join(root, "argument")
	envCwd := filepath.Join(root, "environment")
	for _, cwd := range []string{configCwd, argCwd, envCwd} {
		writeFile(t, filepath.Join(cwd, ".aider.input.history"), "")
		writeFile(t, filepath.Join(cwd, ".aider.chat.history.md"), "")
	}
	writeFile(t, filepath.Join(home, ".aider.conf.yml"), "nested:\n  model: ignored\n")
	writeFile(t, filepath.Join(configCwd, ".aider.conf.yml"), "model: repo-model\n")

	rows, err := NewAiderAdapter(home).Discover(procscan.Snapshot{UID: 1000, Processes: []procscan.Process{
		{PID: 12, UID: 1000, StartTicks: 12, Comm: "aider", Exe: "/usr/bin/aider", Cwd: configCwd},
		{PID: 13, UID: 1000, StartTicks: 13, Comm: "aider", Exe: "/usr/bin/aider", Args: []string{"aider", "--model=argv-model"}, Env: map[string]string{"AIDER_MODEL": "env-model"}, Cwd: argCwd},
		{PID: 14, UID: 1000, StartTicks: 14, Comm: "aider", Exe: "/usr/bin/aider", Env: map[string]string{"AIDER_MODEL": "env-model"}, Cwd: envCwd},
	}})
	if err != nil || len(rows) != 3 || rows[0].Model != "repo-model" || rows[1].Model != "argv-model" || rows[2].Model != "env-model" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestAiderAdapterExpiresDoneRowAndRejectsPIDReuse(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "old")
	input := filepath.Join(cwd, ".aider.input.history")
	chat := filepath.Join(cwd, ".aider.chat.history.md")
	writeFile(t, input, "# old\n+write code\n\n")
	writeFile(t, chat, "#### write code\n\ndone\n\n")
	old := time.Unix(1, 0)
	newer := time.Unix(2, 0)
	if err := os.Chtimes(input, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(chat, newer, newer); err != nil {
		t.Fatal(err)
	}

	adapter := NewAiderAdapter(home)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	adapter.now = func() time.Time { return now }
	snapshot := procscan.Snapshot{UID: 1000, Processes: []procscan.Process{{
		PID: 55, UID: 1000, StartTicks: 100, Comm: "aider", Exe: "/usr/bin/aider", Cwd: cwd,
		Files: []procscan.OpenFile{{FD: 7, Path: input}, {FD: 8, Path: chat}},
	}}}

	rows, err := adapter.Discover(snapshot)
	if err != nil || len(rows) != 1 || rows[0].Status != "idle" || !rows[0].IsDone() {
		t.Fatalf("done rows=%+v err=%v", rows, err)
	}
	// A finished Aider turn must render as DONE, not IDLE: mark it a completed unit.
	if rows[0].Mode != Determinate || rows[0].Done != 1 || rows[0].Total != 1 {
		t.Fatalf("done row should be Determinate 1/1, got %+v", rows[0])
	}
	now = now.Add(4 * time.Second)
	rows, err = adapter.Discover(snapshot)
	if err != nil || len(rows) != 1 || !rows[0].IsDone() {
		t.Fatalf("four-second rows=%+v err=%v", rows, err)
	}
	now = now.Add(time.Nanosecond)
	rows, err = adapter.Discover(snapshot)
	if err != nil || len(rows) != 0 {
		t.Fatalf("expired rows=%+v err=%v", rows, err)
	}
	appendFile(t, input, "# new\n+new work\n\n")
	rows, err = adapter.Discover(snapshot)
	if err != nil || len(rows) != 1 || rows[0].Status != "busy" || rows[0].IsDone() {
		t.Fatalf("later turn rows=%+v err=%v", rows, err)
	}

	rows, err = adapter.Discover(procscan.Snapshot{UID: 1000, Processes: []procscan.Process{{
		PID: 55, UID: 1000, StartTicks: 200, Comm: "aider", Exe: "/usr/bin/aider", Cwd: filepath.Join(t.TempDir(), "new"),
	}}})
	if err != nil || len(rows) != 1 || rows[0].Status != "" || rows[0].IsDone() {
		t.Fatalf("reused PID rows=%+v err=%v", rows, err)
	}
}

func TestAiderAdapterClearsDoneStateWhenHistoryDisappears(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(t.TempDir(), "project")
	input := filepath.Join(cwd, ".aider.input.history")
	chat := filepath.Join(cwd, ".aider.chat.history.md")
	writeFile(t, input, "# old\n+/model sonnet\n\n# old\n+write code\n\n")
	writeFile(t, chat, "#### write code\n\ndone\n\n")
	old := time.Unix(1, 0)
	newer := time.Unix(2, 0)
	if err := os.Chtimes(input, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(chat, newer, newer); err != nil {
		t.Fatal(err)
	}

	adapter := NewAiderAdapter(home)
	snapshot := procscan.Snapshot{UID: 1000, Processes: []procscan.Process{{
		PID: 55, UID: 1000, StartTicks: 100, Comm: "aider", Exe: "/usr/bin/aider", Cwd: cwd,
		Files: []procscan.OpenFile{{FD: 7, Path: input}, {FD: 8, Path: chat}},
	}}}
	rows, err := adapter.Discover(snapshot)
	if err != nil || len(rows) != 1 || rows[0].Model != "sonnet" || !rows[0].IsDone() {
		t.Fatalf("initial rows=%+v err=%v", rows, err)
	}
	if err := os.Remove(chat); err != nil {
		t.Fatal(err)
	}
	rows, err = adapter.Discover(snapshot)
	if err != nil || len(rows) != 1 || rows[0].Model != "unknown" || rows[0].Status != "" || rows[0].IsDone() {
		t.Fatalf("missing history rows=%+v err=%v", rows, err)
	}
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}
	writeFile(t, input, "")
	writeFile(t, chat, "")
	rows, err = adapter.Discover(snapshot)
	if err != nil || len(rows) != 1 || rows[0].Model != "unknown" || rows[0].Status != "" || rows[0].IsDone() {
		t.Fatalf("recreated history rows=%+v err=%v", rows, err)
	}
}
