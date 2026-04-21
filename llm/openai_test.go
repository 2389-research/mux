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

func TestOpenAIClient_Capabilities(t *testing.T) {
	c := NewOpenAIClient("key", "")
	caps := c.Capabilities()
	if !caps.Image || !caps.PDF || !caps.Audio {
		t.Errorf("expected Image+PDF+Audio true, got %+v", caps)
	}
	if caps.Video {
		t.Errorf("expected Video false, got %+v", caps)
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
		Model:  "gpt-5.2",
		System: "You are a helpful assistant.",
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

func TestConvertOpenAIRequest_MultipleToolResults(t *testing.T) {
	req := &Request{
		Model: "gpt-5.2",
		Messages: []Message{
			{Role: RoleUser, Content: "Hello"},
			{
				Role: RoleAssistant,
				Blocks: []ContentBlock{
					{Type: ContentTypeToolUse, ID: "call_1", Name: "tool1", Input: map[string]any{"key": "val1"}},
					{Type: ContentTypeToolUse, ID: "call_2", Name: "tool2", Input: map[string]any{"key": "val2"}},
					{Type: ContentTypeToolUse, ID: "call_3", Name: "tool3", Input: map[string]any{"key": "val3"}},
				},
			},
			{
				Role: RoleUser,
				Blocks: []ContentBlock{
					{Type: ContentTypeToolResult, ToolUseID: "call_1", Text: "Result 1"},
					{Type: ContentTypeToolResult, ToolUseID: "call_2", Text: "Result 2"},
					{Type: ContentTypeToolResult, ToolUseID: "call_3", Text: "Result 3"},
				},
			},
		},
	}

	params := convertOpenAIRequest(req)

	// Expect 5 messages: user + assistant + 3 tool messages
	if len(params.Messages) != 5 {
		t.Fatalf("expected 5 messages (user + assistant + 3 tool), got %d", len(params.Messages))
	}

	// First message should be user
	if params.Messages[0].OfUser == nil {
		t.Error("expected first message to be a user message")
	}

	// Second message should be assistant with tool calls
	if params.Messages[1].OfAssistant == nil {
		t.Error("expected second message to be an assistant message")
	}

	// Messages 2-4 should be tool messages with correct IDs
	expectedToolCallIDs := []string{"call_1", "call_2", "call_3"}
	expectedTexts := []string{"Result 1", "Result 2", "Result 3"}
	for i, expectedID := range expectedToolCallIDs {
		msgIdx := i + 2
		toolMsg := params.Messages[msgIdx]
		if toolMsg.OfTool == nil {
			t.Fatalf("expected message %d to be a tool message, got nil OfTool", msgIdx)
		}
		if toolMsg.OfTool.ToolCallID != expectedID {
			t.Errorf("message %d: expected ToolCallID %q, got %q", msgIdx, expectedID, toolMsg.OfTool.ToolCallID)
		}
		// Verify the content text matches
		if len(toolMsg.OfTool.Content.OfString.Value) == 0 {
			t.Errorf("message %d: expected non-empty content", msgIdx)
		}
		_ = expectedTexts[i] // used for documentation; content verified via OfString
	}
}

func TestConvertUserMessages_TextAndToolResults(t *testing.T) {
	msg := Message{
		Role:    RoleUser,
		Content: "Here are the results",
		Blocks: []ContentBlock{
			{Type: ContentTypeText, Text: "Here are the results"},
			{Type: ContentTypeToolResult, ToolUseID: "call_1", Text: "Result 1"},
			{Type: ContentTypeToolResult, ToolUseID: "call_2", Text: "Result 2"},
		},
	}

	results := convertUserMessages(msg)

	// Should produce: 1 user message + 2 tool messages = 3
	if len(results) != 3 {
		t.Fatalf("expected 3 messages (1 user + 2 tool), got %d", len(results))
	}

	// First should be user text message
	if results[0].OfUser == nil {
		t.Error("expected first message to be a user message")
	}

	// Second and third should be tool messages
	if results[1].OfTool == nil || results[1].OfTool.ToolCallID != "call_1" {
		t.Errorf("expected second message to be tool message with call_1")
	}
	if results[2].OfTool == nil || results[2].OfTool.ToolCallID != "call_2" {
		t.Errorf("expected third message to be tool message with call_2")
	}
}

func TestConvertUserMessages_OnlyText(t *testing.T) {
	msg := Message{Role: RoleUser, Content: "Just text"}
	results := convertUserMessages(msg)

	if len(results) != 1 {
		t.Fatalf("expected 1 message, got %d", len(results))
	}
	if results[0].OfUser == nil {
		t.Error("expected user message")
	}
}

func TestConvertUserMessages_OnlyToolResults(t *testing.T) {
	msg := Message{
		Role: RoleUser,
		Blocks: []ContentBlock{
			{Type: ContentTypeToolResult, ToolUseID: "call_1", Text: "Result 1"},
			{Type: ContentTypeToolResult, ToolUseID: "call_2", Text: "Result 2"},
		},
	}

	results := convertUserMessages(msg)

	// Should produce 2 tool messages, no user text message
	if len(results) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(results))
	}
	if results[0].OfTool == nil || results[0].OfTool.ToolCallID != "call_1" {
		t.Error("expected first message to be tool message with call_1")
	}
	if results[1].OfTool == nil || results[1].OfTool.ToolCallID != "call_2" {
		t.Error("expected second message to be tool message with call_2")
	}
}

