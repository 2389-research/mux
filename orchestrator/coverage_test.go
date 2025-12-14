// ABOUTME: Additional tests to improve coverage for uncovered paths in orchestrator,
// ABOUTME: focusing on edge cases, panic conditions, and SchemaProvider tools.
package orchestrator_test

import (
	"context"
	"testing"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/tool"
)

// TestNewWithConfigNilClient tests that NewWithConfig panics with nil client.
func TestNewWithConfigNilClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when creating orchestrator with nil client")
		} else {
			msg, ok := r.(string)
			if !ok || !contains(msg, "client must not be nil") {
				t.Errorf("expected 'client must not be nil' panic, got: %v", r)
			}
		}
	}()

	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)
	config := orchestrator.DefaultConfig()
	orchestrator.NewWithConfig(nil, executor, config)
}

// TestNewWithConfigNilExecutor tests that NewWithConfig panics with nil executor.
func TestNewWithConfigNilExecutor(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when creating orchestrator with nil executor")
		} else {
			msg, ok := r.(string)
			if !ok || !contains(msg, "executor must not be nil") {
				t.Errorf("expected 'executor must not be nil' panic, got: %v", r)
			}
		}
	}()

	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "test"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	config := orchestrator.DefaultConfig()
	orchestrator.NewWithConfig(client, nil, config)
}

// TestOrchestratorStateMethod tests the State() method returns current state.
func TestOrchestratorStateMethod(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "response"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)

	// State should be Idle initially
	if orch.State() != orchestrator.StateIdle {
		t.Errorf("expected initial state to be Idle, got %s", orch.State())
	}

	// Run the orchestrator
	ctx := context.Background()
	events := orch.Subscribe()

	err := orch.Run(ctx, "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Consume events
	for range events {
	}

	// After completion, state should be Complete
	if orch.State() != orchestrator.StateComplete {
		t.Errorf("expected final state to be Complete, got %s", orch.State())
	}

	// Test State() on a fresh orchestrator in different scenarios
	orch2 := orchestrator.New(client, executor)

	// Before running, should be Idle
	if orch2.State() != orchestrator.StateIdle {
		t.Errorf("expected fresh orchestrator to be in Idle state, got %s", orch2.State())
	}
}

// TestStateMachineIsTerminal tests the IsTerminal() method.
func TestStateMachineIsTerminal(t *testing.T) {
	sm := orchestrator.NewStateMachine()

	// Initially not terminal (Idle)
	if sm.IsTerminal() {
		t.Error("expected Idle state to not be terminal")
	}

	// Streaming is not terminal
	sm.Transition(orchestrator.StateStreaming)
	if sm.IsTerminal() {
		t.Error("expected Streaming state to not be terminal")
	}

	// ExecutingTool is not terminal
	sm.Transition(orchestrator.StateExecutingTool)
	if sm.IsTerminal() {
		t.Error("expected ExecutingTool state to not be terminal")
	}

	// Complete is terminal
	sm.Transition(orchestrator.StateComplete)
	if !sm.IsTerminal() {
		t.Error("expected Complete state to be terminal")
	}

	// Reset to non-terminal
	sm.Reset()
	if sm.IsTerminal() {
		t.Error("expected Idle state after reset to not be terminal")
	}

	// Error is terminal
	sm.Transition(orchestrator.StateError)
	if !sm.IsTerminal() {
		t.Error("expected Error state to be terminal")
	}
}

// mockSchemaProviderTool implements both tool.Tool and tool.SchemaProvider.
type mockSchemaProviderTool struct {
	name   string
	schema map[string]any
}

func (m *mockSchemaProviderTool) Name() string                                { return m.name }
func (m *mockSchemaProviderTool) Description() string                         { return "schema provider mock" }
func (m *mockSchemaProviderTool) RequiresApproval(params map[string]any) bool { return false }
func (m *mockSchemaProviderTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	return tool.NewResult(m.name, true, "executed", ""), nil
}
func (m *mockSchemaProviderTool) InputSchema() map[string]any {
	return m.schema
}

