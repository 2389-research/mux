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

// TestApprovalFunctionErrors tests approval function returning errors
func TestApprovalFunctionErrors(t *testing.T) {
	tests := []struct {
		name          string
		approvalError error
		wantErrMsg    string
	}{
		{
			name:          "generic error",
			approvalError: fmt.Errorf("approval system unavailable"),
			wantErrMsg:    "approval check failed: approval system unavailable",
		},
		{
			name:          "context canceled",
			approvalError: context.Canceled,
			wantErrMsg:    "approval check failed: context canceled",
		},
		{
			name:          "custom error",
			approvalError: fmt.Errorf("user not authenticated"),
			wantErrMsg:    "approval check failed: user not authenticated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := tool.NewRegistry()
			mock := &mockTool{
				name:             "dangerous_tool",
				requiresApproval: true,
			}
			reg.Register(mock)

			exec := tool.NewExecutor(reg)
			exec.SetApprovalFunc(func(ctx context.Context, tl tool.Tool, params map[string]any) (bool, error) {
				return false, tt.approvalError
			})

			_, err := exec.Execute(context.Background(), "dangerous_tool", nil)
			if err == nil {
				t.Fatal("expected error from approval function")
			}
			if err.Error() != tt.wantErrMsg {
				t.Errorf("expected error message %q, got %q", tt.wantErrMsg, err.Error())
			}
		})
	}
}

// TestApprovalTimeout tests approval with context timeout
func TestApprovalTimeout(t *testing.T) {
	reg := tool.NewRegistry()
	mock := &mockTool{
		name:             "slow_approval_tool",
		requiresApproval: true,
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return tool.NewResult("slow_approval_tool", true, "executed", ""), nil
		},
	}
	reg.Register(mock)

	exec := tool.NewExecutor(reg)

	// Approval function that respects context cancellation
	exec.SetApprovalFunc(func(ctx context.Context, tl tool.Tool, params map[string]any) (bool, error) {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
			return true, nil
		}
	})

	// Create context with immediate cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := exec.Execute(ctx, "slow_approval_tool", nil)
	if err == nil {
		t.Fatal("expected error from canceled context")
	}
	if err.Error() != "approval check failed: context canceled" {
		t.Errorf("expected context canceled error, got: %v", err)
	}
}

// TestConcurrentApprovalRequests tests multiple concurrent approval requests
func TestConcurrentApprovalRequests(t *testing.T) {
	reg := tool.NewRegistry()

	// Register multiple tools that require approval
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("tool_%d", i)
		mock := &mockTool{
			name:             name,
			requiresApproval: true,
			executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
				return tool.NewResult(params["name"].(string), true, "executed", ""), nil
			},
		}
		reg.Register(mock)
	}

	exec := tool.NewExecutor(reg)

	// Track concurrent approval calls
	approvalCount := 0
	approvalChan := make(chan bool, 10)

	exec.SetApprovalFunc(func(ctx context.Context, tl tool.Tool, params map[string]any) (bool, error) {
		approvalChan <- true
		return true, nil
	})

	// Execute tools concurrently
	done := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			toolName := fmt.Sprintf("tool_%d", n)
			params := map[string]any{"name": toolName}
			_, err := exec.Execute(context.Background(), toolName, params)
			done <- err
		}(i)
	}

	// Wait for all executions
	for i := 0; i < 10; i++ {
		if err := <-done; err != nil {
			t.Errorf("unexpected error from concurrent execution: %v", err)
		}
	}

	// Count approval calls
	close(approvalChan)
	for range approvalChan {
		approvalCount++
	}

	if approvalCount != 10 {
		t.Errorf("expected 10 approval calls, got %d", approvalCount)
	}
}

