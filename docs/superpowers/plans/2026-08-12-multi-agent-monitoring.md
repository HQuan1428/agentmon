# Multi-Agent Monitoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `agentmon` into a zero-configuration, passive, read-only Linux/WSL monitor for Claude Code, Codex, OpenCode, and Aider, rendered as project → agent → session/model → subagent.

**Architecture:** Capture one current-UID Linux process snapshot per poll, pass it to isolated agent adapters, and normalize their output into the existing session model. The coordinator merges partial successes while preserving per-adapter tail state; Bubble Tea and the sound edge detector consume only normalized sessions. Render a dedicated model column for sessions and a responsive continuation row on narrow terminals.

**Tech Stack:** Go 1.26, Bubble Tea v1.3.10, Lip Gloss v1.1.0, PulseAudio through `github.com/jfreymuth/pulse`, pure-Go SQLite through `modernc.org/sqlite` v1.47.0, Linux `/proc`, JSON/JSONL, HTTP GET over loopback.

## Global Constraints

- Follow `workflow.md`: RED → GREEN → REFACTOR for every behavior change, followed by intent review and code-quality review.
- Run every shell command through `rtk`.
- Initial platform is Linux/WSL. Do not add native macOS or Windows backends in this plan.
- Monitor only processes whose effective UID equals the effective UID of `agentmon`.
- Remain passive and read-only: never modify agent state, install hooks, or change agent configuration.
- Never read OpenCode `auth.json`, arbitrary `.env` values, API keys, tokens, prompts, responses, commands, or source code into diagnostics or UI output.
- A session model is an exact runtime-evidenced value or the literal `unknown`; never guess.
- Subagent rows never display a model.
- Session IDs and child IDs are namespaced by agent.
- A completed turn emits one DONE edge, fades for three seconds, and disappears; later work with the same ID reappears.
- Keep Claude background `BLOCKED` and `needs:` behavior, but do not restore blocked counts to the dashboard header.
- Poll remains one second by default; the 100 ms animation tick performs no I/O.
- Preserve the existing uncommitted header regression test and implementation in `internal/render/dashboard_test.go` and `internal/render/dashboard.go`.

## Locked File Structure

```text
internal/procscan/
  snapshot.go              # process metadata contract and test fake
  procfs_linux.go          # current-UID /proc reader, fd and listener attribution
  procfs_linux_test.go

internal/collector/
  types.go                 # normalized Agent and Session types
  coordinator.go           # Adapter contract, merge, sorting, failure isolation
  coordinator_test.go
  claude_adapter.go        # Claude registry/transcript/job orchestration
  claude_adapter_test.go
  codex_rollout.go         # incremental Codex JSONL reducer
  codex_rollout_test.go
  codex_adapter.go         # Codex process/rollout attribution and child tree
  codex_adapter_test.go
  opencode_store.go        # read-only SQLite records and normalized queries
  opencode_store_test.go
  opencode_adapter.go      # process/store/status-probe orchestration
  opencode_adapter_test.go
  aider_history.go         # Aider input/chat history and model evidence reducer
  aider_history_test.go
  aider_adapter.go         # Aider process attribution
  aider_adapter_test.go
  defaults.go              # production adapter assembly

internal/model/model.go    # consume a normalized collection source
internal/model/model_test.go
internal/model/events_test.go
internal/render/view.go    # four-level hierarchy and model column
internal/render/view_test.go
internal/render/dashboard.go
internal/render/dashboard_test.go
main.go                    # home + proc source + default coordinator wiring
go.mod
go.sum
```

Existing focused Claude parser files (`sessions.go`, `transcript.go`, `jobs.go`) remain in place and are reused by `claude_adapter.go`. Delete the old orchestration in `collector.go` only when Task 12 moves all production call sites to `Coordinator`.

---

### Task 1: Normalize agent and model identity

**Files:**
- Modify: `internal/collector/types.go`
- Modify: `internal/collector/types_test.go`
- Modify: `internal/model/events_test.go`

**Interfaces:**
- Produces: `type Agent string`; constants `AgentClaude`, `AgentCodex`, `AgentOpenCode`, `AgentAider`; `func (Agent) Rank() int`; `func GlobalID(Agent, string) string`; `func ModelOrUnknown(string) string`.
- Produces: `Session.NativeID`, `Session.Agent`, and `Session.Model`.

- [ ] **Step 1: Add failing normalized-identity tests**

```go
func TestAgentIdentityHelpers(t *testing.T) {
	if got := GlobalID(AgentOpenCode, "ses_1"); got != "opencode:ses_1" {
		t.Fatalf("GlobalID=%q", got)
	}
	if got := ModelOrUnknown("  "); got != "unknown" {
		t.Fatalf("empty model=%q", got)
	}
	if got := ModelOrUnknown("gpt-5.6-sol"); got != "gpt-5.6-sol" {
		t.Fatalf("exact model changed: %q", got)
	}
	if !(AgentClaude.Rank() < AgentCodex.Rank() && AgentCodex.Rank() < AgentOpenCode.Rank() && AgentOpenCode.Rank() < AgentAider.Rank()) {
		t.Fatal("agent display order is unstable")
	}
}
```

Add `TestDiffEventsNamespacedIDsDoNotCollide` to `events_test.go` with `codex:same` completing while `claude:same` stays busy; assert exactly one event for `codex:same`.

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `rtk go test ./internal/collector ./internal/model -run 'TestAgentIdentityHelpers|TestDiffEventsNamespacedIDsDoNotCollide' -count=1`

Expected: FAIL because the agent types, helpers, and session fields do not exist.

- [ ] **Step 3: Add the normalized identity types**

```go
type Agent string

const (
	AgentClaude   Agent = "Claude"
	AgentCodex    Agent = "Codex"
	AgentOpenCode Agent = "OpenCode"
	AgentAider    Agent = "Aider"
)

func (a Agent) Rank() int {
	switch a {
	case AgentClaude:
		return 0
	case AgentCodex:
		return 1
	case AgentOpenCode:
		return 2
	case AgentAider:
		return 3
	default:
		return 99
	}
}

func GlobalID(agent Agent, native string) string {
	return strings.ToLower(string(agent)) + ":" + native
}

func ModelOrUnknown(model string) string {
	if strings.TrimSpace(model) == "" {
		return "unknown"
	}
	return model
}
```

Add `NativeID string`, `Agent Agent`, and `Model string` to `Session` without changing `IsDone` or `Fraction`.

