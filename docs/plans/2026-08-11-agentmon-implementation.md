# agentmon Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `agentmon`, a read-only TUI that watches every running Claude Code session on the machine and shows each one's progress as a horizontal pillar bar, ringing a soft chime when a session finishes or a background job blocks on a user decision.

**Architecture:** Three isolated layers — `collector` reads `~/.claude/` files into `[]Session` (pure, file-driven), `model` holds the Bubble Tea state and edge-detects chime events across poll snapshots, `render` draws the pillar bars and subagent tree. A `sound` package synthesizes chimes. `main` wires a ~1s poll tick and a ~100ms animation tick.

**Tech Stack:** Go 1.26, `github.com/charmbracelet/bubbletea` (TUI loop), `github.com/hajimehoshi/oto/v2` (audio out). No other runtime deps. Standard library for all file/JSON/regex work.

## Global Constraints

- Module path: `agentmon`. Go version floor: `go 1.26`.
- **Read-only**: never create, modify, or delete anything under `~/.claude/`. Tests use fixture dirs under the repo's temp, never the real home.
- The `~/.claude` root is resolved once at startup as `filepath.Join(os.UserHomeDir(), ".claude")`, but **every collector function takes the root as a parameter** so tests inject a fixture root. No function reads `$HOME` directly except `main`.
- Bar glyphs (exact runes): empty `⋮` (U+22EE), filled `█` (U+2588), wavefront/sweep `▓` (U+2593).
- Chime frequencies stay in the gentle range **E5 (659.25 Hz) – A5 (880 Hz)**; amplitude ≤ 0.2; every tone fades out. Never emit tones above A5.
- Audio init failure must **degrade to silent, log once, never crash** (WSL2 often has no audio server).
- `sessions/*.json` exposes only `status` ∈ {`busy`,`idle`} — there is no interactive "waiting-for-approval" signal; do not invent one.

---

## File Structure

```
agentmon/
  go.mod
  main.go                     // flags, root resolution, wire ticks → tea.Program
  internal/collector/
    types.go                  // Session, ProgressMode, IsDone, Fraction
    sessions.go               // scan sessions/*.json, filter dead PIDs
    transcript.go             // encodeCwd, parse todos + subagent tree
    jobs.go                   // parse jobs/<jobId>/state.json
    collector.go              // Collect(root) []Session — orchestrate + group
  internal/render/
    bar.go                    // RenderBar, Label — one session's pillar
    view.go                   // RenderView — grouping + subagent tree
  internal/model/
    events.go                 // DiffEvents(prev, cur) []Event
    model.go                  // tea.Model: ticks, keys, View
  internal/sound/
    sound.go                  // synth PCM, Player, silent fallback
```

Tests live beside each file (`*_test.go`). Fixture `.claude` trees are built with `t.TempDir()` inside tests.

---

### Task 1: Module scaffold + Session type and status logic

**Files:**
- Create: `go.mod`
- Create: `internal/collector/types.go`
- Test: `internal/collector/types_test.go`

**Interfaces:**
- Produces: `ProgressMode` (`Determinate=0`, `Indeterminate=1`); `Session` struct (fields below); `Session.IsDone() bool`; `Session.Fraction() float64`.

```go
// Session — one row in the TUI (a session or a subagent).
type Session struct {
    ID        string
    Name      string
    Project   string       // basename(cwd)
    Cwd       string
    Kind      string       // "interactive" | "bg"
    Status    string       // "busy" | "idle"
    JobState  string       // bg only: "running"|"blocked"|"idle"|"done"|""
    PID       int
    Mode      ProgressMode
    Done      int
    Total     int
    Blocked   bool         // bg: JobState=="blocked"
    NeedsHint string       // bg: state.json "needs"
    Children  []Session
    UpdatedAt int64
}
```

- [ ] **Step 1: Create `go.mod`**

```
module agentmon

go 1.26
```

- [ ] **Step 2: Write the failing test**

```go
// internal/collector/types_test.go
package collector

import "testing"

func TestIsDone(t *testing.T) {
    cases := []struct {
        name string
        s    Session
        want bool
    }{
        {"determinate incomplete", Session{Mode: Determinate, Done: 6, Total: 10}, false},
        {"determinate complete", Session{Mode: Determinate, Done: 10, Total: 10}, true},
        {"indeterminate busy", Session{Mode: Indeterminate, Status: "busy"}, false},
        {"indeterminate idle", Session{Mode: Indeterminate, Status: "idle"}, true},
        {"bg running", Session{Kind: "bg", JobState: "running"}, false},
        {"bg done", Session{Kind: "bg", JobState: "done"}, true},
        {"bg blocked not done", Session{Kind: "bg", JobState: "blocked", Blocked: true, Done: 41, Total: 41, Mode: Determinate}, false},
    }
    for _, c := range cases {
        if got := c.s.IsDone(); got != c.want {
            t.Errorf("%s: IsDone()=%v want %v", c.name, got, c.want)
        }
    }
}

func TestFraction(t *testing.T) {
    if f := (Session{Mode: Determinate, Done: 6, Total: 10}).Fraction(); f != 0.6 {
        t.Errorf("determinate fraction=%v want 0.6", f)
    }
    if f := (Session{Mode: Indeterminate, Status: "idle"}).Fraction(); f != 1 {
        t.Errorf("indeterminate-done fraction=%v want 1", f)
    }
    if f := (Session{Mode: Indeterminate, Status: "busy"}).Fraction(); f != 0 {
        t.Errorf("indeterminate-busy fraction=%v want 0", f)
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/collector/ -run 'TestIsDone|TestFraction' -v`
Expected: FAIL — `undefined: Session` / `ProgressMode`.

- [ ] **Step 4: Write minimal implementation**

```go
// internal/collector/types.go
package collector

type ProgressMode int

const (
    Determinate ProgressMode = iota
    Indeterminate
)

type Session struct {
    ID        string
    Name      string
    Project   string
    Cwd       string
    Kind      string
    Status    string
    JobState  string
    PID       int
    Mode      ProgressMode
    Done      int
    Total     int
    Blocked   bool
    NeedsHint string
    Children  []Session
    UpdatedAt int64
}

func (s Session) IsDone() bool {
    switch {
    case s.Blocked:
        return false
    case s.JobState == "done" || s.JobState == "idle":
        return true
    case s.Mode == Determinate:
        return s.Total > 0 && s.Done >= s.Total
    default:
        return s.Status == "idle"
    }
}

func (s Session) Fraction() float64 {
    if s.Mode == Determinate && s.Total > 0 {
        f := float64(s.Done) / float64(s.Total)
        if f > 1 {
            return 1
        }
        return f
    }
    if s.IsDone() {
        return 1
    }
    return 0
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/collector/ -run 'TestIsDone|TestFraction' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod internal/collector/types.go internal/collector/types_test.go
git commit -m "feat(collector): Session type with IsDone/Fraction status logic"
```

---

### Task 2: Scan sessions dir and filter dead PIDs

**Files:**
- Create: `internal/collector/sessions.go`
- Test: `internal/collector/sessions_test.go`

