// ABOUTME: Covers tool-batch integrity across cancellation, resume retries, and
// ABOUTME: a failing running checkpoint - the paths that can orphan or re-run calls.
package orchestrator_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/session"
	"github.com/2389-research/mux/tool"
)

// gatedTool requires approval and runs a caller-supplied func, so a test can
// control when a call finishes and count how often it ran.
type gatedTool struct {
	name string
	run  func(ctx context.Context) (*tool.Result, error)
}

func (g *gatedTool) Name() string                         { return g.name }
func (g *gatedTool) Description() string                  { return "needs approval" }
func (g *gatedTool) RequiresApproval(map[string]any) bool { return true }
func (g *gatedTool) Execute(ctx context.Context, _ map[string]any) (*tool.Result, error) {
	return g.run(ctx)
}

// checkpointFailStore delegates to an inner store but fails every Save carrying
// one status, standing in for a durable store that goes away mid-turn.
type checkpointFailStore struct {
	inner  orchestrator.Store
	failOn orchestrator.Status
	err    error
}

func (s *checkpointFailStore) Save(ctx context.Context, snap *orchestrator.Snapshot) error {
	if snap.Status == s.failOn {
		return s.err
	}
	return s.inner.Save(ctx, snap)
}

func (s *checkpointFailStore) Load(ctx context.Context, id string) (*orchestrator.Snapshot, error) {
	return s.inner.Load(ctx, id)
}
func (s *checkpointFailStore) List(ctx context.Context) ([]string, error) { return s.inner.List(ctx) }
func (s *checkpointFailStore) Delete(ctx context.Context, id string) error {
	return s.inner.Delete(ctx, id)
}

// toolResults indexes every tool_result block in a history by call ID and
// reports any ID carrying more than one result.
func toolResults(msgs []llm.Message) (map[string]llm.ContentBlock, []string) {
	byID := make(map[string]llm.ContentBlock)
	var duplicates []string
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type != llm.ContentTypeToolResult {
				continue
			}
			if _, seen := byID[b.ToolUseID]; seen {
				duplicates = append(duplicates, b.ToolUseID)
			}
			byID[b.ToolUseID] = b
		}
	}
	return byID, duplicates
}

// A cancelled tool batch must leave a complete turn behind: every tool_use in
// the assistant message keeps a matching tool_result, so the next request is not
// rejected as malformed. Calls that ran keep their real output; calls the
// cancellation cut off report that they never ran.
func TestRun_CancelledMidBatch_LeavesNoOrphanedToolUse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	blockerStarted := make(chan struct{})
	blockerRelease := make(chan struct{})
	firstRuns, lastRuns := 0, 0

	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "first", execFunc: func(context.Context, map[string]any) (*tool.Result, error) {
		firstRuns++
		return tool.NewResult("first", true, "first done", ""), nil
	}})
	registry.Register(&mockTool{name: "blocker", execFunc: func(ctx context.Context, _ map[string]any) (*tool.Result, error) {
		close(blockerStarted)
		<-blockerRelease
		return nil, ctx.Err()
	}})
	registry.Register(&mockTool{name: "last", execFunc: func(context.Context, map[string]any) (*tool.Result, error) {
		lastRuns++
		return tool.NewResult("last", true, "last done", ""), nil
	}})
	executor := tool.NewExecutor(registry)
	client := &mockLLMClient{responses: []*llm.Response{{
		Content: []llm.ContentBlock{
			{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "first", Input: map[string]any{}},
			{Type: llm.ContentTypeToolUse, ID: "call-2", Name: "blocker", Input: map[string]any{}},
			{Type: llm.ContentTypeToolUse, ID: "call-3", Name: "last", Input: map[string]any{}},
		},
		StopReason: llm.StopReasonToolUse,
	}}}
	orch := orchestrator.New(client, executor)

	errCh := make(chan error, 1)
	go func() { errCh <- orch.Run(ctx, "run the batch") }()

	<-blockerStarted
	cancel()
	close(blockerRelease)

	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
	if firstRuns != 1 {
		t.Errorf("first ran %d times, want 1", firstRuns)
	}
	if lastRuns != 0 {
		t.Errorf("last ran %d times after cancellation, want 0", lastRuns)
	}

	msgs := orch.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleUser {
		t.Fatalf("history ends with a %s message; the assistant tool_use batch is orphaned", last.Role)
	}
	wantIDs := []string{"call-1", "call-2", "call-3"}
	if len(last.Blocks) != len(wantIDs) {
		t.Fatalf("final message has %d blocks, want %d (one tool_result per call)", len(last.Blocks), len(wantIDs))
	}
	for i, want := range wantIDs {
		if got := last.Blocks[i]; got.Type != llm.ContentTypeToolResult || got.ToolUseID != want {
			t.Fatalf("block %d = {Type:%s ToolUseID:%q}, want a tool_result for %q", i, got.Type, got.ToolUseID, want)
		}
	}
	if b := last.Blocks[0]; b.IsError || b.Text != "first done" {
		t.Errorf("call-1 result = {IsError:%v Text:%q}, want the completed tool's own output", b.IsError, b.Text)
	}
	if b := last.Blocks[1]; !b.IsError {
		t.Errorf("call-2 result = {IsError:%v Text:%q}, want the cancelled tool's error", b.IsError, b.Text)
	}
	if b := last.Blocks[2]; !b.IsError || b.Text != orchestrator.ToolCancelledText {
		t.Errorf("call-3 result = {IsError:%v Text:%q}, want %q", b.IsError, b.Text, orchestrator.ToolCancelledText)
	}
}