- [ ] **Step 4: Run focused and package tests**

Run: `rtk go test ./internal/collector ./internal/model -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

```bash
rtk git add internal/collector/types.go internal/collector/types_test.go internal/model/events_test.go
rtk git commit -m "refactor: normalize agent session identity"
```

---

### Task 2: Capture current-user Linux process metadata

**Files:**
- Create: `internal/procscan/snapshot.go`
- Create: `internal/procscan/procfs_linux.go`
- Create: `internal/procscan/procfs_linux_test.go`

**Interfaces:**
- Produces: `Process`, `OpenFile`, `Listener`, `Snapshot`, `Source`, `ProcFS`, and `NewProcFS()` shown below.
- Whitelisted process environment keys: `XDG_DATA_HOME`, `AIDER_MODEL` only.

- [ ] **Step 1: Write fixture-backed process metadata tests**

```go
func TestProcFSSnapshotFiltersUIDAndSecrets(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, 101, 1000, "codex", []string{"codex"}, "/work/a",
		[]string{"XDG_DATA_HOME=/data/u", "AIDER_MODEL=sonnet", "OPENAI_API_KEY=secret"})
	writeProc(t, root, 202, 2000, "aider", []string{"python", "-m", "aider"}, "/work/b", nil)

	snap, err := (&ProcFS{Root: root, UID: 1000}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Processes) != 1 || snap.Processes[0].PID != 101 {
		t.Fatalf("processes=%+v", snap.Processes)
	}
	if snap.Processes[0].Env["XDG_DATA_HOME"] != "/data/u" || snap.Processes[0].Env["AIDER_MODEL"] != "sonnet" {
		t.Fatalf("whitelist missing: %#v", snap.Processes[0].Env)
	}
	if _, leaked := snap.Processes[0].Env["OPENAI_API_KEY"]; leaked {
		t.Fatal("secret environment value retained")
	}
}

func writeProc(t *testing.T, root string, pid int, uid uint32, comm string, args []string, cwd string, env []string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o755); err != nil { t.Fatal(err) }
	write := func(name, value string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0o644); err != nil { t.Fatal(err) }
	}
	write("status", fmt.Sprintf("Name:\t%s\nUid:\t%d\t%d\t%d\t%d\n", comm, uid, uid, uid, uid))
	statFields := []string{"S", "1"}
	for len(statFields) < 19 { statFields = append(statFields, "0") }
	statFields = append(statFields, "123")
	write("stat", fmt.Sprintf("%d (%s) %s\n", pid, comm, strings.Join(statFields, " ")))
	write("comm", comm+"\n")
	write("cmdline", strings.Join(args, "\x00")+"\x00")
	write("environ", strings.Join(env, "\x00")+"\x00")
	if err := os.Symlink(cwd, filepath.Join(dir, "cwd")); err != nil { t.Fatal(err) }
	if err := os.Symlink(filepath.Join("/usr/bin", comm), filepath.Join(dir, "exe")); err != nil { t.Fatal(err) }
}
```

The fixture helper creates `<root>/<pid>/status`, `stat`, `comm`, `cmdline`, `environ`, and symlinks for `cwd` and `exe`. Add a malformed-process case and assert it is skipped without losing valid processes.

- [ ] **Step 2: Run the process tests and verify RED**

Run: `rtk go test ./internal/procscan -run TestProcFSSnapshot -count=1`

Expected: FAIL because `internal/procscan` does not exist.

- [ ] **Step 3: Add the process snapshot contract**

```go
type OpenFile struct {
	FD   int
	Path string
}

type Listener struct {
	Network string
	Address string
	Port    int
}

type Process struct {
	PID        int
	PPID       int
	UID        uint32
	StartTicks uint64
	Comm       string
	Exe        string
	Cwd        string
	Args       []string
	Env        map[string]string
	Files      []OpenFile
	Listeners  []Listener
}

type Snapshot struct {
	UID       uint32
	Processes []Process
}

func (s Snapshot) HasPID(pid int) bool {
	for _, p := range s.Processes {
		if p.PID == pid {
			return true
		}
	}
	return false
}

type Source interface {
	Snapshot() (Snapshot, error)
}

type ProcFS struct {
	Root string
	UID  uint32
}

func NewProcFS() *ProcFS {
	return &ProcFS{Root: "/proc", UID: uint32(os.Geteuid())}
}
```

Implement `Snapshot()` by iterating numeric directories, parsing effective UID from the second value on `Uid:` in `status`, PPID and start ticks from `stat`, NUL-delimited `cmdline`/`environ`, and symlink targets for `cwd`/`exe`. Retain only the two named environment keys.

- [ ] **Step 4: Run process tests and verify GREEN**

Run: `rtk go test ./internal/procscan -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 2**

```bash
rtk git add internal/procscan
rtk git commit -m "feat: snapshot current-user Linux processes"
```

---

### Task 3: Attribute open files and loopback listeners to processes

**Files:**
- Modify: `internal/procscan/procfs_linux.go`
- Modify: `internal/procscan/procfs_linux_test.go`

**Interfaces:**
- Extends: `Process.Files []OpenFile` from `/proc/<pid>/fd`.
- Extends: `Process.Listeners []Listener` by matching socket inode symlinks to `/proc/net/tcp` and `/proc/net/tcp6` LISTEN rows.

- [ ] **Step 1: Add failing fd and listener attribution tests**

Create `fd/7 -> /home/u/.codex/sessions/2026/08/12/rollout-a.jsonl`, `fd/8 -> socket:[34567]`, and a fixture `net/tcp` LISTEN row whose inode is `34567` and local address is `0100007F:1000`. Assert:

```go
p := snap.Processes[0]
if p.Files[0].Path != "/home/u/.codex/sessions/2026/08/12/rollout-a.jsonl" {
	t.Fatalf("files=%+v", p.Files)
}
if len(p.Listeners) != 1 || p.Listeners[0].Address != "127.0.0.1" || p.Listeners[0].Port != 4096 {
	t.Fatalf("listeners=%+v", p.Listeners)
}
```

Add a non-loopback listener row and assert it is not retained.

- [ ] **Step 2: Run and verify RED**

Run: `rtk go test ./internal/procscan -run 'TestProcFSOpenFiles|TestProcFSLoopbackListeners' -count=1`

Expected: FAIL with empty `Files` and `Listeners`.

