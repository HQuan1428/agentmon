# Multi-Agent Monitoring Design

- Date: 2026-08-12
- Workflow level: Level 2 — Feature
- Status: approved in design review
- Initial platform: Linux/WSL

## Goal

Extend `agentmon` from a Claude-only monitor into a zero-configuration,
passive, read-only monitor for Claude Code, Codex, OpenCode, and Aider. It
discovers coding-agent work started by the OS user running `agentmon`, shows
the exact agent and best-evidenced runtime model, preserves the current
progress and notification behavior, and never instruments or modifies an
agent.

The UI hierarchy is:

```text
project -> agent -> session (with model) -> subagent
```

A completed turn fades for three seconds and then disappears. A new turn in
the same live session makes the session visible again.

## Product Decisions

- Use agent-specific adapters behind one normalized session contract.
- Preserve rich agent-specific data instead of reducing every agent to the
  lowest common denominator.
- Display `unknown` when a model cannot be attributed from trustworthy runtime
  evidence. Never infer a model from popularity, provider, or a stale global
  default.
- Monitor only processes owned by the effective OS user running `agentmon`.
- Remain fully passive and read-only: no hooks, plugins, injected telemetry, or
  config changes.
- Support Linux/WSL first. The interfaces must not prevent later native macOS
  and Windows implementations, but those implementations are not in scope.
- A session displays its model. A subagent does not display a model.

## Architecture

The collector becomes a coordinator. It captures one process snapshot per
poll and passes that immutable snapshot to all adapters.

```text
/proc for current UID
        |
        v
 ProcessSnapshot
        |
        +-- ClaudeAdapter ---- ~/.claude
        +-- CodexAdapter ----- ~/.codex
        +-- OpenCodeAdapter -- OpenCode data root + optional loopback GET API
        +-- AiderAdapter ----- /proc + attributed Aider metadata/history
        |
        v
 normalized []Session
        |
        v
 model -> render -> sound
```

The coordinator owns process discovery, adapter execution, result merging,
global sorting, and failure isolation. Each adapter owns its parser, tail
state, native-to-global ID mapping, and short-lived recently-active cache.

The conceptual adapter contract is:

```go
type Adapter interface {
    Agent() Agent
    Discover(ProcessSnapshot) ([]Session, error)
}
```

The exact Go signature may gain a context or clock dependency in the
implementation plan for cancellation and deterministic tests. It must not
expose vendor-specific records to the model or renderer.

## Process Discovery

`ProcessSnapshot` is collected once per poll from Linux `/proc`. It contains
only the metadata needed by adapters: PID, PPID, effective UID, executable or
command identity, argv, cwd, start time, open-file symlink metadata, and
loopback socket metadata when available.

Discovery rules:

1. Reject a process unless its effective UID equals the effective UID of
   `agentmon`.
2. Match agents by executable identity and known launch shapes. Account for
   wrapper processes such as Python launching Aider or Node launching
   OpenCode.
3. Never inspect or retain unrelated environment variables. An adapter may
   read a specific non-secret path/model variable only when its contract names
   that variable.
4. Never emit argv, environment values, prompts, commands, or code content to
   logs or the UI.

Process existence is the authoritative signal that an interactive agent can
still produce work. Agent state decides whether its current turn is busy,
blocked, or complete.

## Normalized Session Model

The normalized model adds explicit agent and model identity and uses
namespaced IDs.

```go
type Session struct {
    ID          string // e.g. "codex:<native-id>" or "aider:pid:<pid>"
    NativeID    string
    Agent       Agent  // Claude, Codex, OpenCode, Aider
    Model       string // exact runtime value or "unknown"
    Name        string
    Project     string
    Cwd         string
    PID         int

    Status      Status
    Mode        ProgressMode
    Done        int
    Total       int
    Blocked     bool
    NeedsHint   string
    Children    []Session
    UpdatedAt   int64
}
```

`Kind`, which currently mixes background jobs and subagent types, is either
retained only as an internal compatibility detail or replaced by narrowly
named fields during implementation. Do not introduce a generic capability
framework until a consumer needs it.

Namespaced IDs are mandatory for top-level sessions and children so identical
native IDs from two agents cannot collide in edge detection or fade state.

Session name fallback order:

1. Native title or name persisted by the agent.
2. Short native session ID.
3. `PID <n>` when no session ID exists.

Subagents inherit the agent identity but have no model in the rendered UI.

## Adapter Design

### Claude Code

- Use `~/.claude/sessions/*.json` as the live-session registry, preserving the
  current dead-PID filtering.
- Use the attributed project transcript for the latest trustworthy runtime
  model, TodoWrite progress, and Task/Agent child lifecycle.
- Preserve `~/.claude/jobs/<job-id>/state.json` handling for background job
  state, progress, `blocked`, and `needs`.
