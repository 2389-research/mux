//go:build integration

// ABOUTME: Integration tests verifying the full mux stack works together -
// ABOUTME: tools, orchestrator, MCP, and permissions.
package mux_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/coordinator"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/mcp"
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

func (m *mockLLM) Capabilities() llm.Capabilities { return llm.FullCapabilities() }

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

// mcpScriptedLLM is a scripted llm.Client for the assembled MCP test. Its first
// call asks for the echo tool; every later call validates that the request
// carries the exact tool_result correlated to that call before ending the turn.
//
// It deliberately reports a mismatch by making the next provider call fail,
// rather than only recording it: if the orchestrator mis-correlated the result,
// the conversation must not continue as if it had succeeded.
type mcpScriptedLLM struct {
	nonce  string
	toolID string

	mu         sync.Mutex
	calls      int
	resultSeen *llm.ContentBlock
}

func (m *mcpScriptedLLM) CreateMessage(_ context.Context, req *llm.Request) (*llm.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls++
	if m.calls == 1 {
		return &llm.Response{
			Content: []llm.ContentBlock{{
				Type:  llm.ContentTypeToolUse,
				ID:    m.toolID,
				Name:  "echo",
				Input: map[string]any{"message": m.nonce},
			}},
			StopReason: llm.StopReasonToolUse,
		}, nil
	}

	block := lastToolResult(req.Messages)
	if block == nil {
		return nil, fmt.Errorf("provider call %d saw no tool_result for %s", m.calls, m.toolID)
	}
	m.resultSeen = block
	if block.ToolUseID != m.toolID {
		return nil, fmt.Errorf("tool_result is correlated to %q, want %q", block.ToolUseID, m.toolID)
	}
	if block.Text != m.nonce {
		return nil, fmt.Errorf("tool_result carries %q, want echoed nonce %q", block.Text, m.nonce)
	}
	if block.IsError {
		return nil, fmt.Errorf("tool_result is marked an error: %q", block.Text)
	}

	return &llm.Response{
		Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "echoed " + block.Text}},
		StopReason: llm.StopReasonEndTurn,
	}, nil
}

func (m *mcpScriptedLLM) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	resp, err := m.CreateMessage(ctx, req)
	if err != nil {
		return nil, err
	}
	ch := make(chan llm.StreamEvent, 1)
	ch <- llm.StreamEvent{Type: llm.EventMessageStop, Response: resp}
	close(ch)
	return ch, nil
}

func (m *mcpScriptedLLM) Capabilities() llm.Capabilities { return llm.FullCapabilities() }

// resultBlock returns the tool_result the second provider call observed, if any.
func (m *mcpScriptedLLM) resultBlock() *llm.ContentBlock {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resultSeen
}

// lastToolResult finds the trailing tool_result block across history, scanning
// from the newest message so it reports exactly what the provider would read.
func lastToolResult(messages []llm.Message) *llm.ContentBlock {
	for i := len(messages) - 1; i >= 0; i-- {
		for j := len(messages[i].Blocks) - 1; j >= 0; j-- {
			if messages[i].Blocks[j].Type == llm.ContentTypeToolResult {
				return &messages[i].Blocks[j]
			}
		}
	}
	return nil
}

