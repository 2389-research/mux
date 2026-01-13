// ABOUTME: Core compaction logic for conversation context management.
// ABOUTME: Summarizes conversation history when context budget is exceeded.
package orchestrator

import (
	"context"
	"errors"

	"github.com/2389-research/mux/llm"
)

// ErrEmptySummary is returned when compaction produces an empty summary.
var ErrEmptySummary = errors.New("compaction produced empty summary")

// SummarizationPrompt is sent to LLM to generate conversation summary.
const SummarizationPrompt = `You are performing a CONTEXT CHECKPOINT COMPACTION. Create a handoff summary for another LLM that will resume the task.

Include:
- Current progress and key decisions made
- Important context, constraints, or user preferences
- What remains to be done (clear next steps)
- Any critical data, examples, or references needed to continue

Be concise, structured, and focused on helping the next LLM seamlessly continue the work.`

// SummaryPrefix is prepended to the summary when rebuilding history.
const SummaryPrefix = `Another language model started to solve this problem and produced a summary of its thinking process. You also have access to the state of the tools that were used by that language model. Use this to build on the work that has already been done and avoid duplicating work. Here is the summary produced by the other language model, use the information in this summary to assist with your own analysis:`

// CompactionResult holds the output of a compaction operation.
type CompactionResult struct {
	Summary         string
	OriginalTokens  int
	CompactedTokens int
	MessagesRemoved int
}

// compact performs conversation compaction when context budget exceeded.
// Returns nil if compaction not needed or disabled.
// Must be called with o.mu held.
func (o *Orchestrator) compact(ctx context.Context) (*CompactionResult, error) {
	// 1. Return nil if ContextBudget <= 0 (disabled)
	if o.config.ContextBudget <= 0 {
		return nil, nil
	}

	// 2. Estimate current tokens with EstimateMessagesTokens
	originalTokens := EstimateMessagesTokens(o.messages)

	// 3. Return nil if currentTokens <= ContextBudget
	if originalTokens <= o.config.ContextBudget {
		return nil, nil
	}

	// 4. Build summarization request
	req := o.buildSummarizationRequest()

	// 5. Call LLM for summary
	resp, err := o.client.CreateMessage(ctx, req)
	if err != nil {
		return nil, err
	}

	// 6. Track usage with o.usage.Add()
	o.usage.Add(resp.Usage)

	// Extract summary text from response
	summary := resp.TextContent()
	if summary == "" {
		return nil, ErrEmptySummary
	}

	// 7. Collect recent user messages (up to CompactUserMessageMaxTokens)
	recentUserMsgs := o.collectRecentUserMessages(CompactUserMessageMaxTokens)

	// 8. Build compacted history
	originalMsgCount := len(o.messages)
	o.messages = o.buildCompactedHistory(summary, recentUserMsgs)

	// 9. Return result
	compactedTokens := EstimateMessagesTokens(o.messages)
	return &CompactionResult{
		Summary:         summary,
		OriginalTokens:  originalTokens,
		CompactedTokens: compactedTokens,
		MessagesRemoved: originalMsgCount - len(o.messages),
	}, nil
}

// buildSummarizationRequest creates the LLM request for summarization.
func (o *Orchestrator) buildSummarizationRequest() *llm.Request {
	// Determine which model to use
	model := o.config.CompactionModel
	if model == "" {
		model = o.config.Model
	}

	// Create request with conversation and summarization instruction
	return &llm.Request{
		Messages:  o.messages,
		System:    SummarizationPrompt,
		Model:     model,
		MaxTokens: 4096,
	}
}

// collectRecentUserMessages extracts recent user messages up to token limit.
// Returns messages in chronological order, prioritizing the most recent messages.
func (o *Orchestrator) collectRecentUserMessages(maxTokens int) []llm.Message {
	// Collect user messages from most recent to oldest
	var recentFirst []llm.Message
	for i := len(o.messages) - 1; i >= 0; i-- {
		msg := o.messages[i]
		// Include user messages with content in either Content field or Blocks
		if msg.Role == llm.RoleUser && (msg.Content != "" || len(msg.Blocks) > 0) {
			recentFirst = append(recentFirst, msg)
		}
	}

	// Select messages from most recent, respecting token limit
	var selected []llm.Message
	tokenCount := 0

	for _, msg := range recentFirst {
		msgTokens := EstimateMessageTokens(msg)

		// Always include at least the most recent message
		if len(selected) == 0 || tokenCount+msgTokens <= maxTokens {
			selected = append(selected, msg)
			tokenCount += msgTokens
		} else {
			break
		}
	}

	// Reverse to get chronological order
	result := make([]llm.Message, len(selected))
	for i, msg := range selected {
		result[len(selected)-1-i] = msg
	}

	return result
}

// buildCompactedHistory creates new message history with summary + recent messages.
// The summary becomes an assistant message to maintain valid alternating roles.
// Only the most recent user message is kept to ensure user/assistant alternation.
func (o *Orchestrator) buildCompactedHistory(summary string, recentUserMsgs []llm.Message) []llm.Message {
	// Summary becomes assistant message with prefix
	summaryContent := SummaryPrefix + "\n\n" + summary
	summaryMsg := llm.Message{
		Role:   llm.RoleAssistant,
		Blocks: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: summaryContent}},
	}

	// Only keep the most recent user message to ensure valid alternation
	// (assistant -> user is valid, but assistant -> user -> user is not)
	if len(recentUserMsgs) > 0 {
		return []llm.Message{summaryMsg, recentUserMsgs[len(recentUserMsgs)-1]}
	}

	return []llm.Message{summaryMsg}
}
