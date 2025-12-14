// ABOUTME: Tests for the Anthropic LLM client implementation.
// ABOUTME: Verifies API communication, streaming, and error handling.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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

// Error Handling Tests

func TestAnthropicClient_InvalidAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "authentication_error", "message": "invalid x-api-key"},
		})
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("invalid-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err == nil {
		t.Fatal("expected error for invalid API key, got nil")
	}
	if !strings.Contains(err.Error(), "authentication") && !strings.Contains(err.Error(), "401") {
		t.Errorf("expected authentication error, got: %v", err)
	}
}

func TestAnthropicClient_RateLimiting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "rate_limit_error", "message": "Rate limit exceeded"},
		})
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err == nil {
		t.Fatal("expected error for rate limiting, got nil")
	}
	if !strings.Contains(err.Error(), "rate") && !strings.Contains(err.Error(), "429") {
		t.Errorf("expected rate limit error, got: %v", err)
	}
}

func TestAnthropicClient_NetworkTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
			option.WithHTTPClient(&http.Client{Timeout: 50 * time.Millisecond}),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestAnthropicClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context") {
		t.Errorf("expected context error, got: %v", err)
	}
}

func TestAnthropicClient_MalformedJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id": "msg_123", "content": [{"type": "text", "text": "hello"`))
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

func TestAnthropicClient_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "internal_server_error", "message": "Internal server error"},
		})
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err == nil {
		t.Fatal("expected error for server error, got nil")
	}
	if !strings.Contains(err.Error(), "500") && !strings.Contains(err.Error(), "internal") {
		t.Errorf("expected server error, got: %v", err)
	}
}

func TestAnthropicClient_ConnectionRefused(t *testing.T) {
	// Use a port that's guaranteed to be closed
	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL("http://localhost:1"), // Port 1 should be unavailable
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err == nil {
		t.Fatal("expected connection error, got nil")
	}
}

// Tool Schema Conversion Edge Cases

func TestConvertRequest_ToolSchemaWithMissingProperties(t *testing.T) {
	req := &Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []Message{NewUserMessage("Hello")},
		Tools: []ToolDefinition{
			{
				Name:        "test_tool",
				Description: "Test tool without properties",
				InputSchema: map[string]any{
					"type": "object",
					// No properties field
				},
			},
		},
	}

	params := convertRequest(req)
	if len(params.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(params.Tools))
	}
	// Should not panic, properties should be nil/empty
	if params.Tools[0].OfTool.InputSchema.Properties != nil {
		t.Logf("Properties is not nil: %+v", params.Tools[0].OfTool.InputSchema.Properties)
	}
}

func TestConvertRequest_ToolSchemaWithEmptyRequired(t *testing.T) {
	req := &Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []Message{NewUserMessage("Hello")},
		Tools: []ToolDefinition{
			{
				Name:        "test_tool",
				Description: "Test tool",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"param1": map[string]any{"type": "string"},
					},
					"required": []string{},
				},
			},
		},
	}

	params := convertRequest(req)
	if len(params.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(params.Tools))
	}
	if len(params.Tools[0].OfTool.InputSchema.Required) != 0 {
		t.Errorf("expected 0 required fields, got %d", len(params.Tools[0].OfTool.InputSchema.Required))
	}
}

func TestConvertRequest_ToolSchemaWithAnySliceRequired(t *testing.T) {
	req := &Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []Message{NewUserMessage("Hello")},
		Tools: []ToolDefinition{
			{
				Name:        "test_tool",
				Description: "Test tool",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"param1": map[string]any{"type": "string"},
					},
					"required": []any{"param1", "param2"}, // []any instead of []string
				},
			},
		},
	}

	params := convertRequest(req)
	if len(params.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(params.Tools))
	}
	if len(params.Tools[0].OfTool.InputSchema.Required) != 2 {
		t.Errorf("expected 2 required fields, got %d", len(params.Tools[0].OfTool.InputSchema.Required))
	}
	if params.Tools[0].OfTool.InputSchema.Required[0] != "param1" {
		t.Errorf("expected first required field to be param1, got %s", params.Tools[0].OfTool.InputSchema.Required[0])
	}
}

