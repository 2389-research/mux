package tool_test

import (
	"context"
	"testing"

	"github.com/2389-research/mux/tool"
)

// mockTool implements Tool interface for testing
type mockTool struct {
	name             string
	description      string
	requiresApproval bool
	executeFunc      func(ctx context.Context, params map[string]any) (*tool.Result, error)
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string { return m.description }
func (m *mockTool) RequiresApproval(params map[string]any) bool {
	return m.requiresApproval
}
func (m *mockTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	if m.executeFunc != nil {
		return m.executeFunc(ctx, params)
	}
	return &tool.Result{ToolName: m.name, Success: true, Output: "executed"}, nil
}

func TestToolInterface(t *testing.T) {
	mock := &mockTool{
		name:             "test_tool",
		description:      "A test tool",
		requiresApproval: true,
	}

	// Verify interface compliance
	var _ tool.Tool = mock

	if mock.Name() != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", mock.Name())
	}
	if mock.Description() != "A test tool" {
		t.Errorf("expected description 'A test tool', got %q", mock.Description())
	}
	if !mock.RequiresApproval(nil) {
		t.Error("expected RequiresApproval to return true")
	}

	result, err := mock.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success to be true")
	}
}
