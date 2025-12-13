package tool_test

import (
	"context"
	"fmt"
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

func TestRegistry(t *testing.T) {
	reg := tool.NewRegistry()

	// Test empty registry
	if names := reg.List(); len(names) != 0 {
		t.Errorf("expected empty registry, got %d tools", len(names))
	}

	// Register a tool
	mock := &mockTool{name: "test_tool", description: "Test"}
	reg.Register(mock)

	// Test retrieval
	retrieved, ok := reg.Get("test_tool")
	if !ok {
		t.Fatal("expected to find registered tool")
	}
	if retrieved.Name() != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", retrieved.Name())
	}

	// Test list
	names := reg.List()
	if len(names) != 1 || names[0] != "test_tool" {
		t.Errorf("expected ['test_tool'], got %v", names)
	}

	// Test not found
	_, ok = reg.Get("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent tool")
	}

	// Test unregister
	reg.Unregister("test_tool")
	_, ok = reg.Get("test_tool")
	if ok {
		t.Error("expected tool to be unregistered")
	}
}

func TestRegistryConcurrency(t *testing.T) {
	reg := tool.NewRegistry()
	done := make(chan bool)

	// Concurrent writes
	for i := 0; i < 100; i++ {
		go func(n int) {
			mock := &mockTool{name: fmt.Sprintf("tool_%d", n)}
			reg.Register(mock)
			done <- true
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		go func() {
			reg.List()
			reg.Get("tool_1")
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 200; i++ {
		<-done
	}

	// Verify some tools registered
	if reg.Count() == 0 {
		t.Error("expected tools to be registered")
	}
}

func TestExecutor(t *testing.T) {
	reg := tool.NewRegistry()
	mock := &mockTool{
		name:             "test_tool",
		requiresApproval: false,
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return tool.NewResult("test_tool", true, "executed", ""), nil
		},
	}
	reg.Register(mock)

	exec := tool.NewExecutor(reg)

	// Execute tool that doesn't require approval
	result, err := exec.Execute(context.Background(), "test_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	if result.Output != "executed" {
		t.Errorf("expected output 'executed', got %q", result.Output)
	}

	// Execute nonexistent tool
	_, err = exec.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Error("expected error for nonexistent tool")
	}
}

func TestExecutorApprovalFlow(t *testing.T) {
	reg := tool.NewRegistry()
	mock := &mockTool{
		name:             "dangerous_tool",
		requiresApproval: true,
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return tool.NewResult("dangerous_tool", true, "danger executed", ""), nil
		},
	}
	reg.Register(mock)

	// Test with approval granted
	exec := tool.NewExecutor(reg)
	approvalCalled := false
	exec.SetApprovalFunc(func(ctx context.Context, t tool.Tool, params map[string]any) (bool, error) {
		approvalCalled = true
		return true, nil // approve
	})

	result, err := exec.Execute(context.Background(), "dangerous_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approvalCalled {
		t.Error("expected approval function to be called")
	}
	if !result.Success {
		t.Error("expected success after approval")
	}

	// Test with approval denied
	exec.SetApprovalFunc(func(ctx context.Context, t tool.Tool, params map[string]any) (bool, error) {
		return false, nil // deny
	})

	_, err = exec.Execute(context.Background(), "dangerous_tool", nil)
	if err == nil {
		t.Error("expected error when approval denied")
	}
}

func TestExecutorHooks(t *testing.T) {
	reg := tool.NewRegistry()
	mock := &mockTool{name: "hook_test"}
	reg.Register(mock)

	exec := tool.NewExecutor(reg)

	beforeCalled := false
	afterCalled := false

	exec.AddBeforeHook(func(ctx context.Context, toolName string, params map[string]any) {
		beforeCalled = true
	})
	exec.AddAfterHook(func(ctx context.Context, toolName string, params map[string]any, result *tool.Result, err error) {
		afterCalled = true
	})

	_, _ = exec.Execute(context.Background(), "hook_test", nil)

	if !beforeCalled {
		t.Error("expected before hook to be called")
	}
	if !afterCalled {
		t.Error("expected after hook to be called")
	}
}
