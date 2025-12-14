//go:build integration

// ABOUTME: Integration tests verifying the full mux stack works together -
// ABOUTME: tools, orchestrator, MCP, and permissions.
package mux_test

import (
	"context"
	"testing"
	"time"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/permission"
	"github.com/2389-research/mux/tool"
)

type testTool struct {
	name     string
	executed bool
}

func (t *testTool) Name() string                         { return t.name }
func (t *testTool) Description() string                  { return "test" }
func (t *testTool) RequiresApproval(map[string]any) bool { return false }
func (t *testTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	t.executed = true
	return tool.NewResult(t.name, true, "done", ""), nil
}

type mockLLM struct {
	responses []*llm.Response
	idx       int
}

func (m *mockLLM) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
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

	var toolCalled, completed bool
	go func() {
		for event := range events {
			switch event.Type {
			case orchestrator.EventToolCall:
				toolCalled = true
			case orchestrator.EventComplete:
				completed = true
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := orch.Run(ctx, "Calculate")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if !toolCalled {
		t.Error("expected tool call event")
	}
	if !completed {
		t.Error("expected completion")
	}
	if !testT.executed {
		t.Error("expected tool to execute")
	}
}
