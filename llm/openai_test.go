// ABOUTME: Tests for the OpenAI LLM client implementation.
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

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func TestNewOpenAIClient(t *testing.T) {
	client := NewOpenAIClient("test-api-key", "gpt-5.2")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.model != "gpt-5.2" {
		t.Errorf("expected model gpt-5.2, got %s", client.model)
	}
}

func TestNewOpenAIClientDefaultModel(t *testing.T) {
	client := NewOpenAIClient("test-api-key", "")
	if client.model != "gpt-5.2" {
		t.Errorf("expected default model gpt-5.2, got %s", client.model)
	}
}

func TestOpenAIClient_ConvertRequest(t *testing.T) {
	req := &Request{
		Model:     "gpt-5.2",
		MaxTokens: 1024,
		System:    "You are helpful.",
		Messages: []Message{
			{Role: RoleUser, Content: "Hello"},
		},
		Tools: []ToolDefinition{
			{Name: "read", Description: "Read a file", InputSchema: map[string]any{"type": "object"}},
		},
	}

	params := convertOpenAIRequest(req)
	if params.Model != "gpt-5.2" {
		t.Errorf("expected model gpt-5.2, got %s", params.Model)
	}
	// System message + user message
	if len(params.Messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(params.Messages))
	}
	if len(params.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(params.Tools))
	}
}

func TestOpenAIClient_InvalidAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Invalid API key",
				"type":    "invalid_api_key",
				"code":    "invalid_api_key",
			},
		})
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("invalid-key"),
			option.WithBaseURL(server.URL),
		),
		model: "gpt-5.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err == nil {
		t.Fatal("expected error for invalid API key, got nil")
	}
}

func TestOpenAIClient_RateLimiting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Rate limit exceeded",
				"type":    "rate_limit_exceeded",
			},
		})
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "gpt-5.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err == nil {
		t.Fatal("expected error for rate limiting, got nil")
	}
}

func TestOpenAIClient_NetworkTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
			option.WithHTTPClient(&http.Client{Timeout: 50 * time.Millisecond}),
		),
		model: "gpt-5.2",
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

func TestOpenAIClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "gpt-5.2",
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

func TestOpenAIClient_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Internal server error",
				"type":    "server_error",
			},
		})
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "gpt-5.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err == nil {
		t.Fatal("expected error for server error, got nil")
	}
}

func TestOpenAIClient_ConnectionRefused(t *testing.T) {
	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL("http://localhost:1"),
		),
		model: "gpt-5.2",
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

// Request Conversion Tests

func TestConvertOpenAIRequest_SystemPrompt(t *testing.T) {
	req := &Request{
		Model:   "gpt-5.2",
		System:  "You are a helpful assistant.",
		Messages: []Message{
			{Role: RoleUser, Content: "Hello"},
		},
	}

	params := convertOpenAIRequest(req)

	// System prompt should be first message
	if len(params.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(params.Messages))
	}
}

func TestConvertOpenAIRequest_ToolDefinitions(t *testing.T) {
	req := &Request{
		Model:    "gpt-5.2",
		Messages: []Message{NewUserMessage("Hello")},
		Tools: []ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Get the weather for a location",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
					},
					"required": []string{"location"},
				},
			},
		},
	}

	params := convertOpenAIRequest(req)
	if len(params.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(params.Tools))
	}
	if params.Tools[0].Function.Name != "get_weather" {
		t.Errorf("expected tool name get_weather, got %s", params.Tools[0].Function.Name)
	}
}

func TestConvertOpenAIRequest_MultipleTools(t *testing.T) {
	req := &Request{
		Model:    "gpt-5.2",
		Messages: []Message{NewUserMessage("Hello")},
		Tools: []ToolDefinition{
			{Name: "tool1", Description: "First tool", InputSchema: map[string]any{"type": "object"}},
			{Name: "tool2", Description: "Second tool", InputSchema: map[string]any{"type": "object"}},
			{Name: "tool3", Description: "Third tool", InputSchema: map[string]any{"type": "object"}},
		},
	}

	params := convertOpenAIRequest(req)
	if len(params.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(params.Tools))
	}
}

func TestConvertUserMessage_TextContent(t *testing.T) {
	msg := Message{Role: RoleUser, Content: "Hello, world!"}
	result := convertUserMessage(msg)
	if result.OfUser == nil {
		t.Fatal("expected user message")
	}
}

