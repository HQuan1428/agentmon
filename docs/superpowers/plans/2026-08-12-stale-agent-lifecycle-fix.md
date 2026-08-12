# Stale Agent Lifecycle Fix Implementation Plan

> **For agentic workers:** Execute inline. User explicitly waived TDD for this small fix; add regression coverage after implementation.

**Goal:** Stop idle Codex and stopped Linux processes from appearing active.

**Architecture:** Replace Codex's cumulative open-turn set with one current turn. Parse Linux proc state and reject stopped/traced/zombie/dead processes before expensive metadata reads.

**Tech Stack:** Go standard library, existing collector/procscan tests.

## Global Constraints

- Passive and read-only.
- Current OS user only.
- No timeout heuristics.
- No UI, cache-duration, or attribution changes.

---

### Task 1: Reconcile Codex current turn

**Files:**
- Modify: `internal/collector/codex_rollout.go`
- Modify: `internal/collector/codex_rollout_test.go`

- [ ] Replace `openTurns map[string]bool` with `currentTurn string`.
- [ ] Make `task_started` replace the current turn.
- [ ] Accept terminal events only for the current turn; clear it and set terminal state.
- [ ] Update lifecycle coverage for superseded starts, stale terminal events, completion, and reopen.
- [ ] Run `rtk go test ./internal/collector -run TestCodexScanner -count=1`.

### Task 2: Exclude inactive proc states

**Files:**
- Modify: `internal/procscan/procfs_linux.go`
- Modify: `internal/procscan/procfs_linux_test.go`

- [ ] Return process state from `readStat` alongside PPID and start ticks.
- [ ] Reject `T`, `t`, `Z`, `X`, and `x` before reading comm, argv, environment, links, FDs, or listeners.
- [ ] Cover excluded states and retained `R`, `S`, `D`, `I` states using procfs fixtures.
- [ ] Run `rtk go test ./internal/procscan -count=1`.

### Task 3: Verify and ship

- [ ] Run `rtk go test ./... -count=1`.
- [ ] Run `rtk go test -race ./... -count=1`.
- [ ] Run `rtk go vet ./...`.
- [ ] Run `CGO_ENABLED=0 rtk go build -o /tmp/agentmon-stale-fix .`.
- [ ] Run `rtk git diff --check` and review scoped diff.
- [ ] Commit and push `main` after review passes.