// A batch cancelled between calls must not run the completed calls again when
// the caller retries Resume: the repaired turn is persisted, so the retry sees
// which calls already had side effects and dispatches only the rest.
func TestResume_AfterCancelledBatch_DoesNotRerunCompletedCall(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())

	auditStarted := make(chan struct{})
	auditRelease := make(chan struct{})
	var auditOnce sync.Once
	auditRuns, deployRuns := 0, 0

	registry := tool.NewRegistry()
	registry.Register(&gatedTool{name: "audit", run: func(context.Context) (*tool.Result, error) {
		auditRuns++
		// Only the first run blocks; a second one must show up as a count, not
		// as a deadlock on an already-closed channel.
		auditOnce.Do(func() {
			close(auditStarted)
			<-auditRelease
		})
		// The audit finished its work; the cancellation lands on the next call.
		return tool.NewResult("audit", true, "audited", ""), nil
	}})
	registry.Register(&gatedTool{name: "deploy", run: func(context.Context) (*tool.Result, error) {
		deployRuns++
		return tool.NewResult("deploy", true, "deployed", ""), nil
	}})
	executor := tool.NewExecutor(registry)
	client := &mockLLMClient{responses: []*llm.Response{
		{Content: []llm.ContentBlock{
			{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "audit", Input: map[string]any{}},
			{Type: llm.ContentTypeToolUse, ID: "call-2", Name: "deploy", Input: map[string]any{}},
		}, StopReason: llm.StopReasonToolUse},
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "shipped"}}, StopReason: llm.StopReasonEndTurn},
	}}
	orch := orchestrator.NewWithConfig(client, executor, orchestrator.Config{
		MaxIterations: 5, SessionStore: store, ApprovalMode: orchestrator.ApprovalSuspend,
	})

	var susp *orchestrator.Suspended
	if err := orch.Run(context.Background(), "ship"); !errors.As(err, &susp) {
		t.Fatalf("Run err = %v, want *Suspended", err)
	}
	sessionID := orch.SessionID()

	errCh := make(chan error, 1)
	go func() { errCh <- orch.Resume(ctx, sessionID, orchestrator.Approve(true)) }()
	<-auditStarted
	cancel()
	close(auditRelease)
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Resume err = %v, want context.Canceled", err)
	}

	// The cancelled turn must be durable, or the retry below cannot know that
	// audit already ran.
	snap, err := store.Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Load after cancellation: %v", err)
	}
	if snap.Status != orchestrator.StatusSuspended || snap.Suspension == nil {
		t.Fatalf("snapshot after cancellation: Status=%q Suspension=%+v, want a suspended session", snap.Status, snap.Suspension)
	}
	if snap.Suspension.Reason != orchestrator.ReasonCancelled {
		t.Errorf("Reason = %q, want %q", snap.Suspension.Reason, orchestrator.ReasonCancelled)
	}
	if len(snap.Suspension.Pending) != 1 || snap.Suspension.Pending[0].ID != "call-2" {
		t.Errorf("Pending = %+v, want only the call that never ran (call-2)", snap.Suspension.Pending)
	}

	if err := orch.Resume(context.Background(), sessionID, orchestrator.Approve(true)); err != nil {
		t.Fatalf("retried Resume: %v", err)
	}
	if auditRuns != 1 {
		t.Errorf("audit ran %d times across both Resumes, want exactly 1", auditRuns)
	}
	if deployRuns != 1 {
		t.Errorf("deploy ran %d times, want exactly 1", deployRuns)
	}

	results, duplicates := toolResults(orch.Messages())
	if len(duplicates) > 0 {
		t.Errorf("duplicate tool_result blocks for %v; one call must carry one result", duplicates)
	}
	for _, id := range []string{"call-1", "call-2"} {
		b, ok := results[id]
		if !ok {
			t.Fatalf("no tool_result for %s in final history", id)
		}
		if b.IsError {
			t.Errorf("%s result = {IsError:true Text:%q}, want the successful run", id, b.Text)
		}
	}
	final, err := store.Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("Load after retry: %v", err)
	}
	if final.Status != orchestrator.StatusComplete {
		t.Errorf("final Status = %q, want %q", final.Status, orchestrator.StatusComplete)
	}
}

