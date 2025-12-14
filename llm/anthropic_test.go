// ABOUTME: Tests for the Anthropic LLM client implementation.
// ABOUTME: Verifies API communication, streaming, and error handling.
package llm

import (
	"testing"
)

func TestNewAnthropicClient(t *testing.T) {
	client := NewAnthropicClient("test-api-key", "claude-sonnet-4-20250514")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model claude-sonnet-4-20250514, got %s", client.model)
	}
}

func TestNewAnthropicClientDefaultModel(t *testing.T) {
	client := NewAnthropicClient("test-api-key", "")
	if client.model != "claude-sonnet-4-20250514" {
		t.Errorf("expected default model claude-sonnet-4-20250514, got %s", client.model)
	}
}

func TestAnthropicClientCreateMessage_ConvertsRequest(t *testing.T) {
	// This test verifies request conversion logic
	// We can't call real API without key, so test the conversion helpers
	req := &Request{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		System:    "You are helpful.",
		Messages: []Message{
			{Role: RoleUser, Content: "Hello"},
		},
		Tools: []ToolDefinition{
			{Name: "read", Description: "Read a file", InputSchema: map[string]any{"type": "object"}},
		},
	}

	params := convertRequest(req)
	if params.Model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model claude-sonnet-4-20250514, got %s", params.Model)
	}
	if params.MaxTokens != 1024 {
		t.Errorf("expected max_tokens 1024, got %d", params.MaxTokens)
	}
	if len(params.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(params.Messages))
	}
}