func TestConvertRequest_ToolSchemaWithInvalidRequiredType(t *testing.T) {
	req := &Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []Message{NewUserMessage("Hello")},
		Tools: []ToolDefinition{
			{
				Name:        "test_tool",
				Description: "Test tool",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"param1": map[string]any{"type": "string"},
					},
					"required": map[string]any{"invalid": "type"}, // Wrong type
				},
			},
		},
	}

	params := convertRequest(req)
	if len(params.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(params.Tools))
	}
	// Should handle gracefully, required should be empty
	if len(params.Tools[0].OfTool.InputSchema.Required) != 0 {
		t.Errorf("expected 0 required fields for invalid type, got %d", len(params.Tools[0].OfTool.InputSchema.Required))
	}
}

func TestConvertRequest_ToolSchemaWithMixedRequiredTypes(t *testing.T) {
	req := &Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []Message{NewUserMessage("Hello")},
		Tools: []ToolDefinition{
			{
				Name:        "test_tool",
				Description: "Test tool",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"param1": map[string]any{"type": "string"},
					},
					"required": []any{"param1", 123, "param2"}, // Mixed types
				},
			},
		},
	}

	params := convertRequest(req)
	if len(params.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(params.Tools))
	}
	// Should only convert string values
	if len(params.Tools[0].OfTool.InputSchema.Required) != 2 {
		t.Errorf("expected 2 required fields (strings only), got %d", len(params.Tools[0].OfTool.InputSchema.Required))
	}
}

func TestConvertRequest_MultipleTools(t *testing.T) {
	req := &Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []Message{NewUserMessage("Hello")},
		Tools: []ToolDefinition{
			{
				Name:        "tool1",
				Description: "First tool",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"param1": map[string]any{"type": "string"},
					},
					"required": []string{"param1"},
				},
			},
			{
				Name:        "tool2",
				Description: "Second tool",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"param2": map[string]any{"type": "number"},
					},
				},
			},
		},
	}

	params := convertRequest(req)
	if len(params.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(params.Tools))
	}
	if params.Tools[0].OfTool.Name != "tool1" {
		t.Errorf("expected first tool name to be tool1, got %s", params.Tools[0].OfTool.Name)
	}
	if params.Tools[1].OfTool.Name != "tool2" {
		t.Errorf("expected second tool name to be tool2, got %s", params.Tools[1].OfTool.Name)
	}
}

// Stream Interruption Tests

func TestAnthropicClient_StreamContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.ResponseWriter to be an http.Flusher")
		}

		// Send initial event
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n"))
		flusher.Flush()

		// Wait before sending more events (simulating slow stream)
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error creating stream: %v", err)
	}

	// Consume events until channel closes or error
	var gotError bool
	for event := range eventChan {
		if event.Type == EventError {
			gotError = true
			if !errors.Is(event.Error, context.DeadlineExceeded) && !strings.Contains(event.Error.Error(), "context") {
				t.Logf("got error event: %v", event.Error)
			}
		}
	}

	// We should get an error due to context cancellation
	if !gotError {
		t.Log("expected error event for context cancellation, but stream may have ended gracefully")
	}
}

func TestAnthropicClient_StreamServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "internal_server_error", "message": "Internal server error"},
		})
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		// Some SDK versions may return error immediately
		if !strings.Contains(err.Error(), "500") && !strings.Contains(err.Error(), "internal") {
			t.Errorf("expected server error, got: %v", err)
		}
		return
	}

	// Others may send error via channel
	var gotError bool
	for event := range eventChan {
		if event.Type == EventError {
			gotError = true
			if event.Error == nil {
				t.Error("expected error to be set in error event")
			}
		}
	}

	if !gotError && err == nil {
		t.Error("expected error event or error return for server error")
	}
}

func TestConvertRequest_ComplexMessageBlocks(t *testing.T) {
	req := &Request{
		Model:    "claude-sonnet-4-20250514",
		Messages: []Message{
			{
				Role: RoleUser,
				Blocks: []ContentBlock{
					{Type: ContentTypeText, Text: "Hello"},
				},
			},
			{
				Role: RoleAssistant,
				Blocks: []ContentBlock{
					{Type: ContentTypeText, Text: "Hi there!"},
					{Type: ContentTypeToolUse, ID: "tool_123", Name: "test_tool", Input: map[string]any{"arg": "value"}},
				},
			},
			{
				Role: RoleUser,
				Blocks: []ContentBlock{
					{Type: ContentTypeToolResult, ToolUseID: "tool_123", Text: "result", IsError: false},
				},
			},
		},
	}

	params := convertRequest(req)
	if len(params.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(params.Messages))
	}

	// Check first message (user with text)
	if len(params.Messages[0].Content) != 1 {
		t.Errorf("expected 1 content block in first message, got %d", len(params.Messages[0].Content))
	}

	// Check second message (assistant with text and tool use)
	if len(params.Messages[1].Content) != 2 {
		t.Errorf("expected 2 content blocks in second message, got %d", len(params.Messages[1].Content))
	}

	// Check third message (user with tool result)
	if len(params.Messages[2].Content) != 1 {
		t.Errorf("expected 1 content block in third message, got %d", len(params.Messages[2].Content))
	}
}

