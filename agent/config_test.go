// ABOUTME: Tests for the agent Config struct to verify configuration
// ABOUTME: options for creating agents with specific tool access settings.
package agent_test

import (
	"testing"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/tool"
)

func TestAgentConfig(t *testing.T) {
	registry := tool.NewRegistry()

	cfg := agent.Config{
		Name:         "test-agent",
		AllowedTools: []string{"read_file"},
		DeniedTools:  []string{"bash"},
		Registry:     registry,
	}

	if cfg.Name != "test-agent" {
		t.Errorf("expected name test-agent, got %s", cfg.Name)
	}
	if len(cfg.AllowedTools) != 1 {
		t.Errorf("expected 1 allowed tool, got %d", len(cfg.AllowedTools))
	}
}