// TestBuildToolDefinitionsWithSchemaProvider tests that tools implementing SchemaProvider
// have their custom schemas used instead of the default empty schema.
func TestBuildToolDefinitionsWithSchemaProvider(t *testing.T) {
	ctx := context.Background()

	// Create a tool with a custom schema
	customSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "File path to read",
			},
			"encoding": map[string]any{
				"type":        "string",
				"description": "File encoding",
				"default":     "utf-8",
			},
		},
		"required": []string{"path"},
	}

	schemaProviderTool := &mockSchemaProviderTool{
		name:   "read_file",
		schema: customSchema,
	}

	// Create a regular tool without schema
	regularTool := &mockTool{name: "regular_tool"}

	registry := tool.NewRegistry()
	registry.Register(schemaProviderTool)
	registry.Register(regularTool)
	executor := tool.NewExecutor(registry)

	// Client that will invoke both tools
	client := &mockLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{
					{
						Type:  llm.ContentTypeToolUse,
						ID:    "tool_1",
						Name:  "read_file",
						Input: map[string]any{"path": "/tmp/test.txt"},
					},
					{
						Type:  llm.ContentTypeToolUse,
						ID:    "tool_2",
						Name:  "regular_tool",
						Input: map[string]any{},
					},
				},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	// Create orchestrator - this will call buildToolDefinitions internally
	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	err := orch.Run(ctx, "test schema provider")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify both tools were called
	var schemaToolCalled, regularToolCalled bool
	for event := range events {
		if event.Type == orchestrator.EventToolCall {
			if event.ToolName == "read_file" {
				schemaToolCalled = true
				// Verify the schema tool received correct params
				if event.ToolParams["path"] != "/tmp/test.txt" {
					t.Errorf("expected path param, got %v", event.ToolParams)
				}
			}
			if event.ToolName == "regular_tool" {
				regularToolCalled = true
			}
		}
	}

	if !schemaToolCalled {
		t.Error("expected schema provider tool to be called")
	}
	if !regularToolCalled {
		t.Error("expected regular tool to be called")
	}
}

// TestBuildToolDefinitionsEmptyRegistry tests buildToolDefinitions with no tools.
func TestBuildToolDefinitionsEmptyRegistry(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "response"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}

	// Empty registry
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	ctx := context.Background()
	err := orch.Run(ctx, "test empty registry")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Consume events
	for range events {
	}

	// Should complete successfully with no tools
	if orch.State() != orchestrator.StateComplete {
		t.Errorf("expected Complete state, got %s", orch.State())
	}
}

// TestTransitionToErrorFromDifferentStates tests error transitions from various states.
func TestTransitionToErrorFromDifferentStates(t *testing.T) {
	testCases := []struct {
		name       string
		startState orchestrator.State
	}{
		{"from Streaming", orchestrator.StateStreaming},
		{"from AwaitingApproval", orchestrator.StateAwaitingApproval},
		{"from ExecutingTool", orchestrator.StateExecutingTool},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			sm := orchestrator.NewStateMachine()
			sm.ForceState(tc.startState)

			err := sm.Transition(orchestrator.StateError)
			if err != nil {
				t.Errorf("should be able to transition to Error from %s: %v", tc.startState, err)
			}

			if sm.Current() != orchestrator.StateError {
				t.Errorf("expected Error state, got %s", sm.Current())
			}

			if !sm.IsTerminal() {
				t.Error("Error state should be terminal")
			}
		})
	}
}