**Interfaces:**
- Consumes: `Session` (Task 1).
- Produces: `func ScanSessions(root string) ([]Session, error)` — reads `<root>/sessions/*.json`, skips dead PIDs and unparseable files, returns partially-filled `Session` (ID, Name, Cwd, Project, Kind, Status, PID, UpdatedAt, and `jobID` via the unexported field below). `func pidAlive(pid int) bool`.
- Produces (internal): raw JSON is read into a private struct; `ScanSessions` also returns the `jobId` — store it on a package-private map keyed by session, OR add an unexported field. Use an unexported field `jobID string` on `Session` for wiring to Task 6.

- [ ] **Step 1: Add unexported `jobID` field to `Session`**

Edit `internal/collector/types.go`, add to the `Session` struct after `NeedsHint`:

```go
    jobID     string // bg jobId, used to locate jobs/<jobID>/state.json
```

- [ ] **Step 2: Write the failing test**

```go
// internal/collector/sessions_test.go
package collector

import (
    "os"
    "path/filepath"
    "testing"
)

func writeFile(t *testing.T, path, content string) {
    t.Helper()
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
        t.Fatal(err)
    }
}

func TestScanSessionsFiltersDeadPIDs(t *testing.T) {
    root := t.TempDir()
    alive := os.Getpid()
    writeFile(t, filepath.Join(root, "sessions", "1.json"), `{"pid":`+itoa(alive)+`,"sessionId":"aaa","cwd":"/home/u/proj","kind":"interactive","name":"live-one","status":"busy","updatedAt":10,"jobId":""}`)
    writeFile(t, filepath.Join(root, "sessions", "2.json"), `{"pid":2147480000,"sessionId":"bbb","cwd":"/home/u/proj","kind":"bg","name":"dead-one","status":"idle","updatedAt":11,"jobId":"job2"}`)

    got, err := ScanSessions(root)
    if err != nil {
        t.Fatal(err)
    }
    if len(got) != 1 {
        t.Fatalf("want 1 live session, got %d: %+v", len(got), got)
    }
    s := got[0]
    if s.Name != "live-one" || s.Project != "proj" || s.Kind != "interactive" || s.PID != alive {
        t.Errorf("unexpected session: %+v", s)
    }
}

func TestScanSessionsMissingDirIsEmpty(t *testing.T) {
    got, err := ScanSessions(t.TempDir())
    if err != nil {
        t.Fatalf("missing sessions dir should be empty not error: %v", err)
    }
    if len(got) != 0 {
        t.Errorf("want empty, got %d", len(got))
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/collector/ -run TestScanSessions -v`
Expected: FAIL — `undefined: ScanSessions` / `itoa`.

- [ ] **Step 4: Write minimal implementation**

```go
// internal/collector/sessions.go
package collector

import (
    "encoding/json"
    "os"
    "path/filepath"
    "strconv"
    "syscall"
)

func itoa(i int) string { return strconv.Itoa(i) }

type rawSession struct {
    PID       int    `json:"pid"`
    SessionID string `json:"sessionId"`
    Cwd       string `json:"cwd"`
    Kind      string `json:"kind"`
    Name      string `json:"name"`
    Status    string `json:"status"`
    JobID     string `json:"jobId"`
    UpdatedAt int64  `json:"updatedAt"`
}

func pidAlive(pid int) bool {
    if pid <= 0 {
        return false
    }
    // Signal 0 probes existence without affecting the process.
    err := syscall.Kill(pid, 0)
    return err == nil || err == syscall.EPERM
}

func ScanSessions(root string) ([]Session, error) {
    dir := filepath.Join(root, "sessions")
    entries, err := os.ReadDir(dir)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, err
    }
    var out []Session
    for _, e := range entries {
        if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
            continue
        }
        data, err := os.ReadFile(filepath.Join(dir, e.Name()))
        if err != nil {
            continue
        }
        var r rawSession
        if json.Unmarshal(data, &r) != nil {
            continue
        }
        if !pidAlive(r.PID) {
            continue
        }
        out = append(out, Session{
            ID:        r.SessionID,
            Name:      r.Name,
            Project:   filepath.Base(r.Cwd),
            Cwd:       r.Cwd,
            Kind:      r.Kind,
            Status:    r.Status,
            PID:       r.PID,
            jobID:     r.JobID,
            UpdatedAt: r.UpdatedAt,
        })
    }
    return out, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/collector/ -run TestScanSessions -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/collector/types.go internal/collector/sessions.go internal/collector/sessions_test.go
git commit -m "feat(collector): scan sessions dir, filter dead PIDs"
```

---

### Task 3: Transcript path + TodoWrite progress parsing

**Files:**
- Create: `internal/collector/transcript.go`
- Test: `internal/collector/transcript_test.go`

**Interfaces:**
- Produces: `func EncodeCwd(cwd string) string` — replaces every `/` with `-` (so `/home/u/proj` → `-home-u-proj`).
- Produces: `func TranscriptPath(root, cwd, sessionID string) string` = `<root>/projects/<EncodeCwd(cwd)>/<sessionID>.jsonl`.
- Produces: `func ParseTodos(path string) (done, total int, found bool)` — scans the whole file, finds the **last** `tool_use` with `name=="TodoWrite"`, reads `input.todos[]`; `total=len`, `done=` count of `status=="completed"`. `found=false` if no TodoWrite or file missing.

- [ ] **Step 1: Write the failing test**

```go
// internal/collector/transcript_test.go
package collector

import (
    "path/filepath"
    "testing"
)

func TestEncodeCwd(t *testing.T) {
    if got := EncodeCwd("/home/u/proj"); got != "-home-u-proj" {
        t.Errorf("EncodeCwd=%q", got)
    }
}

// One assistant line carrying a TodoWrite tool_use with the given todos JSON array.
func todoLine(todos string) string {
    return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"TodoWrite","input":{"todos":` + todos + `}}]}}`
}

func TestParseTodosLastWins(t *testing.T) {
    root := t.TempDir()
    path := filepath.Join(root, "x.jsonl")
    first := todoLine(`[{"content":"a","status":"completed","activeForm":"a"},{"content":"b","status":"pending","activeForm":"b"}]`)
    last := todoLine(`[{"content":"a","status":"completed","activeForm":"a"},{"content":"b","status":"completed","activeForm":"b"},{"content":"c","status":"in_progress","activeForm":"c"}]`)
    writeFile(t, path, first+"\n"+`{"type":"user","message":{"role":"user","content":[]}}`+"\n"+last+"\n")

    done, total, found := ParseTodos(path)
    if !found || total != 3 || done != 2 {
        t.Fatalf("ParseTodos=(%d,%d,%v) want (2,3,true)", done, total, found)
    }
}