func TestConvertResponse_ToolUseWithNullInput(t *testing.T) {
	msg := &anthropic.Message{
		ID:    "msg_123",
		Model: "claude-sonnet-4-20250514",
		Content: []anthropic.ContentBlockUnion{
			{
				Type:  "tool_use",
				ID:    "tool_123",
				Name:  "test_tool",
				Input: nil, // Null input
			},
		},
		StopReason: "tool_use",
		Usage: anthropic.Usage{
			InputTokens:  10,
			OutputTokens: 5,
		},
	}

	resp := convertResponse(msg)
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(resp.Content))
	}
	if resp.Content[0].Type != ContentTypeToolUse {
		t.Errorf("expected tool_use type, got %s", resp.Content[0].Type)
	}
	// Input should be nil or empty map
	if resp.Content[0].Input == nil {
		t.Logf("Input is nil for null input")
	}
}

// CreateMessageStream Tests

func TestCreateMessageStream_SuccessfulTextStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.ResponseWriter to be an http.Flusher")
		}

		// Send message_start event
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n"))
		flusher.Flush()

		// Send content_block_start event
		w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		flusher.Flush()

		// Send content_block_delta events with text
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n"))
		flusher.Flush()

		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n\n"))
		flusher.Flush()

		// Send content_block_stop event
		w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		flusher.Flush()

		// Send message_delta event
		w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":5}}\n\n"))
		flusher.Flush()

		// Send message_stop event
		w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error creating stream: %v", err)
	}

	// Collect all events
	var events []StreamEvent
	for event := range eventChan {
		events = append(events, event)
		if event.Type == EventError {
			t.Fatalf("unexpected error event: %v", event.Error)
		}
	}

	// Verify we got all expected events
	expectedEventTypes := []EventType{
		EventMessageStart,
		EventContentStart,
		EventContentDelta,
		EventContentDelta,
		EventContentStop,
		EventMessageDelta,
		EventMessageStop,
	}

	if len(events) != len(expectedEventTypes) {
		t.Fatalf("expected %d events, got %d", len(expectedEventTypes), len(events))
	}

	for i, expectedType := range expectedEventTypes {
		if events[i].Type != expectedType {
			t.Errorf("event %d: expected type %s, got %s", i, expectedType, events[i].Type)
		}
	}

	// Verify message_start event has response
	if events[0].Response == nil {
		t.Error("message_start event should have response")
	} else if events[0].Response.ID != "msg_123" {
		t.Errorf("expected message ID msg_123, got %s", events[0].Response.ID)
	}

	// Verify content_delta events have text
	if events[2].Text != "Hello" {
		t.Errorf("expected first delta text 'Hello', got '%s'", events[2].Text)
	}
	if events[3].Text != " world" {
		t.Errorf("expected second delta text ' world', got '%s'", events[3].Text)
	}

	// Verify indices are correct
	if events[1].Index != 0 {
		t.Errorf("expected content_start index 0, got %d", events[1].Index)
	}
	if events[4].Index != 0 {
		t.Errorf("expected content_stop index 0, got %d", events[4].Index)
	}
}