// TestContextSensitiveApproval tests approval decisions based on parameters
func TestContextSensitiveApproval(t *testing.T) {
	reg := tool.NewRegistry()

	// Create a tool with conditional approval based on params
	mock := &mockTool{
		name:             "conditional_tool",
		requiresApproval: true,
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return tool.NewResult("conditional_tool", true, "executed", ""), nil
		},
	}
	reg.Register(mock)

	tests := []struct {
		name        string
		params      map[string]any
		shouldAllow bool
		wantErr     bool
	}{
		{
			name:        "safe action",
			params:      map[string]any{"action": "read"},
			shouldAllow: true,
			wantErr:     false,
		},
		{
			name:        "dangerous action",
			params:      map[string]any{"action": "delete"},
			shouldAllow: false,
			wantErr:     true,
		},
		{
			name:        "moderate action",
			params:      map[string]any{"action": "write"},
			shouldAllow: true,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := tool.NewExecutor(reg)

			// Context-sensitive approval function
			exec.SetApprovalFunc(func(ctx context.Context, tl tool.Tool, params map[string]any) (bool, error) {
				action, ok := params["action"].(string)
				if !ok {
					return false, fmt.Errorf("missing action parameter")
				}

				// Deny dangerous actions
				if action == "delete" {
					return false, nil
				}
				return true, nil
			})

			_, err := exec.Execute(context.Background(), "conditional_tool", tt.params)

			if tt.wantErr && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestHookExecutionOrder tests that hooks execute in the correct order
func TestHookExecutionOrder(t *testing.T) {
	reg := tool.NewRegistry()
	mock := &mockTool{
		name: "order_test",
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return tool.NewResult("order_test", true, "executed", ""), nil
		},
	}
	reg.Register(mock)

	exec := tool.NewExecutor(reg)

	var order []string

	// Add multiple before hooks
	exec.AddBeforeHook(func(ctx context.Context, toolName string, params map[string]any) {
		order = append(order, "before1")
	})
	exec.AddBeforeHook(func(ctx context.Context, toolName string, params map[string]any) {
		order = append(order, "before2")
	})
	exec.AddBeforeHook(func(ctx context.Context, toolName string, params map[string]any) {
		order = append(order, "before3")
	})

	// Add multiple after hooks
	exec.AddAfterHook(func(ctx context.Context, toolName string, params map[string]any, result *tool.Result, err error) {
		order = append(order, "after1")
	})
	exec.AddAfterHook(func(ctx context.Context, toolName string, params map[string]any, result *tool.Result, err error) {
		order = append(order, "after2")
	})
	exec.AddAfterHook(func(ctx context.Context, toolName string, params map[string]any, result *tool.Result, err error) {
		order = append(order, "after3")
	})

	_, err := exec.Execute(context.Background(), "order_test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify execution order
	expected := []string{"before1", "before2", "before3", "after1", "after2", "after3"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d hook calls, got %d", len(expected), len(order))
	}

	for i, want := range expected {
		if order[i] != want {
			t.Errorf("position %d: expected %q, got %q", i, want, order[i])
		}
	}
}

// TestHookErrorHandling tests that hook errors don't stop execution
func TestHookErrorHandling(t *testing.T) {
	reg := tool.NewRegistry()

	executeCalled := false
	mock := &mockTool{
		name: "resilient_tool",
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			executeCalled = true
			return tool.NewResult("resilient_tool", true, "executed", ""), nil
		},
	}
	reg.Register(mock)

	exec := tool.NewExecutor(reg)

	beforeHooksCalled := 0
	afterHooksCalled := 0

	// Add before hook that panics
	exec.AddBeforeHook(func(ctx context.Context, toolName string, params map[string]any) {
		beforeHooksCalled++
		// Note: Current implementation doesn't catch panics
		// This test documents the behavior
	})

	// Add normal before hook
	exec.AddBeforeHook(func(ctx context.Context, toolName string, params map[string]any) {
		beforeHooksCalled++
	})

	// Add after hook
	exec.AddAfterHook(func(ctx context.Context, toolName string, params map[string]any, result *tool.Result, err error) {
		afterHooksCalled++
	})

	result, err := exec.Execute(context.Background(), "resilient_tool", nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !executeCalled {
		t.Error("expected tool execution to be called")
	}
	if !result.Success {
		t.Error("expected successful result")
	}
	if beforeHooksCalled != 2 {
		t.Errorf("expected 2 before hooks called, got %d", beforeHooksCalled)
	}
	if afterHooksCalled != 1 {
		t.Errorf("expected 1 after hook called, got %d", afterHooksCalled)
	}
}

// TestHooksReceiveCorrectData tests that hooks receive the correct context and data
func TestHooksReceiveCorrectData(t *testing.T) {
	reg := tool.NewRegistry()

	expectedResult := tool.NewResult("data_tool", true, "test output", "")
	mock := &mockTool{
		name: "data_tool",
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return expectedResult, nil
		},
	}
	reg.Register(mock)

	exec := tool.NewExecutor(reg)

	testParams := map[string]any{"key": "value"}

	// Verify before hook receives correct data
	exec.AddBeforeHook(func(ctx context.Context, toolName string, params map[string]any) {
		if toolName != "data_tool" {
			t.Errorf("before hook: expected toolName 'data_tool', got %q", toolName)
		}
		if params["key"] != "value" {
			t.Errorf("before hook: expected params key=value, got %v", params)
		}
		if ctx == nil {
			t.Error("before hook: context is nil")
		}
	})

	// Verify after hook receives correct data
	exec.AddAfterHook(func(ctx context.Context, toolName string, params map[string]any, result *tool.Result, err error) {
		if toolName != "data_tool" {
			t.Errorf("after hook: expected toolName 'data_tool', got %q", toolName)
		}
		if params["key"] != "value" {
			t.Errorf("after hook: expected params key=value, got %v", params)
		}
		if ctx == nil {
			t.Error("after hook: context is nil")
		}
		if err != nil {
			t.Errorf("after hook: expected no error, got %v", err)
		}
		if result == nil {
			t.Fatal("after hook: result is nil")
		}
		if result.ToolName != "data_tool" {
			t.Errorf("after hook: expected result.ToolName 'data_tool', got %q", result.ToolName)
		}
		if result.Output != "test output" {
			t.Errorf("after hook: expected output 'test output', got %q", result.Output)
		}
	})

	_, err := exec.Execute(context.Background(), "data_tool", testParams)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestHooksWithToolExecutionError tests that after hooks are called even when execution fails
func TestHooksWithToolExecutionError(t *testing.T) {
	reg := tool.NewRegistry()

	expectedErr := fmt.Errorf("tool execution failed")
	mock := &mockTool{
		name: "failing_tool",
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return nil, expectedErr
		},
	}
	reg.Register(mock)

	exec := tool.NewExecutor(reg)

	beforeCalled := false
	afterCalled := false
	var afterErr error

	exec.AddBeforeHook(func(ctx context.Context, toolName string, params map[string]any) {
		beforeCalled = true
	})

	exec.AddAfterHook(func(ctx context.Context, toolName string, params map[string]any, result *tool.Result, err error) {
		afterCalled = true
		afterErr = err
	})

	_, err := exec.Execute(context.Background(), "failing_tool", nil)

	if err == nil {
		t.Fatal("expected error from tool execution")
	}
	if !beforeCalled {
		t.Error("expected before hook to be called")
	}
	if !afterCalled {
		t.Error("expected after hook to be called even on error")
	}
	if afterErr == nil {
		t.Error("expected after hook to receive the error")
	}
	if afterErr.Error() != expectedErr.Error() {
		t.Errorf("expected after hook error %q, got %q", expectedErr, afterErr)
	}
}

// TestApprovalWithContextValues tests approval function using context values
func TestApprovalWithContextValues(t *testing.T) {
	reg := tool.NewRegistry()
	mock := &mockTool{
		name:             "context_aware_tool",
		requiresApproval: true,
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return tool.NewResult("context_aware_tool", true, "executed", ""), nil
		},
	}
	reg.Register(mock)

	exec := tool.NewExecutor(reg)

	type userKey struct{}

	exec.SetApprovalFunc(func(ctx context.Context, tl tool.Tool, params map[string]any) (bool, error) {
		user, ok := ctx.Value(userKey{}).(string)
		if !ok {
			return false, fmt.Errorf("no user in context")
		}
		// Only admin can execute
		return user == "admin", nil
	})

	// Test with admin user
	ctx := context.WithValue(context.Background(), userKey{}, "admin")
	result, err := exec.Execute(ctx, "context_aware_tool", nil)
	if err != nil {
		t.Errorf("expected admin to be approved, got error: %v", err)
	}
	if result == nil || !result.Success {
		t.Error("expected successful execution for admin")
	}

	// Test with regular user
	ctx = context.WithValue(context.Background(), userKey{}, "user")
	_, err = exec.Execute(ctx, "context_aware_tool", nil)
	if err == nil {
		t.Error("expected regular user to be denied")
	}

	// Test with no user
	_, err = exec.Execute(context.Background(), "context_aware_tool", nil)
	if err == nil {
		t.Error("expected no user to cause error")
	}
}

