# Stale Agent Lifecycle Fix

## Problem

Agentmon can show an idle agent as running for two independent reasons:

1. Codex rollout reduction retains every unmatched historical `task_started` event in an open-turn set. A newer turn can finish while an older malformed or superseded turn keeps the session busy forever.
2. Linux process discovery treats stopped, traced, zombie, and dead processes as live because it filters only by effective UID and PID presence.

## Design

### Codex lifecycle

Treat a rollout as having one current turn.

- `task_started(turnID)` replaces any earlier current turn.
- `task_complete(turnID)` or `task_aborted(turnID)` completes the rollout only when `turnID` matches the current turn.
- A terminal event for an older or unknown turn is ignored.
- A new start after completion reopens the session.

This matches the UI requirement: display current work, not accumulated malformed history. No timeout heuristic is introduced.

### Linux process eligibility

Parse process state from `/proc/<pid>/stat` and exclude states `T`, `t`, `Z`, `X`, and `x` before reading argv, environment, links, file descriptors, or listeners.

Running and sleeping states remain eligible because an interactive agent commonly waits while still active.

## Scope

Modify only Codex rollout lifecycle reduction and Linux procfs eligibility. Do not change UI, fade duration, cache duration, adapter attribution, or other agent lifecycle rules.

## Failure handling

- Malformed stat data remains per-process fail-closed.
- Unknown Codex terminal events do not change lifecycle state.
- Existing current-user and passive/read-only constraints remain unchanged.

## Tests

- Historical unmatched starts cannot keep a completed current Codex turn busy.
- A newer Codex start supersedes an older current turn.
- A terminal event for a superseded turn is ignored.
- A completed rollout can reopen on a new start.
- Stopped, traced, zombie, and dead procfs fixtures are excluded.
- Running/sleeping current-user fixtures remain included.
- Focused packages, full suite, race, vet, and CGO-free build pass.
