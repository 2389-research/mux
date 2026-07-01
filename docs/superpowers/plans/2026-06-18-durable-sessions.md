# Durable Sessions (Suspend / Resume) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the think-act loop suspend when a tool needs human approval, persist its full state to a pluggable `Store`, and resume — in the same process or a fresh one — from a caller-supplied approval `Decision`.

**Architecture:** Suspension is surfaced as a typed sentinel error (`*orchestrator.Suspended`, detected with `errors.As`) returned from the existing `Run`/`Continue` (their `error` signatures are unchanged). A new `ApprovalSuspend` mode makes the loop checkpoint a `Snapshot` and return that sentinel *before* an approval-requiring tool runs, instead of calling the synchronous approval func. `Resume` reloads the snapshot, replays the pending tool batch through the **existing** `executeTools` path with a decision-backed approval func (approved → executes; denied → the executor's existing `ErrApprovalDenied` → error result block), then continues the loop. All session types and the `Store` interface live in package `orchestrator` (where they are consumed); the only file-backed implementation lives in a new `session` package that imports `orchestrator` (never the reverse), avoiding an import cycle.

**Tech Stack:** Go 1.24, standard library only (`encoding/json`, `os`, `path/filepath`, `errors`, `time`). No new dependencies. Tests use the in-repo test-double pattern (`mockLLMClient`, `mockTool`).

**Source spec:** `docs/superpowers/specs/2026-06-18-mux-durable-sessions-design.md` (Part B).

## Global Constraints

- **Go module:** `github.com/2389-research/mux`, Go 1.24. Standard library only — no new third-party deps.
- **TDD, every task:** failing test → run-and-watch-it-fail → minimal implementation → run-and-watch-it-pass → commit. No exceptions.
- **Conventional commits**, imperative mood, present tense (e.g. `feat: add durable-session Store interface`).
- **Never** `git commit --no-verify` (or `--no-hooks` / `--no-pre-commit-hook`). If hooks fail, read the output, fix the root cause, re-run. Pre-commit runs `gofmt`, `go vet`, `golangci-lint`, and `go test`.
- **ABOUTME header:** every new `.go` file starts with two `// ABOUTME: ` comment lines describing the file.
- **No production mock mode.** Test doubles named `mock*` in `_test.go` files are the established house pattern and are allowed; do not add mock/fake behavior to non-test code.
- **Do not rewrite working code.** Tasks 5–7 *extract* existing loop code into helpers with behavior preserved — the safety net is that every pre-existing test stays green after the extraction. If an extraction changes behavior, stop.
- **Package layout (cycle-avoidance):** session types + `Store` interface go in `orchestrator`; the file-backed `Store` impl goes in `session`. `session` imports `orchestrator`; `orchestrator` must **not** import `session`.
- **`Run`/`Continue`/`Resume` signatures return only `error`.** Suspension travels as `*Suspended` via `errors.As`.
- **`vet` copylocks:** `TokenUsage` embeds a `sync.RWMutex`. Never copy a `TokenUsage` by value from an existing instance. Construct fresh literals (as `Snapshot()` does) and restore field-by-field via a pointer parameter (`Restore(*TokenUsage)`).

---

## File Structure

**New files:**
- `orchestrator/session.go` — session value types (`Status`, `Reason`, `PendingToolCall`, `Suspension`, `Snapshot`), the `Store` interface, `ErrSessionNotFound`, `ApprovalMode`, the `Suspended` sentinel error, and `Decision` / `Approve`. One responsibility: the durable-session vocabulary the loop and callers share.
- `orchestrator/session_internal_test.go` (`package orchestrator`) — white-box unit tests for unexported helpers (`Decision.approves`).
- `session/file_store.go` — `FileStore`, the JSON-file-backed `Store`. Atomic writes via temp+rename.
- `session/file_store_test.go` — round-trip / missing / list / delete tests.

**Modified files:**
- `orchestrator/usage.go` — add `Restore(*TokenUsage)` (inverse of `Snapshot()`).
- `orchestrator/orchestrator.go` — `Config` fields, `NewWithConfig` panic rule, loop-plumbing extraction (`withSessionHooks`, `runIterations`), checkpoint helpers, suspend logic, `Resume`.
- `orchestrator/orchestrator_test.go` — black-box integration tests (suspend, resume-approve, resume-deny, checkpoint, cross-process). Adds an `approvalTool` test double.
- `tool/executor.go` — add `NeedsApproval` and an `ApprovalFunc` getter.
- `tool/executor_test.go` — tests for the two new executor methods.
- `agent/config.go` — pass-through `SessionStore` / `ApprovalMode` fields.
- `agent/agent.go` — wire the new config into the orchestrator; add `Agent.Resume`.
- `agent/agent_test.go` (or nearest existing agent test file) — agent-level suspend/resume test.

---

## Task 1: Durable-session types, `Store` interface, and `Decision`

**Files:**
- Create: `orchestrator/session.go`
- Test: `orchestrator/session_internal_test.go` (`package orchestrator`)

**Interfaces:**
- Consumes: `github.com/2389-research/mux/llm` (`llm.Message`), `orchestrator.TokenUsage` (existing, in `usage.go`).
- Produces (relied on by every later task):
  - `type Status string`; consts `StatusRunning = "running"`, `StatusSuspended = "suspended"`, `StatusComplete = "complete"`.
  - `type Reason string`; consts `ReasonApprovalRequired = "authorization.required"`, `ReasonInputRequired = "input.requested"` (reserved; not produced by this plan).
  - `type PendingToolCall struct { ID string; Name string; Params map[string]any; NeedsApproval bool }`.
  - `type Suspension struct { Reason Reason; Pending []PendingToolCall }`.
  - `type Snapshot struct { SessionID string; Status Status; Messages []llm.Message; Suspension *Suspension; Usage TokenUsage; Iteration int; UpdatedAt time.Time }`.
  - `type Store interface { Save(ctx, *Snapshot) error; Load(ctx, sessionID string) (*Snapshot, error); List(ctx) ([]string, error); Delete(ctx, sessionID string) error }`.
  - `var ErrSessionNotFound = errors.New("session not found")`.
  - `type ApprovalMode int`; consts `ApprovalSync ApprovalMode = iota` (default), `ApprovalSuspend`.
  - `type Suspended struct { SessionID string; Suspension Suspension }` with `func (s *Suspended) Error() string`.
  - `type Decision struct { Approvals map[string]bool; DefaultApprove bool }`.
  - `func Approve(all bool) Decision` → `Decision{DefaultApprove: all}`.
  - `func (d Decision) approves(id string) bool` (unexported).

- [ ] **Step 1: Write the failing test** (`orchestrator/session_internal_test.go`)

```go
// ABOUTME: White-box unit tests for unexported durable-session helpers.
// ABOUTME: Lives in package orchestrator to reach internal decision logic.
package orchestrator

import "testing"

func TestDecisionApproves_PerIDOverride(t *testing.T) {
	d := Decision{Approvals: map[string]bool{"a": true, "b": false}, DefaultApprove: false}
	if !d.approves("a") {
		t.Errorf("approves(a) = false, want true")
	}
	if d.approves("b") {
		t.Errorf("approves(b) = true, want false")
	}
}

func TestDecisionApproves_DefaultFallback(t *testing.T) {
	if got := (Decision{DefaultApprove: true}).approves("missing"); !got {
		t.Errorf("approves(missing) with DefaultApprove=true = false, want true")
	}
	if got := (Decision{}).approves("missing"); got {
		t.Errorf("approves(missing) with zero Decision = true, want false")
	}
}

func TestApprove_SetsDefault(t *testing.T) {
	if !Approve(true).DefaultApprove {
		t.Errorf("Approve(true).DefaultApprove = false, want true")
	}
	if Approve(false).DefaultApprove {
		t.Errorf("Approve(false).DefaultApprove = true, want false")
	}
}

func TestSuspendedError_MentionsSessionAndReason(t *testing.T) {
	s := &Suspended{SessionID: "session-abc", Suspension: Suspension{Reason: ReasonApprovalRequired}}
	msg := s.Error()
	if msg == "" {
		t.Fatal("Suspended.Error() returned empty string")
	}
	// Must reference the session and reason so logs are actionable.
	for _, want := range []string{"session-abc", string(ReasonApprovalRequired)} {
		if !contains(msg, want) {
			t.Errorf("Suspended.Error() = %q, missing %q", msg, want)
		}
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./orchestrator/ -run 'TestDecisionApproves|TestApprove_SetsDefault|TestSuspendedError' -v`
Expected: FAIL — compile error, `undefined: Decision`, `undefined: Approve`, `undefined: Suspended`.

- [ ] **Step 3: Write minimal implementation** (`orchestrator/session.go`)

```go
// ABOUTME: Defines durable-session value types, the Store persistence interface,
// ABOUTME: and the suspend/resume vocabulary shared by the loop and its callers.
package orchestrator

import (
	"errors"
	"fmt"
	"time"

	"github.com/2389-research/mux/llm"
)

// Status is the lifecycle state of a persisted session snapshot.
type Status string

const (
	StatusRunning   Status = "running"
	StatusSuspended Status = "suspended"
	StatusComplete  Status = "complete"
)

// Reason explains why a session suspended. Values mirror the eve.dev vocabulary.
type Reason string

const (
	// ReasonApprovalRequired: the loop paused because a tool needs human approval.
	ReasonApprovalRequired Reason = "authorization.required"
	// ReasonInputRequired is reserved for a future gene (the loop does not yet produce it).
	ReasonInputRequired Reason = "input.requested"
)

// PendingToolCall is a caller-facing projection of one tool call from the
// suspending assistant turn. It is informational: re-execution on Resume reads
// the authoritative tool_use blocks back out of the persisted messages, not this.
type PendingToolCall struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Params        map[string]any `json:"params,omitempty"`
	NeedsApproval bool           `json:"needs_approval"`
}

// Suspension describes why and on what the loop paused.
type Suspension struct {
	Reason  Reason            `json:"reason"`
	Pending []PendingToolCall `json:"pending,omitempty"`
}

// Snapshot is the complete persisted state of a session at a checkpoint.
type Snapshot struct {
	SessionID  string        `json:"session_id"`
	Status     Status        `json:"status"`
	Messages   []llm.Message `json:"messages"`
	Suspension *Suspension   `json:"suspension,omitempty"`
	Usage      TokenUsage    `json:"usage"`
	Iteration  int           `json:"iteration"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

// Store persists and retrieves session snapshots. Implementations must be safe
// for use by one orchestrator at a time per session ID.
type Store interface {
	Save(ctx context.Context, snap *Snapshot) error
	Load(ctx context.Context, sessionID string) (*Snapshot, error)
	List(ctx context.Context) ([]string, error)
	Delete(ctx context.Context, sessionID string) error
}

// ErrSessionNotFound is returned by Store.Load when no snapshot exists.
var ErrSessionNotFound = errors.New("session not found")

// ApprovalMode selects how the loop handles a tool that requires approval.
type ApprovalMode int

const (
	// ApprovalSync calls the executor's approval func inline (the default, pre-existing behavior).
	ApprovalSync ApprovalMode = iota
	// ApprovalSuspend checkpoints and returns *Suspended instead of calling the approval func.
	ApprovalSuspend
)

// Suspended is returned by Run/Continue/Resume when the loop pauses awaiting a
// decision. Callers detect it with errors.As(err, &target).
type Suspended struct {
	SessionID  string
	Suspension Suspension
}

func (s *Suspended) Error() string {
	return fmt.Sprintf("orchestrator: session %s suspended (%s)", s.SessionID, s.Suspension.Reason)
}

// Decision carries the caller's approval choices into Resume. Approvals is keyed
// by PendingToolCall.ID; DefaultApprove is the fallback for IDs not present.
type Decision struct {
	Approvals      map[string]bool
	DefaultApprove bool
}

// Approve returns a Decision that approves (all=true) or denies (all=false) every
// pending tool call by default.
func Approve(all bool) Decision { return Decision{DefaultApprove: all} }

func (d Decision) approves(id string) bool {
	if v, ok := d.Approvals[id]; ok {
		return v
	}
	return d.DefaultApprove
}
```

Note: `session.go` references `context` (in the `Store` interface). Add `"context"` to the import block. The implementer must include it — the block above lists `errors`, `fmt`, `time`, `llm`; add `context` so the file compiles.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./orchestrator/ -run 'TestDecisionApproves|TestApprove_SetsDefault|TestSuspendedError' -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add orchestrator/session.go orchestrator/session_internal_test.go
git commit -m "feat: add durable-session types, Store interface, and Decision"
```

---

## Task 2: `TokenUsage.Restore`, `Config` fields, and the suspend-without-store panic

**Files:**
- Modify: `orchestrator/usage.go`
- Modify: `orchestrator/orchestrator.go:66-74` (`Config`), `orchestrator/orchestrator.go:105-123` (`NewWithConfig`)
- Test: `orchestrator/session_internal_test.go` (append; `package orchestrator`) and `orchestrator/orchestrator_test.go` (panic test, black-box)

**Interfaces:**
- Consumes: `Store`, `ApprovalMode`, `ApprovalSuspend` (Task 1); `TokenUsage` (existing).
- Produces:
  - `func (u *TokenUsage) Restore(s *TokenUsage)` — copies the six counters from `s` into `u` under `u`'s lock.
  - `Config.SessionStore Store` and `Config.ApprovalMode ApprovalMode` fields.
  - `NewWithConfig` panics with `"mux: ApprovalSuspend requires a SessionStore"` when `config.ApprovalMode == ApprovalSuspend && config.SessionStore == nil`.

- [ ] **Step 1: Write the failing tests**

Append to `orchestrator/session_internal_test.go`:

```go
func TestTokenUsageRestore_CopiesCounters(t *testing.T) {
	src := TokenUsage{
		InputTokens:      11,
		OutputTokens:     22,
		ThinkingTokens:   3,
		CacheReadTokens:  4,
		CacheWriteTokens: 5,
		RequestCount:     6,
	}
	dst := NewTokenUsage()
	dst.Restore(&src)
	got := dst.Snapshot()
	if got != src {
		t.Errorf("Restore produced %+v, want %+v", got, src)
	}
}
```

Note: `got != src` compares two `TokenUsage` *values* with `==`. That is legal and vet-clean because both operands are fresh struct values returned/constructed here (a struct containing only an unexported zero-value mutex and comparable fields is comparable; copylocks flags copying *used* locks, not comparing fresh literals). `Snapshot()` already returns such a fresh value.

Add to `orchestrator/orchestrator_test.go` (black-box, `package orchestrator_test`):

```go
func TestNewWithConfig_SuspendRequiresStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when ApprovalSuspend has no SessionStore")
		}
	}()
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)
	orchestrator.NewWithConfig(&mockLLMClient{}, executor, orchestrator.Config{
		MaxIterations: 5,
		ApprovalMode:  orchestrator.ApprovalSuspend,
		// SessionStore intentionally nil
	})
}

func TestNewWithConfig_DefaultsDoNotPanic(t *testing.T) {
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)
	// ApprovalSync (zero value) + nil store must remain today's behavior: no panic.
	_ = orchestrator.NewWithConfig(&mockLLMClient{}, executor, orchestrator.Config{MaxIterations: 5})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./orchestrator/ -run 'TestTokenUsageRestore|TestNewWithConfig_SuspendRequiresStore|TestNewWithConfig_DefaultsDoNotPanic' -v`
Expected: FAIL — `dst.Restore undefined`; `unknown field ApprovalMode in struct literal`.

- [ ] **Step 3a: Implement `Restore`** (append to `orchestrator/usage.go`, after `Snapshot`)

```go
// Restore overwrites the counters from a snapshot value. The inverse of Snapshot.
// s is taken by pointer to avoid copying its (zero-value) mutex; callers pass a
// freshly deserialized snapshot that no other goroutine touches.
func (u *TokenUsage) Restore(s *TokenUsage) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.InputTokens = s.InputTokens
	u.OutputTokens = s.OutputTokens
	u.ThinkingTokens = s.ThinkingTokens
	u.CacheReadTokens = s.CacheReadTokens
	u.CacheWriteTokens = s.CacheWriteTokens
	u.RequestCount = s.RequestCount
}
```

- [ ] **Step 3b: Add `Config` fields** (`orchestrator/orchestrator.go`, the `Config` struct at lines 66-74)

Add these two fields to the existing `Config` struct (keep all existing fields):

```go
	SessionStore Store        // Optional snapshot store; enables checkpointing/resume (nil = disabled)
	ApprovalMode ApprovalMode // How approval-required tools are handled (default ApprovalSync)
```

- [ ] **Step 3c: Add the panic rule** (`orchestrator/orchestrator.go`, inside `NewWithConfig`, after the existing nil-executor panic at lines 109-111, before the `return`)

```go
	if config.ApprovalMode == ApprovalSuspend && config.SessionStore == nil {
		panic("mux: ApprovalSuspend requires a SessionStore")
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./orchestrator/ -run 'TestTokenUsageRestore|TestNewWithConfig_SuspendRequiresStore|TestNewWithConfig_DefaultsDoNotPanic' -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Run the full orchestrator suite to confirm no regressions**

Run: `go test ./orchestrator/`
Expected: PASS (all pre-existing tests still green).

- [ ] **Step 6: Commit**

```bash
git add orchestrator/usage.go orchestrator/orchestrator.go orchestrator/session_internal_test.go orchestrator/orchestrator_test.go
git commit -m "feat: add TokenUsage.Restore and SessionStore/ApprovalMode config"
```

---

## Task 3: File-backed `Store` (`session` package)

**Files:**
- Create: `session/file_store.go`
- Test: `session/file_store_test.go`

**Interfaces:**
- Consumes: `orchestrator.Snapshot`, `orchestrator.Store`, `orchestrator.ErrSessionNotFound` (Task 1).
- Produces:
  - `type FileStore struct { dir string }`.
  - `func NewFileStore(dir string) *FileStore` (`*FileStore` satisfies `orchestrator.Store`).
  - Compile-time assertion `var _ orchestrator.Store = (*FileStore)(nil)`.

- [ ] **Step 1: Write the failing tests** (`session/file_store_test.go`)

```go
// ABOUTME: Tests for the file-backed session Store: round-trip, missing, list, delete.
package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/session"
)

