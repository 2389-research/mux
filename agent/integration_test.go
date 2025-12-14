// agent/integration_test.go
package agent_test

import (
	"testing"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

func TestAgentToolFiltering(t *testing.T) {
	// Setup registry with multiple tools
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})
	registry.Register(&mockTool{name: "write_file"})
	registry.Register(&mockTool{name: "bash"})
	registry.Register(&mockTool{name: "dangerous"})

	// Mock client that uses a tool
	toolUsed := ""
	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	// Create agent with restricted tools
	a := agent.New(agent.Config{
		Name:         "restricted",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read_file"},
		DeniedTools:  []string{"dangerous"},
	})

	// Verify agent only sees allowed tools
	// We can check this through the executor's source
	exec := a.Executor()
	source := exec.Source()

	// read_file should be accessible
	if _, ok := source.Get("read_file"); !ok {
		t.Error("expected read_file to be accessible")
	}

	// write_file should NOT be accessible (not in allowed list)
	if _, ok := source.Get("write_file"); ok {
		t.Error("expected write_file to be filtered out")
	}

	// dangerous should NOT be accessible (in denied list)
	if _, ok := source.Get("dangerous"); ok {
		t.Error("expected dangerous to be filtered out")
	}

	// Verify tool count
	if source.Count() != 1 {
		t.Errorf("expected 1 visible tool, got %d", source.Count())
	}

	_ = toolUsed // silence unused warning
}

func TestAgentHierarchyToolRestriction(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read"})
	registry.Register(&mockTool{name: "write"})
	registry.Register(&mockTool{name: "exec"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	// Root agent has all tools
	root := agent.New(agent.Config{
		Name:         "root",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read", "write", "exec"},
	})

	// Level 1: restrict to read + write
	level1, _ := root.SpawnChild(agent.Config{
		Name:         "level1",
		AllowedTools: []string{"read", "write"},
	})

	// Level 2: further restrict to read only
	level2, _ := level1.SpawnChild(agent.Config{
		Name:         "level2",
		AllowedTools: []string{"read"},
	})

	// Verify IDs
	if root.ID() != "root" {
		t.Errorf("expected root ID, got %s", root.ID())
	}
	if level1.ID() != "root.level1" {
		t.Errorf("expected root.level1 ID, got %s", level1.ID())
	}
	if level2.ID() != "root.level1.level2" {
		t.Errorf("expected root.level1.level2 ID, got %s", level2.ID())
	}

	// Verify tool counts at each level
	if root.Executor().Source().Count() != 3 {
		t.Errorf("root should have 3 tools, got %d", root.Executor().Source().Count())
	}
	if level1.Executor().Source().Count() != 2 {
		t.Errorf("level1 should have 2 tools, got %d", level1.Executor().Source().Count())
	}
	if level2.Executor().Source().Count() != 1 {
		t.Errorf("level2 should have 1 tool, got %d", level2.Executor().Source().Count())
	}
}
