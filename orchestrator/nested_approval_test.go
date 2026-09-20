// ABOUTME: Pins that a nested tool call resolves approval against its own
// ABOUTME: identity instead of inheriting the outer call's resumed decision.
package orchestrator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/session"
	"github.com/2389-research/mux/tool"
)

// nestingTool runs nested tool calls through the same executor it was
// registered on, the way a compose- or sub-agent-style tool would.
type nestingTool struct {
	name     string
	executor *tool.Executor
	inner    string
	err      error
}

func (n *nestingTool) Name() string                         { return n.name }
func (n *nestingTool) Description() string                  { return "runs a nested tool call" }
func (n *nestingTool) RequiresApproval(map[string]any) bool { return true }
func (n *nestingTool) Execute(ctx context.Context, _ map[string]any) (*tool.Result, error) {
	n.err = nil
	_, err := n.executor.Execute(ctx, n.inner, map[string]any{})
	n.err = err
	if err != nil {
		return nil, err
	}
	return tool.NewResult(n.name, true, "outer and inner ran", ""), nil
}

// A resumed batch approves the outer call by ID. The nested call the outer tool
// makes is its own call with its own identity: it must be resolved against the
// caller's approval func, not against the outer call's decision.
func TestResume_NestedToolCall_ResolvesItsOwnApproval(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	registry := tool.NewRegistry()
	innerRuns := 0
	var nested *nestingTool
	registry.Register(&gatedTool{name: "inner", run: func(context.Context) (*tool.Result, error) {
		innerRuns++
		return tool.NewResult("inner", true, "inner ran", ""), nil
	}})
	executor := tool.NewExecutor(registry)
	nested = &nestingTool{name: "outer", executor: executor, inner: "inner"}
	registry.Register(nested)

	var asked []string
	executor.SetApprovalFunc(func(_ context.Context, tl tool.Tool, _ map[string]any) (bool, error) {
		asked = append(asked, tl.Name())
		return false, nil
	})

	client := scriptedLLM([]*llm.Response{
		{Content: []llm.ContentBlock{
			{Type: llm.ContentTypeToolUse, ID: "call-outer", Name: "outer", Input: map[string]any{}},
		}, StopReason: llm.StopReasonToolUse},
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}}, StopReason: llm.StopReasonEndTurn},
	})
	orch := orchestrator.NewWithConfig(client, executor, orchestrator.Config{
		MaxIterations: 5, SessionStore: store, ApprovalMode: orchestrator.ApprovalSuspend,
	})

	var susp *orchestrator.Suspended
	if err := orch.Run(context.Background(), "go"); !errors.As(err, &susp) {
		t.Fatalf("Run err = %v, want *Suspended", err)
	}

	// The decision approves the outer call only. Its own nested call must not
	// ride along on that approval.
	if err := orch.Resume(context.Background(), orch.SessionID(), orchestrator.Decision{
		Approvals: map[string]bool{"call-outer": true},
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	if innerRuns != 0 {
		t.Errorf("inner ran %d times, want 0: it inherited the outer call's approval", innerRuns)
	}
	if !errors.Is(nested.err, tool.ErrApprovalDenied) {
		t.Errorf("nested Execute err = %v, want tool.ErrApprovalDenied from the caller's own func", nested.err)
	}
	foundInner := false
	for _, name := range asked {
		if name == "inner" {
			foundInner = true
		}
	}
	if !foundInner {
		t.Errorf("caller's approval func was asked about %v, want it to include the nested tool %q", asked, "inner")
	}
}

// The nested call consults the caller's approval func on the approved path too:
// a nested call the caller allows still runs, so the fix routes identity rather
// than blocking every nested call.
func TestResume_NestedToolCall_ApprovedByCallerStillRuns(t *testing.T) {
	store := session.NewFileStore(t.TempDir())
	registry := tool.NewRegistry()
	innerRuns := 0
	registry.Register(&gatedTool{name: "inner", run: func(context.Context) (*tool.Result, error) {
		innerRuns++
		return tool.NewResult("inner", true, "inner ran", ""), nil
	}})
	executor := tool.NewExecutor(registry)
	registry.Register(&nestingTool{name: "outer", executor: executor, inner: "inner"})

	// The caller approves the nested call by its own identity while the resumed
	// decision governs only the outer one.
	executor.SetApprovalFunc(func(_ context.Context, tl tool.Tool, _ map[string]any) (bool, error) {
		return tl.Name() == "inner", nil
	})

	client := scriptedLLM([]*llm.Response{
		{Content: []llm.ContentBlock{
			{Type: llm.ContentTypeToolUse, ID: "call-outer", Name: "outer", Input: map[string]any{}},
		}, StopReason: llm.StopReasonToolUse},
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}}, StopReason: llm.StopReasonEndTurn},
	})
	orch := orchestrator.NewWithConfig(client, executor, orchestrator.Config{
		MaxIterations: 5, SessionStore: store, ApprovalMode: orchestrator.ApprovalSuspend,
	})

	var susp *orchestrator.Suspended
	if err := orch.Run(context.Background(), "go"); !errors.As(err, &susp) {
		t.Fatalf("Run err = %v, want *Suspended", err)
	}
	if err := orch.Resume(context.Background(), orch.SessionID(), orchestrator.Decision{
		Approvals: map[string]bool{"call-outer": true},
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	if innerRuns != 1 {
		t.Errorf("inner ran %d times, want 1: the caller approved it under its own identity", innerRuns)
	}
}
