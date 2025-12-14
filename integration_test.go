//go:build integration

// ABOUTME: Integration tests verifying the full mux stack works together -
// ABOUTME: tools, orchestrator, MCP, and permissions.
package mux_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/coordinator"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/permission"
	"github.com/2389-research/mux/tool"
)

type testTool struct {
	mu       sync.Mutex
	name     string
	executed bool
}

func (t *testTool) Name() string                         { return t.name }
func (t *testTool) Description() string                  { return "test" }
func (t *testTool) RequiresApproval(map[string]any) bool { return false }
func (t *testTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	t.mu.Lock()
	t.executed = true
	t.mu.Unlock()
	return tool.NewResult(t.name, true, "done", ""), nil
}
func (t *testTool) WasExecuted() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.executed
}

type mockLLM struct {
	mu        sync.Mutex
	responses []*llm.Response
	idx       int
}

func (m *mockLLM) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.idx >= len(m.responses) {
		return &llm.Response{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		}, nil
	}
	resp := m.responses[m.idx]
	m.idx++
	return resp, nil
}

func (m *mockLLM) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		resp, _ := m.CreateMessage(ctx, req)
		ch <- llm.StreamEvent{Type: llm.EventMessageStop, Response: resp}
		close(ch)
	}()
	return ch, nil
}

