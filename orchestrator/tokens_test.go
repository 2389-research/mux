// ABOUTME: Tests for token estimation utilities used by conversation compaction.
// ABOUTME: Verifies byte-based heuristic token counting for messages and content blocks.
package orchestrator

import (
	"testing"

	"github.com/2389-research/mux/llm"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected int
	}{
		{
			name:     "empty string",
			text:     "",
			expected: 0,
		},
		{
			name:     "single character",
			text:     "a",
			expected: 1, // 1 byte / 4 = 0.25, rounds up to 1
		},
		{
			name:     "four bytes equals one token",
			text:     "test",
			expected: 1,
		},
		{
			name:     "eight bytes equals two tokens",
			text:     "test1234",
			expected: 2,
		},
		{
			name:     "partial token rounds up",
			text:     "hello", // 5 bytes -> 1.25 -> 2 tokens
			expected: 2,
		},
		{
			name:     "longer text",
			text:     "The quick brown fox jumps over the lazy dog", // 43 bytes -> 11 tokens
			expected: 11,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.text)
			if got != tt.expected {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.text, got, tt.expected)
			}
		})
	}
}

func TestEstimateMessageTokens(t *testing.T) {
	tests := []struct {
		name     string
		msg      llm.Message
		expected int
	}{
		{
			name:     "empty message",
			msg:      llm.Message{},
			expected: 0,
		},
		{
			name:     "text content only",
			msg:      llm.NewUserMessage("test"),
			expected: 1, // 4 bytes
		},
		{
			name: "text block",
			msg: llm.Message{
				Role: llm.RoleAssistant,
				Blocks: []llm.ContentBlock{
					{Type: llm.ContentTypeText, Text: "test1234"},
				},
			},
			expected: 2, // 8 bytes
		},
		{
			name: "tool use block",
			msg: llm.Message{
				Role: llm.RoleAssistant,
				Blocks: []llm.ContentBlock{
					{
						Type:  llm.ContentTypeToolUse,
						ID:    "id12",                       // 4 bytes
						Name:  "tool",                       // 4 bytes
						Input: map[string]any{"key": "val"}, // ~15 bytes JSON
					},
				},
			},
			expected: 6, // ~23 bytes -> 6 tokens
		},
		{
			name: "tool result block",
			msg: llm.Message{
				Role: llm.RoleUser,
				Blocks: []llm.ContentBlock{
					{
						Type:      llm.ContentTypeToolResult,
						ToolUseID: "id12",   // 4 bytes
						Text:      "result", // 6 bytes
					},
				},
			},
			expected: 3, // 10 bytes -> 3 tokens
		},
		{
			name: "multiple blocks",
			msg: llm.Message{
				Role: llm.RoleAssistant,
				Blocks: []llm.ContentBlock{
					{Type: llm.ContentTypeText, Text: "test"}, // 4 bytes
					{Type: llm.ContentTypeText, Text: "more"}, // 4 bytes
				},
			},
			expected: 2, // 8 bytes total
		},
		{
			name: "content and blocks both set",
			msg: llm.Message{
				Role:    llm.RoleUser,
				Content: "test",
				Blocks: []llm.ContentBlock{
					{Type: llm.ContentTypeText, Text: "more"},
				},
			},
			expected: 2, // 4 + 4 = 8 bytes
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateMessageTokens(tt.msg)
			if got != tt.expected {
				t.Errorf("EstimateMessageTokens() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestEstimateMessagesTokens(t *testing.T) {
	tests := []struct {
		name     string
		msgs     []llm.Message
		expected int
	}{
		{
			name:     "empty slice",
			msgs:     []llm.Message{},
			expected: 0,
		},
		{
			name:     "nil slice",
			msgs:     nil,
			expected: 0,
		},
		{
			name: "single message",
			msgs: []llm.Message{
				llm.NewUserMessage("test"),
			},
			expected: 1,
		},
		{
			name: "multiple messages",
			msgs: []llm.Message{
				llm.NewUserMessage("test"),      // 1 token
				llm.NewAssistantMessage("test"), // 1 token
			},
			expected: 2,
		},
		{
			name: "mixed content types",
			msgs: []llm.Message{
				llm.NewUserMessage("hello"), // 5 bytes -> 2 tokens
				{
					Role: llm.RoleAssistant,
					Blocks: []llm.ContentBlock{
						{Type: llm.ContentTypeText, Text: "test"}, // 4 bytes -> 1 token
					},
				},
			},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateMessagesTokens(tt.msgs)
			if got != tt.expected {
				t.Errorf("EstimateMessagesTokens() = %d, want %d", got, tt.expected)
			}
		})
	}
}
