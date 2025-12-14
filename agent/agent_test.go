// ABOUTME: Tests for the Agent type including construction, tool filtering,
// ABOUTME: and interaction with the orchestrator for agent-based workflows.
package agent_test

import (
	"context"
	"errors"
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

func (m *mockTool) Name() string                                { return m.name }
func (m *mockTool) Description() string                         { return "mock" }
func (m *mockTool) RequiresApproval(params map[string]any) bool { return false }
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

func TestAgentSpawnChild(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})
	registry.Register(&mockTool{name: "write_file"})
	registry.Register(&mockTool{name: "bash"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:         "parent",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read_file", "write_file", "bash"},
	})

	// Spawn child with restricted tools
	child, err := parent.SpawnChild(agent.Config{
		Name:         "child",
		AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check hierarchical ID
	if child.ID() != "parent.child" {
		t.Errorf("expected ID parent.child, got %s", child.ID())
	}

	// Check parent reference
	if child.Parent() != parent {
		t.Error("expected child.Parent() to return parent")
	}
}

func TestAgentSpawnChildInheritsTools(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})
	registry.Register(&mockTool{name: "bash"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:         "parent",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read_file", "bash"},
	})

	// Child with empty AllowedTools inherits parent's tools
	child, err := parent.SpawnChild(agent.Config{
		Name: "child",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Child should inherit parent's allowed tools
	cfg := child.Config()
	if len(cfg.AllowedTools) != 2 {
		t.Errorf("expected child to inherit 2 tools, got %d", len(cfg.AllowedTools))
	}
}

func TestAgentSpawnChildDeniedAccumulates(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "bash"})
	registry.Register(&mockTool{name: "rm"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:        "parent",
		Registry:    registry,
		LLMClient:   client,
		DeniedTools: []string{"rm"},
	})

	child, err := parent.SpawnChild(agent.Config{
		Name:        "child",
		DeniedTools: []string{"bash"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Child should have both rm and bash denied
	cfg := child.Config()
	if len(cfg.DeniedTools) != 2 {
		t.Errorf("expected 2 denied tools, got %d: %v", len(cfg.DeniedTools), cfg.DeniedTools)
	}
}

func TestAgentSpawnChildInvalidTools(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})
	registry.Register(&mockTool{name: "bash"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:         "parent",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read_file"}, // Only read_file allowed
	})

	// Child requesting tool parent doesn't have should fail
	_, err := parent.SpawnChild(agent.Config{
		Name:         "child",
		AllowedTools: []string{"bash"}, // Parent doesn't have bash
	})

	if !errors.Is(err, agent.ErrInvalidChildTools) {
		t.Errorf("expected ErrInvalidChildTools, got %v", err)
	}
}