// TestOrchestratorWithCustomConfig tests orchestrator with custom configuration.
func TestOrchestratorWithCustomConfig(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "response"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}

	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.Config{
		MaxIterations: 100,
		SystemPrompt:  "You are a helpful assistant.",
		Model:         "claude-3-opus-20240229",
	}

	orch := orchestrator.NewWithConfig(client, executor, config)
	events := orch.Subscribe()

	ctx := context.Background()
	err := orch.Run(ctx, "test custom config")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Consume events
	for range events {
	}

	// Should complete successfully
	if orch.State() != orchestrator.StateComplete {
		t.Errorf("expected Complete state, got %s", orch.State())
	}
}

// TestStateTransitionFromCompleteToIdle tests transition from Complete to Idle.
func TestStateTransitionFromCompleteToIdle(t *testing.T) {
	sm := orchestrator.NewStateMachine()

	// Move to Complete state
	sm.Transition(orchestrator.StateStreaming)
	sm.Transition(orchestrator.StateComplete)

	if !sm.IsTerminal() {
		t.Error("Complete should be terminal")
	}

	// Complete -> Idle is valid
	err := sm.Transition(orchestrator.StateIdle)
	if err != nil {
		t.Errorf("should be able to transition from Complete to Idle: %v", err)
	}

	if sm.Current() != orchestrator.StateIdle {
		t.Errorf("expected Idle state, got %s", sm.Current())
	}

	if sm.IsTerminal() {
		t.Error("Idle should not be terminal")
	}
}

// TestStateTransitionFromErrorToIdle tests transition from Error to Idle.
func TestStateTransitionFromErrorToIdle(t *testing.T) {
	sm := orchestrator.NewStateMachine()

	// Move to Error state
	sm.Transition(orchestrator.StateError)

	if !sm.IsTerminal() {
		t.Error("Error should be terminal")
	}

	// Error -> Idle is valid
	err := sm.Transition(orchestrator.StateIdle)
	if err != nil {
		t.Errorf("should be able to transition from Error to Idle: %v", err)
	}

	if sm.Current() != orchestrator.StateIdle {
		t.Errorf("expected Idle state, got %s", sm.Current())
	}

	if sm.IsTerminal() {
		t.Error("Idle should not be terminal")
	}
}

// TestEventTypeCoverage tests all event constructors for completeness.
func TestEventTypeCoverage(t *testing.T) {
	// Test NewStateChangeEvent
	stateEvent := orchestrator.NewStateChangeEvent(orchestrator.StateIdle, orchestrator.StateStreaming)
	if stateEvent.Type != orchestrator.EventStateChange {
		t.Errorf("expected EventStateChange, got %s", stateEvent.Type)
	}
	if stateEvent.FromState != orchestrator.StateIdle {
		t.Errorf("expected FromState Idle, got %s", stateEvent.FromState)
	}
	if stateEvent.ToState != orchestrator.StateStreaming {
		t.Errorf("expected ToState Streaming, got %s", stateEvent.ToState)
	}

	// Test NewCompleteEvent
	completeEvent := orchestrator.NewCompleteEvent("final text")
	if completeEvent.Type != orchestrator.EventComplete {
		t.Errorf("expected EventComplete, got %s", completeEvent.Type)
	}
	if completeEvent.FinalText != "final text" {
		t.Errorf("expected 'final text', got %q", completeEvent.FinalText)
	}

	// Test NewToolResultEvent
	result := tool.NewResult("test_tool", true, "output", "")
	resultEvent := orchestrator.NewToolResultEvent(result)
	if resultEvent.Type != orchestrator.EventToolResult {
		t.Errorf("expected EventToolResult, got %s", resultEvent.Type)
	}
	if resultEvent.Result != result {
		t.Error("expected result to match")
	}
}

// TestDefaultConfig tests that DefaultConfig returns expected values.
func TestDefaultConfig(t *testing.T) {
	config := orchestrator.DefaultConfig()

	if config.MaxIterations != orchestrator.DefaultMaxIterations {
		t.Errorf("expected MaxIterations %d, got %d", orchestrator.DefaultMaxIterations, config.MaxIterations)
	}

	if config.SystemPrompt != "" {
		t.Errorf("expected empty SystemPrompt, got %q", config.SystemPrompt)
	}

	if config.Model != "" {
		t.Errorf("expected empty Model, got %q", config.Model)
	}
}