func sampleSnapshot(id string) *orchestrator.Snapshot {
	usage := orchestrator.NewTokenUsage()
	usage.Add(llm.Usage{InputTokens: 10, OutputTokens: 20})
	return &orchestrator.Snapshot{
		SessionID: id,
		Status:    orchestrator.StatusSuspended,
		Messages:  []llm.Message{llm.NewUserMessage("hello")},
		Suspension: &orchestrator.Suspension{
			Reason:  orchestrator.ReasonApprovalRequired,
			Pending: []orchestrator.PendingToolCall{{ID: "t1", Name: "write", NeedsApproval: true}},
		},
		Usage:     usage.Snapshot(),
		Iteration: 2,
		UpdatedAt: time.Unix(1750000000, 0).UTC(),
	}
}

func TestFileStore_SaveLoadRoundTrip(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	want := sampleSnapshot("session-aaa")
	if err := store.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load(ctx, "session-aaa")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.SessionID != want.SessionID || got.Status != want.Status || got.Iteration != want.Iteration {
		t.Errorf("scalar mismatch: got %+v", got)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, want.UpdatedAt)
	}
	if got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 20 {
		t.Errorf("Usage = %+v, want input=10 output=20", got.Usage)
	}
	if got.Suspension == nil || got.Suspension.Reason != orchestrator.ReasonApprovalRequired {
		t.Errorf("Suspension = %+v", got.Suspension)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "hello" {
		t.Errorf("Messages = %+v", got.Messages)
	}
}