- [ ] **Step 3: Implement safe fd and socket parsing**

```go
func readFDs(procDir string) (files []OpenFile, socketInodes map[string]bool) {
	socketInodes = map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(procDir, "fd"))
	if err != nil {
		return nil, socketInodes
	}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(procDir, "fd", entry.Name()))
		if err != nil {
			continue
		}
		if strings.HasPrefix(target, "socket:[") {
			socketInodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = true
			continue
		}
		fd, err := strconv.Atoi(entry.Name())
		if err == nil && filepath.IsAbs(target) {
			files = append(files, OpenFile{FD: fd, Path: target})
		}
	}
	return files, socketInodes
}
```

Parse TCP state `0A` only, decode IPv4/IPv6 loopback addresses and hex ports, match inode membership, sort files/listeners for deterministic output, and never connect during process discovery.

- [ ] **Step 4: Run the procscan package tests**

Run: `rtk go test ./internal/procscan -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

```bash
rtk git add internal/procscan/procfs_linux.go internal/procscan/procfs_linux_test.go
rtk git commit -m "feat: attribute agent files and listeners"
```

---

### Task 4: Add the adapter coordinator and failure isolation

**Files:**
- Create: `internal/collector/coordinator.go`
- Create: `internal/collector/coordinator_test.go`

**Interfaces:**
- Consumes: `procscan.Source`, `procscan.Snapshot`, normalized `Session`.
- Produces: `Adapter`, `AdapterError`, `Collection`, `Coordinator`, `NewCoordinator`, and `(*Coordinator).Collect()`.

- [ ] **Step 1: Write failing coordinator contract tests**

```go
type fakeSource struct{ snap procscan.Snapshot }
func (f fakeSource) Snapshot() (procscan.Snapshot, error) { return f.snap, nil }

type fakeAdapter struct {
	agent Agent
	rows  []Session
	err   error
	panicValue any
}
func (f fakeAdapter) Agent() Agent { return f.agent }
func (f fakeAdapter) Discover(procscan.Snapshot) ([]Session, error) {
	if f.panicValue != nil { panic(f.panicValue) }
	return f.rows, f.err
}

func TestCoordinatorKeepsHealthyAdapterResults(t *testing.T) {
	c := NewCoordinator(fakeSource{},
		fakeAdapter{agent: AgentClaude, err: errors.New("bad schema")},
		fakeAdapter{agent: AgentCodex, rows: []Session{{ID: "codex:1", Agent: AgentCodex, Project: "p"}}},
		fakeAdapter{agent: AgentAider, panicValue: "boom"},
	)
	got := c.Collect()
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "codex:1" { t.Fatalf("sessions=%+v", got.Sessions) }
	if len(got.Errors) != 2 { t.Fatalf("errors=%+v", got.Errors) }
}
```

Add tests for duplicate global IDs (keep first, emit an error) and stable project → agent rank → active-before-done → name sorting.

- [ ] **Step 2: Run and verify RED**

Run: `rtk go test ./internal/collector -run TestCoordinator -count=1`

Expected: FAIL because coordinator types do not exist.

- [ ] **Step 3: Implement the coordinator**

```go
type Adapter interface {
	Agent() Agent
	Discover(procscan.Snapshot) ([]Session, error)
}

type AdapterError struct {
	Agent Agent
	Err   error
}

type Collection struct {
	Sessions []Session
	Errors   []AdapterError
}

type Coordinator struct {
	source   procscan.Source
	adapters []Adapter
}

func NewCoordinator(source procscan.Source, adapters ...Adapter) *Coordinator {
	return &Coordinator{source: source, adapters: adapters}
}
```

Implement `safeDiscover` with `defer/recover`, namespace validation, duplicate rejection, partial-success merging, and the exact stable ordering from the test. Source failure returns no sessions and one system-level error whose `Agent` is empty.

- [ ] **Step 4: Run coordinator and existing collector tests**

Run: `rtk go test ./internal/collector -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 4**

```bash
rtk git add internal/collector/coordinator.go internal/collector/coordinator_test.go
rtk git commit -m "feat: coordinate isolated agent adapters"
```

---

### Task 5: Convert Claude collection into an adapter and capture model

**Files:**
- Create: `internal/collector/claude_adapter.go`
- Create: `internal/collector/claude_adapter_test.go`
- Modify: `internal/collector/sessions.go`
- Modify: `internal/collector/sessions_test.go`
- Modify: `internal/collector/transcript.go`
- Modify: `internal/collector/transcript_test.go`
- Modify: `internal/collector/collector_test.go`

**Interfaces:**
- Consumes: `procscan.Snapshot.HasPID`, existing Claude session/job formats.
- Produces: `TranscriptSnapshot`; `func (*Scanner) Scan(string) TranscriptSnapshot`; `ClaudeAdapter`; `NewClaudeAdapter(root string)`.

- [ ] **Step 1: Add failing model and adapter tests**

Use a fake Claude root with one live PID and transcript line:

```json
{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-6","content":[]}}
```

Then assert:

```go
got, err := NewClaudeAdapter(root).Discover(procscan.Snapshot{
	UID: 1000,
	Processes: []procscan.Process{{PID: 77, UID: 1000}},
})
if err != nil { t.Fatal(err) }
s := got[0]
if s.ID != "claude:sess1" || s.NativeID != "sess1" || s.Agent != AgentClaude || s.Model != "claude-opus-4-6" {
	t.Fatalf("identity=%+v", s)
}
if s.Children[0].ID != "claude:sess1/tool-B" || s.Children[0].Agent != AgentClaude || s.Children[0].Model != "" {
	t.Fatalf("child=%+v", s.Children[0])
}
```

Add cases for missing model → `unknown`, dead PID omitted, background blocked preserved, and incomplete final JSONL line ignored until the next scan.

- [ ] **Step 2: Run and verify RED**

Run: `rtk go test ./internal/collector -run 'TestClaudeAdapter|TestScannerCapturesModel' -count=1`

Expected: FAIL because `ClaudeAdapter`, `TranscriptSnapshot`, and model parsing do not exist.

- [ ] **Step 3: Refactor the transcript scanner to return a value object**

```go
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
	// Keep the current open/stat/truncate/seek/read-complete-lines loop here;
	// only its return shape changes from a tuple to TranscriptSnapshot.
	return TranscriptSnapshot{
		Done: st.done, Total: st.total, HaveTodos: st.haveTodos,
		Model: ModelOrUnknown(st.model), Children: st.buildSubs(),
	}
}
```