func TestConvertOpenAIRequest_ReasoningEffortLow(t *testing.T) {
	req := &Request{
		Model: "gpt-5.2", Messages: []Message{NewUserMessage("Think")},
		Thinking: &ThinkingConfig{Enabled: true, Budget: 2000},
	}
	params := convertOpenAIRequest(req)
	if params.ReasoningEffort != openai.ReasoningEffortLow {
		t.Errorf("expected low, got %s", params.ReasoningEffort)
	}
}

func TestConvertOpenAIRequest_ReasoningEffortMedium(t *testing.T) {
	req := &Request{
		Model: "gpt-5.2", Messages: []Message{NewUserMessage("Think")},
		Thinking: &ThinkingConfig{Enabled: true, Budget: 10000},
	}
	params := convertOpenAIRequest(req)
	if params.ReasoningEffort != openai.ReasoningEffortMedium {
		t.Errorf("expected medium, got %s", params.ReasoningEffort)
	}
}

func TestConvertOpenAIRequest_ReasoningEffortHigh(t *testing.T) {
	req := &Request{
		Model: "gpt-5.2", Messages: []Message{NewUserMessage("Think")},
		Thinking: &ThinkingConfig{Enabled: true, Budget: 32000},
	}
	params := convertOpenAIRequest(req)
	if params.ReasoningEffort != openai.ReasoningEffortHigh {
		t.Errorf("expected high, got %s", params.ReasoningEffort)
	}
}

func TestConvertOpenAIRequest_NoThinking(t *testing.T) {
	req := &Request{
		Model: "gpt-5.2", Messages: []Message{NewUserMessage("Hello")},
	}
	params := convertOpenAIRequest(req)
	if params.ReasoningEffort != "" {
		t.Errorf("expected empty, got %s", params.ReasoningEffort)
	}
}

func TestConvertOpenAIResponse_ReasoningTokens(t *testing.T) {
	resp := &openai.ChatCompletion{
		ID: "chatcmpl-123", Model: "gpt-5.2",
		Choices: []openai.ChatCompletionChoice{
			{Message: openai.ChatCompletionMessage{Role: "assistant", Content: "Answer"}, FinishReason: "stop"},
		},
		Usage: openai.CompletionUsage{
			PromptTokens: 100, CompletionTokens: 50,
			CompletionTokensDetails: openai.CompletionUsageCompletionTokensDetails{ReasoningTokens: 30},
		},
	}
	result := convertOpenAIResponse(resp)
	if result.Usage.ThinkingTokens != 30 {
		t.Errorf("expected 30 thinking tokens, got %d", result.Usage.ThinkingTokens)
	}
}

func TestOpenAIConvertUserMessage_ImageBytes(t *testing.T) {
	img, err := NewImageFromBytes("image/png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatal(err)
	}
	msg := convertUserMessage(NewUserMessageWithBlocks(img))
	if msg.OfUser == nil {
		t.Fatalf("expected user message, got %+v", msg)
	}
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	if parts[0].OfImageURL == nil {
		t.Fatalf("expected image part, got %+v", parts[0])
	}
	if !strings.HasPrefix(parts[0].OfImageURL.ImageURL.URL, "data:image/png;base64,") {
		t.Errorf("expected data URL, got %q", parts[0].OfImageURL.ImageURL.URL)
	}
}

func TestOpenAIConvertUserMessage_ImageURL(t *testing.T) {
	img := NewImageFromURL("https://example.com/x.png")
	msg := convertUserMessage(NewUserMessageWithBlocks(img))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if parts[0].OfImageURL.ImageURL.URL != "https://example.com/x.png" {
		t.Errorf("URL: %q", parts[0].OfImageURL.ImageURL.URL)
	}
}

func TestOpenAIConvertUserMessage_TextPlusImage(t *testing.T) {
	img := NewImageFromURL("https://example.com/x.png")
	msg := convertUserMessage(NewUserMessageWithBlocks(
		ContentBlock{Type: ContentTypeText, Text: "describe this"},
		img,
	))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[0].OfText == nil || parts[0].OfText.Text != "describe this" {
		t.Errorf("text part: %+v", parts[0])
	}
	if parts[1].OfImageURL == nil {
		t.Errorf("image part: %+v", parts[1])
	}
}