func TestFileStore_LoadMissing(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	_, err := store.Load(context.Background(), "session-nope")
	if !errors.Is(err, orchestrator.ErrSessionNotFound) {
		t.Fatalf("Load missing: err = %v, want ErrSessionNotFound", err)
	}
}

func TestFileStore_ListSorted(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	for _, id := range []string{"session-c", "session-a", "session-b"} {
		if err := store.Save(ctx, sampleSnapshot(id)); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"session-a", "session-b", "session-c"}
	if len(ids) != len(want) {
		t.Fatalf("List = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("List = %v, want %v", ids, want)
		}
	}
}

func TestFileStore_DeleteIdempotent(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	ctx := context.Background()
	if err := store.Save(ctx, sampleSnapshot("session-x")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "session-x"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Load(ctx, "session-x"); !errors.Is(err, orchestrator.ErrSessionNotFound) {
		t.Errorf("after Delete, Load err = %v, want ErrSessionNotFound", err)
	}
	// Second delete is a no-op, not an error.
	if err := store.Delete(ctx, "session-x"); err != nil {
		t.Errorf("second Delete: %v, want nil", err)
	}
}

func TestFileStore_ListEmptyDir(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	ids, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List on empty dir: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("List = %v, want empty", ids)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./session/ -v`
Expected: FAIL — package `session` does not exist / `undefined: session.NewFileStore`.

- [ ] **Step 3: Write minimal implementation** (`session/file_store.go`)

```go
// ABOUTME: File-backed implementation of orchestrator.Store that persists each
// ABOUTME: session snapshot as one JSON file, written atomically via temp+rename.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/2389-research/mux/orchestrator"
)

// FileStore persists session snapshots as JSON files under a directory.
type FileStore struct {
	dir string
}

var _ orchestrator.Store = (*FileStore)(nil)

// NewFileStore returns a FileStore rooted at dir. The directory is created on
// first Save if it does not exist.
func NewFileStore(dir string) *FileStore {
	return &FileStore{dir: dir}
}

// path returns the on-disk path for a session ID, rejecting IDs that could
// escape the store directory.
func (s *FileStore) path(sessionID string) (string, error) {
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) || strings.Contains(sessionID, "..") {
		return "", fmt.Errorf("session: invalid session id %q", sessionID)
	}
	return filepath.Join(s.dir, sessionID+".json"), nil
}

// Save writes snap atomically: marshal to a temp file in the same directory,
// then rename over the target so a crash never leaves a half-written snapshot.
func (s *FileStore) Save(_ context.Context, snap *orchestrator.Snapshot) error {
	target, err := s.path(snap.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// Load reads and decodes a snapshot, returning ErrSessionNotFound if absent.
func (s *FileStore) Load(_ context.Context, sessionID string) (*orchestrator.Snapshot, error) {
	target, err := s.path(sessionID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, orchestrator.ErrSessionNotFound
		}
		return nil, err
	}
	var snap orchestrator.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("session: decode %s: %w", sessionID, err)
	}
	return &snap, nil
}

// List returns the session IDs with snapshots, sorted. A missing dir lists empty.
func (s *FileStore) List(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(ids)
	return ids, nil
}