Add `Model string` to `transcriptLine.Message`, retain the latest non-empty assistant model, and update current scanner callers/tests to use the value object.

- [ ] **Step 4: Make session scanning accept process liveness from the snapshot**

```go
func scanSessions(root string, alive func(int) bool) ([]Session, error) {
	// Keep existing JSON decoding; replace pidAlive(r.PID) with alive(r.PID).
}
```

Keep `pidAlive` only for backward-compatible focused tests until Task 12 removes the old `Collect` production path.

- [ ] **Step 5: Implement `ClaudeAdapter.Discover`**

```go
type ClaudeAdapter struct {
	root    string
	scanner *Scanner
}

func NewClaudeAdapter(root string) *ClaudeAdapter {
	return &ClaudeAdapter{root: root, scanner: NewScanner()}
}

func (a *ClaudeAdapter) Agent() Agent { return AgentClaude }
```

Discover sessions using `snapshot.HasPID`, scan transcript before applying optional background job state, set `NativeID`, namespaced parent/child IDs, `AgentClaude`, session model, child model empty, and keep the current todo/job semantics.

- [ ] **Step 6: Run all collector tests**

Run: `rtk go test ./internal/collector -count=1`

Expected: PASS.

- [ ] **Step 7: Commit Task 5**

```bash
rtk git add internal/collector/claude_adapter.go internal/collector/claude_adapter_test.go internal/collector/sessions.go internal/collector/sessions_test.go internal/collector/transcript.go internal/collector/transcript_test.go internal/collector/collector_test.go
rtk git commit -m "feat: adapt Claude sessions into normalized monitoring"
```

---

### Task 6: Reduce Codex rollout JSONL incrementally

**Files:**
- Create: `internal/collector/codex_rollout.go`
- Create: `internal/collector/codex_rollout_test.go`

**Interfaces:**
- Produces: `CodexRolloutSnapshot`; `CodexScanner`; `NewCodexScanner`; `func (*CodexScanner) Scan(string) CodexRolloutSnapshot`.

- [ ] **Step 1: Write failing lifecycle, model, rotation, and partial-line tests**

```go
func TestCodexScannerReducesLifecycleAndModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"session_meta","payload":{"id":"abc","cwd":"/work/p","parent_thread_id":"parent"}}`,
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.6-sol"}}`,
	}, "\n")+"\n")
	s := NewCodexScanner().Scan(path)
	if s.NativeID != "abc" || s.ParentID != "parent" || s.Cwd != "/work/p" || s.Model != "gpt-5.6-sol" || !s.Busy {
		t.Fatalf("snapshot=%+v", s)
	}
}
```

Append matching `task_complete` and assert `Busy=false`, `Done=true`. Add `task_aborted`, malformed line, incomplete final line, and file truncate tests.

- [ ] **Step 2: Run and verify RED**

Run: `rtk go test ./internal/collector -run TestCodexScanner -count=1`

Expected: FAIL because the Codex scanner does not exist.

- [ ] **Step 3: Implement the incremental reducer**

```go
type CodexRolloutSnapshot struct {
	NativeID string
	ParentID string
	Cwd      string
	Model    string
	Busy     bool
	Done     bool
	UpdatedAt int64
}

type codexRolloutState struct {
	offset int64
	meta   CodexRolloutSnapshot
	openTurns map[string]bool
}

type CodexScanner struct {
	states map[string]*codexRolloutState
}

func NewCodexScanner() *CodexScanner {
	return &CodexScanner{states: map[string]*codexRolloutState{}}
}
```

Tail complete newline-terminated records. Decode only `session_meta`, `turn_context`, and `event_msg` lifecycle fields. A start inserts the turn ID; complete/abort removes it; `Busy` is `len(openTurns)>0`; `Done` is true only after at least one terminal event and no open turns. Reset on truncate.

- [ ] **Step 4: Run Codex reducer tests**

Run: `rtk go test ./internal/collector -run TestCodexScanner -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 6**

```bash
rtk git add internal/collector/codex_rollout.go internal/collector/codex_rollout_test.go
rtk git commit -m "feat: reduce Codex rollout lifecycle"
```

---

### Task 7: Attribute Codex processes, sessions, and subagents

**Files:**
- Create: `internal/collector/codex_adapter.go`
- Create: `internal/collector/codex_adapter_test.go`

**Interfaces:**
- Consumes: `procscan.Process.Files`, `CodexScanner`.
- Produces: `CodexAdapter`; `NewCodexAdapter(root string)`.

- [ ] **Step 1: Write failing attribution and hierarchy tests**

Create parent and child rollout fixtures, each referenced by the matching process `Files`. The child metadata names the parent native ID. Assert one top-level parent with one child, exact parent model, empty child model, and global IDs `codex:parent` and `codex:parent/child`.

Add these exact cases:

```go
func TestCodexAdapterDoesNotGuessUnmatchedRollout(t *testing.T) {
	a := NewCodexAdapter(filepath.Join(t.TempDir(), ".codex"))
	rows, err := a.Discover(procscan.Snapshot{Processes: []procscan.Process{{
		PID: 42, UID: 1000, Comm: "codex", Exe: "/usr/bin/codex", Cwd: "/work/p",
	}}})
	if err != nil { t.Fatal(err) }
	if len(rows) != 1 || rows[0].ID != "codex:pid:42" || rows[0].Model != "unknown" || rows[0].Status != "" || rows[0].IsDone() {
		t.Fatalf("observation=%+v", rows)
	}
}
```

Also assert Node/wrapper argv containing a Codex executable is recognized, unrelated processes are ignored, and a just-completed row remains available for four seconds through an injected clock.

- [ ] **Step 2: Run and verify RED**

Run: `rtk go test ./internal/collector -run TestCodexAdapter -count=1`

Expected: FAIL because `CodexAdapter` does not exist.

- [ ] **Step 3: Implement Codex process matching and exact fd attribution**

```go
type CodexAdapter struct {
	root    string
	scanner *CodexScanner
	now     func() time.Time
	recent  map[string]recentSession
}

type recentSession struct {
	Row       Session
	ExpiresAt time.Time
}

func NewCodexAdapter(root string) *CodexAdapter {
	return &CodexAdapter{
		root: root, scanner: NewCodexScanner(), now: time.Now,
		recent: map[string]recentSession{},
	}
}

func (a *CodexAdapter) Agent() Agent { return AgentCodex }
```