- Return `unknown` when runtime model metadata is absent.

### Codex

- Discover Codex processes owned by the current UID.
- Prefer open-file attribution through `/proc/<pid>/fd` to associate a process
  with its active `~/.codex/sessions/**/rollout-*.jsonl`. This avoids choosing
  the wrong rollout when multiple sessions share a cwd.
- Read `session_meta` for native ID, cwd, source, and parent-thread metadata.
- Read the latest applicable `turn_context.model` as the effective runtime
  model.
- Reduce lifecycle events by turn ID: `task_started` makes the session busy;
  matching `task_complete` or `task_aborted` completes the turn.
- Parse todo/subagent data only where the rollout supplies attributable native
  events. Otherwise use indeterminate progress.
- An unmatched Codex process is represented as `PID <n>` with model `unknown`.
  It must not be paired with the newest rollout merely because cwd or mtime is
  similar. Until attribution succeeds it is an indeterminate observation row,
  is not counted as busy, and cannot emit lifecycle sounds. It disappears only
  when the process exits or becomes an attributed session.

### OpenCode

- Discover OpenCode processes owned by the current UID and resolve the standard
  or XDG data root without reading credentials.
- Open the OpenCode SQLite database in read-only mode. Use session directory,
  title/slug, agent mode, persisted model/provider, todo rows, and parent ID.
- Prefer the latest assistant/user message runtime `providerID/modelID` over a
  session-level default.
- When an OpenCode-owned loopback HTTP endpoint is discoverable, use only
  bounded GET requests for live session status. Validate that the listening
  socket belongs to the matched current-UID process before probing it.
- If the API is absent, derive busy state from attributable incomplete
  assistant messages or pending/running tool parts. Completion, abort, or error
  closes the turn.
- Use `parent_id` to build the subagent tree and the todo table for determinate
  progress.

### Aider

- Treat each current-UID Aider process as one session, identified by PID when
  Aider provides no native session ID.
- Derive project and cwd from the process.
- Resolve the model only from evidence attributable to that process, in this
  order: a runtime `/model` change in the process-attributed input history, an
  explicit `--model` argv value, an unambiguous effective model setting, then
  `unknown`.
- A launch-time argv/config model remains eligible only while the adapter can
  observe that process's attributable input history for later `/model`
  changes. Otherwise downgrade it to `unknown` rather than risk displaying a
  stale launch model.
- Tail only input/chat history files attributable to the process. An
  attributable submitted LLM request opens a turn; the corresponding assistant
  completion closes it. Slash commands that do not call an LLM do not open a
  turn.
- If multiple Aider processes share one history file and events cannot be
  attributed, do not cross-assign model or lifecycle events.
- Aider progress is indeterminate and it has no subagent tree in this version.

## Lifecycle and Event Semantics

Adapters emit a busy session while a turn is active. When a turn completes,
the adapter must emit the same session ID in a done/idle state for at least one
poll and retain it in its recently-active cache long enough for the model's
three-second grace period. It may evict the idle record afterward even if the
interactive process remains at its prompt.

The model keeps existing behavior:

- `!done -> done` emits one DONE event.
- `!blocked -> blocked` emits one APPROVAL event only when an adapter has an
  explicit blocked signal.
- Idle alone never becomes blocked.
- A model change updates the row but emits no sound.
- A later busy turn with the same session ID clears fade state and makes the
  row visible again.
- Process exit removes the session and prunes adapter cache state.

On the initial poll, existing busy/done state seeds the baseline without a
startup chime storm.

## Rendering

The wide layout adds a dedicated model column:

```text
PROJECT / AGENT / SESSION       MODEL              PROGRESS       TASKS  STATUS
-------------------------------------------------------------------------------
v monitor-multi-agent
  v Claude
    > fix-auth-flow             claude-opus-4-6    [██████▓...]   6/10   BUSY
      |- Review implementation                    [sweep]         --     SWEEP
      `- Run tests                                [██████████]    DONE   DONE
  v Codex
    > 019ff411                  gpt-5.6-sol        [sweep]         --     SWEEP
  v OpenCode
    > silent-cabin             MiniMax-M3         [████▓.....]    4/9   BUSY
  v Aider
    > PID 18420                unknown             [sweep]         --     SWEEP