// A StatusRunning checkpoint that fails after a batch ran leaves the durable
// snapshot behind the tool effects. The orchestrator cannot tell a lost result
// from a call that never ran, so it reports the calls whose results are in
// memory only instead of re-running them.
func TestRun_CheckpointFailureAfterToolBatch_ReportsUnpersistedCalls(t *testing.T) {
	boom := errors.New("store offline")
	store := &checkpointFailStore{inner: session.NewFileStore(t.TempDir()), failOn: orchestrator.StatusRunning, err: boom}
	runs := 0
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "deploy", execFunc: func(context.Context, map[string]any) (*tool.Result, error) {
		runs++
		return tool.NewResult("deploy", true, "deployed", ""), nil
	}})
	executor := tool.NewExecutor(registry)
	client := &mockLLMClient{responses: []*llm.Response{
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "deploy", Input: map[string]any{}}}, StopReason: llm.StopReasonToolUse},
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}}, StopReason: llm.StopReasonEndTurn},
	}}
	orch := orchestrator.NewWithConfig(client, executor, orchestrator.Config{MaxIterations: 5, SessionStore: store})

	err := orch.Run(context.Background(), "go")

	var ckErr *orchestrator.CheckpointError
	if !errors.As(err, &ckErr) {
		t.Fatalf("Run err = %v (%T), want *orchestrator.CheckpointError", err, err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("Run err does not wrap the store error: %v", err)
	}
	if ckErr.SessionID != orch.SessionID() {
		t.Errorf("CheckpointError.SessionID = %q, want %q", ckErr.SessionID, orch.SessionID())
	}
	if len(ckErr.Calls) != 1 || ckErr.Calls[0] != "call-1" {
		t.Errorf("CheckpointError.Calls = %v, want [call-1] - the calls whose results are not durable", ckErr.Calls)
	}
	if runs != 1 {
		t.Errorf("deploy ran %d times, want 1; a failed checkpoint must not re-run the batch", runs)
	}
	if orch.State() != orchestrator.StateError {
		t.Errorf("State = %q, want %q", orch.State(), orchestrator.StateError)
	}
}

// The same report must reach a caller whose resumed batch cannot be persisted:
// the store still holds the suspended snapshot, so a blind retry would dispatch
// the approved calls a second time.
func TestResume_CheckpointFailureAfterToolBatch_ReportsUnpersistedCalls(t *testing.T) {
	boom := errors.New("store offline")
	store := &checkpointFailStore{inner: session.NewFileStore(t.TempDir()), failOn: orchestrator.StatusRunning, err: boom}
	runs := 0
	registry := tool.NewRegistry()
	registry.Register(&gatedTool{name: "deploy", run: func(context.Context) (*tool.Result, error) {
		runs++
		return tool.NewResult("deploy", true, "deployed", ""), nil
	}})
	executor := tool.NewExecutor(registry)
	client := &mockLLMClient{responses: []*llm.Response{
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "deploy", Input: map[string]any{}}}, StopReason: llm.StopReasonToolUse},
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}}, StopReason: llm.StopReasonEndTurn},
	}}
	orch := orchestrator.NewWithConfig(client, executor, orchestrator.Config{
		MaxIterations: 5, SessionStore: store, ApprovalMode: orchestrator.ApprovalSuspend,
	})

	var susp *orchestrator.Suspended
	if err := orch.Run(context.Background(), "ship"); !errors.As(err, &susp) {
		t.Fatalf("Run err = %v, want *Suspended", err)
	}

	err := orch.Resume(context.Background(), orch.SessionID(), orchestrator.Approve(true))

	var ckErr *orchestrator.CheckpointError
	if !errors.As(err, &ckErr) {
		t.Fatalf("Resume err = %v (%T), want *orchestrator.CheckpointError", err, err)
	}
	if len(ckErr.Calls) != 1 || ckErr.Calls[0] != "call-1" {
		t.Errorf("CheckpointError.Calls = %v, want [call-1]", ckErr.Calls)
	}
	if runs != 1 {
		t.Errorf("deploy ran %d times, want 1", runs)
	}
	// The durable record is still the pre-batch suspension: the error is the
	// only warning the caller gets that retrying would run deploy again.
	snap, loadErr := store.Load(context.Background(), orch.SessionID())
	if loadErr != nil {
		t.Fatalf("Load: %v", loadErr)
	}
	if snap.Status != orchestrator.StatusSuspended {
		t.Errorf("snapshot Status = %q, want %q (the failed checkpoint changed nothing)", snap.Status, orchestrator.StatusSuspended)
	}
}