Match canonical executable/comm names and known Node wrapper argv. Accept an fd path only after `filepath.Rel(root/sessions, path)` proves it is inside the Codex session tree and its basename starts with `rollout-` and ends with `.jsonl`.

- [ ] **Step 4: Build normalized rows and child relationships**

For an attributed rollout, set status `busy` while open turns exist and `idle` after terminal state, mode `Indeterminate`, exact-or-unknown model, project basename, and short-ID fallback name. Store terminal rows in a four-second recent cache. Reparent child snapshots by native parent ID; child IDs include parent and child native IDs, child model is empty. Emit an unmatched process as a non-busy, non-done indeterminate observation row.

- [ ] **Step 5: Run Codex adapter and all collector tests**

Run: `rtk go test ./internal/collector -count=1`

Expected: PASS.

- [ ] **Step 6: Commit Task 7**

```bash
rtk git add internal/collector/codex_adapter.go internal/collector/codex_adapter_test.go
rtk git commit -m "feat: monitor Codex processes and child sessions"
```

---

### Task 8: Read active OpenCode state from SQLite without CGO

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/collector/opencode_store.go`
- Create: `internal/collector/opencode_store_test.go`

**Interfaces:**
- Produces: `OpenCodeTodo`, `OpenCodeRecord`, `OpenCodeStore`, `SQLiteOpenCodeStore`, `NewSQLiteOpenCodeStore(path string)`.
- Dependency: `modernc.org/sqlite` v1.47.0.

- [ ] **Step 1: Add the pinned pure-Go SQLite dependency**

Run: `rtk go get modernc.org/sqlite@v1.47.0`

Expected: `go.mod` and `go.sum` add the driver and its indirect dependencies; no CGO driver appears.

- [ ] **Step 2: Write failing read-only store tests against a temporary database**

Create the minimal `session`, `message`, `part`, and `todo` tables matching the verified OpenCode columns. Insert:

- one active parent whose latest assistant message has no `time.completed`;
- one completed child with `parent_id` and runtime `providerID/modelID`;
- todo rows in completed/in_progress/pending states;
- one old unrelated directory.

Assert the store returns only candidate directories, uses message runtime model over session model, counts todo completion, identifies incomplete tool/message state as busy, and never opens `auth.json`.

```go
records, err := store.Candidates(context.Background(), []string{"/work/p"}, now.Add(-5*time.Second).UnixMilli())
if err != nil { t.Fatal(err) }
if records[0].ProviderID != "openai" || records[0].ModelID != "gpt-5.6-sol" || !records[0].Busy {
	t.Fatalf("record=%+v", records[0])
}
```

- [ ] **Step 3: Run and verify RED**

Run: `rtk go test ./internal/collector -run TestSQLiteOpenCodeStore -count=1`

Expected: FAIL because the store types do not exist.

- [ ] **Step 4: Implement read-only store types and queries**

```go
type OpenCodeTodo struct {
	Content string
	Status  string
}

type OpenCodeRecord struct {
	ID, ParentID, Title, Directory, AgentMode string
	ProviderID, ModelID string
	UpdatedAt int64
	Busy bool
	Todos []OpenCodeTodo
}

