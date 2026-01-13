// ABOUTME: Token estimation utilities for conversation compaction.
// ABOUTME: Uses a byte-based heuristic (~4 bytes per token) for fast approximation.
package orchestrator

import (
	"encoding/json"

	"github.com/2389-research/mux/llm"
)

// ApproxBytesPerToken is the approximate number of bytes per token.
// This is a rough heuristic that works reasonably well for English text.
const ApproxBytesPerToken = 4

// EstimateTokens estimates the number of tokens in a text string.
// Uses a simple byte-based heuristic: ~4 bytes per token.
// Returns at least 1 for non-empty strings.
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	tokens := (len(text) + ApproxBytesPerToken - 1) / ApproxBytesPerToken
	return tokens
}

// EstimateMessageTokens estimates the number of tokens in a message.
// Handles text content and blocks (Text, ToolUse, ToolResult).
func EstimateMessageTokens(msg llm.Message) int {
	var totalBytes int

	// Count Content field
	totalBytes += len(msg.Content)

	// Count Blocks
	for _, block := range msg.Blocks {
		totalBytes += estimateBlockBytes(block)
	}

	if totalBytes == 0 {
		return 0
	}
	return (totalBytes + ApproxBytesPerToken - 1) / ApproxBytesPerToken
}

// EstimateMessagesTokens estimates the total tokens across a slice of messages.
func EstimateMessagesTokens(msgs []llm.Message) int {
	total := 0
	for _, msg := range msgs {
		total += EstimateMessageTokens(msg)
	}
	return total
}

// estimateBlockBytes estimates the byte size of a content block.
func estimateBlockBytes(block llm.ContentBlock) int {
	switch block.Type {
	case llm.ContentTypeText:
		return len(block.Text)
	case llm.ContentTypeToolUse:
		bytes := len(block.ID) + len(block.Name)
		if block.Input != nil {
			// Marshal input to JSON for size estimation
			if data, err := json.Marshal(block.Input); err == nil {
				bytes += len(data)
			}
		}
		return bytes
	case llm.ContentTypeToolResult:
		return len(block.ToolUseID) + len(block.Text)
	default:
		return 0
	}
}
