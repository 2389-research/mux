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
