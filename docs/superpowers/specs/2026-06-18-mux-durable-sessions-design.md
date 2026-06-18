<!-- ABOUTME: Design spec for the mux "gene transfer" program and its first sub-project,
     ABOUTME: durable sessions with human-in-the-loop suspend/resume. -->

# mux Gene Transfer — Program Roadmap & Sub-project #1: Durable Sessions

- **Date:** 2026-06-18
- **Status:** Approved design; pending spec review → implementation plan
- **Author:** Harper + Claude
- **Context:** We reviewed [eve.dev](https://eve.dev/docs/introduction) — a TypeScript
  framework for *durable agents* — and identified which of its ideas ("genes") are worth
  transplanting into **mux** (`github.com/2389-research/mux`), a Go library for agentic
  infrastructure. eve is a framework/platform; mux is a library. The transfer principle is
  therefore: **re-express eve's genes as Go primitives (interface + one default impl), leaving
  ownership with the caller** — not port eve's framework-ness wholesale.

This document contains two parts:
- **Part A — the full program roadmap** (all genes, sequenced).
- **Part B — the detailed design for sub-project #1** (durable sessions + suspend/resume),
  which is the only sub-project specified here. Each later gene gets its own
  brainstorm → spec → plan → build cycle.

---

## Part A — Program Roadmap

Seven genes, sequenced by **dependency**, not enthusiasm. Effort is rough order-of-magnitude
(S ≈ days, M ≈ 1–2 weeks, L ≈ multi-week).

| # | Gene | Depends on | Effort | Why here / what it unlocks |
|---|------|-----------|--------|----------------------------|
| **1** | **Durable sessions + suspend/resume** | — | **M** | Foundation. Unlocks real human-in-the-loop, and the "park waiting for a reply" behavior that #5 and #6 need. **Detailed in Part B.** |
| **2** | **Skills** (progressive disclosure) | — | **S** | Independent quick win. Markdown procedures + a built-in `load_skill` tool that injects instructions (not a new execution surface) on demand. |
| **7** | **OpenAPI → tools connector** | — | **S–M** | Independent. Pure capability gap vs eve (mux is MCP-only today). |
| **3** | **Declarative config loading** | 2, (5) | **M** | The Go-legal slice of eve's "filesystem-first": load skills, MCP connection manifests, and schedules from files. Needs things to load, so it follows #2. |
| **5** | **Schedules** (cron → Run) | **1** | **S–M** | Handler-mode "park until a channel replies" only works once #1 provides durable suspend. |
| **6** | **Channels + HTTP session API** | **1** | **M** | The `/session` + `/session/{id}` resume API *is* sessionID ↔ continuationToken. Ship the `Channel` interface + **one** reference HTTP adapter — not the Slack/Discord/Teams zoo (that's eve being a product). |
| **4** | **Sandbox** (+ network egress policy, credential broker) | (1) | **L** | Large independent block; highest security value. Soft-depends on #1 so a resumed session can re-seed its `/workspace`. The credential-broker idea (inject secrets at the network boundary, never into the sandbox) is the standout gene. |

**Recommended execution order:** `1 → (2 ‖ 7 as parallel quick wins) → 5 → 6 → 3 → 4`.

**Explicitly NOT transferred** (category errors for a Go library): pure "drop-a-`.ts`-file → it's
a tool" magic (fights Go's compiler — tool registration stays explicit); a managed
Workflow-SDK-style runtime (a `Store` + suspension points gets ~90% of the value); the built-in
platform-adapter zoo.

---

## Part B — Sub-project #1: Durable Sessions + Suspend/Resume

### B0. Decisions log (resolved)

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Plan shape | Program roadmap + deep-dive on #1 only | Avoids one unreviewable mega-spec; respects phased execution. |
| Durability goal | **HITL, restart-survivable** | Checkpoint at iteration + suspension boundaries. A crash *mid-tool* is best-effort on resume (at-least-once), not guaranteed. Mid-tool write-ahead (the "full crash recovery" option) is explicitly out of scope. |
| Suspend API | **Return value + `Resume()`**, realized as a **typed sentinel error** | `Run`/`Continue` return `error` today; a suspension that satisfies `error` (the `io.EOF` pattern, read via `errors.As`) keeps signatures byte-for-byte unchanged. Alternative (new `RunSession(...) (RunResult, error)` methods) rejected to avoid two ways to run. |
| Persistence format | **Reuse the existing `llm.Message` JSON** (same shape `agent.Transcript` writes) | Don't invent a second message format; the snapshot wraps the message log with suspension metadata + counters. |
| Package layout | **Types + `Store` interface in `orchestrator`; file impl in a new `session` package** | Avoids an import cycle (`orchestrator` never imports `session`) and matches Go idiom: define an interface in the package that consumes it. |
| Roadmap order | As in Part A | #1 first because #5/#6 depend on it. |

### B1. Goal & success criteria

A run that hits an approval gate **persists itself, returns control, and can be resumed later by a
different process** with the human's decision. Testable criteria:

1. Run an agent whose tool requires approval → `Run` returns a suspension (does **not** block).
2. Kill the process. The snapshot is on disk.
3. New process, new orchestrator → `Resume(sessionID, approve)` → the pending tool runs **only
   now**, and the loop continues to completion.
4. `Resume(sessionID, deny)` → a synthesized denial turn is fed back to the model instead of the
   tool running.
5. With **no `Store` configured**, behavior is byte-for-byte today's behavior (non-breaking).

### B2. New components

The suspend/resume **types and the `Store` interface live in `orchestrator`** — the package that
consumes them. That is idiomatic Go (define an interface where it's used) and it lets
`Snapshot.Usage` reuse `orchestrator.TokenUsage` with no import cycle. A small new **`session`
package provides the file-backed `Store` implementation** and imports `orchestrator`. The executor
is almost untouched.

```go
// package orchestrator — suspend/resume types + the Store interface (defined at the consumer)

type Status string
const (
    StatusRunning   Status = "running"
    StatusSuspended Status = "suspended"
    StatusComplete  Status = "complete"
    StatusError     Status = "error"
)

type Reason string
const (
    ReasonApprovalRequired Reason = "authorization.required"
    ReasonInputRequired    Reason = "input.requested" // designed-for now, implemented later
)

type PendingToolCall struct {
    ID            string
    Name          string
    Params        map[string]any
    NeedsApproval bool
}
type Suspension struct {
    Reason  Reason
    Pending []PendingToolCall       // the whole assistant-turn tool batch
}

type Snapshot struct {
    SessionID  string
    Status     Status
    Messages   []llm.Message        // same llm.Message JSON that agent.Transcript writes
    Suspension *Suspension          // non-nil iff Status == StatusSuspended
    Usage      TokenUsage           // same-package type — no import cycle
    Iteration  int                  // resume the loop at the right place
    UpdatedAt  time.Time
}

// Store is consumed by the orchestrator; implementations live in other packages.
type Store interface {
    Save(ctx context.Context, s *Snapshot) error
    Load(ctx context.Context, sessionID string) (*Snapshot, error)
    List(ctx context.Context) ([]string, error)
    Delete(ctx context.Context, sessionID string) error
}
```

```go
// package session — the default file-backed implementation (imports orchestrator)
func NewFileStore(dir string) orchestrator.Store // one <sessionID>.json per session
```

The `Messages` field marshals with the same `llm.Message` JSON tags `agent.Transcript` already
uses, so there is a single on-disk message shape. No import cycle: `orchestrator` owns the types and
interface and never imports `session`; `session` imports `orchestrator`; the caller wires the two
together.

### B3. The API (non-breaking, typed sentinel)

```go
// package orchestrator

// Suspended satisfies error so existing error-returning signatures are unchanged.
type Suspended struct {
    SessionID string
    Reason    Reason
    Pending   []PendingToolCall
}
func (s *Suspended) Error() string { return "session suspended: " + string(s.Reason) }

type Decision struct {
    // Approvals maps a pending tool-call ID to its verdict; a call absent from the
    // map falls back to DefaultApprove. So Approve(true) approves the whole batch.
    Approvals      map[string]bool
    DefaultApprove bool
    // future: Input string for ReasonInputRequired
}
func Approve(all bool) Decision // returns Decision{DefaultApprove: all}

// Resume reloads the snapshot and continues. Returns nil (complete),
// *Suspended (parked again), or a real error.
func (o *Orchestrator) Resume(ctx context.Context, sessionID string, d Decision) error
```

Caller usage:

```go
err := orch.Run(ctx, prompt)
var susp *orchestrator.Suspended
if errors.As(err, &susp) {              // snapshot already persisted to the Store
    // ...process may exit here; resume later, possibly in a new process...
    err = orch.Resume(ctx, susp.SessionID, orchestrator.Approve(true))
}
```

Configuration plugs in exactly like `HookManager` does, so `Agent` passes it through and gains
`Agent.Resume`:

```go
type Config struct {
    // ...existing fields...
    SessionStore Store        // nil = today's behavior; e.g. session.NewFileStore("./sessions")
    ApprovalMode ApprovalMode // ApprovalSync (default; uses approvalFunc) | ApprovalSuspend
}
```

**Config rules** (resolving the obvious edge cases):
- `SessionStore == nil` → durability disabled; behavior identical to today.
- `ApprovalMode == ApprovalSuspend` with `SessionStore == nil` → `panic` at `New`: a suspend run
  with nowhere to persist is a configuration error (matches mux's existing nil-arg panics).
- With a `Store` set, the loop checkpoints (`Running`/`Complete`) **regardless** of `ApprovalMode`;
  *suspension* only happens in `ApprovalSuspend`. So `ApprovalSync` + `Store` gives crash-resilient
  history without HITL parking.

### B4. Data flow

mux's state machine **already permits these transitions** (`orchestrator/state.go:24-25`):
`Streaming → AwaitingApproval → ExecutingTool`. `StateAwaitingApproval` is currently defined but
never entered — this sub-project finishes wiring the architecture already drew.

```
Run/Continue → loop:
  iteration start → [Store.Save: Running]
  LLM responds with tool calls
  ApprovalSuspend mode: for each call, check tool.RequiresApproval(params)
     any need approval & undecided?
        → build Suspension, [Store.Save: Suspended], state → AwaitingApproval,
          return &Suspended{}          ← process may now exit
  (ApprovalSync mode: today's synchronous approvalFunc path, unchanged)

Resume(id, decision):
  [Store.Load] → restore messages / usage / iteration
  state AwaitingApproval → ExecutingTool
  approved calls: execute via executor (a decision-backed approvalFunc returns the stored verdict)
  denied calls:   synthesize a "denied" tool-result turn for the model
  continue loop → nil (complete) | &Suspended (parked again) | error
```

The executor barely changes: on resume the orchestrator hands it an `ApprovalFunc` that returns the
stored decision, so `tool.Executor.Execute` (`tool/executor.go:99-110`) keeps working as-is. The
**suspend/resume orchestration lives in the orchestrator** (where the loop and state machine are);
the executor stays "dumb."

### B5. Error handling & the mid-tool-crash boundary

- **Crash mid-tool:** the last snapshot is `Running` at the iteration boundary *before* the batch.
  On reload the batch may re-run → documented as **at-least-once**; tools should be idempotent.
  Approval-required tools never run before a recorded decision, so the dangerous ones are safe.
  No per-tool write-ahead in v1 (that is the rejected "full crash recovery" scope).
- **Denied approval:** becomes a synthesized tool-result turn ("denied"), so the model can adapt —
  not a hard `error`.
- **`Store.Save` failure:** surfaced as the error from `Run`/`Resume`. We never silently pretend to
  be durable.

### B6. Testing strategy (TDD)

- **Unit:** file `Store` round-trips (running + suspended snapshots; list; delete); `Decision` /
  `Approve` helpers; state-machine suspend/resume transitions
  (`Streaming→AwaitingApproval→ExecutingTool`).
- **Integration:** drive the loop with mux's **existing** orchestrator test client/harness (match
  whatever `orchestrator/*_test.go` currently uses to feed canned LLM responses — no new production
  mock mode is introduced). Assert: an approval-required tool does **not** run before `Resume`;
  approve → it runs; deny → a denial turn appears.
- **E2E:** `Run` → `*Suspended` → serialize to disk → **fresh orchestrator instance** → `Resume` →
  completes. This is the literal success criterion from B1.

> Implementation note: confirm (don't assume) exactly how existing orchestrator tests feed canned
> LLM responses, so new loop tests match house style and the no-mock rule.

### B7. Out of scope (for this sub-project)

- Full crash recovery / mid-tool write-ahead (idempotency beyond at-least-once).
- `ReasonInputRequired` implementation (the type is designed for it; wiring comes later).
- Channels, schedules, sandbox, skills, declarative config — separate sub-projects (Part A).

---

## Appendix — mux baseline (as of this design)

- `Orchestrator.Run/Continue` return `error`; results read via `Messages()`
  (`orchestrator/orchestrator.go:202,223`).
- Conversation history is an in-memory `[]llm.Message`; `Messages()`/`SetMessages()` expose it.
- `agent.Transcript` already provides JSON/JSONL save/load (`agent/transcript.go`).
- `StateAwaitingApproval` and its transitions exist but are never entered
  (`orchestrator/state.go:16,24-25`).
- Approval today is synchronous inside `tool.Executor.Execute` (`tool/executor.go:99-110`).
- Hooks/events exist (`hooks/`, `orchestrator.Subscribe()`), in-process only.
