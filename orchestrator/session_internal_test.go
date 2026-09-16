// ABOUTME: White-box unit tests for unexported durable-session helpers.
// ABOUTME: Lives in package orchestrator to reach internal decision logic.
package orchestrator

import (
	"context"
	"testing"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

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

// contains is defined in usage_test.go (same package).

// scriptedResultTool is a tool.Tool whose Execute always returns a fixed Result,
// for tests that need to control exactly what the executor hands back.
type scriptedResultTool struct {
	name   string
	result *tool.Result
}

func (t *scriptedResultTool) Name() string                         { return t.name }
func (t *scriptedResultTool) Description() string                  { return "scripted result for tests" }
func (t *scriptedResultTool) RequiresApproval(map[string]any) bool { return false }
func (t *scriptedResultTool) Execute(context.Context, map[string]any) (*tool.Result, error) {
	return t.result, nil
}

// TestFailedResultReachesHistory guards the fix for mux#5ez6: a failed Result
// with an empty Output (as tool.NewErrorResult produces) must still put its
// Error text into the tool_result block the model sees, and that text must
// survive into the next built request unchanged.
func TestFailedResultReachesHistory(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&scriptedResultTool{
		name:   "fail",
		result: tool.NewErrorResult("fail", "unknown skill: absent"),
	})
	executor := tool.NewExecutor(registry)
	client := &compactMockClient{}
	orch := New(client, executor)

	orch.mu.Lock()
	defer orch.mu.Unlock()

	if err := orch.transition(StateStreaming); err != nil {
		t.Fatalf("transition to StateStreaming: %v", err)
	}
	toolUses := []llm.ContentBlock{
		{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "fail", Input: map[string]any{}},
	}
	if err := orch.executeTools(context.Background(), toolUses, nil); err != nil {
		t.Fatalf("executeTools: %v", err)
	}

	if len(orch.messages) == 0 {
		t.Fatal("expected at least one message appended to history")
	}
	last := orch.messages[len(orch.messages)-1]
	if len(last.Blocks) != 1 {
		t.Fatalf("expected exactly one result block, got %d", len(last.Blocks))
	}
	block := last.Blocks[0]
	if block.Text != "unknown skill: absent" {
		t.Errorf("Text = %q, want %q", block.Text, "unknown skill: absent")
	}
	if block.ToolUseID != "call-1" {
		t.Errorf("ToolUseID = %q, want %q", block.ToolUseID, "call-1")
	}
	if block.Name != "fail" {
		t.Errorf("Name = %q, want %q", block.Name, "fail")
	}
	if !block.IsError {
		t.Error("IsError = false, want true")
	}

	// A follow-on buildRequest must carry the same block through unchanged.
	req := orch.buildRequest()
	reqLast := req.Messages[len(req.Messages)-1]
	if len(reqLast.Blocks) != 1 || reqLast.Blocks[0].Text != "unknown skill: absent" {
		t.Errorf("buildRequest last block = %+v, want Text %q", reqLast.Blocks, "unknown skill: absent")
	}
}

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
		t.Errorf("Restore counters mismatch: got input=%d out=%d thinking=%d cacheRead=%d cacheWrite=%d req=%d, want input=%d out=%d thinking=%d cacheRead=%d cacheWrite=%d req=%d",
			got.InputTokens, got.OutputTokens, got.ThinkingTokens, got.CacheReadTokens, got.CacheWriteTokens, got.RequestCount,
			src.InputTokens, src.OutputTokens, src.ThinkingTokens, src.CacheReadTokens, src.CacheWriteTokens, src.RequestCount,
		)
	}
}
