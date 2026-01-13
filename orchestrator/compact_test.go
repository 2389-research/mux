// ABOUTME: Tests for conversation compaction logic.
// ABOUTME: Verifies budget checking, message collection, and history rebuilding.
package orchestrator

import (
	"context"
	"testing"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

// compactMockClient implements llm.Client for testing compaction.
type compactMockClient struct {
	response *llm.Response
	err      error
	requests []*llm.Request // capture requests for inspection
}

func (m *compactMockClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	m.requests = append(m.requests, req)
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func (m *compactMockClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		resp, _ := m.CreateMessage(ctx, req)
		ch <- llm.StreamEvent{Type: llm.EventMessageStop, Response: resp}
		close(ch)
	}()
	return ch, nil
}

// newTestExecutor creates a test executor with an empty registry.
func newTestExecutor() *tool.Executor {
	registry := tool.NewRegistry()
	return tool.NewExecutor(registry)
}

func TestCompactDisabled(t *testing.T) {
	// When ContextBudget is 0, compaction should be disabled
	client := &compactMockClient{}
	executor := newTestExecutor()
	config := Config{
		MaxIterations: 10,
		ContextBudget: 0, // disabled
	}
	orch := NewWithConfig(client, executor, config)

	// Add some messages to simulate conversation
	orch.mu.Lock()
	orch.messages = []llm.Message{
		llm.NewUserMessage("Hello, this is a test message"),
		llm.NewAssistantMessage("I understand, let me help you with that"),
	}
	result, err := orch.compact(context.Background())
	orch.mu.Unlock()

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result when compaction disabled, got: %+v", result)
	}
}

func TestCompactUnderBudget(t *testing.T) {
	// When token count is under budget, compaction should not trigger
	client := &compactMockClient{}
	executor := newTestExecutor()
	config := Config{
		MaxIterations: 10,
		ContextBudget: 100000, // very high budget
	}
	orch := NewWithConfig(client, executor, config)

	// Add a small message that's definitely under budget
	orch.mu.Lock()
	orch.messages = []llm.Message{
		llm.NewUserMessage("Hello"),
	}
	result, err := orch.compact(context.Background())
	orch.mu.Unlock()

	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result when under budget, got: %+v", result)
	}
}

func TestCollectRecentUserMessages(t *testing.T) {
	client := &compactMockClient{}
	executor := newTestExecutor()
	config := DefaultConfig()
	orch := NewWithConfig(client, executor, config)

	// Set up a conversation with multiple user messages
	orch.mu.Lock()
	orch.messages = []llm.Message{
		llm.NewUserMessage("First user message"),
		llm.NewAssistantMessage("First assistant response"),
		llm.NewUserMessage("Second user message"),
		llm.NewAssistantMessage("Second assistant response"),
		llm.NewUserMessage("Third user message"),
	}

	// Collect recent user messages with high token limit
	recent := orch.collectRecentUserMessages(10000)
	orch.mu.Unlock()

	// Should collect all user messages in chronological order
	if len(recent) != 3 {
		t.Fatalf("expected 3 user messages, got %d", len(recent))
	}
	if recent[0].Content != "First user message" {
		t.Errorf("expected first message to be 'First user message', got %q", recent[0].Content)
	}
	if recent[2].Content != "Third user message" {
		t.Errorf("expected last message to be 'Third user message', got %q", recent[2].Content)
	}
}

func TestCollectRecentUserMessagesWithTokenLimit(t *testing.T) {
	client := &compactMockClient{}
	executor := newTestExecutor()
	config := DefaultConfig()
	orch := NewWithConfig(client, executor, config)

	// Set up messages with known sizes
	orch.mu.Lock()
	orch.messages = []llm.Message{
		llm.NewUserMessage("First message that is quite long and should take up tokens"),
		llm.NewAssistantMessage("Response"),
		llm.NewUserMessage("Second message"),
		llm.NewAssistantMessage("Response"),
		llm.NewUserMessage("Third"),
	}

	// Collect with very small token limit - should only get most recent messages
	recent := orch.collectRecentUserMessages(5) // very small limit
	orch.mu.Unlock()

	// Should get at least one message (the most recent)
	if len(recent) == 0 {
		t.Fatal("expected at least one message")
	}
	// Most recent message should be included
	if recent[len(recent)-1].Content != "Third" {
		t.Errorf("expected most recent message to be 'Third', got %q", recent[len(recent)-1].Content)
	}
}

