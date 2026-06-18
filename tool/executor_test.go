// ABOUTME: Tests for Executor approval helpers: NeedsApproval and ApprovalFunc getter.
// ABOUTME: Uses a configurable approvalProbe test double to verify lookup and delegation.
package tool_test

import (
	"context"
	"testing"

	"github.com/2389-research/mux/tool"
)

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

func (a *approvalProbe) Name() string                         { return a.name }
func (a *approvalProbe) Description() string                  { return "probe" }
func (a *approvalProbe) RequiresApproval(map[string]any) bool { return a.needs }
func (a *approvalProbe) Execute(context.Context, map[string]any) (*tool.Result, error) {
	return tool.NewResult(a.name, true, "ok", ""), nil
}