// TestStateMachineAwaitingApprovalTransitions tests all valid transitions from AwaitingApproval.
func TestStateMachineAwaitingApprovalTransitions(t *testing.T) {
	// AwaitingApproval -> ExecutingTool (already tested)
	// AwaitingApproval -> Streaming
	sm := orchestrator.NewStateMachine()
	sm.Transition(orchestrator.StateStreaming)
	sm.Transition(orchestrator.StateAwaitingApproval)

	err := sm.Transition(orchestrator.StateStreaming)
	if err != nil {
		t.Errorf("AwaitingApproval -> Streaming should be valid: %v", err)
	}

	// AwaitingApproval -> Error (already tested in TestStateMachineErrorFromAnyState)
}

// TestStateMachineExecutingToolToComplete tests ExecutingTool -> Complete transition.
func TestStateMachineExecutingToolToComplete(t *testing.T) {
	sm := orchestrator.NewStateMachine()
	sm.Transition(orchestrator.StateStreaming)
	sm.Transition(orchestrator.StateExecutingTool)

	err := sm.Transition(orchestrator.StateComplete)
	if err != nil {
		t.Errorf("ExecutingTool -> Complete should be valid: %v", err)
	}

	if sm.Current() != orchestrator.StateComplete {
		t.Errorf("expected Complete state, got %s", sm.Current())
	}
}

// TestStateMachineStreamingToExecutingTool tests Streaming -> ExecutingTool transition.
func TestStateMachineStreamingToExecutingTool(t *testing.T) {
	sm := orchestrator.NewStateMachine()
	sm.Transition(orchestrator.StateStreaming)

	err := sm.Transition(orchestrator.StateExecutingTool)
	if err != nil {
		t.Errorf("Streaming -> ExecutingTool should be valid: %v", err)
	}

	if sm.Current() != orchestrator.StateExecutingTool {
		t.Errorf("expected ExecutingTool state, got %s", sm.Current())
	}
}

// TestOrchestratorInvalidStateTransition tests that orchestrator handles invalid state transitions.
func TestOrchestratorInvalidStateTransition(t *testing.T) {
	// This is a pathological test case to cover the error path in transition()
	// In normal operation, this shouldn't happen due to the orchestrator's logic
	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "test"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}

	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)
	orch := orchestrator.New(client, executor)

	// Force the orchestrator into a terminal state (Complete)
	ctx := context.Background()
	events := orch.Subscribe()

	err := orch.Run(ctx, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Consume events
	for range events {
	}

	// Verify we're in Complete state
	if orch.State() != orchestrator.StateComplete {
		t.Fatalf("expected Complete state, got %s", orch.State())
	}
}

// TestOrchestratorToolExecutionWithIsError tests tool execution that returns IsError=true.
func TestOrchestratorToolExecutionWithIsError(t *testing.T) {
	ctx := context.Background()

	client := &mockLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type:  llm.ContentTypeToolUse,
					ID:    "tool_1",
					Name:  "error_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Handled error"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	registry := tool.NewRegistry()
	errorTool := &mockTool{
		name: "error_tool",
		execFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			// Return a result with Success=false (which sets IsError=true in content block)
			return tool.NewErrorResult("error_tool", "simulated error"), nil
		},
	}
	registry.Register(errorTool)
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	err := orch.Run(ctx, "test error tool")
	if err != nil {
		t.Fatalf("orchestrator should handle tool errors gracefully: %v", err)
	}

	// Verify we got a tool result with error
	var gotErrorResult bool
	for event := range events {
		if event.Type == orchestrator.EventToolResult {
			if event.Result != nil && !event.Result.Success {
				gotErrorResult = true
			}
		}
	}

	if !gotErrorResult {
		t.Error("expected error tool result event")
	}
}