func TestFullStackIntegration(t *testing.T) {
	registry := tool.NewRegistry()
	testT := &testTool{name: "calculator"}
	registry.Register(testT)

	executor := tool.NewExecutor(registry)
	checker := permission.NewChecker(permission.ModeAuto)
	executor.SetApprovalFunc(func(ctx context.Context, tl tool.Tool, params map[string]any) (bool, error) {
		return checker.Check(ctx, tl.Name(), params)
	})

	mockClient := &mockLLM{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type:  llm.ContentTypeToolUse,
					ID:    "tool_1",
					Name:  "calculator",
					Input: map[string]any{"a": 1},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done!"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	orch := orchestrator.New(mockClient, executor)
	events := orch.Subscribe()

	var mu sync.Mutex
	var toolCalled, completed bool
	go func() {
		for event := range events {
			mu.Lock()
			switch event.Type {
			case orchestrator.EventToolCall:
				toolCalled = true
			case orchestrator.EventComplete:
				completed = true
			}
			mu.Unlock()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := orch.Run(ctx, "Calculate")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	tc := toolCalled
	comp := completed
	mu.Unlock()

	if !tc {
		t.Error("expected tool call event")
	}
	if !comp {
		t.Error("expected completion")
	}
	if !testT.WasExecuted() {
		t.Error("expected tool to execute")
	}
}

// TestMultiAgentCoordination tests multiple agents working together with resource coordination.
func TestMultiAgentCoordination(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&testTool{name: "read_file"})
	registry.Register(&testTool{name: "write_file"})
	registry.Register(&testTool{name: "compute"})

	mockClient := &mockLLM{
		responses: []*llm.Response{
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	// Create coordinator for resource management
	coord := coordinator.New()

	// Create root agent with all tools
	rootAgent := agent.New(agent.Config{
		Name:         "root",
		Registry:     registry,
		LLMClient:    mockClient,
		AllowedTools: []string{"read_file", "write_file", "compute"},
	})

	// Create child agents with restricted capabilities
	readerAgent, err := rootAgent.SpawnChild(agent.Config{
		Name:         "reader",
		AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatalf("failed to spawn reader agent: %v", err)
	}

	writerAgent, err := rootAgent.SpawnChild(agent.Config{
		Name:         "writer",
		AllowedTools: []string{"write_file"},
	})
	if err != nil {
		t.Fatalf("failed to spawn writer agent: %v", err)
	}

	// Test resource coordination
	ctx := context.Background()
	resource := "test_file.txt"

	// Reader acquires lock
	if err := coord.Acquire(ctx, readerAgent.ID(), resource); err != nil {
		t.Fatalf("reader failed to acquire lock: %v", err)
	}

	// Writer should fail to acquire same resource
	if err := coord.Acquire(ctx, writerAgent.ID(), resource); err == nil {
		t.Error("writer should not be able to acquire locked resource")
	}

	// Release and verify writer can now acquire
	if err := coord.Release(readerAgent.ID(), resource); err != nil {
		t.Fatalf("reader failed to release lock: %v", err)
	}

	if err := coord.Acquire(ctx, writerAgent.ID(), resource); err != nil {
		t.Fatalf("writer failed to acquire lock after release: %v", err)
	}

	// Cleanup
	coord.ReleaseAll(writerAgent.ID())

	// Verify agent hierarchy
	if readerAgent.Parent() != rootAgent {
		t.Error("reader agent parent should be root")
	}
	if writerAgent.Parent() != rootAgent {
		t.Error("writer agent parent should be root")
	}

	children := rootAgent.Children()
	if len(children) != 2 {
		t.Errorf("expected 2 children, got %d", len(children))
	}
}

// TestMCPAgentIntegration tests integration between MCP tools and agent execution.
func TestMCPAgentIntegration(t *testing.T) {
	t.Skip("Skipping MCP integration test - requires external MCP server")

	// This test demonstrates how MCP and Agent would integrate
	// In practice, this requires a real MCP server process

	// registry := tool.NewRegistry()
	//
	// // Create MCP client (would need real server)
	// mcpConfig := mcp.ServerConfig{
	// 	Name:      "test-server",
	// 	Transport: "stdio",
	// 	Command:   "node",
	// 	Args:      []string{"test-mcp-server.js"},
	// }
	//
	// mcpClient := mcp.NewClient(mcpConfig)
	// ctx := context.Background()
	//
	// if err := mcpClient.Start(ctx); err != nil {
	// 	t.Fatalf("failed to start MCP client: %v", err)
	// }
	// defer mcpClient.Close()
	//
	// // List tools from MCP server
	// mcpTools, err := mcpClient.ListTools(ctx)
	// if err != nil {
	// 	t.Fatalf("failed to list MCP tools: %v", err)
	// }
	//
	// // Wrap MCP tools and register them
	// for _, mcpTool := range mcpTools {
	// 	wrapped := &mcpToolWrapper{
	// 		client: mcpClient,
	// 		info:   mcpTool,
	// 	}
	// 	registry.Register(wrapped)
	// }
	//
	// // Create agent with MCP tools
	// mockLLM := &mockLLM{...}
	// agent := agent.New(agent.Config{
	// 	Name:      "mcp-agent",
	// 	Registry:  registry,
	// 	LLMClient: mockLLM,
	// })
	//
	// // Run agent task using MCP tools
	// err = agent.Run(ctx, "Use MCP tools to complete task")
	// if err != nil {
	// 	t.Fatalf("agent execution failed: %v", err)
	// }
}

// TestPermissionApprovalFlow tests the full permission system with approval flows.
func TestPermissionApprovalFlow(t *testing.T) {
	registry := tool.NewRegistry()

	// Create tool that requires approval
	dangerousTool := &approvalTool{name: "dangerous_operation", requiresApproval: true}
	safeTool := &approvalTool{name: "safe_operation", requiresApproval: false}

	registry.Register(dangerousTool)
	registry.Register(safeTool)

	executor := tool.NewExecutor(registry)

	// Test 1: Auto mode - all approved
	checker := permission.NewChecker(permission.ModeAuto)
	executor.SetApprovalFunc(func(ctx context.Context, tl tool.Tool, params map[string]any) (bool, error) {
		return checker.Check(ctx, tl.Name(), params)
	})

	mockClient := &mockLLM{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{
					{
						Type:  llm.ContentTypeToolUse,
						ID:    "tool_1",
						Name:  "dangerous_operation",
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

	orch := orchestrator.New(mockClient, executor)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := orch.Run(ctx, "test")
	if err != nil {
		t.Fatalf("auto mode should approve all: %v", err)
	}

	if !dangerousTool.WasExecuted() {
		t.Error("dangerous tool should have executed in auto mode")
	}

	// Test 2: Deny mode - tool won't execute but orchestrator continues
	dangerousTool.Reset()
	checker.SetMode(permission.ModeDeny)

	mockClient2 := &mockLLM{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{
					{
						Type:  llm.ContentTypeToolUse,
						ID:    "tool_2",
						Name:  "dangerous_operation",
						Input: map[string]any{},
					},
				},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Handled denial"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}
	orch2 := orchestrator.New(mockClient2, executor)

	err = orch2.Run(ctx, "test")
	if err != nil {
		t.Fatalf("orchestrator should handle permission denial gracefully: %v", err)
	}

	if dangerousTool.WasExecuted() {
		t.Error("dangerous tool should not have executed in deny mode")
	}

	// Test 3: Ask mode with specific rules
	dangerousTool.Reset()
	checker.SetMode(permission.ModeAsk)
	checker.AddRule(permission.AllowTool("safe_operation"))
	checker.AddRule(permission.DenyTool("dangerous_operation"))

	mockClient3 := &mockLLM{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{
					{
						Type:  llm.ContentTypeToolUse,
						ID:    "tool_3",
						Name:  "dangerous_operation",
						Input: map[string]any{},
					},
				},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Handled denial"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}
	orch3 := orchestrator.New(mockClient3, executor)

	err = orch3.Run(ctx, "test")
	if err != nil {
		t.Fatalf("orchestrator should handle permission denial gracefully: %v", err)
	}

	if dangerousTool.WasExecuted() {
		t.Error("dangerous tool should not have executed with deny rule")
	}

	// Test safe operation is allowed
	mockClient4 := &mockLLM{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{
					{
						Type:  llm.ContentTypeToolUse,
						ID:    "tool_4",
						Name:  "safe_operation",
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

	executor2 := tool.NewExecutor(registry)
	executor2.SetApprovalFunc(func(ctx context.Context, tl tool.Tool, params map[string]any) (bool, error) {
		return checker.Check(ctx, tl.Name(), params)
	})

	orch4 := orchestrator.New(mockClient4, executor2)
	err = orch4.Run(ctx, "test")
	if err != nil {
		t.Fatalf("safe operation should be allowed: %v", err)
	}

	if !safeTool.WasExecuted() {
		t.Error("safe tool should have executed")
	}
}

// TestEndToEndErrorPropagation tests error handling through the full stack.
func TestEndToEndErrorPropagation(t *testing.T) {
	registry := tool.NewRegistry()

	// Create tool that always fails
	failingTool := &errorTool{name: "failing_tool", shouldError: true}
	registry.Register(failingTool)

	executor := tool.NewExecutor(registry)
	checker := permission.NewChecker(permission.ModeAuto)
	executor.SetApprovalFunc(func(ctx context.Context, tl tool.Tool, params map[string]any) (bool, error) {
		return checker.Check(ctx, tl.Name(), params)
	})

	// Test orchestrator error handling - tool errors are sent to LLM, not returned
	mockClient := &mockLLM{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{
					{
						Type:  llm.ContentTypeToolUse,
						ID:    "tool_1",
						Name:  "failing_tool",
						Input: map[string]any{},
					},
				},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Error handled"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	orch := orchestrator.New(mockClient, executor)

	var mu sync.Mutex
	var toolResultReceived bool
	events := orch.Subscribe()
	go func() {
		for event := range events {
			mu.Lock()
			if event.Type == orchestrator.EventToolResult {
				toolResultReceived = true
			}
			mu.Unlock()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The orchestrator should handle tool errors gracefully
	err := orch.Run(ctx, "test")
	if err != nil {
		t.Fatalf("orchestrator should handle tool errors gracefully: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	resultReceived := toolResultReceived
	mu.Unlock()

	if !resultReceived {
		t.Error("expected tool result event to be published")
	}

	// Test context cancellation propagation
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2() // Cancel immediately

	mockClient2 := &mockLLM{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{
					{
						Type:  llm.ContentTypeToolUse,
						ID:    "tool_1",
						Name:  "failing_tool",
						Input: map[string]any{},
					},
				},
				StopReason: llm.StopReasonToolUse,
			},
		},
	}
	orch2 := orchestrator.New(mockClient2, executor)

	err = orch2.Run(ctx2, "test")
	if err == nil {
		t.Error("expected context cancellation error")
	}
}

// TestPerformanceUnderLoad tests system behavior with multiple concurrent agents.
func TestPerformanceUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	registry := tool.NewRegistry()
	fastTool := &testTool{name: "fast_tool"}
	registry.Register(fastTool)

	const numAgents = 10

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run agents concurrently - each with its own client to avoid state conflicts
	var wg sync.WaitGroup
	errChan := make(chan error, numAgents)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentID int) {
			defer wg.Done()

			// Each agent gets its own mock client
			mockClient := &mockLLM{
				responses: []*llm.Response{
					{
						Content: []llm.ContentBlock{
							{
								Type:  llm.ContentTypeToolUse,
								ID:    "tool_1",
								Name:  "fast_tool",
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

			a := agent.New(agent.Config{
				Name:      fmt.Sprintf("agent_%d", agentID),
				Registry:  registry,
				LLMClient: mockClient,
			})

			err := a.Run(ctx, fmt.Sprintf("operation_%d", agentID))
			if err != nil {
				errChan <- err
			}
		}(i)
	}

	// Wait for all operations to complete
	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("agent operation failed: %v", err)
	}
}

// TestResourceCleanup tests cleanup in complex scenarios with nested agents.
func TestResourceCleanup(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&testTool{name: "resource_tool"})

	mockClient := &mockLLM{
		responses: []*llm.Response{
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	coord := coordinator.New()

	// Create nested agent hierarchy
	root := agent.New(agent.Config{
		Name:      "root",
		Registry:  registry,
		LLMClient: mockClient,
	})

	level1, err := root.SpawnChild(agent.Config{Name: "level1"})
	if err != nil {
		t.Fatalf("failed to spawn level1: %v", err)
	}

	level2, err := level1.SpawnChild(agent.Config{Name: "level2"})
	if err != nil {
		t.Fatalf("failed to spawn level2: %v", err)
	}

	level3, err := level2.SpawnChild(agent.Config{Name: "level3"})
	if err != nil {
		t.Fatalf("failed to spawn level3: %v", err)
	}

	ctx := context.Background()

	// Each agent acquires resources
	resources := []string{"res1", "res2", "res3", "res4"}
	if err := coord.Acquire(ctx, root.ID(), resources[0]); err != nil {
		t.Fatalf("root failed to acquire resource: %v", err)
	}
	if err := coord.Acquire(ctx, level1.ID(), resources[1]); err != nil {
		t.Fatalf("level1 failed to acquire resource: %v", err)
	}
	if err := coord.Acquire(ctx, level2.ID(), resources[2]); err != nil {
		t.Fatalf("level2 failed to acquire resource: %v", err)
	}
	if err := coord.Acquire(ctx, level3.ID(), resources[3]); err != nil {
		t.Fatalf("level3 failed to acquire resource: %v", err)
	}

	// Cleanup entire subtree
	coord.ReleaseAll(level1.ID())
	coord.ReleaseAll(level2.ID())
	coord.ReleaseAll(level3.ID())

	// Verify all resources for subtree are released
	// Root's resource should still be locked
	if err := coord.Acquire(ctx, "other", resources[0]); err == nil {
		t.Error("root's resource should still be locked")
	}

	// Other resources should be available
	for i := 1; i < len(resources); i++ {
		if err := coord.Acquire(ctx, "other", resources[i]); err != nil {
			t.Errorf("resource %s should be available after cleanup: %v", resources[i], err)
		}
	}

	// Final cleanup
	coord.ReleaseAll(root.ID())
	coord.ReleaseAll("other")
}

// approvalTool is a test tool that can require approval
type approvalTool struct {
	mu               sync.Mutex
	name             string
	requiresApproval bool
	executed         bool
}

func (t *approvalTool) Name() string        { return t.name }
func (t *approvalTool) Description() string { return "test approval tool" }
func (t *approvalTool) RequiresApproval(map[string]any) bool {
	return t.requiresApproval
}
func (t *approvalTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	t.mu.Lock()
	t.executed = true
	t.mu.Unlock()
	return tool.NewResult(t.name, true, "executed", ""), nil
}
func (t *approvalTool) WasExecuted() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.executed
}
func (t *approvalTool) Reset() {
	t.mu.Lock()
	t.executed = false
	t.mu.Unlock()
}

// errorTool is a test tool that can return errors
type errorTool struct {
	name        string
	shouldError bool
}

func (t *errorTool) Name() string                         { return t.name }
func (t *errorTool) Description() string                  { return "test error tool" }
func (t *errorTool) RequiresApproval(map[string]any) bool { return false }
func (t *errorTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	if t.shouldError {
		return nil, fmt.Errorf("tool execution failed")
	}
	return tool.NewResult(t.name, true, "success", ""), nil
}
