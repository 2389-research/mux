// ABOUTME: Tests for the Agent type including construction, tool filtering,
// ABOUTME: and interaction with the orchestrator for agent-based workflows.
package agent_test

import (
	"context"
	"testing"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

// mockClient implements llm.Client for testing
type mockClient struct {
	response *llm.Response
}

func (m *mockClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return m.response, nil
}

func (m *mockClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

// mockTool implements tool.Tool for testing
type mockTool struct {
	name string
}

func (m *mockTool) Name() string                                                     { return m.name }
func (m *mockTool) Description() string                                              { return "mock" }
func (m *mockTool) RequiresApproval(params map[string]any) bool                      { return false }
func (m *mockTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	return tool.NewResult(m.name, true, "ok", ""), nil
}

func TestNewAgent(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hello"}},
		},
	}

	a := agent.New(agent.Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	if a == nil {
		t.Fatal("expected non-nil agent")
	}
	if a.ID() != "test-agent" {
		t.Errorf("expected ID test-agent, got %s", a.ID())
	}
}

func TestAgentRun(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "response"}},
		},
	}

	a := agent.New(agent.Config{
		Name:      "runner",
		Registry:  registry,
		LLMClient: client,
	})

	events := a.Subscribe()

	err := a.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Drain events
	for range events {
	}
}