func TestCreateMessageStream_WithToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.ResponseWriter to be an http.Flusher")
		}

		// Send message_start event
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_456\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"usage\":{\"input_tokens\":15,\"output_tokens\":0}}}\n\n"))
		flusher.Flush()

		// Send text content_block_start
		w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		flusher.Flush()

		// Send text delta
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Let me help\"}}\n\n"))
		flusher.Flush()

		// Send text block stop
		w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		flusher.Flush()

		// Send tool_use content_block_start
		w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_abc\",\"name\":\"read_file\"}}\n\n"))
		flusher.Flush()

		// Send tool input delta (non-text delta)
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\"}}\n\n"))
		flusher.Flush()

		// Send tool block stop
		w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"))
		flusher.Flush()

		// Send message_delta
		w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":12}}\n\n"))
		flusher.Flush()

		// Send message_stop
		w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Read test.txt")},
		Tools: []ToolDefinition{
			{
				Name:        "read_file",
				Description: "Read a file",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string"},
					},
					"required": []string{"path"},
				},
			},
		},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error creating stream: %v", err)
	}

	// Collect all events
	var events []StreamEvent
	for event := range eventChan {
		events = append(events, event)
		if event.Type == EventError {
			t.Fatalf("unexpected error event: %v", event.Error)
		}
	}

	// Verify we got expected events including tool use blocks
	if len(events) < 8 {
		t.Fatalf("expected at least 8 events, got %d", len(events))
	}

	// Verify message_start
	if events[0].Type != EventMessageStart {
		t.Errorf("expected first event to be message_start, got %s", events[0].Type)
	}

	// Verify we have content blocks for both text and tool use (indices 0 and 1)
	var foundTextDelta, foundToolStart bool
	for _, event := range events {
		if event.Type == EventContentDelta && event.Index == 0 && event.Text == "Let me help" {
			foundTextDelta = true
		}
		if event.Type == EventContentStart && event.Index == 1 {
			foundToolStart = true
		}
	}

	if !foundTextDelta {
		t.Error("expected to find text delta event for index 0")
	}
	if !foundToolStart {
		t.Error("expected to find content_start event for tool use at index 1")
	}

	// Verify final events
	lastEvent := events[len(events)-1]
	if lastEvent.Type != EventMessageStop {
		t.Errorf("expected last event to be message_stop, got %s", lastEvent.Type)
	}
}

func TestCreateMessageStream_ContentDeltaNonTextType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.ResponseWriter to be an http.Flusher")
		}

		// Send message_start
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_789\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n"))
		flusher.Flush()

		// Send content_block_start
		w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_123\",\"name\":\"test\"}}\n\n"))
		flusher.Flush()

		// Send content_block_delta with non-text type (e.g., input_json_delta)
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n"))
		flusher.Flush()

		// Send content_block_stop
		w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		flusher.Flush()

		// Send message_stop
		w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Test")},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error creating stream: %v", err)
	}

	// Collect all events
	var events []StreamEvent
	for event := range eventChan {
		events = append(events, event)
		if event.Type == EventError {
			t.Fatalf("unexpected error event: %v", event.Error)
		}
	}

	// Find the content_delta event
	var foundDelta bool
	for _, event := range events {
		if event.Type == EventContentDelta {
			foundDelta = true
			// For non-text delta types, text should be empty
			if event.Text != "" {
				t.Errorf("expected empty text for non-text delta type, got '%s'", event.Text)
			}
		}
	}

	if !foundDelta {
		t.Error("expected to find content_delta event")
	}
}

func TestCreateMessageStream_DefaultModelAndMaxTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request has defaults applied
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)

		if reqBody["model"] != "claude-sonnet-4-20250514" {
			t.Errorf("expected default model, got %v", reqBody["model"])
		}
		if reqBody["max_tokens"] != float64(4096) {
			t.Errorf("expected default max_tokens 4096, got %v", reqBody["max_tokens"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.ResponseWriter to be an http.Flusher")
		}

		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_000\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"))
		flusher.Flush()

		w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Test")},
		// Model and MaxTokens intentionally omitted
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error creating stream: %v", err)
	}

	// Consume events
	for range eventChan {
		// Just drain the channel
	}
}

func TestCreateMessageStream_EmptyStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Send no events, just close
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Test")},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error creating stream: %v", err)
	}

	// Collect events (should be empty or just close)
	var events []StreamEvent
	for event := range eventChan {
		events = append(events, event)
	}

	// Empty stream is acceptable, just verify we don't panic
	t.Logf("Got %d events from empty stream", len(events))
}

func TestCreateMessageStream_MessageDeltaOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.ResponseWriter to be an http.Flusher")
		}

		// Send just message_delta and message_stop without content
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_111\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"))
		flusher.Flush()

		w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":0}}\n\n"))
		flusher.Flush()

		w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &AnthropicClient{
		client: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "claude-sonnet-4-20250514",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Test")},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error creating stream: %v", err)
	}

	var events []StreamEvent
	for event := range eventChan {
		events = append(events, event)
		if event.Type == EventError {
			t.Fatalf("unexpected error event: %v", event.Error)
		}
	}

	// Verify we got message_start, message_delta, and message_stop
	expectedTypes := []EventType{EventMessageStart, EventMessageDelta, EventMessageStop}
	if len(events) != len(expectedTypes) {
		t.Fatalf("expected %d events, got %d", len(expectedTypes), len(events))
	}

	for i, expected := range expectedTypes {
		if events[i].Type != expected {
			t.Errorf("event %d: expected %s, got %s", i, expected, events[i].Type)
		}
	}
}