type OpenCodeStore interface {
	Candidates(context.Context, []string, int64) ([]OpenCodeRecord, error)
	ByIDs(context.Context, []string) ([]OpenCodeRecord, error)
}
```

Build the DSN with `(url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String()`, pass it to `sql.Open("sqlite", dsn)`, set one open connection, and use context-bounded SELECT statements only. Candidate criteria are directory membership plus either an incomplete latest assistant/tool record or `session.time_updated >= recentAfter`. Parse JSON in Go, selecting only role/time/error/model/provider/tool-state fields. Fetch todos only for selected IDs.

- [ ] **Step 5: Run store tests with CGO disabled**

Run: `CGO_ENABLED=0 rtk go test ./internal/collector -run TestSQLiteOpenCodeStore -count=1`

Expected: PASS.

- [ ] **Step 6: Commit Task 8**

```bash
rtk git add go.mod go.sum internal/collector/opencode_store.go internal/collector/opencode_store_test.go
rtk git commit -m "feat: read OpenCode state from SQLite"
```

---

### Task 9: Monitor OpenCode through loopback status with SQLite fallback

**Files:**
- Create: `internal/collector/opencode_adapter.go`
- Create: `internal/collector/opencode_adapter_test.go`

**Interfaces:**
- Consumes: `procscan.Process.Listeners`, `OpenCodeStore`.
- Produces: `OpenCodeStatusProbe`, `LoopbackOpenCodeProbe`, `OpenCodeAdapter`, `NewOpenCodeAdapter(home string)`.

- [ ] **Step 1: Write failing status-probe and fallback tests**

Use `httptest.Server` to return:

```json
{"ses_parent":{"type":"busy"},"ses_child":{"type":"idle"}}
```

Assert GET `/session/status`, a 150 ms client timeout, no POST, and exact status mapping. Add an unreachable listener case where the fake store's incomplete assistant state marks the session busy.

Add hierarchy/progress assertions:

```go
if parent.ID != "opencode:ses_parent" || parent.Agent != AgentOpenCode || parent.Model != "openai/gpt-5.6-sol" {
	t.Fatalf("parent=%+v", parent)
}
if len(parent.Children) != 1 || parent.Children[0].Model != "" {
	t.Fatalf("children=%+v", parent.Children)
}
if parent.Mode != Determinate || parent.Done != 1 || parent.Total != 3 {
	t.Fatalf("progress=%+v", parent)
}
```

- [ ] **Step 2: Run and verify RED**

Run: `rtk go test ./internal/collector -run 'TestLoopbackOpenCodeProbe|TestOpenCodeAdapter' -count=1`

Expected: FAIL because probe and adapter types do not exist.

- [ ] **Step 3: Implement GET-only loopback probing**

```go
type OpenCodeStatusProbe interface {
	Statuses(context.Context, []procscan.Listener) (map[string]string, error)
}

type LoopbackOpenCodeProbe struct {
	Client *http.Client
}
```

Probe only listeners already attributed to an OpenCode process and whose address is loopback. Request `/session/status` with GET, accept only HTTP 200 JSON objects, stop at the first valid OpenCode-shaped response, and return an error without credentials when all probes fail.

- [ ] **Step 4: Implement OpenCode adapter orchestration**

Match `opencode`, `opencode-cli`, and known Node wrapper argv. Resolve data root per process from whitelisted `XDG_DATA_HOME`, else `<home>/.local/share/opencode`; never open `auth.json`. Query API IDs through `ByIDs`, merge recent/incomplete SQLite candidates, build parent trees, use session title/slug fallback, render model as `provider/model`, and derive todo progress. Cache terminal rows for four seconds.

- [ ] **Step 5: Run OpenCode and all collector tests**

Run: `rtk go test ./internal/collector -count=1`

Expected: PASS.

- [ ] **Step 6: Commit Task 9**

```bash
rtk git add internal/collector/opencode_adapter.go internal/collector/opencode_adapter_test.go
rtk git commit -m "feat: monitor OpenCode sessions read-only"
```

---

### Task 10: Reduce Aider history and runtime model evidence

**Files:**
- Create: `internal/collector/aider_history.go`
- Create: `internal/collector/aider_history_test.go`

**Interfaces:**
- Produces: `AiderHistorySnapshot`, `AiderHistoryScanner`, `NewAiderHistoryScanner`, `ResolveAiderModel`.

- [ ] **Step 1: Write failing history/model tests**

Use prompt-toolkit input history entries with `+`-prefixed lines and Aider chat Markdown with `#### user` / `#### assistant` headings. Cover:

- submitted non-slash input newer than assistant completion → busy;
- assistant append → done;
- `/model openrouter/deepseek/deepseek-chat` updates model without opening a turn;
- `/help` does not open a turn;
- explicit `--model` is retained only when attributable input history is available;
- conflicting argv/environment/config values without runtime attribution → `unknown`;
- shared/ambiguous history → `unknown` and no false completion.

```go
if got := ResolveAiderModel(ModelEvidence{
	RuntimeModel: "openrouter/deepseek/deepseek-chat",
	ArgModel: "sonnet",
	HistoryAttributed: true,
}); got != "openrouter/deepseek/deepseek-chat" {
	t.Fatalf("model=%q", got)
}
```

- [ ] **Step 2: Run and verify RED**

Run: `rtk go test ./internal/collector -run 'TestAiderHistory|TestResolveAiderModel' -count=1`

Expected: FAIL because Aider history types do not exist.

- [ ] **Step 3: Implement incremental Aider history reducers**

```go
type AiderHistorySnapshot struct {
	Busy         bool
	Done         bool
	RuntimeModel string
}

type ModelEvidence struct {
	RuntimeModel      string
	ArgModel          string
	EnvModel          string
	ConfigModels      []string
	HistoryAttributed bool
}
```

Tail complete entries by inode/offset, reset on truncate, parse only entry boundaries and slash-command names, and never retain prompt/response bodies after classifying the entry. `ResolveAiderModel` returns runtime model first; otherwise requires attributed history and one unambiguous launch/config value; all other cases return `unknown`.

- [ ] **Step 4: Run Aider reducer tests**

Run: `rtk go test ./internal/collector -run 'TestAiderHistory|TestResolveAiderModel' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 10**

```bash
rtk git add internal/collector/aider_history.go internal/collector/aider_history_test.go
rtk git commit -m "feat: reduce Aider runtime evidence"
```

---

### Task 11: Attribute Aider processes without cross-session guesses

**Files:**
- Create: `internal/collector/aider_adapter.go`
- Create: `internal/collector/aider_adapter_test.go`

**Interfaces:**
- Consumes: `procscan.Process`, `AiderHistoryScanner`.
- Produces: `AiderAdapter`, `NewAiderAdapter(home string)`.

- [ ] **Step 1: Write failing process and ambiguity tests**

Cover executable `aider`, `python -m aider`, and installed Python entry-point argv. Assert unrelated Python processes are ignored. Use fd-attributed input/chat files for one exact process and shared default files for two same-cwd processes.

```go
if row.ID != "aider:pid:55" || row.NativeID != "pid:55" || row.Agent != AgentAider || row.Name != "PID 55" {
	t.Fatalf("row=%+v", row)
}
if row.Model != "sonnet" || row.Mode != Indeterminate || row.Status != "busy" || len(row.Children) != 0 {
	t.Fatalf("runtime=%+v", row)
}
```

For ambiguous shared history, assert both rows have model `unknown` and neither receives a false done edge.

- [ ] **Step 2: Run and verify RED**

Run: `rtk go test ./internal/collector -run TestAiderAdapter -count=1`

Expected: FAIL because `AiderAdapter` does not exist.

- [ ] **Step 3: Implement process matching and history attribution**

```go
type AiderAdapter struct {
	home    string
	history *AiderHistoryScanner
	now     func() time.Time
	recent  map[string]recentSession
}

func NewAiderAdapter(home string) *AiderAdapter {
	return &AiderAdapter{
		home: home, history: NewAiderHistoryScanner(), now: time.Now,
		recent: map[string]recentSession{},
	}
}

func (a *AiderAdapter) Agent() Agent { return AgentAider }
```

Prefer fd-attributed history paths. Use cwd default paths only when exactly one Aider process owns that cwd. Parse `--model`/`--model=<value>`, whitelisted `AIDER_MODEL`, and top-level `model:` lines from home/repo `.aider.conf.yml` without logging the file. Pass evidence to `ResolveAiderModel`.

- [ ] **Step 4: Emit normalized Aider lifecycle rows**

Use `aider:pid:<pid>`, `PID <pid>`, project basename, no children, indeterminate progress, and history-derived busy/idle. Retain terminal rows for four seconds. A detected process with ambiguous lifecycle remains a non-busy, non-done indeterminate observation row and emits no sound.

- [ ] **Step 5: Run Aider and full collector tests**

Run: `rtk go test ./internal/collector -count=1`

Expected: PASS.

- [ ] **Step 6: Commit Task 11**

```bash
rtk git add internal/collector/aider_adapter.go internal/collector/aider_adapter_test.go
rtk git commit -m "feat: monitor Aider processes passively"
```

---

### Task 12: Wire the default coordinator into Bubble Tea

**Files:**
- Create: `internal/collector/defaults.go`
- Modify: `internal/collector/collector.go`
- Modify: `internal/collector/collector_test.go`
- Modify: `internal/model/model.go`
- Modify: `internal/model/model_test.go`
- Modify: `main.go`

**Interfaces:**
- Produces: `NewDefault(home string, source procscan.Source) *Coordinator`.
- Produces in model: `type SessionSource interface { Collect() collector.Collection }`; `New(SessionSource, *sound.Player, time.Duration) Model`.

- [ ] **Step 1: Write failing default assembly and model polling tests**

```go
type fakeCollectionSource struct{ result collector.Collection }
func (f fakeCollectionSource) Collect() collector.Collection { return f.result }

func TestPollCmdUsesNormalizedSource(t *testing.T) {
	source := fakeCollectionSource{result: collector.Collection{Sessions: []collector.Session{{ID: "codex:1"}}}}
	m := New(source, nil, time.Second)
	msg := m.pollCmd()().(pollMsg)
	if len(msg) != 1 || msg[0].ID != "codex:1" { t.Fatalf("msg=%+v", msg) }
}
```

Add `TestNewDefaultIncludesFourAdapters` in collector using package-private inspection and assert exact agent order.

- [ ] **Step 2: Run and verify RED**

Run: `rtk go test ./internal/collector ./internal/model -run 'TestNewDefault|TestPollCmdUsesNormalizedSource' -count=1`

Expected: FAIL because default assembly and the new model constructor do not exist.

- [ ] **Step 3: Assemble production adapters**

```go
func NewDefault(home string, source procscan.Source) *Coordinator {
	return NewCoordinator(source,
		NewClaudeAdapter(filepath.Join(home, ".claude")),
		NewCodexAdapter(filepath.Join(home, ".codex")),
		NewOpenCodeAdapter(home),
		NewAiderAdapter(home),
	)
}
```

Move coordinator code out of the old `collector.go` if needed, delete the obsolete Claude-only `Collect(root, scanner)` production path, and retain focused parser APIs used by adapter tests.

- [ ] **Step 4: Inject `SessionSource` into the Bubble Tea model**

```go
type SessionSource interface {
	Collect() collector.Collection
}

func New(source SessionSource, player *sound.Player, interval time.Duration) Model {
	return Model{
		source: source, player: player, interval: interval,
		soundOn: true, doneAt: map[string]time.Time{}, nowFn: time.Now,
	}
}

func (m Model) pollCmd() tea.Cmd {
	return func() tea.Msg { return pollMsg(m.source.Collect().Sessions) }
}
```

Update existing model tests to pass `fakeCollectionSource{}`. Keep first-poll seeding, event edges, grace filtering, scrolling, and sound behavior unchanged.

- [ ] **Step 5: Update main wiring**

Resolve `home` once, build `procscan.NewProcFS()`, call `collector.NewDefault(home, source)`, and pass the coordinator to `model.New`. Remove the Claude-specific root from `main.go`.

- [ ] **Step 6: Run collector/model tests**

Run: `rtk go test ./internal/collector ./internal/model -count=1`

Expected: PASS.

- [ ] **Step 7: Commit Task 12**

```bash
rtk git add internal/collector/defaults.go internal/collector/collector.go internal/collector/collector_test.go internal/model/model.go internal/model/model_test.go main.go
rtk git commit -m "feat: poll all supported coding agents"
```

---

### Task 13: Render project → agent → session/model → subagent

**Files:**
- Modify: `internal/render/view.go`
- Modify: `internal/render/view_test.go`
- Modify: `internal/render/dashboard.go`
- Modify: `internal/render/dashboard_test.go`
- Modify: `internal/model/model.go`

**Interfaces:**
- Produces: `Layout`, `LayoutForWidth(int) Layout`.
- Changes: `BodyLines([]collector.Session, int, map[string]bool, int) []string` receives total terminal width.

- [ ] **Step 1: Add failing hierarchy/model-column tests**

Create one project with Claude and Codex sessions, one Claude child, and assert line order:

```go
out := stripANSI(strings.Join(BodyLines(sessions, 0, nil, 120), "\n"))
ordered := []string{"▾ proj", "  ▾ Claude", "    ▸ claude-work", "      └─ ⌁ review", "  ▾ Codex", "    ▸ codex-work"}
last := -1
for _, want := range ordered {
	idx := strings.Index(out, want)
	if idx <= last { t.Fatalf("%q out of order:\n%s", want, out) }
	last = idx
}
if !strings.Contains(out, "claude-opus-4-6") || !strings.Contains(out, "gpt-5.6-sol") {
	t.Fatalf("session models missing:\n%s", out)
}
childLine := lineContaining(out, "review")
if strings.Contains(childLine, "claude-opus-4-6") || strings.Contains(childLine, "gpt-5.6-sol") {
	t.Fatalf("subagent leaked model: %s", childLine)
}

func lineContaining(text, needle string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) { return line }
	}
	return ""
}
```

Update dashboard header expectation from `PROJECT / SESSION` to `PROJECT / AGENT / SESSION`, require `MODEL`, and keep `TestHeaderOmitsBlockedCount` unchanged.

- [ ] **Step 2: Run and verify RED**

Run: `rtk go test ./internal/render -run 'TestBodyLinesAgentHierarchy|TestComposeChrome' -count=1`

Expected: FAIL because agent groups and model column are absent.

- [ ] **Step 3: Add wide layout and agent grouping**

```go
type Layout struct {
	Compact    bool
	SessionW   int
	ModelW     int
	BarW       int
	TasksW     int
}