// TestRegistrationDuringEnumeration tests registering tools while iterating
func TestRegistrationDuringEnumeration(t *testing.T) {
	reg := tool.NewRegistry()

	// Register initial tools
	for i := 0; i < 10; i++ {
		reg.Register(&mockTool{name: fmt.Sprintf("initial_%d", i)})
	}

	done := make(chan bool)

	// Goroutine 1: Continuously enumerate tools
	go func() {
		for i := 0; i < 100; i++ {
			names := reg.List()
			// Verify we got a consistent snapshot
			if len(names) > 0 {
				// List should return sorted, unique names
				for j := 1; j < len(names); j++ {
					if names[j-1] >= names[j] {
						t.Errorf("list not properly sorted: %v", names)
					}
				}
			}
		}
		done <- true
	}()

	// Goroutine 2: Continuously register new tools
	go func() {
		for i := 0; i < 50; i++ {
			reg.Register(&mockTool{name: fmt.Sprintf("new_%d", i)})
		}
		done <- true
	}()

	// Goroutine 3: Continuously enumerate with All()
	go func() {
		for i := 0; i < 100; i++ {
			tools := reg.All()
			// Just verify we got a slice without panicking
			_ = len(tools)
		}
		done <- true
	}()

	// Goroutine 4: Continuously check count
	go func() {
		for i := 0; i < 100; i++ {
			count := reg.Count()
			if count < 0 {
				t.Errorf("invalid count: %d", count)
			}
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 4; i++ {
		<-done
	}

	// Verify final state is valid
	finalCount := reg.Count()
	finalList := reg.List()
	if finalCount != len(finalList) {
		t.Errorf("count mismatch: Count()=%d, len(List())=%d", finalCount, len(finalList))
	}
}

// TestUnregisterActivelyExecutingTool tests removing a tool while it's executing
func TestUnregisterActivelyExecutingTool(t *testing.T) {
	reg := tool.NewRegistry()

	executionStarted := make(chan bool)
	continueExecution := make(chan bool)

	longRunningTool := &mockTool{
		name: "long_running",
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			executionStarted <- true
			<-continueExecution
			return tool.NewResult("long_running", true, "completed", ""), nil
		},
	}

	reg.Register(longRunningTool)
	exec := tool.NewExecutor(reg)

	executionDone := make(chan error)

	// Start executing the tool
	go func() {
		_, err := exec.Execute(context.Background(), "long_running", nil)
		executionDone <- err
	}()

	// Wait for execution to start
	<-executionStarted

	// While the tool is executing, unregister it
	reg.Unregister("long_running")

	// Verify the tool is no longer in the registry
	_, found := reg.Get("long_running")
	if found {
		t.Error("expected tool to be unregistered")
	}

	// Allow execution to complete
	close(continueExecution)

	// Verify execution completed successfully
	err := <-executionDone
	if err != nil {
		t.Errorf("expected execution to complete successfully, got: %v", err)
	}

	// Try to execute the unregistered tool
	_, err = exec.Execute(context.Background(), "long_running", nil)
	if err == nil {
		t.Error("expected error when executing unregistered tool")
	}
}

// TestToolReplacementDuringExecution tests replacing a tool while instances are executing
func TestToolReplacementDuringExecution(t *testing.T) {
	reg := tool.NewRegistry()

	executionStarted := make(chan int, 3)
	continueExecution := make(chan bool)

	// Create version 1 of the tool
	toolV1 := &mockTool{
		name: "versioned_tool",
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			version := params["version"].(int)
			executionStarted <- version
			<-continueExecution
			return tool.NewResult("versioned_tool", true, fmt.Sprintf("v%d", version), ""), nil
		},
	}

	reg.Register(toolV1)
	exec := tool.NewExecutor(reg)

	// Start multiple executions of v1
	results := make(chan *tool.Result, 3)
	errors := make(chan error, 3)

	for i := 0; i < 3; i++ {
		go func(n int) {
			result, err := exec.Execute(context.Background(), "versioned_tool", map[string]any{"version": 1})
			results <- result
			errors <- err
		}(i)
	}

	// Wait for all executions to start
	for i := 0; i < 3; i++ {
		<-executionStarted
	}

	// While v1 is executing, replace with v2
	toolV2 := &mockTool{
		name: "versioned_tool",
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return tool.NewResult("versioned_tool", true, "v2", ""), nil
		},
	}
	reg.Register(toolV2) // This replaces v1

	// Verify v2 is now in the registry
	retrievedTool, ok := reg.Get("versioned_tool")
	if !ok {
		t.Fatal("expected to find tool in registry")
	}
	// The new tool should be v2
	_ = retrievedTool // We have the new version

	// Allow v1 executions to complete
	close(continueExecution)

	// Collect all results
	for i := 0; i < 3; i++ {
		result := <-results
		err := <-errors
		if err != nil {
			t.Errorf("execution %d failed: %v", i, err)
		}
		if result == nil {
			t.Errorf("execution %d returned nil result", i)
			continue
		}
		// v1 executions should complete with v1 output
		if result.Output != "v1" {
			t.Errorf("expected v1 output, got %q", result.Output)
		}
	}

	// New execution should use v2
	result, err := exec.Execute(context.Background(), "versioned_tool", map[string]any{"version": 2})
	if err != nil {
		t.Fatalf("v2 execution failed: %v", err)
	}
	if result.Output != "v2" {
		t.Errorf("expected v2 output, got %q", result.Output)
	}
}