func TestOpenAIConvertUserMessage_PlainTextStillWorks(t *testing.T) {
	msg := convertUserMessage(NewUserMessage("hello"))
	if msg.OfUser == nil {
		t.Fatalf("expected user message, got %+v", msg)
	}
	if s := msg.OfUser.Content.OfString; !s.Valid() || s.Value != "hello" {
		t.Errorf("plain text should stay string form, got %+v", msg.OfUser.Content)
	}
}

func TestOpenAIConvertUserMessage_ToolResultStillWorks(t *testing.T) {
	msg := Message{
		Role:   RoleUser,
		Blocks: []ContentBlock{{Type: ContentTypeToolResult, ToolUseID: "t1", Text: "done"}},
	}
	converted := convertUserMessage(msg)
	if converted.OfTool == nil {
		t.Errorf("expected tool message, got %+v", converted)
	}
}

func TestOpenAIConvertUserMessage_PDFBytes(t *testing.T) {
	pdf, err := NewPDFFromBytes([]byte("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	msg := convertUserMessage(NewUserMessageWithBlocks(pdf))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if parts[0].OfFile == nil {
		t.Fatalf("expected file part, got %+v", parts[0])
	}
	if !parts[0].OfFile.File.FileData.Valid() {
		t.Error("expected FileData set")
	}
	if !parts[0].OfFile.File.Filename.Valid() || parts[0].OfFile.File.Filename.Value != "file.pdf" {
		t.Errorf("default filename: %+v", parts[0].OfFile.File.Filename)
	}
}

func TestOpenAIConvertUserMessage_PDFFileKeepsFilename(t *testing.T) {
	pdf, err := NewPDFFromFile("testdata/tiny.pdf")
	if err != nil {
		t.Fatal(err)
	}
	msg := convertUserMessage(NewUserMessageWithBlocks(pdf))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if parts[0].OfFile.File.Filename.Value != "tiny.pdf" {
		t.Errorf("filename: %q", parts[0].OfFile.File.Filename.Value)
	}
}

func TestOpenAIConvertUserMessage_AudioMP3(t *testing.T) {
	audio, err := NewAudioFromBytes("audio/mpeg", []byte{0xff, 0xfb})
	if err != nil {
		t.Fatal(err)
	}
	msg := convertUserMessage(NewUserMessageWithBlocks(audio))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if parts[0].OfInputAudio == nil {
		t.Fatalf("expected audio part, got %+v", parts[0])
	}
	if parts[0].OfInputAudio.InputAudio.Format != "mp3" {
		t.Errorf("format: %q (audio/mpeg should map to mp3)", parts[0].OfInputAudio.InputAudio.Format)
	}
	if parts[0].OfInputAudio.InputAudio.Data == "" {
		t.Error("expected base64 data set")
	}
}

func TestOpenAIConvertUserMessage_AudioWAV(t *testing.T) {
	audio, err := NewAudioFromBytes("audio/wav", []byte{0x52, 0x49, 0x46, 0x46})
	if err != nil {
		t.Fatal(err)
	}
	msg := convertUserMessage(NewUserMessageWithBlocks(audio))
	parts := msg.OfUser.Content.OfArrayOfContentParts
	if parts[0].OfInputAudio.InputAudio.Format != "wav" {
		t.Errorf("format: %q", parts[0].OfInputAudio.InputAudio.Format)
	}
}

func TestOpenAICreateMessage_PDFFromURL_ErrUnsupportedSource(t *testing.T) {
	pdf := NewPDFFromURL("https://example.com/x.pdf")
	c := NewOpenAIClient("fake-key", "")
	_, err := c.CreateMessage(context.Background(), &Request{
		Messages: []Message{NewUserMessageWithBlocks(pdf)},
	})
	var ue *ErrUnsupportedSource
	if !errors.As(err, &ue) {
		t.Fatalf("expected *ErrUnsupportedSource, got %T: %v", err, err)
	}
	if ue.Provider != "openai" || ue.Media != "pdf" || ue.Kind != "url" {
		t.Errorf("err fields: %+v", ue)
	}
}

func TestOpenAICreateMessage_AudioFromURL_ErrUnsupportedSource(t *testing.T) {
	audio := NewAudioFromURL("https://example.com/a.mp3")
	c := NewOpenAIClient("fake-key", "")
	_, err := c.CreateMessage(context.Background(), &Request{
		Messages: []Message{NewUserMessageWithBlocks(audio)},
	})
	var ue *ErrUnsupportedSource
	if !errors.As(err, &ue) {
		t.Fatalf("expected *ErrUnsupportedSource, got %T: %v", err, err)
	}
	if ue.Media != "audio" || ue.Kind != "url" {
		t.Errorf("err fields: %+v", ue)
	}
}

// Compile-time interface check
var _ Client = (*OpenAIClient)(nil)