func LayoutForWidth(total int) Layout {
	cw := contentWidth(total)
	if cw < 92 {
		return Layout{Compact: true, SessionW: cw, ModelW: 0, BarW: 12, TasksW: 7}
	}
	return Layout{SessionW: 32, ModelW: 20, BarW: 15, TasksW: 8}
}
```

Track both `curProject` and `curAgent`. Emit agent group only after its project group. Use prefixes `    ▸ ` for sessions and `      ├─/└─ ⌁ ` for children. Wide session rows include padded `ModelOrUnknown(s.Model)`; child rows emit a blank model cell.

- [ ] **Step 4: Update model body rendering and column header**

Pass `m.width` into `BodyLines`; build the wide header from the same `Layout` so body/header columns align. Keep status styles, blocked details, dimming, bar animation, and counts.

- [ ] **Step 5: Run render and model tests**

Run: `rtk go test ./internal/render ./internal/model -count=1`

Expected: PASS.

- [ ] **Step 6: Commit Task 13**

```bash
rtk git add internal/render/view.go internal/render/view_test.go internal/render/dashboard.go internal/render/dashboard_test.go internal/model/model.go
rtk git commit -m "feat: render agent and model hierarchy"
```

---

### Task 14: Add narrow model continuation rows and preserve interaction semantics

**Files:**
- Modify: `internal/render/view.go`
- Modify: `internal/render/view_test.go`
- Modify: `internal/render/dashboard.go`
- Modify: `internal/render/dashboard_test.go`
- Modify: `internal/model/model_test.go`

**Interfaces:**
- Consumes: `Layout.Compact`.
- Preserves: `c` hides subagents only; project/agent/session rows remain.

- [ ] **Step 1: Write failing narrow-layout and collapse tests**

At width 70, assert the session name and full model occur on separate lines, the model line also contains progress/tasks/status, and no model appears on child lines. At width 40, assert ellipsis may occur but no different model string appears.

Add a model test that presses `c` and asserts project, agent, and session remain while child text disappears.

```go
lines := BodyLines(sessions, 0, nil, 70)
nameIdx := indexLine(lines, "codex-work")
modelIdx := indexLine(lines, "gpt-5.6-sol")
if nameIdx < 0 || modelIdx != nameIdx+1 {
	t.Fatalf("compact rows=%q", lines)
}