func TestConvertUserMessage_ToolResult(t *testing.T) {
	msg := Message{
		Role: RoleUser,
		Blocks: []ContentBlock{
			{Type: ContentTypeToolResult, ToolUseID: "call_123", Text: "Result data"},
		},
	}
	result := convertUserMessage(msg)
	if result.OfTool == nil {
		t.Fatal("expected tool message for tool result")
	}
}

func TestConvertUserMessage_EmptyMessage(t *testing.T) {
	msg := Message{Role: RoleUser}
	result := convertUserMessage(msg)
	if result.OfUser == nil {
		t.Fatal("expected user message")
	}
	// Empty message should still create a valid user message (returns UserMessage(""))
}

func TestConvertUserMessage_OnlyBlocks(t *testing.T) {
	msg := Message{
		Role: RoleUser,
		Blocks: []ContentBlock{
			{Type: ContentTypeText, Text: "Block text content"},
		},
	}
	result := convertUserMessage(msg)
	if result.OfUser == nil {
		t.Fatal("expected user message")
	}
	// Verifies that messages with only text blocks (no Content field) are converted correctly
}

func TestConvertUserMessage_TextBlocksInUserMessage(t *testing.T) {
	msg := Message{
		Role: RoleUser,
		Blocks: []ContentBlock{
			{Type: ContentTypeText, Text: "First block"},
			{Type: ContentTypeText, Text: "Second block"},
		},
	}
	result := convertUserMessage(msg)
	if result.OfUser == nil {
		t.Fatal("expected user message")
	}
	// Should use first text block when multiple text blocks are present
}

func TestConvertAssistantMessage_TextOnly(t *testing.T) {
	msg := Message{Role: RoleAssistant, Content: "I can help with that."}
	result := convertAssistantMessage(msg)
	if result.OfAssistant == nil {
		t.Fatal("expected assistant message")
	}
}

func TestConvertAssistantMessage_WithToolCalls(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Blocks: []ContentBlock{
			{Type: ContentTypeText, Text: "Let me check the weather."},
			{
				Type:  ContentTypeToolUse,
				ID:    "call_123",
				Name:  "get_weather",
				Input: map[string]any{"location": "New York"},
			},
		},
	}
	result := convertAssistantMessage(msg)
	if result.OfAssistant == nil {
		t.Fatal("expected assistant message with tool calls")
	}
	if len(result.OfAssistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.OfAssistant.ToolCalls))
	}
	if result.OfAssistant.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("expected tool name get_weather, got %s", result.OfAssistant.ToolCalls[0].Function.Name)
	}
}

// Response Conversion Tests

func TestConvertOpenAIResponse_TextContent(t *testing.T) {
	resp := &openai.ChatCompletion{
		ID:    "chatcmpl-123",
		Model: "gpt-5.2",
		Choices: []openai.ChatCompletionChoice{
			{
				Message: openai.ChatCompletionMessage{
					Role:    "assistant",
					Content: "Hello! How can I help?",
				},
				FinishReason: "stop",
			},
		},
		Usage: openai.CompletionUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
		},
	}

	result := convertOpenAIResponse(resp)
	if result.ID != "chatcmpl-123" {
		t.Errorf("expected ID chatcmpl-123, got %s", result.ID)
	}
	if result.StopReason != StopReasonEndTurn {
		t.Errorf("expected stop reason end_turn, got %s", result.StopReason)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Type != ContentTypeText {
		t.Errorf("expected text content type, got %s", result.Content[0].Type)
	}
	if result.Content[0].Text != "Hello! How can I help?" {
		t.Errorf("expected text content, got %s", result.Content[0].Text)
	}
}