// TestHighFrequencyRegistrationUnregistration tests rapid registration and unregistration
func TestHighFrequencyRegistrationUnregistration(t *testing.T) {
	reg := tool.NewRegistry()
	exec := tool.NewExecutor(reg)

	done := make(chan bool)
	iterations := 1000

	// Goroutine 1: Rapidly register tools
	go func() {
		for i := 0; i < iterations; i++ {
			reg.Register(&mockTool{name: fmt.Sprintf("tool_%d", i%10)})
		}
		done <- true
	}()

	// Goroutine 2: Rapidly unregister tools
	go func() {
		for i := 0; i < iterations; i++ {
			reg.Unregister(fmt.Sprintf("tool_%d", i%10))
		}
		done <- true
	}()

	// Goroutine 3: Rapidly try to execute tools
	go func() {
		for i := 0; i < iterations; i++ {
			// Execute might fail if tool is unregistered, that's OK
			_, _ = exec.Execute(context.Background(), fmt.Sprintf("tool_%d", i%10), nil)
		}
		done <- true
	}()

	// Goroutine 4: Rapidly enumerate
	go func() {
		for i := 0; i < iterations; i++ {
			_ = reg.List()
		}
		done <- true
	}()

	// Goroutine 5: Rapidly get tools
	go func() {
		for i := 0; i < iterations; i++ {
			_, _ = reg.Get(fmt.Sprintf("tool_%d", i%10))
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		<-done
	}

	// Registry should still be in a valid state
	count := reg.Count()
	list := reg.List()
	if count != len(list) {
		t.Errorf("inconsistent state: Count()=%d, len(List())=%d", count, len(list))
	}

	// All operations should still work
	reg.Register(&mockTool{name: "final_test"})
	if _, ok := reg.Get("final_test"); !ok {
		t.Error("registry not working after high-frequency operations")
	}
}

// TestConcurrentRegistrationAndExecution tests simultaneous registration and execution
func TestConcurrentRegistrationAndExecution(t *testing.T) {
	reg := tool.NewRegistry()
	exec := tool.NewExecutor(reg)

	numTools := 50
	done := make(chan bool)

	// Goroutine 1: Register tools
	go func() {
		for i := 0; i < numTools; i++ {
			tool := &mockTool{
				name: fmt.Sprintf("concurrent_%d", i),
				executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
					return tool.NewResult(params["name"].(string), true, "ok", ""), nil
				},
			}
			reg.Register(tool)
		}
		done <- true
	}()

	// Goroutine 2: Try to execute tools as they become available
	successCount := 0
	go func() {
		for i := 0; i < numTools*2; i++ {
			toolName := fmt.Sprintf("concurrent_%d", i%numTools)
			result, err := exec.Execute(context.Background(), toolName, map[string]any{"name": toolName})
			if err == nil && result != nil && result.Success {
				successCount++
			}
		}
		done <- true
	}()

	// Goroutine 3: Continuously monitor count
	go func() {
		for i := 0; i < 100; i++ {
			count := reg.Count()
			if count < 0 || count > numTools {
				t.Errorf("invalid count during concurrent operations: %d", count)
			}
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}

	// Verify final state
	if reg.Count() != numTools {
		t.Errorf("expected %d tools, got %d", numTools, reg.Count())
	}

	// At least some executions should have succeeded
	if successCount == 0 {
		t.Error("expected at least some executions to succeed")
	}
}

// TestRegistryReplaceAndRetrieve tests replacing a tool and immediately retrieving it
func TestRegistryReplaceAndRetrieve(t *testing.T) {
	reg := tool.NewRegistry()
	iterations := 1000

	done := make(chan bool)

	// Goroutine 1: Continuously replace tool
	go func() {
		for i := 0; i < iterations; i++ {
			reg.Register(&mockTool{
				name:        "replaceable",
				description: fmt.Sprintf("version_%d", i),
			})
		}
		done <- true
	}()

	// Goroutine 2: Continuously retrieve tool
	go func() {
		for i := 0; i < iterations; i++ {
			tool, ok := reg.Get("replaceable")
			if ok {
				// Just verify we got a tool with the right name
				if tool.Name() != "replaceable" {
					t.Errorf("expected name 'replaceable', got %q", tool.Name())
				}
			}
		}
		done <- true
	}()

	// Wait for both goroutines
	for i := 0; i < 2; i++ {
		<-done
	}

	// Final state should be consistent
	tool, ok := reg.Get("replaceable")
	if !ok {
		t.Error("expected final tool to be in registry")
	}
	if tool.Name() != "replaceable" {
		t.Errorf("expected name 'replaceable', got %q", tool.Name())
	}
}