func indexLine(lines []string, needle string) int {
	for i, line := range lines {
		if strings.Contains(stripANSI(line), needle) { return i }
	}
	return -1
}
```

- [ ] **Step 2: Run and verify RED**

Run: `rtk go test ./internal/render ./internal/model -run 'TestCompactModelContinuation|TestCollapseKeepsAgentHierarchy' -count=1`

Expected: FAIL because compact continuation rows are not implemented.

- [ ] **Step 3: Implement compact rows from the shared layout**

For compact sessions, emit:

```text
    ▸ session-name
      model: exact-model [progress] tasks status
```

Use visible-rune truncation only after reserving space for `model:`, bar, tasks, and status. Never replace the model with a default. Keep children one line, keep their model absent, and count continuation lines in scrolling/body budget naturally through the returned line slice.

- [ ] **Step 4: Preserve collapse and count behavior**

`stripChildren` must remove only `Children`; it must not alter `Agent`, `Model`, or top-level grouping. `CountSessions` counts session rows, not project/agent group rows. Keep blocked excluded from busy and absent from header rendering.

- [ ] **Step 5: Run all UI/model tests**

Run: `rtk go test ./internal/render ./internal/model -count=1`

Expected: PASS.

- [ ] **Step 6: Commit Task 14**

```bash
rtk git add internal/render/view.go internal/render/view_test.go internal/render/dashboard.go internal/render/dashboard_test.go internal/model/model_test.go
rtk git commit -m "feat: adapt agent model rows to narrow terminals"
```

---

### Task 15: Verify privacy, buildability, and real-agent behavior

**Files:**
- Modify only files implicated by a failing verification; do not add unrelated refactors.

**Interfaces:**
- Verifies all interfaces and acceptance criteria from the approved design.

- [ ] **Step 1: Format and run static diff checks**

Run:

```bash
rtk gofmt -w main.go internal/collector internal/model internal/procscan internal/render internal/sound
rtk git diff --check
```

Expected: no formatting or whitespace errors.

- [ ] **Step 2: Run the full test and race suites**

Run:

```bash
rtk go test ./... -count=1
rtk go test -race ./... -count=1
rtk go vet ./...
```

Expected: all packages pass and `go vet` reports no issues.

- [ ] **Step 3: Prove the binary remains CGO-free**

Run:

```bash
CGO_ENABLED=0 rtk go build -o /tmp/agentmon-linux .
rtk go list -deps .
```

Expected: build exits zero; dependency output contains `modernc.org/sqlite` and does not contain `github.com/mattn/go-sqlite3`.

- [ ] **Step 4: Run privacy-focused tests explicitly**

Run:

```bash
rtk go test ./internal/procscan ./internal/collector -run 'UID|Secret|Auth|Ambiguous|DoesNotGuess|ReadOnly' -count=1
```

Expected: PASS, proving other-UID filtering, environment whitelisting, no auth access, and no ambiguous model/session assignment.

- [ ] **Step 5: Perform manual Linux/WSL smoke verification**

Run `rtk go run . --no-sound --interval 500ms`, then in separate terminals execute one real turn for each installed agent. Record evidence in the final handoff, not in agent state:

1. Claude appears under project → Claude with model, todo/subagent behavior intact.
2. Codex appears under project → Codex with the rollout's effective model; completion fades once.
3. OpenCode appears under project → OpenCode with `provider/model`, todos, and child session hierarchy when present.
4. Aider appears under project → Aider; exact model appears only with attributable runtime evidence, otherwise `unknown`.
5. A completed turn rings once when sound is enabled, dims for three seconds, disappears, and reappears on the next turn.
6. Header never displays blocked; a blocked Claude background session still displays `BLOCKED` and `needs:` in its row.

If Aider is not installed, run the fixture acceptance test and state that live Aider smoke verification remains unavailable; do not install it without user authorization.

- [ ] **Step 6: Run independent review gates**

Use `superpowers:requesting-code-review`. Review first for conformance to this plan and the approved spec, then for code quality, privacy, parser resilience, and performance. Apply findings through new RED → GREEN cycles.

- [ ] **Step 7: Commit verification fixes if any**

```bash
rtk git add main.go go.mod go.sum internal/collector internal/model internal/procscan internal/render internal/sound
rtk git commit -m "fix: harden multi-agent monitoring verification"
```

Skip this commit only when Step 1–6 produce no code changes.

## Plan Self-Review Checklist

- Spec coverage: process ownership, four adapters, exact-or-unknown models, hierarchy, fade/reactivation, blocked semantics, read-only/privacy, performance, failure isolation, responsive UI, and Linux verification each map to explicit tasks.
- Type consistency: all adapters consume `procscan.Snapshot` and return `[]collector.Session`; production polling flows through `Coordinator.Collect() collector.Collection`; session identity helpers are defined in Task 1 before adapter use.
- Dependency consistency: OpenCode alone adds `modernc.org/sqlite` v1.47.0; the final build explicitly verifies `CGO_ENABLED=0`.
- No production task bypasses a failing test. Dependency installation and final verification are paired with immediate tests.
- Existing uncommitted blocked-header removal is preserved and covered in Tasks 13–15.