// Delete removes a snapshot. Deleting a non-existent session is a no-op.
func (s *FileStore) Delete(_ context.Context, sessionID string) error {
	target, err := s.path(sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./session/ -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add session/file_store.go session/file_store_test.go
git commit -m "feat: add file-backed session Store"
```

---

## Task 4: Executor approval helpers (`NeedsApproval`, `ApprovalFunc` getter)

**Files:**
- Modify: `tool/executor.go` (add two methods near `SetApprovalFunc` at lines 62-65)
- Test: `tool/executor_test.go`

**Interfaces:**
- Consumes: `Executor.source` (`ToolSource`, has `Get`), `Tool.RequiresApproval` (both existing in `tool` package).
- Produces:
  - `func (e *Executor) NeedsApproval(toolName string, params map[string]any) bool` — looks the tool up via `source.Get`; returns its `RequiresApproval(params)`; returns `false` if the tool is unknown (a later `Execute` surfaces `ErrToolNotFound`).
  - `func (e *Executor) ApprovalFunc() ApprovalFunc` — returns the currently set approval func (may be nil).

- [ ] **Step 1: Write the failing tests** (append to `tool/executor_test.go`)

First check the test package declaration at the top of `tool/executor_test.go`. If it is `package tool_test` (black-box), use the version below with the `tool.` qualifier. If it is `package tool` (white-box), drop the `tool.` qualifiers and the import alias accordingly. The code below assumes black-box `package tool_test`.

```go
func TestExecutor_NeedsApproval(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&approvalProbe{name: "danger", needs: true})
	registry.Register(&approvalProbe{name: "safe", needs: false})
	exec := tool.NewExecutor(registry)

	if !exec.NeedsApproval("danger", nil) {
		t.Errorf("NeedsApproval(danger) = false, want true")
	}
	if exec.NeedsApproval("safe", nil) {
		t.Errorf("NeedsApproval(safe) = true, want false")
	}
	if exec.NeedsApproval("unknown", nil) {
		t.Errorf("NeedsApproval(unknown) = true, want false")
	}
}

func TestExecutor_ApprovalFuncGetter(t *testing.T) {
	exec := tool.NewExecutor(tool.NewRegistry())
	if exec.ApprovalFunc() != nil {
		t.Errorf("ApprovalFunc() on fresh executor = non-nil, want nil")
	}
	called := false
	exec.SetApprovalFunc(func(_ context.Context, _ tool.Tool, _ map[string]any) (bool, error) {
		called = true
		return true, nil
	})
	got := exec.ApprovalFunc()
	if got == nil {
		t.Fatal("ApprovalFunc() = nil after SetApprovalFunc, want non-nil")
	}
	_, _ = got(context.Background(), nil, nil)
	if !called {
		t.Errorf("returned approval func was not the one set")
	}
}

// approvalProbe is a tool double whose approval requirement is configurable.
type approvalProbe struct {
	name  string
	needs bool
}

func (a *approvalProbe) Name() string                                { return a.name }
func (a *approvalProbe) Description() string                         { return "probe" }
func (a *approvalProbe) RequiresApproval(map[string]any) bool        { return a.needs }
func (a *approvalProbe) Execute(context.Context, map[string]any) (*tool.Result, error) {
	return tool.NewResult(a.name, true, "ok", ""), nil
}
```

Note: ensure `tool/executor_test.go` imports `context`. If the surrounding test file already declares an `approvalProbe`/similar double or already imports `context`, reuse those rather than redeclaring (a duplicate type or import is a compile error). Search the file first: `rg -n "approvalProbe|\"context\"" tool/executor_test.go`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./tool/ -run 'TestExecutor_NeedsApproval|TestExecutor_ApprovalFuncGetter' -v`
Expected: FAIL — `exec.NeedsApproval undefined`, `exec.ApprovalFunc undefined`.

- [ ] **Step 3: Write minimal implementation** (`tool/executor.go`, insert after `SetApprovalFunc` at line 65)

```go
// ApprovalFunc returns the currently configured approval function (may be nil).
func (e *Executor) ApprovalFunc() ApprovalFunc {
	return e.approvalFunc
}

// NeedsApproval reports whether the named tool would require approval for these
// params, without executing it. Unknown tools return false; Execute will surface
// ErrToolNotFound when actually invoked.
func (e *Executor) NeedsApproval(toolName string, params map[string]any) bool {
	t, ok := e.source.Get(toolName)
	if !ok {
		return false
	}
	return t.RequiresApproval(params)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./tool/ -run 'TestExecutor_NeedsApproval|TestExecutor_ApprovalFuncGetter' -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
git add tool/executor.go tool/executor_test.go
git commit -m "feat: add Executor.NeedsApproval and ApprovalFunc getter"
```

---

## Task 5: Loop-plumbing refactor + checkpointing (behavior-preserving)

This task introduces **no new externally observable behavior for the nil-store / ApprovalSync default** — its purpose is to carve clean seams (`withSessionHooks`, `runIterations`) and add checkpoint write points. The safety net is that every pre-existing orchestrator test stays green. The one new behavior is: when a `SessionStore` is configured, the loop writes `running` checkpoints after each tool batch and a `complete` checkpoint at the end — in **both** approval modes.

**Files:**
- Modify: `orchestrator/orchestrator.go` (`runWithHooks` at lines 236-275, `runLoop` at lines 277-372)
- Test: `orchestrator/orchestrator_test.go`

**Interfaces:**
- Consumes: `Snapshot`, `Status*`, `Store`, `Suspended` (Task 1); `Config.SessionStore` (Task 2).
- Produces (used by Tasks 6-7):
  - `func (o *Orchestrator) withSessionHooks(ctx context.Context, prompt, source string, core func() error) error`.
  - `func (o *Orchestrator) runIterations(ctx context.Context, startIter int, prompt string) error`.
  - `func (o *Orchestrator) snapshot(status Status) *Snapshot`.
  - `func (o *Orchestrator) checkpoint(ctx context.Context, status Status) error` (no-op when `SessionStore == nil`).

- [ ] **Step 1: Write the failing test** (`orchestrator/orchestrator_test.go`, black-box)

```go
func TestRun_CheckpointsWithStore(t *testing.T) {
	// One tool round-trip then end_turn. A store is configured in the default
	// ApprovalSync mode: the loop must persist a running checkpoint after the
	// tool batch and a complete checkpoint at the end.
	store := session.NewFileStore(t.TempDir())
	registry := tool.NewRegistry()
	executed := false
	registry.Register(&mockTool{name: "noop", execFunc: func(_ context.Context, _ map[string]any) (*tool.Result, error) {
		executed = true
		return tool.NewResult("noop", true, "done", ""), nil
	}})
	executor := tool.NewExecutor(registry)
	client := &mockLLMClient{responses: []*llm.Response{
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "u1", Name: "noop", Input: map[string]any{}}}, StopReason: llm.StopReasonToolUse},
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "all done"}}, StopReason: llm.StopReasonEndTurn},
	}}
	orch := orchestrator.NewWithConfig(client, executor, orchestrator.Config{
		MaxIterations: 5,
		SessionStore:  store,
	})
	if err := orch.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !executed {
		t.Fatal("tool was not executed")
	}
	snap, err := store.Load(context.Background(), orch.SessionID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.Status != orchestrator.StatusComplete {
		t.Errorf("final snapshot Status = %q, want %q", snap.Status, orchestrator.StatusComplete)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./orchestrator/ -run TestRun_CheckpointsWithStore -v`
Expected: FAIL — `store.Load` returns `ErrSessionNotFound` (no checkpoint written yet).

- [ ] **Step 3a: Add snapshot/checkpoint helpers** (`orchestrator/orchestrator.go`, near the other private helpers, e.g. after `handleError`)

```go
// snapshot captures the current loop state. Usage is copied via Snapshot() to a
// fresh, mutex-free value safe to serialize.
func (o *Orchestrator) snapshot(status Status) *Snapshot {
	msgs := make([]llm.Message, len(o.messages))
	copy(msgs, o.messages)
	return &Snapshot{
		SessionID: o.sessionID,
		Status:    status,
		Messages:  msgs,
		Usage:     o.usage.Snapshot(),
		Iteration: o.iteration,
		UpdatedAt: time.Now().UTC(),
	}
}

// checkpoint persists the current state if a store is configured; otherwise it
// is a no-op (preserving today's store-less behavior exactly).
func (o *Orchestrator) checkpoint(ctx context.Context, status Status) error {
	if o.config.SessionStore == nil {
		return nil
	}
	return o.config.SessionStore.Save(ctx, o.snapshot(status))
}
```

Add `"time"` to the `orchestrator/orchestrator.go` import block.

- [ ] **Step 3b: Extract `withSessionHooks` from `runWithHooks`** (`orchestrator/orchestrator.go`, replace the body of `runWithHooks` at lines 236-275)

Replace the existing `runWithHooks` with a thin delegator plus the extracted helper. The SessionEnd reason logic gains a `*Suspended` case (used by Tasks 6-7; harmless now):

```go
// runWithHooks wraps the normal run loop with session lifecycle hooks.
// Must be called with mutex held.
func (o *Orchestrator) runWithHooks(ctx context.Context, prompt string, source string) error {
	return o.withSessionHooks(ctx, prompt, source, func() error {
		return o.runLoop(ctx, prompt)
	})
}

// withSessionHooks fires SessionStart, runs core, and fires SessionEnd with a
// reason derived from how core returned. Must be called with mutex held.
func (o *Orchestrator) withSessionHooks(ctx context.Context, prompt, source string, core func() error) error {
	if o.hookManager != nil {
		event := &hooks.SessionStartEvent{SessionID: o.sessionID, Source: source, Prompt: prompt}
		if err := o.hookManager.FireSessionStart(ctx, event); err != nil {
			return o.handleError(err)
		}
	}

	var runErr error
	defer func() {
		if o.hookManager != nil {
			reason := "complete"
			if runErr != nil {
				var susp *Suspended
				switch {
				case errors.As(runErr, &susp):
					reason = "suspended"
				case ctx.Err() != nil:
					reason = "cancelled"
				default:
					reason = "error"
				}
			}
			event := &hooks.SessionEndEvent{SessionID: o.sessionID, Error: runErr, Reason: reason}
			_ = o.hookManager.FireSessionEnd(ctx, event) //nolint:errcheck // notification-only hook
		}
	}()

	runErr = core()
	return runErr
}
```

Add `"errors"` to the `orchestrator/orchestrator.go` import block.

- [ ] **Step 3c: Extract `runIterations` from `runLoop` and add checkpoints** (`orchestrator/orchestrator.go`, replace `runLoop` at lines 277-372)

Split the loop: `runLoop` keeps the one-time resets and delegates the `for` to `runIterations`. The body is moved **verbatim** except (1) the loop now starts at `startIter`, (2) a `running` checkpoint after a successful tool batch, and (3) a `complete` checkpoint before the final `return nil`.

```go
// runLoop executes the core think-act loop from a fresh turn. Must be called with mutex held.
func (o *Orchestrator) runLoop(ctx context.Context, prompt string) error {
	o.consecutiveToolIterations = 0
	o.justCompacted = false
	return o.runIterations(ctx, 0, prompt)
}

// runIterations runs the think-act loop starting at iteration startIter.
// Resume re-enters here after replaying a pending tool batch. Must be called with mutex held.
func (o *Orchestrator) runIterations(ctx context.Context, startIter int, prompt string) error {
	for i := startIter; i < o.config.MaxIterations; i++ {
		o.iteration = i
		select {
		case <-ctx.Done():
			return o.handleError(ctx.Err())
		default:
		}

		if result, err := o.compact(ctx); err != nil {
			return o.handleError(fmt.Errorf("compaction failed: %w", err))
		} else if result != nil {
			o.justCompacted = true
			if o.hookManager != nil {
				event := &hooks.CompactionEvent{
					SessionID:       o.sessionID,
					OriginalTokens:  result.OriginalTokens,
					CompactedTokens: result.CompactedTokens,
					MessagesRemoved: result.MessagesRemoved,
					Summary:         result.Summary,
				}
				if err := o.hookManager.FireCompaction(ctx, event); err != nil {
					return o.handleError(err)
				}
			}
		}

		if o.hookManager != nil {
			event := &hooks.IterationEvent{SessionID: o.sessionID, Iteration: i}
			if err := o.hookManager.FireIteration(ctx, event); err != nil {
				return o.handleError(err)
			}
		}

		if err := o.transition(StateStreaming); err != nil {
			return o.handleError(err)
		}

		resp, err := o.client.CreateMessage(ctx, o.buildRequest())
		o.justCompacted = false
		if err != nil {
			return o.handleError(err)
		}

		o.usage.Add(resp.Usage)
		o.processResponse(resp)

		if resp.HasToolUse() {
			o.consecutiveToolIterations++
			if err := o.executeTools(ctx, resp.ToolUses()); err != nil {
				return o.handleError(err)
			}
			if err := o.checkpoint(ctx, StatusRunning); err != nil {
				return o.handleError(fmt.Errorf("checkpoint failed: %w", err))
			}
			continue
		}
		o.consecutiveToolIterations = 0

		if o.hookManager != nil {
			stopEvent := &hooks.StopEvent{SessionID: o.sessionID, FinalText: resp.TextContent()}
			continueLoop, err := o.hookManager.FireStop(ctx, stopEvent)
			if err != nil {
				return o.handleError(err)
			}
			if continueLoop {
				o.state.Reset()
				o.messages = append(o.messages, llm.NewUserMessage("continue"))
				continue
			}
		}

		if err := o.transition(StateComplete); err != nil {
			return o.handleError(err)
		}
		o.eventBus.Publish(NewCompleteEvent(resp.TextContent()))
		if err := o.checkpoint(ctx, StatusComplete); err != nil {
			return o.handleError(fmt.Errorf("checkpoint failed: %w", err))
		}
		return nil
	}

	return o.handleError(fmt.Errorf("exceeded max iterations (%d) while processing: %s", o.config.MaxIterations, prompt))
}
```

The original `runLoop` set `o.iteration = 0` before the loop; that line is now redundant (the loop assigns `o.iteration = i` starting at 0) and is intentionally dropped.

- [ ] **Step 4: Run the new test, then the whole suite**

Run: `go test ./orchestrator/ -run TestRun_CheckpointsWithStore -v`
Expected: PASS.

Run: `go test ./orchestrator/`
Expected: PASS — **all pre-existing tests still green** (this is the behavior-preservation gate for the extraction).

- [ ] **Step 5: Commit**

```bash
git add orchestrator/orchestrator.go orchestrator/orchestrator_test.go
git commit -m "refactor: extract runIterations/withSessionHooks and add checkpointing"
```

---

## Task 6: Suspend before approval

**Files:**
- Modify: `orchestrator/orchestrator.go` (`runIterations` tool-use branch; add `pendingApproval` + `suspend`)
- Test: `orchestrator/orchestrator_test.go` (add `approvalTool` double)

**Interfaces:**
- Consumes: `Suspension`, `PendingToolCall`, `ReasonApprovalRequired`, `StatusSuspended`, `ApprovalSuspend`, `Suspended` (Task 1); `Executor.NeedsApproval` (Task 4); `snapshot`, `checkpoint` (Task 5).
- Produces:
  - `func (o *Orchestrator) pendingApproval(toolUses []llm.ContentBlock) *Suspension` — returns a `Suspension` (Reason `ReasonApprovalRequired`, `Pending` listing **all** tool uses in the batch with per-call `NeedsApproval`) iff at least one needs approval; otherwise `nil`.
  - `func (o *Orchestrator) suspend(ctx context.Context, susp Suspension) error` — transitions to `StateAwaitingApproval`, saves a `StatusSuspended` snapshot carrying `susp`, returns `*Suspended`.

- [ ] **Step 1: Write the failing test** (`orchestrator/orchestrator_test.go`, black-box)

```go
// approvalTool is a tool double that always requires approval and records
// whether it was actually executed.
type approvalTool struct {
	name     string
	executed *bool
}

func (a *approvalTool) Name() string                         { return a.name }
func (a *approvalTool) Description() string                  { return "needs approval" }
func (a *approvalTool) RequiresApproval(map[string]any) bool { return true }
func (a *approvalTool) Execute(_ context.Context, _ map[string]any) (*tool.Result, error) {
	if a.executed != nil {
		*a.executed = true
	}
	return tool.NewResult(a.name, true, "executed", ""), nil
}

func TestRun_SuspendsOnApprovalTool(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	registry := tool.NewRegistry()
	executed := false
	registry.Register(&approvalTool{name: "deploy", executed: &executed})
	executor := tool.NewExecutor(registry)
	client := &mockLLMClient{responses: []*llm.Response{
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "deploy", Input: map[string]any{"env": "prod"}}}, StopReason: llm.StopReasonToolUse},
	}}
	orch := orchestrator.NewWithConfig(client, executor, orchestrator.Config{
		MaxIterations: 5,
		SessionStore:  store,
		ApprovalMode:  orchestrator.ApprovalSuspend,
	})

	err := orch.Run(context.Background(), "ship it")

	var susp *orchestrator.Suspended
	if !errors.As(err, &susp) {
		t.Fatalf("Run err = %v, want *Suspended", err)
	}
	if susp.Suspension.Reason != orchestrator.ReasonApprovalRequired {
		t.Errorf("Reason = %q, want %q", susp.Suspension.Reason, orchestrator.ReasonApprovalRequired)
	}
	if len(susp.Suspension.Pending) != 1 || susp.Suspension.Pending[0].ID != "call-1" || !susp.Suspension.Pending[0].NeedsApproval {
		t.Errorf("Pending = %+v", susp.Suspension.Pending)
	}
	if executed {
		t.Error("tool executed despite suspension; must not run before approval")
	}
	if orch.State() != orchestrator.StateAwaitingApproval {
		t.Errorf("State = %q, want %q", orch.State(), orchestrator.StateAwaitingApproval)
	}
	snap, err := store.Load(context.Background(), orch.SessionID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.Status != orchestrator.StatusSuspended || snap.Suspension == nil {
		t.Errorf("snapshot Status=%q Suspension=%+v", snap.Status, snap.Suspension)
	}
}
```

Ensure `orchestrator_test.go` imports `errors` and `github.com/2389-research/mux/session` (add if missing).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./orchestrator/ -run TestRun_SuspendsOnApprovalTool -v`
Expected: FAIL — currently the tool executes synchronously (no approval func set → `Run` returns an `ErrApprovalRequired`-wrapped error, not `*Suspended`), and no `awaiting_approval` state.

- [ ] **Step 3a: Add `pendingApproval` and `suspend`** (`orchestrator/orchestrator.go`)

```go
// pendingApproval returns a Suspension if any tool use in the batch needs
// approval, listing every call in the batch (so Resume replays the whole turn).
// Returns nil when nothing needs approval.
func (o *Orchestrator) pendingApproval(toolUses []llm.ContentBlock) *Suspension {
	pending := make([]PendingToolCall, 0, len(toolUses))
	needsAny := false
	for _, use := range toolUses {
		needs := o.executor.NeedsApproval(use.Name, use.Input)
		if needs {
			needsAny = true
		}
		pending = append(pending, PendingToolCall{
			ID:            use.ID,
			Name:          use.Name,
			Params:        use.Input,
			NeedsApproval: needs,
		})
	}
	if !needsAny {
		return nil
	}
	return &Suspension{Reason: ReasonApprovalRequired, Pending: pending}
}

// suspend checkpoints the loop awaiting approval and returns the *Suspended
// sentinel. Not routed through handleError: suspension is a pause, not a failure.
func (o *Orchestrator) suspend(ctx context.Context, susp Suspension) error {
	if err := o.transition(StateAwaitingApproval); err != nil {
		return o.handleError(err)
	}
	snap := o.snapshot(StatusSuspended)
	snap.Suspension = &susp
	if err := o.config.SessionStore.Save(ctx, snap); err != nil {
		return o.handleError(fmt.Errorf("suspend checkpoint failed: %w", err))
	}
	return &Suspended{SessionID: o.sessionID, Suspension: susp}
}
```

- [ ] **Step 3b: Gate the tool batch on suspension** (`orchestrator/orchestrator.go`, in `runIterations`, the `if resp.HasToolUse()` branch from Task 5)

Replace that branch's body with:

```go
		if resp.HasToolUse() {
			o.consecutiveToolIterations++
			toolUses := resp.ToolUses()
			if o.config.ApprovalMode == ApprovalSuspend {
				if susp := o.pendingApproval(toolUses); susp != nil {
					return o.suspend(ctx, *susp)
				}
			}
			if err := o.executeTools(ctx, toolUses); err != nil {
				return o.handleError(err)
			}
			if err := o.checkpoint(ctx, StatusRunning); err != nil {
				return o.handleError(fmt.Errorf("checkpoint failed: %w", err))
			}
			continue
		}
```

- [ ] **Step 4: Run the new test, then the suite**

Run: `go test ./orchestrator/ -run TestRun_SuspendsOnApprovalTool -v`
Expected: PASS.

Run: `go test ./orchestrator/`
Expected: PASS (all green).

- [ ] **Step 5: Commit**

```bash
git add orchestrator/orchestrator.go orchestrator/orchestrator_test.go
git commit -m "feat: suspend the loop before an approval-required tool"
```

---

## Task 7: `Resume`

**Files:**
- Modify: `orchestrator/orchestrator.go` (add `Resume`, `resumeCore`, `lastAssistantToolUses`, `installDecisionApproval`)
- Test: `orchestrator/orchestrator_test.go`

**Interfaces:**
- Consumes: `Store.Load`, `Decision`, `Decision.approves`, `Snapshot`, `StatusSuspended`, `Suspension` (Tasks 1-2); `Executor.NeedsApproval`, `Executor.ApprovalFunc`, `Executor.SetApprovalFunc` (Task 4); `withSessionHooks`, `runIterations`, `checkpoint` (Task 5); `executeTools` (existing).
- Produces:
  - `func (o *Orchestrator) Resume(ctx context.Context, sessionID string, d Decision) error`.

- [ ] **Step 1: Write the failing tests** (`orchestrator/orchestrator_test.go`, black-box)

```go
func TestResume_Approve_ExecutesAndCompletes(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	registry := tool.NewRegistry()
	executed := false
	registry.Register(&approvalTool{name: "deploy", executed: &executed})
	executor := tool.NewExecutor(registry)
	// First response asks for the tool; after resume the second response ends the turn.
	client := &mockLLMClient{responses: []*llm.Response{
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "deploy", Input: map[string]any{}}}, StopReason: llm.StopReasonToolUse},
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "deployed"}}, StopReason: llm.StopReasonEndTurn},
	}}
	cfg := orchestrator.Config{MaxIterations: 5, SessionStore: store, ApprovalMode: orchestrator.ApprovalSuspend}
	orch := orchestrator.NewWithConfig(client, executor, cfg)

	var susp *orchestrator.Suspended
	if err := orch.Run(context.Background(), "ship"); !errors.As(err, &susp) {
		t.Fatalf("Run err = %v, want *Suspended", err)
	}

	if err := orch.Resume(context.Background(), orch.SessionID(), orchestrator.Approve(true)); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !executed {
		t.Error("approved tool did not execute on Resume")
	}
	snap, err := store.Load(context.Background(), orch.SessionID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.Status != orchestrator.StatusComplete {
		t.Errorf("final Status = %q, want %q", snap.Status, orchestrator.StatusComplete)
	}
}