func TestBuildCompactedHistory(t *testing.T) {
	client := &compactMockClient{}
	executor := newTestExecutor()
	config := DefaultConfig()
	orch := NewWithConfig(client, executor, config)

	orch.mu.Lock()
	summary := "This is a summary of the conversation so far."
	recentMsgs := []llm.Message{
		llm.NewUserMessage("Recent user question"),
	}

	compacted := orch.buildCompactedHistory(summary, recentMsgs)
	orch.mu.Unlock()

	// Should have 2 messages: summary (as user) and the recent user messages
	if len(compacted) != 2 {
		t.Fatalf("expected 2 messages in compacted history, got %d", len(compacted))
	}

	// First message should be the summary with prefix
	if compacted[0].Role != llm.RoleUser {
		t.Errorf("expected first message to be user role, got %v", compacted[0].Role)
	}
	if compacted[0].Content == "" {
		t.Error("expected first message to contain summary content")
	}

	// Second message should be the recent user message
	if compacted[1].Content != "Recent user question" {
		t.Errorf("expected second message to be recent user question, got %q", compacted[1].Content)
	}
}

func TestBuildSummarizationRequest(t *testing.T) {
	client := &compactMockClient{}
	executor := newTestExecutor()
	config := Config{
		MaxIterations:   10,
		SystemPrompt:    "You are helpful",
		Model:           "claude-3-opus",
		CompactionModel: "claude-3-haiku", // use different model for compaction
	}
	orch := NewWithConfig(client, executor, config)

	orch.mu.Lock()
	orch.messages = []llm.Message{
		llm.NewUserMessage("Test message"),
		llm.NewAssistantMessage("Test response"),
	}
	req := orch.buildSummarizationRequest()
	orch.mu.Unlock()

	// Should use compaction model
	if req.Model != "claude-3-haiku" {
		t.Errorf("expected model to be 'claude-3-haiku', got %q", req.Model)
	}

	// Should include the conversation messages
	if len(req.Messages) == 0 {
		t.Error("expected messages in request")
	}

	// Should have system prompt with summarization instruction
	if req.System == "" {
		t.Error("expected system prompt")
	}
}

func TestBuildSummarizationRequestUsesMainModel(t *testing.T) {
	// When CompactionModel is empty, should use main Model
	client := &compactMockClient{}
	executor := newTestExecutor()
	config := Config{
		MaxIterations:   10,
		Model:           "claude-3-opus",
		CompactionModel: "", // empty - should fall back to main model
	}
	orch := NewWithConfig(client, executor, config)

	orch.mu.Lock()
	orch.messages = []llm.Message{
		llm.NewUserMessage("Test"),
	}
	req := orch.buildSummarizationRequest()
	orch.mu.Unlock()

	if req.Model != "claude-3-opus" {
		t.Errorf("expected model to be 'claude-3-opus' (fallback), got %q", req.Model)
	}
}

func TestCompactIntegration(t *testing.T) {
	// Integration test: compaction should work end-to-end
	summaryText := "Summary of the conversation: user asked about Go testing."
	client := &compactMockClient{
		response: &llm.Response{
			ID: "test-response",
			Content: []llm.ContentBlock{
				{Type: llm.ContentTypeText, Text: summaryText},
			},
			StopReason: llm.StopReasonEndTurn,
			Usage:      llm.Usage{InputTokens: 100, OutputTokens: 50},
		},
	}
	executor := newTestExecutor()

	// Create a large conversation that exceeds budget
	// Each character is roughly 0.25 tokens, so 4000 chars ~ 1000 tokens
	largeText := make([]byte, 4000)
	for i := range largeText {
		largeText[i] = 'x'
	}

	config := Config{
		MaxIterations: 10,
		ContextBudget: 100, // very low budget to trigger compaction
		Model:         "claude-3-opus",
	}
	orch := NewWithConfig(client, executor, config)

	orch.mu.Lock()
	orch.messages = []llm.Message{
		llm.NewUserMessage(string(largeText)),
		llm.NewAssistantMessage("Response to the large message"),
	}
	originalLen := len(orch.messages)
	originalTokens := EstimateMessagesTokens(orch.messages)

	result, err := orch.compact(context.Background())
	orch.mu.Unlock()

	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected compaction result, got nil")
	}

	// Verify result
	if result.Summary != summaryText {
		t.Errorf("expected summary %q, got %q", summaryText, result.Summary)
	}
	if result.OriginalTokens != originalTokens {
		t.Errorf("expected original tokens %d, got %d", originalTokens, result.OriginalTokens)
	}
	if result.MessagesRemoved != originalLen {
		t.Errorf("expected %d messages removed, got %d", originalLen, result.MessagesRemoved)
	}

	// Verify LLM was called
	if len(client.requests) != 1 {
		t.Errorf("expected 1 LLM request, got %d", len(client.requests))
	}
}
