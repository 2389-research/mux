// ABOUTME: Tests for preset agent configurations - application and customization.
// ABOUTME: Validates that presets correctly apply defaults and allow overrides.
package agent

import (
	"context"
	"testing"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

func TestPresetApply(t *testing.T) {
	preset := Preset{
		Name:          "test-preset",
		SystemPrompt:  "test prompt",
		AllowedTools:  []string{"tool1", "tool2"},
		MaxIterations: 25,
	}

	// Empty base config should get all preset values
	cfg := preset.Apply(Config{})

	if cfg.Name != "test-preset" {
		t.Errorf("Name = %q, want %q", cfg.Name, "test-preset")
	}
	if cfg.SystemPrompt != "test prompt" {
		t.Errorf("SystemPrompt = %q, want %q", cfg.SystemPrompt, "test prompt")
	}
	if len(cfg.AllowedTools) != 2 {
		t.Errorf("AllowedTools len = %d, want 2", len(cfg.AllowedTools))
	}
	if cfg.MaxIterations != 25 {
		t.Errorf("MaxIterations = %d, want 25", cfg.MaxIterations)
	}
}

func TestPresetApplyWithOverrides(t *testing.T) {
	preset := Preset{
		Name:          "test-preset",
		SystemPrompt:  "preset prompt",
		AllowedTools:  []string{"tool1"},
		MaxIterations: 25,
	}

	// Base config values should override preset
	cfg := preset.Apply(Config{
		Name:          "custom-name",
		SystemPrompt:  "custom prompt",
		MaxIterations: 100,
	})

	if cfg.Name != "custom-name" {
		t.Errorf("Name = %q, want %q", cfg.Name, "custom-name")
	}
	if cfg.SystemPrompt != "custom prompt" {
		t.Errorf("SystemPrompt = %q, want %q", cfg.SystemPrompt, "custom prompt")
	}
	if cfg.MaxIterations != 100 {
		t.Errorf("MaxIterations = %d, want 100", cfg.MaxIterations)
	}
	// AllowedTools should still come from preset since base didn't specify
	if len(cfg.AllowedTools) != 1 {
		t.Errorf("AllowedTools len = %d, want 1", len(cfg.AllowedTools))
	}
}

func TestPresetWithMethods(t *testing.T) {
	preset := ExplorerPreset.
		WithName("custom-explorer").
		WithMaxIterations(50).
		WithAllowedTools("read", "grep")

	if preset.Name != "custom-explorer" {
		t.Errorf("Name = %q, want %q", preset.Name, "custom-explorer")
	}
	if preset.MaxIterations != 50 {
		t.Errorf("MaxIterations = %d, want 50", preset.MaxIterations)
	}
	if len(preset.AllowedTools) != 2 {
		t.Errorf("AllowedTools len = %d, want 2", len(preset.AllowedTools))
	}
}

func TestExplorerPreset(t *testing.T) {
	if ExplorerPreset.Name != "explorer" {
		t.Errorf("Name = %q, want %q", ExplorerPreset.Name, "explorer")
	}
	if ExplorerPreset.SystemPrompt == "" {
		t.Error("SystemPrompt should not be empty")
	}
	if len(ExplorerPreset.AllowedTools) == 0 {
		t.Error("AllowedTools should not be empty")
	}
}

func TestPlannerPreset(t *testing.T) {
	if PlannerPreset.Name != "planner" {
		t.Errorf("Name = %q, want %q", PlannerPreset.Name, "planner")
	}
	if PlannerPreset.SystemPrompt == "" {
		t.Error("SystemPrompt should not be empty")
	}
}

func TestResearcherPreset(t *testing.T) {
	if ResearcherPreset.Name != "researcher" {
		t.Errorf("Name = %q, want %q", ResearcherPreset.Name, "researcher")
	}
	// Researcher should have web access
	hasWebTool := false
	for _, t := range ResearcherPreset.AllowedTools {
		if t == "web_search" || t == "web_fetch" {
			hasWebTool = true
			break
		}
	}
	if !hasWebTool {
		t.Error("Researcher should have web access tools")
	}
}

func TestWriterPreset(t *testing.T) {
	if WriterPreset.Name != "writer" {
		t.Errorf("Name = %q, want %q", WriterPreset.Name, "writer")
	}
	// Writer should not restrict tools
	if len(WriterPreset.AllowedTools) != 0 {
		t.Error("Writer should not restrict tools (needs write access)")
	}
}

func TestReviewerPreset(t *testing.T) {
	if ReviewerPreset.Name != "reviewer" {
		t.Errorf("Name = %q, want %q", ReviewerPreset.Name, "reviewer")
	}
}

func TestSpawnExplorer(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockPresetLLMClient{}

	parent := New(Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	child, err := parent.SpawnExplorer(Config{})
	if err != nil {
		t.Fatalf("SpawnExplorer failed: %v", err)
	}

	if child.config.Name != "explorer" {
		t.Errorf("child Name = %q, want %q", child.config.Name, "explorer")
	}
	if child.config.SystemPrompt == "" {
		t.Error("child SystemPrompt should not be empty")
	}
}

func TestSpawnPlanner(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockPresetLLMClient{}

	parent := New(Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	child, err := parent.SpawnPlanner(Config{})
	if err != nil {
		t.Fatalf("SpawnPlanner failed: %v", err)
	}

	if child.config.Name != "planner" {
		t.Errorf("child Name = %q, want %q", child.config.Name, "planner")
	}
}

func TestSpawnWithCustomName(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockPresetLLMClient{}

	parent := New(Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	child, err := parent.SpawnExplorer(Config{Name: "my-explorer"})
	if err != nil {
		t.Fatalf("SpawnExplorer failed: %v", err)
	}

	if child.config.Name != "my-explorer" {
		t.Errorf("child Name = %q, want %q", child.config.Name, "my-explorer")
	}
}

func TestAllSpawnMethods(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockPresetLLMClient{}

	parent := New(Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	tests := []struct {
		name   string
		spawn  func(Config) (*Agent, error)
		expect string
	}{
		{"Explorer", parent.SpawnExplorer, "explorer"},
		{"Planner", parent.SpawnPlanner, "planner"},
		{"Researcher", parent.SpawnResearcher, "researcher"},
		{"Writer", parent.SpawnWriter, "writer"},
		{"Reviewer", parent.SpawnReviewer, "reviewer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			child, err := tt.spawn(Config{})
			if err != nil {
				t.Fatalf("Spawn%s failed: %v", tt.name, err)
			}
			if child.config.Name != tt.expect {
				t.Errorf("child Name = %q, want %q", child.config.Name, tt.expect)
			}
		})
	}
}

// mockPresetLLMClient for preset tests
type mockPresetLLMClient struct{}

func (m *mockPresetLLMClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
	}, nil
}

func (m *mockPresetLLMClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}