func TestResume_Deny_SynthesizesErrorResultAndCompletes(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	registry := tool.NewRegistry()
	executed := false
	registry.Register(&approvalTool{name: "deploy", executed: &executed})
	executor := tool.NewExecutor(registry)
	client := &mockLLMClient{responses: []*llm.Response{
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "deploy", Input: map[string]any{}}}, StopReason: llm.StopReasonToolUse},
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok, cancelled"}}, StopReason: llm.StopReasonEndTurn},
	}}
	cfg := orchestrator.Config{MaxIterations: 5, SessionStore: store, ApprovalMode: orchestrator.ApprovalSuspend}
	orch := orchestrator.NewWithConfig(client, executor, cfg)

	var susp *orchestrator.Suspended
	if err := orch.Run(context.Background(), "ship"); !errors.As(err, &susp) {
		t.Fatalf("Run err = %v, want *Suspended", err)
	}
	if err := orch.Resume(context.Background(), orch.SessionID(), orchestrator.Approve(false)); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if executed {
		t.Error("denied tool must NOT execute")
	}
	// The denial must appear in history as an error tool_result so the model can react.
	msgs := orch.Messages()
	foundDenial := false
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.ContentTypeToolResult && b.ToolUseID == "call-1" && b.IsError {
				foundDenial = true
			}
		}
	}
	if !foundDenial {
		t.Error("no error tool_result for the denied call-1 in history")
	}
}