// TestMCPAgentIntegration drives the assembled agent -> orchestrator -> real MCP
// server path. It launches the echo server as an actual child process over
// stdio, discovers its tool, registers it with an agent, and runs one agent turn
// that calls it. The assertions cover the parts a mock cannot: that a separate
// process answered the JSON-RPC call, that the echoed nonce arrived exactly
// correlated to the request, and that shutdown closes the transport.
//
// This proves the local protocol path, not live provider compatibility; that is
// what TestLiveProviderEcho (build tag `live`) covers.
func TestMCPAgentIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	serverPath := buildEchoServer(ctx, t)
	echoLog := filepath.Join(t.TempDir(), "echo.log")

	client, err := mcp.NewClient(mcp.ServerConfig{
		Name:      "echo",
		Transport: "stdio",
		Command:   serverPath,
		Env:       map[string]string{"MUX_ECHO_SERVER_LOG": echoLog},
	})
	if err != nil {
		t.Fatalf("create MCP client: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("start MCP client: %v", err)
	}

	infos, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list MCP tools: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "echo" {
		t.Fatalf("expected exactly the echo tool, got %+v", infos)
	}

	// The MCP adapter requires approval by default; ModeAuto is the explicit
	// decision to let this agent's tool run.
	registry := tool.NewRegistry()
	registry.Register(mcp.NewToolAdapter(infos[0], client))
	checker := permission.NewChecker(permission.ModeAuto)

	nonce := randomNonce(t)
	toolID := "call_" + nonce
	llmClient := &mcpScriptedLLM{nonce: nonce, toolID: toolID}

	mcpAgent := agent.New(agent.Config{
		Name:         "mcp-agent",
		Registry:     registry,
		LLMClient:    llmClient,
		AllowedTools: []string{"echo"},
		ApprovalFunc: func(ctx context.Context, tl tool.Tool, params map[string]any) (bool, error) {
			return checker.Check(ctx, tl.Name(), params)
		},
	})
	events := mcpAgent.Subscribe()

	if err := mcpAgent.Run(ctx, "Echo the nonce "+nonce); err != nil {
		t.Fatalf("agent run failed: %v", err)
	}

	if seen := llmClient.resultBlock(); seen == nil {
		t.Fatal("the second provider call never received a tool_result")
	} else if seen.Text != nonce {
		t.Fatalf("tool_result text = %q, want echoed nonce %q", seen.Text, nonce)
	}

	// The child process itself recorded the nonce: this is what distinguishes a
	// real process call from in-process state that only looks like one.
	logged, err := os.ReadFile(echoLog)
	if err != nil {
		t.Fatalf("read echo server log: %v", err)
	}
	if got := strings.TrimSpace(string(logged)); got != nonce {
		t.Fatalf("echo server recorded %q, want exactly one nonce %q", got, nonce)
	}

	assertMCPHistory(t, mcpAgent.Messages(), toolID, nonce)
	assertMCPEvents(t, collectEvents(events), toolID)

	// Protocol-level negative cases: the server rejects a bad call rather than
	// answering it, so a future fixture that quietly returns values for
	// anything cannot make the assembled path look healthy.
	if _, err := client.CallTool(ctx, "does_not_exist", map[string]any{}); err == nil {
		t.Error("calling an unknown MCP tool returned no error")
	}
	if _, err := client.CallTool(ctx, "echo", map[string]any{"message": ""}); err == nil {
		t.Error("echo with an empty message returned no error")
	}

	// Close is terminal: a closed client refuses further calls rather than
	// silently reusing or leaking the child process.
	if err := client.Close(); err != nil {
		t.Fatalf("close MCP client: %v", err)
	}
	if _, err := client.ListTools(ctx); !errors.Is(err, mcp.ErrTransportClosed) {
		t.Fatalf("ListTools after Close = %v, want %v", err, mcp.ErrTransportClosed)
	}
}

// collectEvents drains the events already published to the subscription. The
// orchestrator publishes synchronously during Run, and the bus buffers events,
// so after Run returns everything is available without racing.
func collectEvents(events <-chan orchestrator.Event) []orchestrator.Event {
	var collected []orchestrator.Event
	for {
		select {
		case e, ok := <-events:
			if !ok {
				return collected
			}
			collected = append(collected, e)
		default:
			return collected
		}
	}
}

// assertMCPHistory checks the provider-visible history pairs the assistant
// tool_use with exactly one correlated tool_result carrying the nonce.
func assertMCPHistory(t *testing.T, messages []llm.Message, toolID, nonce string) {
	t.Helper()

	var toolUses, toolResults int
	for _, msg := range messages {
		for _, block := range msg.Blocks {
			switch block.Type {
			case llm.ContentTypeToolUse:
				if block.ID == toolID {
					toolUses++
				}
			case llm.ContentTypeToolResult:
				toolResults++
				if block.ToolUseID != toolID {
					t.Errorf("tool_result correlated to %q, want %q", block.ToolUseID, toolID)
				}
				if block.Text != nonce {
					t.Errorf("tool_result text = %q, want %q", block.Text, nonce)
				}
			}
		}
	}
	if toolUses != 1 {
		t.Errorf("history holds %d tool_use blocks for %s, want 1", toolUses, toolID)
	}
	if toolResults != 1 {
		t.Errorf("history holds %d tool_result blocks, want 1", toolResults)
	}
}

// assertMCPEvents checks the run published the tool call and a successful result
// carrying the nonce.
func assertMCPEvents(t *testing.T, events []orchestrator.Event, toolID string) {
	t.Helper()

	var (
		gotCall, gotResult bool
	)
	for _, e := range events {
		switch e.Type {
		case orchestrator.EventToolCall:
			if e.ToolID == toolID && e.ToolName == "echo" {
				gotCall = true
			}
		case orchestrator.EventToolResult:
			if e.Result != nil && e.Result.ToolName == "echo" && e.Result.Success {
				gotResult = true
			}
		}
	}
	if !gotCall {
		t.Errorf("no tool call event for %s", toolID)
	}
	if !gotResult {
		t.Error("no successful tool result event for echo")
	}
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
