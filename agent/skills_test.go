// ABOUTME: Agent-level integration tests for skills: catalog injection into the
// ABOUTME: system prompt, load_skill registration, and allowlist auto-reachability.
package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/skill"
	"github.com/2389-research/mux/tool"
)

// skillsDir builds a one-skill registry (greet) from a temp directory.
func skillsDir(t *testing.T) *skill.Registry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "greet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: greet\ndescription: Say hi to the user.\n---\n\nSay hello warmly.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := skill.LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// toolResultText returns the Text of the first tool_result block for the named
// tool in the conversation history, or "" if none is present.
func toolResultText(msgs []llm.Message, toolName string) string {
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.ContentTypeToolResult && b.Name == toolName {
				return b.Text
			}
		}
	}
	return ""
}

func TestAgentInjectsSkillCatalog(t *testing.T) {
	client := &capturingClient{
		response: &llm.Response{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}}},
	}
	a := agent.New(agent.Config{
		Name:         "root",
		Registry:     tool.NewRegistry(),
		LLMClient:    client,
		SystemPrompt: "Base prompt.",
		Skills:       skillsDir(t),
	})
	if err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.lastRequest == nil {
		t.Fatal("no request captured")
	}
	sys := client.lastRequest.System
	if !strings.Contains(sys, "Base prompt.") {
		t.Errorf("system prompt lost the base:\n%s", sys)
	}
	if !strings.Contains(sys, "## Available Skills") || !strings.Contains(sys, "- **greet** — Say hi to the user.") {
		t.Errorf("system prompt missing catalog:\n%s", sys)
	}
}

func TestAgentLoadSkillRoundTrip(t *testing.T) {
	client := &scriptedClient{responses: []*llm.Response{
		{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "load_skill", Input: map[string]any{"name": "greet"}}},
			StopReason: llm.StopReasonToolUse,
		},
		{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		},
	}}
	a := agent.New(agent.Config{
		Name:      "root",
		Registry:  tool.NewRegistry(),
		LLMClient: client,
		Skills:    skillsDir(t),
	})
	if err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := toolResultText(a.Messages(), "load_skill"); got != "Say hello warmly." {
		t.Errorf("load_skill result = %q, want skill body", got)
	}
}

func TestAgentLoadSkillReachableWithAllowlist(t *testing.T) {
	client := &scriptedClient{responses: []*llm.Response{
		{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "load_skill", Input: map[string]any{"name": "greet"}}},
			StopReason: llm.StopReasonToolUse,
		},
		{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		},
	}}
	// Non-empty allowlist that omits load_skill: wiring must auto-allow it.
	a := agent.New(agent.Config{
		Name:         "root",
		Registry:     tool.NewRegistry(),
		LLMClient:    client,
		AllowedTools: []string{"some_other_tool"},
		Skills:       skillsDir(t),
	})
	if err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := toolResultText(a.Messages(), "load_skill"); got != "Say hello warmly." {
		t.Errorf("load_skill not reachable under allowlist: got %q", got)
	}
	// The caller's stored allowlist is unchanged (no silent mutation).
	if got := a.Config().AllowedTools; len(got) != 1 || got[0] != "some_other_tool" {
		t.Errorf("Config().AllowedTools = %v, want [some_other_tool]", got)
	}
}

func TestAgentNoSkillsUnaffected(t *testing.T) {
	client := &capturingClient{
		response: &llm.Response{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}}},
	}
	reg := tool.NewRegistry()
	a := agent.New(agent.Config{
		Name:      "root",
		Registry:  reg,
		LLMClient: client,
	})
	if err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.lastRequest != nil && strings.Contains(client.lastRequest.System, "## Available Skills") {
		t.Error("catalog injected without Skills set")
	}
	if _, ok := reg.Get("load_skill"); ok {
		t.Error("load_skill registered without Skills set")
	}
}