func TestResume_NotSuspended(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)
	cfg := orchestrator.Config{MaxIterations: 5, SessionStore: store, ApprovalMode: orchestrator.ApprovalSuspend}
	orch := orchestrator.NewWithConfig(&mockLLMClient{}, executor, cfg)
	// No snapshot saved for this ID.
	if err := orch.Resume(context.Background(), "session-missing", orchestrator.Approve(true)); err == nil {
		t.Fatal("Resume on missing session = nil error, want error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./orchestrator/ -run TestResume -v`
Expected: FAIL — `orch.Resume undefined`.

- [ ] **Step 3: Implement `Resume` and helpers** (`orchestrator/orchestrator.go`)

```go
// Resume reloads a suspended session and continues it using the caller's
// approval Decision. Approved tools execute; denied tools become error
// tool_results (so the model can react). The loop then continues until the next
// suspension or completion. Returns *Suspended again if it re-suspends.
func (o *Orchestrator) Resume(ctx context.Context, sessionID string, d Decision) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.config.SessionStore == nil {
		return fmt.Errorf("mux: Resume requires a SessionStore")
	}
	snap, err := o.config.SessionStore.Load(ctx, sessionID)
	if err != nil {
		return err
	}
	if snap.Status != StatusSuspended || snap.Suspension == nil {
		return fmt.Errorf("mux: session %s is not suspended (status %q)", sessionID, snap.Status)
	}

	// Restore loop state from the snapshot.
	o.sessionID = snap.SessionID
	o.messages = make([]llm.Message, len(snap.Messages))
	copy(o.messages, snap.Messages)
	o.usage.Restore(&snap.Usage)
	o.iteration = snap.Iteration
	o.consecutiveToolIterations = 0
	o.justCompacted = false
	o.state.Reset()
	defer o.eventBus.Reset()

	return o.withSessionHooks(ctx, "", "resume", func() error {
		return o.resumeCore(ctx, d)
	})
}

// resumeCore replays the pending tool batch under the Decision, then continues
// the loop from the next iteration. Must be called with mutex held.
func (o *Orchestrator) resumeCore(ctx context.Context, d Decision) error {
	toolUses := lastAssistantToolUses(o.messages)
	if len(toolUses) == 0 {
		return o.handleError(fmt.Errorf("mux: resume found no pending tool calls"))
	}

	if err := o.transition(StateStreaming); err != nil {
		return o.handleError(err)
	}

	restore := o.installDecisionApproval(toolUses, d)
	err := o.executeTools(ctx, toolUses)
	restore()
	if err != nil {
		return o.handleError(err)
	}
	if err := o.checkpoint(ctx, StatusRunning); err != nil {
		return o.handleError(fmt.Errorf("checkpoint failed: %w", err))
	}

	return o.runIterations(ctx, o.iteration+1, "")
}

// installDecisionApproval sets a temporary approval func that resolves each
// approval-required tool in toolUses (in batch order) against d, and returns a
// closure that restores the previous approval func. executeTools invokes the
// approval func only for tools whose RequiresApproval is true, in the same order
// as toolUses, so an ordered queue of those tool-use IDs aligns 1:1 with the calls.
func (o *Orchestrator) installDecisionApproval(toolUses []llm.ContentBlock, d Decision) func() {
	queue := make([]string, 0, len(toolUses))
	for _, use := range toolUses {
		if o.executor.NeedsApproval(use.Name, use.Input) {
			queue = append(queue, use.ID)
		}
	}
	prev := o.executor.ApprovalFunc()
	idx := 0
	o.executor.SetApprovalFunc(func(_ context.Context, _ tool.Tool, _ map[string]any) (bool, error) {
		id := ""
		if idx < len(queue) {
			id = queue[idx]
		}
		idx++
		return d.approves(id), nil
	})
	return func() { o.executor.SetApprovalFunc(prev) }
}

// lastAssistantToolUses returns the tool_use blocks of the most recent assistant
// message — the authoritative pending batch to replay on resume.
func lastAssistantToolUses(messages []llm.Message) []llm.ContentBlock {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleAssistant {
			continue
		}
		uses := make([]llm.ContentBlock, 0)
		for _, b := range messages[i].Blocks {
			if b.Type == llm.ContentTypeToolUse {
				uses = append(uses, b)
			}
		}
		return uses
	}
	return nil
}
```

- [ ] **Step 4: Run the resume tests, then the suite**

Run: `go test ./orchestrator/ -run TestResume -v`
Expected: PASS (3 tests).

Run: `go test ./orchestrator/`
Expected: PASS (all green).

- [ ] **Step 5: Commit**

```bash
git add orchestrator/orchestrator.go orchestrator/orchestrator_test.go
git commit -m "feat: add Orchestrator.Resume for decision-driven continuation"
```

---

## Task 8: Cross-process resume (end-to-end)

Proves the headline guarantee: a **fresh** orchestrator instance (simulating a process restart — new in-memory state, new executor) resumes a session purely from the on-disk store.

**Files:**
- Test: `orchestrator/orchestrator_test.go` (black-box; reuses `approvalTool`, `mockLLMClient`)

**Interfaces:**
- Consumes: everything from Tasks 1-7. No new production code.

- [ ] **Step 1: Write the failing test**

```go
func TestResume_CrossProcess(t *testing.T) {
	dir := t.TempDir()
	const prompt = "ship"

	// --- "Process A": suspends and persists, then is discarded. ---
	storeA := session.NewFileStore(dir)
	regA := tool.NewRegistry()
	regA.Register(&approvalTool{name: "deploy"})
	execA := tool.NewExecutor(regA)
	clientA := &mockLLMClient{responses: []*llm.Response{
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "deploy", Input: map[string]any{}}}, StopReason: llm.StopReasonToolUse},
	}}
	orchA := orchestrator.NewWithConfig(clientA, execA, orchestrator.Config{
		MaxIterations: 5, SessionStore: storeA, ApprovalMode: orchestrator.ApprovalSuspend,
	})
	var susp *orchestrator.Suspended
	if err := orchA.Run(context.Background(), prompt); !errors.As(err, &susp) {
		t.Fatalf("process A Run err = %v, want *Suspended", err)
	}
	sessionID := orchA.SessionID()

	// --- "Process B": brand-new orchestrator + executor, same on-disk store. ---
	storeB := session.NewFileStore(dir)
	regB := tool.NewRegistry()
	executed := false
	regB.Register(&approvalTool{name: "deploy", executed: &executed})
	execB := tool.NewExecutor(regB)
	clientB := &mockLLMClient{responses: []*llm.Response{
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "deployed"}}, StopReason: llm.StopReasonEndTurn},
	}}
	orchB := orchestrator.NewWithConfig(clientB, execB, orchestrator.Config{
		MaxIterations: 5, SessionStore: storeB, ApprovalMode: orchestrator.ApprovalSuspend,
	})

	if err := orchB.Resume(context.Background(), sessionID, orchestrator.Approve(true)); err != nil {
		t.Fatalf("process B Resume: %v", err)
	}
	if !executed {
		t.Error("tool did not execute in resuming process")
	}
	snap, err := storeB.Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.Status != orchestrator.StatusComplete {
		t.Errorf("final Status = %q, want %q", snap.Status, orchestrator.StatusComplete)
	}
}
```

- [ ] **Step 2: Run test to verify it fails (or passes outright)**

Run: `go test ./orchestrator/ -run TestResume_CrossProcess -v`
Expected: PASS if Tasks 1-7 are correct. If it fails, the failure pinpoints a state-restoration gap (most likely: messages/usage/iteration not faithfully restored, or the `Idle→Streaming→ExecutingTool` transition path in `resumeCore`). Fix in the relevant prior task's code — do not weaken the test.

- [ ] **Step 3: Commit**

```bash
git add orchestrator/orchestrator_test.go
git commit -m "test: cross-process suspend/resume end-to-end"
```

---

## Task 9: Agent pass-through + `Agent.Resume`

**Files:**
- Modify: `agent/config.go` (add fields to `Config`)
- Modify: `agent/agent.go` (wire fields into the orchestrator config in `init`; add `Resume`)
- Test: `agent/agent_test.go` (or the nearest existing agent test file)

**Interfaces:**
- Consumes: `orchestrator.Store`, `orchestrator.ApprovalMode`, `orchestrator.Decision`, `orchestrator.Config` fields (Tasks 1-2); `Orchestrator.Resume` (Task 7).
- Produces:
  - `agent.Config.SessionStore orchestrator.Store` and `agent.Config.ApprovalMode orchestrator.ApprovalMode`.
  - `func (a *Agent) Resume(ctx context.Context, sessionID string, d orchestrator.Decision) error`.

**Before writing:** read `agent/agent.go`'s `init` to find exactly where `orchConfig` (the `orchestrator.Config` literal) is built, and confirm the `Agent` field that holds the orchestrator (e.g. `a.orch`). Match the existing field-assignment style.

- [ ] **Step 1: Write the failing test** (`agent/agent_test.go`)

```go
func TestAgent_SuspendAndResume(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	// Build an agent whose registry has an approval-required tool, ApprovalSuspend mode.
	// (Use the agent package's existing construction helpers / mock client pattern;
	// mirror a nearby agent test for how to register tools and inject the LLM client.)
	ag := newTestAgentWithApprovalTool(t, store) // helper: see note below

	var susp *orchestrator.Suspended
	if err := ag.Run(context.Background(), "ship"); !errors.As(err, &susp) {
		t.Fatalf("Agent.Run err = %v, want *Suspended", err)
	}
	if err := ag.Resume(context.Background(), ag.SessionID(), orchestrator.Approve(true)); err != nil {
		t.Fatalf("Agent.Resume: %v", err)
	}
	snap, err := store.Load(context.Background(), ag.SessionID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.Status != orchestrator.StatusComplete {
		t.Errorf("final Status = %q, want %q", snap.Status, orchestrator.StatusComplete)
	}
}
```

Note on the helper: implement `newTestAgentWithApprovalTool` inline in the test by following the construction pattern in the existing `agent` tests (how they build a `Config`, register tools, and inject a scripted `llm.Client`). It must set `Config.SessionStore = store`, `Config.ApprovalMode = orchestrator.ApprovalSuspend`, register a tool whose `RequiresApproval` returns true, and script a two-response client (tool_use, then end_turn) like Task 7. Confirm `Agent` exposes `SessionID()`; if not, use the orchestrator's ID via whatever accessor the agent provides (read `agent/agent.go`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./agent/ -run TestAgent_SuspendAndResume -v`
Expected: FAIL — `unknown field SessionStore in agent.Config` and/or `ag.Resume undefined`.

- [ ] **Step 3a: Add agent `Config` fields** (`agent/config.go`, in the `Config` struct)

```go
	// SessionStore enables durable suspend/resume; passed through to the orchestrator.
	SessionStore orchestrator.Store
	// ApprovalMode selects synchronous approval (default) or suspend-on-approval.
	ApprovalMode orchestrator.ApprovalMode
```

- [ ] **Step 3b: Wire into the orchestrator config** (`agent/agent.go`, where `orchConfig` is built in `init`)

Add to the `orchestrator.Config{...}` literal:

```go
		SessionStore: a.config.SessionStore,
		ApprovalMode: a.config.ApprovalMode,
```

- [ ] **Step 3c: Add `Agent.Resume`** (`agent/agent.go`, near `Run`/`Continue`)

```go
// Resume continues a suspended session using the caller's approval Decision.
// See orchestrator.Resume. Returns *orchestrator.Suspended if it re-suspends.
func (a *Agent) Resume(ctx context.Context, sessionID string, d orchestrator.Decision) error {
	return a.orch.Resume(ctx, sessionID, d)
}
```

If the `Agent` struct's orchestrator field is named other than `orch`, use that name (confirmed when reading `agent/agent.go`).

- [ ] **Step 4: Run the agent test, then the full suite**

Run: `go test ./agent/ -run TestAgent_SuspendAndResume -v`
Expected: PASS.

Run: `go test ./...`
Expected: PASS (whole repo green).

- [ ] **Step 5: Commit**

```bash
git add agent/config.go agent/agent.go agent/agent_test.go
git commit -m "feat: thread SessionStore/ApprovalMode through Agent and add Agent.Resume"
```

---

## Final verification (run after Task 9)

- [ ] `gofmt -l .` → no files listed.
- [ ] `go vet ./...` → clean (in particular, no copylocks findings around `TokenUsage`).
- [ ] `go test ./...` → all packages pass.
- [ ] `golangci-lint run` (if configured; the pre-commit hook runs it) → clean.

---

## Self-Review — spec coverage

| Spec section (Part B) | Covered by |
| --- | --- |
| B1 Suspension as typed sentinel (`errors.As`) | Task 1 (`Suspended`), Task 6 (returned from `suspend`) |
| B1 `Status` / `Reason` vocabulary (`authorization.required`, `input.requested`) | Task 1 |
| B2 `Snapshot` shape (messages, usage, iteration, suspension) | Task 1; populated in Task 5 (`snapshot`) |
| B2 `Store` interface (Save/Load/List/Delete) + `ErrSessionNotFound` | Task 1; file impl Task 3 |
| B2 `TokenUsage` serialization via `Snapshot()` copy; restore | Task 1 (`Snapshot.Usage TokenUsage`), Task 2 (`Restore`) |
| B3 `ApprovalMode` (Sync default / Suspend); panic when Suspend+nil store | Task 1 (enum), Task 2 (panic) |
| B3 checkpoint regardless of mode; suspend only in ApprovalSuspend | Task 5 (`checkpoint` always when store set), Task 6 (suspend gated on mode) |
| B4 Suspend at the approval boundary; tool not run before approval | Task 6 |
| B4 `StateAwaitingApproval` wired (previously defined, unentered) | Task 6 (`suspend` transitions into it) |
| B5 `Resume` + `Decision` / `Approve(all)` / `DefaultApprove` fallback | Task 1 (`Decision`, `Approve`, `approves`), Task 7 (`Resume`) |
| B5 Denial-turn synthesis (denied → error tool_result) | Task 7 (reuses executor `ErrApprovalDenied` → `IsError` block via decision approval func) |
| B6 Cross-process / restart-survivable resume | Task 8 |
| B7 Agent pass-through + `Agent.Resume` | Task 9 |
| Appendix: package layout (types in `orchestrator`, file impl in `session`; no cycle) | Tasks 1 & 3 |
| Appendix: `Run`/`Continue` signatures unchanged | Honored throughout (suspension via `*Suspended`) |

**Open follow-ups (out of scope for this plan, by design):** `ReasonInputRequired` is defined but unused (a later input-request gene); mid-tool-crash is at-least-once best-effort (a tool that ran but whose result was not yet checkpointed re-runs on resume) — acceptable per the spec's durability goal and not separately tested here.
