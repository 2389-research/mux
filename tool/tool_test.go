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