func TestParseTodosNoneFound(t *testing.T) {
    root := t.TempDir()
    path := filepath.Join(root, "x.jsonl")
    writeFile(t, path, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`+"\n")
    if _, _, found := ParseTodos(path); found {
        t.Error("expected found=false when no TodoWrite present")
    }
    if _, _, found := ParseTodos(filepath.Join(root, "missing.jsonl")); found {
        t.Error("expected found=false for missing file")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/collector/ -run 'TestEncodeCwd|TestParseTodos' -v`
Expected: FAIL — `undefined: EncodeCwd` / `ParseTodos`.

- [ ] **Step 3: Write minimal implementation**

```go
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
            Type       string          `json:"type"`
            Name       string          `json:"name"`
            ID         string          `json:"id"`
            ToolUseID  string          `json:"tool_use_id"`
            Input      json.RawMessage `json:"input"`
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/collector/ -run 'TestEncodeCwd|TestParseTodos' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/transcript.go internal/collector/transcript_test.go
git commit -m "feat(collector): transcript path + TodoWrite progress parsing"
```

---

### Task 4: Subagent tree from transcript

**Files:**
- Modify: `internal/collector/transcript.go`
- Test: `internal/collector/transcript_test.go` (add)

**Interfaces:**
- Produces: `func ParseSubagents(path string) []Session` — every `tool_use` with `name` in {`Task`,`Agent`} is a child; a child whose `id` has no matching `tool_result.tool_use_id` later in the file is **running** (`Mode=Indeterminate, Status="busy"`); one with a matching result is **done** (`Status="idle"`). Child `Name = input.description`, and Kind is set to `"sub:"+input.subagent_type`.

- [ ] **Step 1: Write the failing test**

```go
// add to internal/collector/transcript_test.go
func taskLine(id, desc, subtype string) string {
    return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"` + id + `","name":"Task","input":{"description":"` + desc + `","subagent_type":"` + subtype + `"}}]}}`
}
func resultLine(useID string) string {
    return `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + useID + `"}]}}`
}

func TestParseSubagents(t *testing.T) {
    root := t.TempDir()
    path := filepath.Join(root, "x.jsonl")
    // task A completed, task B still running
    writeFile(t, path, taskLine("A", "Implement Task 1", "general-purpose")+"\n"+
        resultLine("A")+"\n"+
        taskLine("B", "Review Task 1", "general-purpose")+"\n")

    subs := ParseSubagents(path)
    if len(subs) != 2 {
        t.Fatalf("want 2 subagents, got %d", len(subs))
    }
    byName := map[string]Session{}
    for _, s := range subs {
        byName[s.Name] = s
    }
    if !byName["Implement Task 1"].IsDone() {
        t.Error("task A should be done")
    }
    if byName["Review Task 1"].IsDone() {
        t.Error("task B should still be running")
    }
    if byName["Review Task 1"].Kind != "sub:general-purpose" {
        t.Errorf("kind=%q", byName["Review Task 1"].Kind)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/collector/ -run TestParseSubagents -v`
Expected: FAIL — `undefined: ParseSubagents`.

- [ ] **Step 3: Write minimal implementation**

```go
// add to internal/collector/transcript.go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/collector/ -run TestParseSubagents -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/transcript.go internal/collector/transcript_test.go
git commit -m "feat(collector): parse subagent tree from transcript"
```

---

### Task 5: bg job state parsing

**Files:**
- Create: `internal/collector/jobs.go`
- Test: `internal/collector/jobs_test.go`

**Interfaces:**
- Produces: `func ParseJob(root, jobID string) (state string, blocked bool, needs string, done, total int, ok bool)` — reads `<root>/jobs/<jobID>/state.json`. `state=state.json.state`; `blocked = state=="blocked"`; `needs = state.json.needs`. Progress: if `detail` matches regex `(\d+)/(\d+)\s+tasks` → `done,total` from the two numbers; else `0,0`. `ok=false` if the file is missing/unparseable.

- [ ] **Step 1: Write the failing test**

```go
// internal/collector/jobs_test.go
package collector

import (
    "path/filepath"
    "testing"
)

func TestParseJobBlockedWithProgress(t *testing.T) {
    root := t.TempDir()
    writeFile(t, filepath.Join(root, "jobs", "job1", "state.json"),
        `{"state":"blocked","detail":"pipeline 41/41 tasks done","needs":"decide: commit now?","inFlight":{"tasks":0,"queued":0}}`)

    state, blocked, needs, done, total, ok := ParseJob(root, "job1")
    if !ok || state != "blocked" || !blocked || needs != "decide: commit now?" || done != 41 || total != 41 {
        t.Fatalf("ParseJob=(%q,%v,%q,%d,%d,%v)", state, blocked, needs, done, total, ok)
    }
}

func TestParseJobRunningNoProgress(t *testing.T) {
    root := t.TempDir()
    writeFile(t, filepath.Join(root, "jobs", "job2", "state.json"), `{"state":"running","detail":"working","needs":""}`)
    state, blocked, _, done, total, ok := ParseJob(root, "job2")
    if !ok || state != "running" || blocked || done != 0 || total != 0 {
        t.Fatalf("ParseJob running=(%q,%v,%d,%d,%v)", state, blocked, done, total, ok)
    }
}

func TestParseJobMissing(t *testing.T) {
    if _, _, _, _, _, ok := ParseJob(t.TempDir(), "nope"); ok {
        t.Error("expected ok=false for missing job")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/collector/ -run TestParseJob -v`
Expected: FAIL — `undefined: ParseJob`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/collector/jobs.go
package collector

import (
    "encoding/json"
    "os"
    "path/filepath"
    "regexp"
    "strconv"
)

var tasksRe = regexp.MustCompile(`(\d+)/(\d+)\s+tasks`)

func ParseJob(root, jobID string) (state string, blocked bool, needs string, done, total int, ok bool) {
    data, err := os.ReadFile(filepath.Join(root, "jobs", jobID, "state.json"))
    if err != nil {
        return "", false, "", 0, 0, false
    }
    var j struct {
        State  string `json:"state"`
        Detail string `json:"detail"`
        Needs  string `json:"needs"`
    }
    if json.Unmarshal(data, &j) != nil {
        return "", false, "", 0, 0, false
    }
    if m := tasksRe.FindStringSubmatch(j.Detail); m != nil {
        done, _ = strconv.Atoi(m[1])
        total, _ = strconv.Atoi(m[2])
    }
    return j.State, j.State == "blocked", j.Needs, done, total, true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/collector/ -run TestParseJob -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/jobs.go internal/collector/jobs_test.go
git commit -m "feat(collector): parse bg job state.json"
```

---

### Task 6: Collect orchestration

**Files:**
- Create: `internal/collector/collector.go`
- Test: `internal/collector/collector_test.go`

**Interfaces:**
- Consumes: `ScanSessions`, `TranscriptPath`, `ParseTodos`, `ParseSubagents`, `ParseJob`.
- Produces: `func Collect(root string) ([]Session, error)` — for each live session: if `Kind=="bg"` and its `jobID` resolves via `ParseJob`, fill `JobState/Blocked/NeedsHint/Done/Total` and set `Mode` (Determinate if `total>0`, else Indeterminate); otherwise (interactive) run `ParseTodos` on its transcript (Determinate if found, else Indeterminate) and attach `ParseSubagents` as `Children`. Result is sorted by `Project` then `Name` for stable rendering.

- [ ] **Step 1: Write the failing test**

```go
// internal/collector/collector_test.go
package collector

import (
    "os"
    "path/filepath"
    "testing"
)

func TestCollectInteractiveWithTodosAndSubagent(t *testing.T) {
    root := t.TempDir()
    alive := os.Getpid()
    cwd := "/home/u/proj"
    writeFile(t, filepath.Join(root, "sessions", "1.json"),
        `{"pid":`+itoa(alive)+`,"sessionId":"sess1","cwd":"`+cwd+`","kind":"interactive","name":"work","status":"busy","updatedAt":5,"jobId":""}`)
    tp := TranscriptPath(root, cwd, "sess1")
    writeFile(t, tp, todoLine(`[{"content":"a","status":"completed","activeForm":"a"},{"content":"b","status":"pending","activeForm":"b"}]`)+"\n"+
        taskLine("B", "Review Task 1", "general-purpose")+"\n")

    sessions, err := Collect(root)
    if err != nil {
        t.Fatal(err)
    }
    if len(sessions) != 1 {
        t.Fatalf("want 1, got %d", len(sessions))
    }
    s := sessions[0]
    if s.Mode != Determinate || s.Done != 1 || s.Total != 2 {
        t.Errorf("progress wrong: mode=%v done=%d total=%d", s.Mode, s.Done, s.Total)
    }
    if len(s.Children) != 1 || s.Children[0].Name != "Review Task 1" {
        t.Errorf("subagent not attached: %+v", s.Children)
    }
}

func TestCollectBgBlocked(t *testing.T) {
    root := t.TempDir()
    alive := os.Getpid()
    writeFile(t, filepath.Join(root, "sessions", "2.json"),
        `{"pid":`+itoa(alive)+`,"sessionId":"sess2","cwd":"/home/u/proj","kind":"bg","name":"bgjob","status":"busy","updatedAt":9,"jobId":"job1"}`)
    writeFile(t, filepath.Join(root, "jobs", "job1", "state.json"),
        `{"state":"blocked","detail":"pipeline 41/41 tasks done","needs":"decide: commit?"}`)

    sessions, _ := Collect(root)
    if len(sessions) != 1 {
        t.Fatalf("want 1, got %d", len(sessions))
    }
    s := sessions[0]
    if !s.Blocked || s.JobState != "blocked" || s.Done != 41 || s.Total != 41 || s.Mode != Determinate {
        t.Errorf("bg blocked session wrong: %+v", s)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/collector/ -run TestCollect -v`
Expected: FAIL — `undefined: Collect`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/collector/collector.go
package collector

import "sort"

func Collect(root string) ([]Session, error) {
    sessions, err := ScanSessions(root)
    if err != nil {
        return nil, err
    }
    for i := range sessions {
        s := &sessions[i]
        if s.Kind == "bg" && s.jobID != "" {
            if state, blocked, needs, done, total, ok := ParseJob(root, s.jobID); ok {
                s.JobState, s.Blocked, s.NeedsHint, s.Done, s.Total = state, blocked, needs, done, total
                if total > 0 {
                    s.Mode = Determinate
                } else {
                    s.Mode = Indeterminate
                }
                continue
            }
        }
        // interactive (or bg without a job file): use transcript
        tp := TranscriptPath(root, s.Cwd, s.ID)
        if done, total, found := ParseTodos(tp); found {
            s.Mode, s.Done, s.Total = Determinate, done, total
        } else {
            s.Mode = Indeterminate
        }
        s.Children = ParseSubagents(tp)
    }
    sort.Slice(sessions, func(a, b int) bool {
        if sessions[a].Project != sessions[b].Project {
            return sessions[a].Project < sessions[b].Project
        }
        return sessions[a].Name < sessions[b].Name
    })
    return sessions, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/collector/ -run TestCollect -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/collector/collector.go internal/collector/collector_test.go
git commit -m "feat(collector): Collect orchestration for sessions/jobs/subagents"
```

---

### Task 7: Incremental transcript read (tail + offset)

**Files:**
- Modify: `internal/collector/transcript.go`
- Modify: `internal/collector/collector.go`
- Test: `internal/collector/scanner_test.go`

**Interfaces:**
- Produces: `type Scanner struct{ ... }` with `func NewScanner() *Scanner` and `func (sc *Scanner) Scan(path string) (done, total int, todosFound bool, subs []Session)`. It remembers, **per path**, the byte offset already consumed plus the cumulative todo state and subagent spawn/result maps, so calling `Scan` repeatedly after appends yields the same result as parsing the whole file once. On file shrink/rotation (current size < stored offset) it resets that path's state.
- `Collect` gains a `*Scanner` parameter: `func Collect(root string, sc *Scanner) ([]Session, error)`. Update Task 6 call sites/tests accordingly (pass `NewScanner()`).

**Note:** This task supersedes the standalone `ParseTodos`/`ParseSubagents` calls inside `Collect` with a single stateful `Scan`. Keep `ParseTodos`/`ParseSubagents` (still unit-tested) — `Scan` may reuse their line-parsing logic, but must maintain offset state.

- [ ] **Step 1: Write the failing test**

```go
// internal/collector/scanner_test.go
package collector

import (
    "os"
    "path/filepath"
    "testing"
)

func TestScannerIncrementalEqualsFull(t *testing.T) {
    root := t.TempDir()
    path := filepath.Join(root, "t.jsonl")
    chunk1 := todoLine(`[{"content":"a","status":"completed","activeForm":"a"},{"content":"b","status":"pending","activeForm":"b"}]`) + "\n" +
        taskLine("A", "Task A", "general-purpose") + "\n"
    chunk2 := resultLine("A") + "\n" +
        todoLine(`[{"content":"a","status":"completed","activeForm":"a"},{"content":"b","status":"completed","activeForm":"b"}]`) + "\n"

    // Incremental: write chunk1, scan; append chunk2, scan again.
    writeFile(t, path, chunk1)
    sc := NewScanner()
    sc.Scan(path)
    appendFile(t, path, chunk2)
    done, total, found, subs := sc.Scan(path)

    // Full: parse whole file fresh.
    full := NewScanner()
    fDone, fTotal, fFound, fSubs := full.Scan(path)

    if done != fDone || total != fTotal || found != fFound {
        t.Errorf("todos incremental=(%d,%d,%v) full=(%d,%d,%v)", done, total, found, fDone, fTotal, fFound)
    }
    if len(subs) != len(fSubs) || len(subs) != 1 || !subs[0].IsDone() {
        t.Errorf("subs incremental=%+v full=%+v", subs, fSubs)
    }
}

func appendFile(t *testing.T, path, content string) {
    t.Helper()
    f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
    if err != nil {
        t.Fatal(err)
    }
    defer f.Close()
    if _, err := f.WriteString(content); err != nil {
        t.Fatal(err)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/collector/ -run TestScannerIncremental -v`
Expected: FAIL — `undefined: NewScanner`.

- [ ] **Step 3: Write minimal implementation**

```go
// add to internal/collector/transcript.go
import "io" // add to existing import block

type scanState struct {
    offset    int64
    haveTodos bool
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

func (sc *Scanner) Scan(path string) (done, total int, todosFound bool, subs []Session) {
    st := sc.states[path]
    if st == nil {
        st = &scanState{spawns: map[string]struct{ name, subtype string }{}, doneSub: map[string]bool{}}
        sc.states[path] = st
    }
    f, err := os.Open(path)
    if err != nil {
        return st.done, st.total, st.haveTodos, st.buildSubs()
    }
    defer f.Close()
    if info, err := f.Stat(); err == nil && info.Size() < st.offset {
        *st = scanState{spawns: map[string]struct{ name, subtype string }{}, doneSub: map[string]bool{}} // rotated
    }
    if _, err := f.Seek(st.offset, io.SeekStart); err != nil {
        return st.done, st.total, st.haveTodos, st.buildSubs()
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
    return st.done, st.total, st.haveTodos, st.buildSubs()
}

func (st *scanState) apply(raw []byte) {
    var line transcriptLine
    if json.Unmarshal(raw, &line) != nil {
        return
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
```

Then update `Collect` (Task 6) to use the scanner:

```go
// change signature and interactive branch in internal/collector/collector.go
func Collect(root string, sc *Scanner) ([]Session, error) {
    // ... unchanged up to the interactive branch ...
        // interactive (or bg without a job file): use transcript scanner
        tp := TranscriptPath(root, s.Cwd, s.ID)
        done, total, found, subs := sc.Scan(tp)
        if found {
            s.Mode, s.Done, s.Total = Determinate, done, total
        } else {
            s.Mode = Indeterminate
        }
        s.Children = subs
    // ...
}
```

Update Task 6 tests to call `Collect(root, NewScanner())`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/collector/ -v`
Expected: PASS (all collector tests, including updated `TestCollect*`).

- [ ] **Step 5: Commit**

```bash
git add internal/collector/
git commit -m "feat(collector): incremental tail scanner with offset state"
```

---

### Task 8: Render one pillar bar

**Files:**
- Create: `internal/render/bar.go`
- Test: `internal/render/bar_test.go`

**Interfaces:**
- Consumes: `collector.Session`.
- Produces: `func RenderBar(s collector.Session, width, phase int) string` and `func Label(s collector.Session) string`.
  - Determinate: `filled=round(Fraction*width)`; output `strings.Repeat("█", filled)`, then if `!IsDone() && filled<width` a single `▓`, then `⋮` for the remainder — total rune width exactly `width`.
  - Indeterminate running (`!IsDone()`): a 3-rune `▓▓▓` block on a `⋮` track, its left index bouncing in `[0, width-3]` as a function of `phase`.
  - Done: `strings.Repeat("█", width)`.
  - `Label`: `Blocked`→`"⏸ blocked"`; Determinate→`"done"` if IsDone else `fmt.Sprintf("%d/%d", Done, Total)`; Indeterminate→`"done"` if IsDone else `"sweep"`.

- [ ] **Step 1: Write the failing test**

```go
// internal/render/bar_test.go
package render

import (
    "strings"
    "testing"

    "agentmon/internal/collector"
)

func runeLen(s string) int { return len([]rune(s)) }

func TestRenderBarDeterminate(t *testing.T) {
    s := collector.Session{Mode: collector.Determinate, Done: 6, Total: 10, Status: "busy"}
    bar := RenderBar(s, 10, 0)
    if runeLen(bar) != 10 {
        t.Fatalf("width=%d want 10 (%q)", runeLen(bar), bar)
    }
    if strings.Count(bar, "█") != 6 {
        t.Errorf("filled=%d want 6 (%q)", strings.Count(bar, "█"), bar)
    }
    if !strings.Contains(bar, "▓") {
        t.Errorf("expected wavefront in %q", bar)
    }
}

func TestRenderBarDone(t *testing.T) {
    s := collector.Session{Mode: collector.Indeterminate, Status: "idle"}
    bar := RenderBar(s, 8, 3)
    if bar != strings.Repeat("█", 8) {
        t.Errorf("done bar=%q", bar)
    }
}

func TestRenderBarSweepWidthStable(t *testing.T) {
    s := collector.Session{Mode: collector.Indeterminate, Status: "busy"}
    for phase := 0; phase < 20; phase++ {
        bar := RenderBar(s, 12, phase)
        if runeLen(bar) != 12 {
            t.Fatalf("phase %d width=%d want 12 (%q)", phase, runeLen(bar), bar)
        }
        if strings.Count(bar, "▓") != 3 {
            t.Errorf("phase %d sweep block=%d want 3", phase, strings.Count(bar, "▓"))
        }
    }
}

func TestLabel(t *testing.T) {
    if l := Label(collector.Session{Kind: "bg", JobState: "blocked", Blocked: true}); l != "⏸ blocked" {
        t.Errorf("blocked label=%q", l)
    }
    if l := Label(collector.Session{Mode: collector.Determinate, Done: 6, Total: 10, Status: "busy"}); l != "6/10" {
        t.Errorf("determinate label=%q", l)
    }
    if l := Label(collector.Session{Mode: collector.Indeterminate, Status: "busy"}); l != "sweep" {
        t.Errorf("sweep label=%q", l)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/render/ -v`
Expected: FAIL — `undefined: RenderBar` / `Label`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/render/bar.go
package render

import (
    "fmt"
    "math"
    "strings"

    "agentmon/internal/collector"
)

const (
    empty = "⋮"
    full  = "█"
    wave  = "▓"
)

func RenderBar(s collector.Session, width, phase int) string {
    if width < 1 {
        width = 1
    }
    if s.IsDone() {
        return strings.Repeat(full, width)
    }
    if s.Mode == collector.Determinate {
        filled := int(math.Round(s.Fraction() * float64(width)))
        if filled > width {
            filled = width
        }
        b := strings.Repeat(full, filled)
        rest := width - filled
        if rest > 0 {
            b += wave
            rest--
        }
        return b + strings.Repeat(empty, rest)
    }
    // Indeterminate running: bounce a 3-wide block.
    block := 3
    if block > width {
        block = width
    }
    span := width - block
    pos := 0
    if span > 0 {
        cycle := 2 * span
        p := phase % cycle
        if p <= span {
            pos = p
        } else {
            pos = cycle - p
        }
    }
    runes := make([]string, width)
    for i := range runes {
        runes[i] = empty
    }
    for i := 0; i < block; i++ {
        runes[pos+i] = wave
    }
    return strings.Join(runes, "")
}

func Label(s collector.Session) string {
    if s.Blocked {
        return "⏸ blocked"
    }
    if s.IsDone() {
        return "done"
    }
    if s.Mode == collector.Determinate {
        return fmt.Sprintf("%d/%d", s.Done, s.Total)
    }
    return "sweep"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/render/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/render/bar.go internal/render/bar_test.go
git commit -m "feat(render): pillar bar + label rendering"
```

---

### Task 9: Render full view (grouping + subagent tree)

**Files:**
- Create: `internal/render/view.go`
- Test: `internal/render/view_test.go`

**Interfaces:**
- Consumes: `RenderBar`, `Label`, `collector.Session`.
- Produces: `func RenderView(sessions []collector.Session, barWidth, phase int) string` — groups top-level sessions under a `▸ <project>` header (sessions arrive pre-sorted by project); each session renders `  <name padded>  <bar>  <label>`; children render indented with `├─ / └─` branch glyphs and their own bar/label; a bg blocked session appends a `needs: <NeedsHint>` line.

- [ ] **Step 1: Write the failing test**

```go
// internal/render/view_test.go
package render

import (
    "strings"
    "testing"

    "agentmon/internal/collector"
)

func TestRenderViewGroupsAndTree(t *testing.T) {
    sessions := []collector.Session{
        {Name: "work", Project: "proj", Kind: "interactive", Mode: collector.Determinate, Done: 6, Total: 10, Status: "busy",
            Children: []collector.Session{
                {Name: "Review Task 6", Kind: "sub:general-purpose", Mode: collector.Indeterminate, Status: "idle"},
            }},
        {Name: "bgjob", Project: "proj", Kind: "bg", JobState: "blocked", Blocked: true, NeedsHint: "commit now?", Mode: collector.Determinate, Done: 41, Total: 41},
    }
    out := RenderView(sessions, 12, 0)

    if !strings.Contains(out, "▸ proj") {
        t.Errorf("missing project header:\n%s", out)
    }
    if !strings.Contains(out, "└─") {
        t.Errorf("missing tree branch:\n%s", out)
    }
    if !strings.Contains(out, "6/10") || !strings.Contains(out, "⏸ blocked") {
        t.Errorf("missing labels:\n%s", out)
    }
    if !strings.Contains(out, "needs: commit now?") {
        t.Errorf("missing needs hint:\n%s", out)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/render/ -run TestRenderView -v`
Expected: FAIL — `undefined: RenderView`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/render/view.go
package render

import (
    "fmt"
    "strings"

    "agentmon/internal/collector"
)

func RenderView(sessions []collector.Session, barWidth, phase int) string {
    var b strings.Builder
    curProject := ""
    for _, s := range sessions {
        if s.Project != curProject {
            if curProject != "" {
                b.WriteString("\n")
            }
            fmt.Fprintf(&b, "▸ %s\n", s.Project)
            curProject = s.Project
        }
        fmt.Fprintf(&b, "   %-22s %s  %s\n", truncate(s.Name, 22), RenderBar(s, barWidth, phase), Label(s))
        if s.Blocked && s.NeedsHint != "" {
            fmt.Fprintf(&b, "   %-22s needs: %s\n", "", truncate(s.NeedsHint, 60))
        }
        for i, c := range s.Children {
            branch := "├─"
            if i == len(s.Children)-1 {
                branch = "└─"
            }
            fmt.Fprintf(&b, "      %s %-16s %s  %s\n", branch, truncate(c.Name, 16), RenderBar(c, barWidth, phase), Label(c))
        }
    }
    return b.String()
}

func truncate(s string, n int) string {
    r := []rune(s)
    if len(r) <= n {
        return s
    }
    if n <= 1 {
        return string(r[:n])
    }
    return string(r[:n-1]) + "…"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/render/ -run TestRenderView -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/render/view.go internal/render/view_test.go
git commit -m "feat(render): grouped view with subagent tree"
```

---

### Task 10: Chime event edge-detection

**Files:**
- Create: `internal/model/events.go`
- Test: `internal/model/events_test.go`

**Interfaces:**
- Consumes: `collector.Session`.
- Produces: `type EventKind int` (`DoneEvent=0`, `ApprovalEvent=1`); `type Event struct{ Kind EventKind; SessionID string }`; `func DiffEvents(prev, cur []collector.Session) []Event`. It flattens each snapshot (top-level sessions **and** their children) keyed by `ID`, then emits: `DoneEvent` when a key goes `!IsDone → IsDone`; `ApprovalEvent` when a key goes `!Blocked → Blocked`. Keys absent in `prev` use a zero-value baseline (`IsDone=false, Blocked=false`), so a session that first appears already blocked emits `ApprovalEvent`, and one that first appears already done emits `DoneEvent`.

- [ ] **Step 1: Write the failing test**

```go
// internal/model/events_test.go
package model

import (
    "testing"

    "agentmon/internal/collector"
)

func TestDiffEventsDoneTransition() {}

func TestDiffEvents(t *testing.T) {
    prev := []collector.Session{
        {ID: "a", Mode: collector.Determinate, Done: 6, Total: 10}, // not done
        {ID: "b", Kind: "bg", JobState: "running"},                 // not blocked
    }
    cur := []collector.Session{
        {ID: "a", Mode: collector.Determinate, Done: 10, Total: 10}, // -> done
        {ID: "b", Kind: "bg", JobState: "blocked", Blocked: true},   // -> blocked
    }
    evs := DiffEvents(prev, cur)
    got := map[string]EventKind{}
    for _, e := range evs {
        got[e.SessionID] = e.Kind
    }
    if got["a"] != DoneEvent {
        t.Errorf("a: want DoneEvent, got %v", got["a"])
    }
    if got["b"] != ApprovalEvent {
        t.Errorf("b: want ApprovalEvent, got %v", got["b"])
    }
}

func TestDiffEventsNoRepeat(t *testing.T) {
    done := []collector.Session{{ID: "a", Mode: collector.Determinate, Done: 10, Total: 10}}
    if evs := DiffEvents(done, done); len(evs) != 0 {
        t.Errorf("stable done should emit nothing, got %v", evs)
    }
}

func TestDiffEventsChildDone(t *testing.T) {
    prev := []collector.Session{{ID: "p", Status: "busy", Mode: collector.Indeterminate,
        Children: []collector.Session{{ID: "c", Mode: collector.Indeterminate, Status: "busy"}}}}
    cur := []collector.Session{{ID: "p", Status: "busy", Mode: collector.Indeterminate,
        Children: []collector.Session{{ID: "c", Mode: collector.Indeterminate, Status: "idle"}}}}
    evs := DiffEvents(prev, cur)
    if len(evs) != 1 || evs[0].SessionID != "c" || evs[0].Kind != DoneEvent {
        t.Errorf("want child DoneEvent, got %v", evs)
    }
}
```

Delete the stray empty `TestDiffEventsDoneTransition` stub before running (it exists only to remind you children must be covered — Task 10's real coverage is the three tests below it).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/model/ -run TestDiffEvents -v`
Expected: FAIL — `undefined: DiffEvents`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/model/events.go
package model

import "agentmon/internal/collector"

type EventKind int

const (
    DoneEvent EventKind = iota
    ApprovalEvent
)

type Event struct {
    Kind      EventKind
    SessionID string
}

type flatState struct {
    done    bool
    blocked bool
}

func flatten(sessions []collector.Session, into map[string]flatState) {
    for _, s := range sessions {
        into[s.ID] = flatState{done: s.IsDone(), blocked: s.Blocked}
        if len(s.Children) > 0 {
            flatten(s.Children, into)
        }
    }
}

func DiffEvents(prev, cur []collector.Session) []Event {
    prevMap := map[string]flatState{}
    curMap := map[string]flatState{}
    flatten(prev, prevMap)
    flatten(cur, curMap)

    var evs []Event
    for _, s := range flattenOrder(cur) {
        p := prevMap[s] // zero value if absent: {false,false}
        c := curMap[s]
        if !p.done && c.done {
            evs = append(evs, Event{Kind: DoneEvent, SessionID: s})
        }
        if !p.blocked && c.blocked {
            evs = append(evs, Event{Kind: ApprovalEvent, SessionID: s})
        }
    }
    return evs
}

// flattenOrder returns IDs in a stable top-down, children-after-parent order.
func flattenOrder(sessions []collector.Session) []string {
    var ids []string
    var walk func([]collector.Session)
    walk = func(ss []collector.Session) {
        for _, s := range ss {
            ids = append(ids, s.ID)
            walk(s.Children)
        }
    }
    walk(sessions)
    return ids
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/model/ -run TestDiffEvents -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/model/events.go internal/model/events_test.go
git commit -m "feat(model): edge-detect done/approval chime events"
```

---

### Task 11: Sound synthesis with silent fallback

**Files:**
- Create: `internal/sound/sound.go`
- Test: `internal/sound/sound_test.go`

**Interfaces:**
- Consumes: `model.EventKind` is NOT imported (avoid cycle) — sound defines its own `type Chime int` (`ChimeDone=0`, `ChimeApproval=1`).
- Produces: `func Synth(c Chime, sampleRate int) []byte` — returns signed 16-bit LE mono PCM for a two-note "ting-ting" motif (ChimeDone rises E5→A5; ChimeApproval repeats A5–A5), amplitude ≤ 0.2, each note fades out (envelope reaches ~0 at note end). `func NewPlayer() *Player`; `func (p *Player) Play(c Chime)` (non-blocking; no-op if audio unavailable); `func (p *Player) Enabled() bool`.
- Add dependency `github.com/hajimehoshi/oto/v2`.

- [ ] **Step 1: Add the audio dependency**

Run:
```bash
go get github.com/hajimehoshi/oto/v2@v2.4.2
```

- [ ] **Step 2: Write the failing test**

```go
// internal/sound/sound_test.go
package sound

import "testing"

const sr = 44100

func TestSynthLengthAndBounds(t *testing.T) {
    pcm := Synth(ChimeDone, sr)
    if len(pcm) == 0 || len(pcm)%2 != 0 {
        t.Fatalf("pcm bytes=%d (must be non-zero, even)", len(pcm))
    }
    // Amplitude bound: no sample magnitude exceeds 0.2*32767 with headroom.
    limit := int16(0.25 * 32767)
    for i := 0; i+1 < len(pcm); i += 2 {
        s := int16(pcm[i]) | int16(pcm[i+1])<<8
        if s > limit || s < -limit {
            t.Fatalf("sample %d exceeds amplitude bound: %d", i/2, s)
        }
    }
}

func TestSynthFadesOut(t *testing.T) {
    pcm := Synth(ChimeApproval, sr)
    n := len(pcm)
    // Last sample should be near zero (fade-out envelope).
    last := int16(pcm[n-2]) | int16(pcm[n-1])<<8
    if last > 400 || last < -400 {
        t.Errorf("expected fade-out near zero, last sample=%d", last)
    }
}

func TestSynthDistinctMotifs(t *testing.T) {
    if string(Synth(ChimeDone, sr)) == string(Synth(ChimeApproval, sr)) {
        t.Error("done and approval motifs must differ")
    }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/sound/ -v`
Expected: FAIL — `undefined: Synth` / `ChimeDone`.

- [ ] **Step 4: Write minimal implementation**

```go
// internal/sound/sound.go
package sound

import (
    "bytes"
    "log"
    "math"
    "sync"

    "github.com/hajimehoshi/oto/v2"
)

type Chime int

const (
    ChimeDone Chime = iota
    ChimeApproval
)

const (
    sampleRate = 44100
    amplitude  = 0.18 // ≤ 0.2
    noteDur    = 0.12 // 120ms per note
    gapDur     = 0.05
)

// note frequencies (Hz), kept within E5..A5.
const (
    e5 = 659.25
    a5 = 880.0
)

func motif(c Chime) []float64 {
    switch c {
    case ChimeApproval:
        return []float64{a5, a5}
    default: // ChimeDone rises
        return []float64{e5, a5}
    }
}

// Synth renders a two-note motif to int16 LE mono PCM.
func Synth(c Chime, sr int) []byte {
    var buf bytes.Buffer
    notes := motif(c)
    noteSamples := int(noteDur * float64(sr))
    gapSamples := int(gapDur * float64(sr))
    for ni, freq := range notes {
        for i := 0; i < noteSamples; i++ {
            t := float64(i) / float64(sr)
            // Linear fade-out envelope so the tail reaches zero.
            env := 1.0 - float64(i)/float64(noteSamples)
            v := amplitude * env * math.Sin(2*math.Pi*freq*t)
            s := int16(v * 32767)
            buf.WriteByte(byte(s))
            buf.WriteByte(byte(s >> 8))
        }
        if ni < len(notes)-1 {
            for i := 0; i < gapSamples; i++ {
                buf.WriteByte(0)
                buf.WriteByte(0)
            }
        }
    }
    return buf.Bytes()
}

type Player struct {
    ctx     *oto.Context
    enabled bool
    mu      sync.Mutex
    pcm     map[Chime][]byte
}

var initOnce sync.Once

func NewPlayer() *Player {
    p := &Player{pcm: map[Chime][]byte{
        ChimeDone:     Synth(ChimeDone, sampleRate),
        ChimeApproval: Synth(ChimeApproval, sampleRate),
    }}
    ctx, ready, err := oto.NewContext(sampleRate, 1, 2)
    if err != nil {
        initOnce.Do(func() { log.Printf("agentmon: audio disabled (%v)", err) })
        return p
    }
    <-ready
    p.ctx = ctx
    p.enabled = true
    return p
}

func (p *Player) Enabled() bool { return p.enabled }

func (p *Player) Play(c Chime) {
    if !p.enabled {
        return
    }
    p.mu.Lock()
    data := p.pcm[c]
    p.mu.Unlock()
    go func() {
        player := p.ctx.NewPlayer(bytes.NewReader(data))
        player.Play()
        // Player plays asynchronously; keep a reference until drained.
        for player.IsPlaying() {
        }
        player.Close()
    }()
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/sound/ -v`
Expected: PASS. (Tests exercise `Synth` only; `NewPlayer`/`Play` are manually verified in Task 13.)

- [ ] **Step 6: Commit**

```bash
git add internal/sound/ go.mod go.sum
git commit -m "feat(sound): synth gentle done/approval chimes with silent fallback"
```

---

### Task 12: Bubble Tea model (ticks, keys, view)

**Files:**
- Create: `internal/model/model.go`
- Test: `internal/model/model_test.go`

**Interfaces:**
- Consumes: `collector.Collect`, `collector.NewScanner`, `render.RenderView`, `DiffEvents`, `sound.Player`.
- Produces: `type Model` and `func New(root string, player *sound.Player, interval time.Duration) Model`; standard `Init/Update/View`. Messages: `pollMsg []collector.Session` (result of a poll), `animMsg` (advance `phase`), `tea.KeyMsg`. Keys: `q`/`ctrl+c` quit; `s` toggles `soundOn`; `up`/`down` adjust `scroll`; `c` toggles `collapsed` (hide children).
- Produces (testable pure helper): `func (m Model) applyPoll(sessions []collector.Session) (Model, []Event)` — stores new snapshot, returns fired events (used by `Update` to call `player.Play`). This is the unit-tested seam; the live `tea` loop is manually verified.

- [ ] **Step 1: Write the failing test**

```go
// internal/model/model_test.go
package model

import (
    "testing"
    "time"

    "agentmon/internal/collector"
)

func TestApplyPollFiresEvents(t *testing.T) {
    m := New("/fake", nil, time.Second)
    m.sessions = []collector.Session{{ID: "a", Mode: collector.Determinate, Done: 6, Total: 10}}
    m2, evs := m.applyPoll([]collector.Session{{ID: "a", Mode: collector.Determinate, Done: 10, Total: 10}})
    if len(evs) != 1 || evs[0].Kind != DoneEvent {
        t.Fatalf("want one DoneEvent, got %v", evs)
    }
    if len(m2.sessions) != 1 || !m2.sessions[0].IsDone() {
        t.Errorf("snapshot not updated: %+v", m2.sessions)
    }
}

func TestToggleSound(t *testing.T) {
    m := New("/fake", nil, time.Second)
    if !m.soundOn {
        t.Fatal("sound should default on")
    }
    m = m.toggleSound()
    if m.soundOn {
        t.Error("sound should be off after toggle")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/model/ -run 'TestApplyPoll|TestToggleSound' -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/model/model.go
package model

import (
    "time"

    tea "github.com/charmbracelet/bubbletea"

    "agentmon/internal/collector"
    "agentmon/internal/render"
    "agentmon/internal/sound"
)

const barWidth = 16

type pollMsg []collector.Session
type animMsg struct{}

type Model struct {
    root     string
    player   *sound.Player
    scanner  *collector.Scanner
    interval time.Duration
    sessions []collector.Session
    phase    int
    scroll   int
    soundOn  bool
    collapse bool
}

func New(root string, player *sound.Player, interval time.Duration) Model {
    return Model{root: root, player: player, scanner: collector.NewScanner(), interval: interval, soundOn: true}
}

func (m Model) Init() tea.Cmd {
    return tea.Batch(m.pollCmd(), m.animCmd())
}

func (m Model) pollCmd() tea.Cmd {
    return func() tea.Msg {
        sessions, _ := collector.Collect(m.root, m.scanner)
        return pollMsg(sessions)
    }
}

func (m Model) animCmd() tea.Cmd {
    return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return animMsg{} })
}

func (m Model) scheduleNextPoll() tea.Cmd {
    return tea.Tick(m.interval, func(time.Time) tea.Msg { return pollTickMsg{} })
}

type pollTickMsg struct{}

func (m Model) applyPoll(sessions []collector.Session) (Model, []Event) {
    evs := DiffEvents(m.sessions, sessions)
    m.sessions = sessions
    return m, evs
}

func (m Model) toggleSound() Model { m.soundOn = !m.soundOn; return m }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch msg.String() {
        case "q", "ctrl+c":
            return m, tea.Quit
        case "s":
            return m.toggleSound(), nil
        case "c":
            m.collapse = !m.collapse
            return m, nil
        case "up":
            if m.scroll > 0 {
                m.scroll--
            }
            return m, nil
        case "down":
            m.scroll++
            return m, nil
        }
    case pollMsg:
        m2, evs := m.applyPoll([]collector.Session(msg))
        if m2.soundOn && m2.player != nil {
            for _, e := range evs {
                switch e.Kind {
                case DoneEvent:
                    m2.player.Play(sound.ChimeDone)
                case ApprovalEvent:
                    m2.player.Play(sound.ChimeApproval)
                }
            }
        }
        return m2, m2.scheduleNextPoll()
    case pollTickMsg:
        return m, m.pollCmd()
    case animMsg:
        m.phase++
        return m, m.animCmd()
    }
    return m, nil
}

func (m Model) View() string {
    view := m.sessions
    if m.collapse {
        view = stripChildren(view)
    }
    header := "agentmon — q quit · s sound · c tree · ↑/↓ scroll\n\n"
    if len(view) == 0 {
        return header + "  (no live sessions)\n"
    }
    return header + render.RenderView(view, barWidth, m.phase)
}

func stripChildren(sessions []collector.Session) []collector.Session {
    out := make([]collector.Session, len(sessions))
    copy(out, sessions)
    for i := range out {
        out[i].Children = nil
    }
    return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/model/ -v`
Expected: PASS (events + model tests).

- [ ] **Step 5: Commit**

```bash
git add internal/model/model.go internal/model/model_test.go go.mod go.sum
git commit -m "feat(model): Bubble Tea model with poll/anim ticks and key handling"
```

---

### Task 13: main wiring + flags, end-to-end manual verification

**Files:**
- Create: `main.go`

**Interfaces:**
- Consumes: everything. Flags via stdlib `flag`: `--no-sound` (bool), `--interval` (duration, default `1s`).

- [ ] **Step 1: Write `main.go`**

```go
// main.go
package main

import (
    "flag"
    "fmt"
    "os"
    "path/filepath"
    "time"

    tea "github.com/charmbracelet/bubbletea"

    "agentmon/internal/model"
    "agentmon/internal/sound"
)

func main() {
    noSound := flag.Bool("no-sound", false, "disable chimes")
    interval := flag.Duration("interval", time.Second, "poll interval")
    flag.Parse()

    home, err := os.UserHomeDir()
    if err != nil {
        fmt.Fprintln(os.Stderr, "cannot resolve home dir:", err)
        os.Exit(1)
    }
    root := filepath.Join(home, ".claude")

    var player *sound.Player
    if !*noSound {
        player = sound.NewPlayer()
    }

    m := model.New(root, player, *interval)
    if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
        fmt.Fprintln(os.Stderr, "agentmon error:", err)
        os.Exit(1)
    }
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: builds with no errors.

- [ ] **Step 3: Vet + full test suite**

Run: `go vet ./... && go test ./...`
Expected: all packages PASS.

- [ ] **Step 4: Manual run (real sessions)**

Run: `go run . --interval 1s`
Verify by observation:
- Live sessions render, grouped by project, dead PIDs absent.
- Determinate sessions show `N/M`, indeterminate busy ones show a moving `▓▓▓` sweep, idle ones show a full bar `done`.
- A bg job in `blocked` state shows `⏸ blocked` + `needs:` line.
- Subagents appear as `├─/└─` children.
- `s` toggles sound, `c` collapses the tree, `q` quits.

Run once with audio available and once on WSL2 without audio: confirm no crash either way (silent fallback), and a completing session rings once (not repeatedly).

- [ ] **Step 5: Commit**

```bash
git add main.go
git commit -m "feat: main wiring, flags, alt-screen TUI"
```

- [ ] **Step 6: Final `go mod tidy` and commit if changed**

```bash
go mod tidy
git add go.mod go.sum
git diff --cached --quiet || git commit -m "chore: go mod tidy"
```

---

## Self-Review (completed during authoring)

- **Spec coverage:** data sources (Task 2/3/5), auto-detect progress (Task 6), bar glyphs + wavefront/sweep (Task 8), idle=done (Task 1), subagent tree (Task 4/9), done chime edge-detect (Task 10), approval chime bg-only (Task 10), gentle synth + WSL2 silent fallback (Task 11), poll 1s / anim 100ms (Task 12), keys (Task 12), tail+offset efficient read (Task 7), read-only (all tasks use fixture roots; main is the only `$HOME` reader). All spec sections map to a task.
- **Placeholder scan:** no TBD/TODO; all steps carry real code and exact run commands. The one intentional stub (`TestDiffEventsDoneTransition`) is explicitly deleted in Task 10 Step 1's note.
- **Type consistency:** `Session`, `ProgressMode`, `IsDone`, `Fraction`, `Scanner`/`NewScanner`/`Scan`, `Collect(root, *Scanner)`, `RenderBar`/`Label`/`RenderView`, `Event`/`EventKind`/`DiffEvents`, `Chime`/`Synth`/`Player`, `model.New` — names and signatures are used identically across tasks. `Collect`'s signature change in Task 7 is called out with its test/callsite update.