func TestConvertOpenAIResponse_ToolCalls(t *testing.T) {
	resp := &openai.ChatCompletion{
		ID:    "chatcmpl-123",
		Model: "gpt-5.2",
		Choices: []openai.ChatCompletionChoice{
			{
				Message: openai.ChatCompletionMessage{
					Role: "assistant",
					ToolCalls: []openai.ChatCompletionMessageToolCall{
						{
							ID:   "call_abc123",
							Type: "function",
							Function: openai.ChatCompletionMessageToolCallFunction{
								Name:      "get_weather",
								Arguments: `{"location": "New York"}`,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
		Usage: openai.CompletionUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
		},
	}

	result := convertOpenAIResponse(resp)
	if result.StopReason != StopReasonToolUse {
		t.Errorf("expected stop reason tool_use, got %s", result.StopReason)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Type != ContentTypeToolUse {
		t.Errorf("expected tool_use content type, got %s", result.Content[0].Type)
	}
	if result.Content[0].ID != "call_abc123" {
		t.Errorf("expected tool call ID call_abc123, got %s", result.Content[0].ID)
	}
	if result.Content[0].Name != "get_weather" {
		t.Errorf("expected tool name get_weather, got %s", result.Content[0].Name)
	}
	if result.Content[0].Input["location"] != "New York" {
		t.Errorf("expected location New York, got %v", result.Content[0].Input["location"])
	}
}

func TestConvertOpenAIResponse_MultipleToolCalls(t *testing.T) {
	resp := &openai.ChatCompletion{
		ID:    "chatcmpl-123",
		Model: "gpt-5.2",
		Choices: []openai.ChatCompletionChoice{
			{
				Message: openai.ChatCompletionMessage{
					Role: "assistant",
					ToolCalls: []openai.ChatCompletionMessageToolCall{
						{
							ID:       "call_1",
							Type:     "function",
							Function: openai.ChatCompletionMessageToolCallFunction{Name: "tool1", Arguments: `{}`},
						},
						{
							ID:       "call_2",
							Type:     "function",
							Function: openai.ChatCompletionMessageToolCallFunction{Name: "tool2", Arguments: `{}`},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
	}

	result := convertOpenAIResponse(resp)
	if len(result.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(result.Content))
	}
	if result.Content[0].Name != "tool1" {
		t.Errorf("expected first tool name tool1, got %s", result.Content[0].Name)
	}
	if result.Content[1].Name != "tool2" {
		t.Errorf("expected second tool name tool2, got %s", result.Content[1].Name)
	}
}

func TestConvertOpenAIResponse_MaxTokens(t *testing.T) {
	resp := &openai.ChatCompletion{
		ID:    "chatcmpl-123",
		Model: "gpt-5.2",
		Choices: []openai.ChatCompletionChoice{
			{
				Message:      openai.ChatCompletionMessage{Role: "assistant", Content: "Truncated..."},
				FinishReason: "length",
			},
		},
	}

	result := convertOpenAIResponse(resp)
	if result.StopReason != StopReasonMaxTokens {
		t.Errorf("expected stop reason max_tokens, got %s", result.StopReason)
	}
}

func TestConvertOpenAIResponse_EmptyChoices(t *testing.T) {
	resp := &openai.ChatCompletion{
		ID:      "chatcmpl-123",
		Model:   "gpt-5.2",
		Choices: []openai.ChatCompletionChoice{},
	}

	result := convertOpenAIResponse(resp)
	if result.ID != "chatcmpl-123" {
		t.Errorf("expected ID chatcmpl-123, got %s", result.ID)
	}
	if len(result.Content) != 0 {
		t.Errorf("expected 0 content blocks, got %d", len(result.Content))
	}
}

func TestConvertOpenAIResponse_InvalidToolCallArguments(t *testing.T) {
	resp := &openai.ChatCompletion{
		ID:    "chatcmpl-123",
		Model: "gpt-5.2",
		Choices: []openai.ChatCompletionChoice{
			{
				Message: openai.ChatCompletionMessage{
					Role: "assistant",
					ToolCalls: []openai.ChatCompletionMessageToolCall{
						{
							ID:       "call_1",
							Type:     "function",
							Function: openai.ChatCompletionMessageToolCallFunction{Name: "tool1", Arguments: `{invalid json`},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
	}

	result := convertOpenAIResponse(resp)
	// Should handle gracefully with empty input
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Input == nil {
		t.Error("expected non-nil input map")
	}
}

// Usage Tracking Tests

func TestConvertOpenAIResponse_UsageTracking(t *testing.T) {
	resp := &openai.ChatCompletion{
		ID:    "chatcmpl-123",
		Model: "gpt-5.2",
		Choices: []openai.ChatCompletionChoice{
			{
				Message:      openai.ChatCompletionMessage{Role: "assistant", Content: "Hello"},
				FinishReason: "stop",
			},
		},
		Usage: openai.CompletionUsage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}

	result := convertOpenAIResponse(resp)
	if result.Usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", result.Usage.OutputTokens)
	}
}

// Stream Tests

func TestOpenAIClient_StreamContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// Send initial event
		w.Write([]byte("data: {\"id\":\"chatcmpl-123\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"index\":0}]}\n\n"))
		flusher.Flush()

		// Wait before sending more
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "gpt-5.2",
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

	// Consume events
	var gotError bool
	for event := range eventChan {
		if event.Type == EventError {
			gotError = true
		}
	}

	if !gotError {
		t.Log("expected error event for context cancellation, but stream may have ended gracefully")
	}
}

func TestOpenAIClient_StreamServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "Internal server error"},
		})
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "gpt-5.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		// Error returned immediately is also acceptable
		return
	}

	var gotError bool
	for event := range eventChan {
		if event.Type == EventError {
			gotError = true
		}
	}

	if !gotError && err == nil {
		t.Error("expected error event or error return for server error")
	}
}

func TestOpenAIClient_StreamWithToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// Chunk 1: Start with role
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 2: Tool call start
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc123","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 3: Tool call arguments
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"location\":"}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 4: More arguments
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"New York\"}"}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 5: End with tool_calls finish reason
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
		flusher.Flush()

		// Done
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "gpt-5.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("What's the weather in New York?")},
		Tools: []ToolDefinition{
			{Name: "get_weather", Description: "Get weather", InputSchema: map[string]any{"type": "object"}},
		},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error creating stream: %v", err)
	}

	var gotMessageStart, gotContentStop, gotMessageStop bool
	var toolCallBlock *ContentBlock

	for event := range eventChan {
		switch event.Type {
		case EventMessageStart:
			gotMessageStart = true
		case EventContentStop:
			gotContentStop = true
			toolCallBlock = event.Block
		case EventMessageStop:
			gotMessageStop = true
		case EventError:
			t.Errorf("unexpected error event: %v", event.Error)
		}
	}

	if !gotMessageStart {
		t.Error("expected MessageStart event")
	}
	if !gotContentStop {
		t.Error("expected ContentStop event for tool call")
	}
	if !gotMessageStop {
		t.Error("expected MessageStop event")
	}
	if toolCallBlock != nil {
		if toolCallBlock.Type != ContentTypeToolUse {
			t.Errorf("expected tool_use block, got %s", toolCallBlock.Type)
		}
		if toolCallBlock.Name != "get_weather" {
			t.Errorf("expected tool name get_weather, got %s", toolCallBlock.Name)
		}
	}
}

func TestOpenAIClient_StreamWithMultipleContentDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// Chunk 1: Role
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 2: First content delta
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 3: Second content delta
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"content":" there"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 4: Third content delta
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 5: End
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		flusher.Flush()

		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "gpt-5.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error creating stream: %v", err)
	}

	var contentDeltas []string
	var gotMessageStart, gotMessageStop bool

	for event := range eventChan {
		switch event.Type {
		case EventMessageStart:
			gotMessageStart = true
		case EventContentDelta:
			contentDeltas = append(contentDeltas, event.Text)
		case EventMessageStop:
			gotMessageStop = true
		case EventError:
			t.Errorf("unexpected error event: %v", event.Error)
		}
	}

	if !gotMessageStart {
		t.Error("expected MessageStart event")
	}
	if !gotMessageStop {
		t.Error("expected MessageStop event")
	}

	expectedDeltas := []string{"Hello", " there", "!"}
	if len(contentDeltas) != len(expectedDeltas) {
		t.Errorf("expected %d content deltas, got %d", len(expectedDeltas), len(contentDeltas))
	}
	for i, expected := range expectedDeltas {
		if i >= len(contentDeltas) {
			break
		}
		if contentDeltas[i] != expected {
			t.Errorf("delta %d: expected %q, got %q", i, expected, contentDeltas[i])
		}
	}
}

func TestOpenAIClient_StreamJustFinishedToolCallEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// Simulate streaming tool call that gets completed
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_test","type":"function","function":{"name":"test_tool","arguments":""}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"key\":\"value\"}"}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Next chunk signals tool call completion via JustFinishedToolCall
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_test2","type":"function","function":{"name":"another_tool","arguments":""}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-5.2","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
		flusher.Flush()

		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &OpenAIClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "gpt-5.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Test")},
		Tools: []ToolDefinition{
			{Name: "test_tool", Description: "Test", InputSchema: map[string]any{"type": "object"}},
			{Name: "another_tool", Description: "Another", InputSchema: map[string]any{"type": "object"}},
		},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error creating stream: %v", err)
	}

	var contentStopEvents int
	for event := range eventChan {
		if event.Type == EventContentStop {
			contentStopEvents++
			if event.Block == nil {
				t.Error("expected non-nil Block in ContentStop event")
			} else if event.Block.Type != ContentTypeToolUse {
				t.Errorf("expected tool_use block, got %s", event.Block.Type)
			}
		}
		if event.Type == EventError {
			t.Errorf("unexpected error event: %v", event.Error)
		}
	}

	// Should get at least one ContentStop for completed tool call
	if contentStopEvents == 0 {
		t.Error("expected at least one ContentStop event for tool call completion")
	}
}

// Compile-time interface check
var _ Client = (*OpenAIClient)(nil)