```

The production renderer retains the existing Unicode glyphs and styles; the
ASCII example above avoids making the spec depend on terminal rendering.

Rendering rules:

- Group by project, then agent, then session, then existing subagent tree.
- Agent group rows contain only the agent name.
- The model column is populated only for session rows.
- On narrow screens, move the model to a session continuation line before
  dropping lower-priority information. Extremely narrow terminals may
  ellipsize, but must never substitute another model name.
- Keep project and agent groups visible. The existing `c` key hides or shows
  subagents only.
- Keep `BLOCKED` and `needs:` on the affected session. Do not restore a blocked
  counter to the header.
- Keep the header's active/busy and sound indicators.

Stable ordering is project name, fixed agent order
`Claude -> Codex -> OpenCode -> Aider`, active sessions before fading sessions,
then session name.

## Read-Only and Privacy Constraints

- Never create, modify, or delete data under any agent state directory.
- Never access sessions belonging to another OS UID.
- Never read OpenCode `auth.json`, API keys, tokens, or arbitrary `.env`
  values.
- When a config file can contain secrets, decode only the named model/path
  fields and never retain or log the raw document.
- Never display or persist prompts, responses, commands, tool inputs, or source
  code.
- Loopback OpenCode access is GET-only, current-UID-process-attributed, and
  bounded by a short timeout.

## Performance and Failure Handling

- Keep the existing default one-second poll and 100 ms render-only animation
  tick.
- Snapshot `/proc` once per poll.
- Use file candidates attributed from processes rather than scanning all
  historical sessions every second.
- Tail append-only files by file identity and byte offset. Reset state on
  truncate or rotation.
- Query OpenCode SQLite by candidate session with short read-only queries that
  do not block its writer.
- Prune per-adapter cache after process disappearance or the completion grace
  window.
- A malformed record removes only that record. An adapter error must not erase
  successful results from other adapters.
- Recover an adapter panic at the coordinator boundary and treat it as that
  adapter's poll failure.
- Rate-limit repeated diagnostics so they do not corrupt the alternate-screen
  UI.
- Missing or changed upstream fields degrade to `unknown` or indeterminate
  state rather than fabricated data.

## Testing Strategy

All implementation tasks follow RED -> GREEN -> REFACTOR and two-layer review.

1. Process discovery tests use a fixture-backed process filesystem abstraction;
   unit tests never depend on live `/proc`.
2. Each adapter has fixtures for valid data, missing fields, malformed records,
   truncation/rotation, model changes, multiple sessions in one project, and
   ambiguous attribution.
3. Shared contract tests require current-UID filtering, namespaced IDs, correct
   agent identity, exact-or-unknown model behavior, and no cross-session
   attribution.
4. Coordinator tests cover merge ordering, duplicate protection, cache pruning,
   one-adapter failure, and panic isolation.
5. Event tests cover identical native IDs across agents, one-shot completion,
   explicit blocked transitions, reactivation after fade, and model changes
   without sound.
6. Render tests cover the four-level hierarchy, model only on session rows,
   responsive continuation layout, stable ordering, blocked session details,
   and unchanged subagent tree behavior.
7. Verification requires the full Go test suite, `go vet`, Linux build, and
   manual smoke tests against real installed agents without modifying their
   state.

## Acceptance Criteria

- Starting a supported agent under the current OS user causes its active work
  to appear without configuration.
- The row shows the correct agent and a runtime-evidenced model, or exactly
  `unknown` when evidence is insufficient.
- Multiple agents and multiple sessions in the same project appear under the
  approved project/agent/session/subagent hierarchy.
- Completion rings once, fades for three seconds, and disappears; a subsequent
  turn reappears.
- Existing Claude todo, subagent, background job, blocked, needs, and sound
  behavior remains intact.
- No adapter writes agent state or accesses another OS user's sessions.
- Failure or schema drift in one adapter does not take down the dashboard or
  hide sessions from healthy adapters.

## Out of Scope

- Native macOS and Windows process/path backends.
- Monitoring other OS users.
- Hooks, plugins, or telemetry injection.
- Persistent session history after fade.
- User-configurable adapter paths or commands.
- A generic third-party adapter SDK.
- Aider subagents or determinate todo progress without an upstream source.
- Per-agent collapse controls.

## Verified Source Notes

- Codex writes canonical session rollout items as JSONL under the Codex session
  directory; current rollout metadata includes session context and lifecycle
  events. See the official Codex repository's
  [rollout recorder](https://github.com/openai/codex/blob/main/codex-rs/rollout/src/recorder.rs)
  and the OpenAI repository discussion on
  [inspecting rollout context](https://github.com/openai/codex/discussions/12668).
- OpenCode documents its Linux/macOS data root and project/session storage in
  [Troubleshooting](https://dev.opencode.ai/docs/troubleshooting/), and its CLI
  documents model identifiers, session listing/export, and the read-only server
  surface in the [CLI reference](https://dev.opencode.ai/docs/cli/).
- Aider documents command-line, YAML, and environment configuration precedence
  in [Configuration](https://aider.chat/docs/config.html), model selection and
  automatic defaults in [Models and API keys](https://aider.chat/docs/troubleshooting/models-and-keys.html),
  runtime `/model` switching in [Usage](https://aider.chat/docs/usage.html), and
  history file settings in the [YAML configuration reference](https://aider.chat/docs/config/aider_conf.html).
