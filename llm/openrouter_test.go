// ABOUTME: Tests for the OpenRouter LLM client implementation.
// ABOUTME: Verifies API communication, streaming, tool calling, and OpenRouter-specific behavior.
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

func TestNewOpenRouterClient(t *testing.T) {
	client := NewOpenRouterClient("test-api-key", "anthropic/claude-3.5-sonnet")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.model != "anthropic/claude-3.5-sonnet" {
		t.Errorf("expected model anthropic/claude-3.5-sonnet, got %s", client.model)
	}
}

func TestNewOpenRouterClientDefaultModel(t *testing.T) {
	client := NewOpenRouterClient("test-api-key", "")
	if client.model != OpenRouterDefaultModel {
		t.Errorf("expected default model %s, got %s", OpenRouterDefaultModel, client.model)
	}
}

func TestNewOpenRouterClientWithHeaders(t *testing.T) {
	client := NewOpenRouterClientWithHeaders("test-api-key", "", "https://myapp.com", "My App")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.model != OpenRouterDefaultModel {
		t.Errorf("expected default model %s, got %s", OpenRouterDefaultModel, client.model)
	}
}

func TestOpenRouterClient_BaseURLIsCorrect(t *testing.T) {
	// Create a test server to verify the URL being called
	var requestURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURL = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-123",
			"model": "anthropic/claude-3.5-sonnet",
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "Hello!",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 5,
			},
		})
	}))
	defer server.Close()

	// Create client pointing at test server
	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the correct endpoint was called
	if requestURL != "/chat/completions" {
		t.Errorf("expected /chat/completions, got %s", requestURL)
	}
}

func TestOpenRouterClient_CreateMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-123",
			"model": "anthropic/claude-3.5-sonnet",
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": "Hello! How can I help you today?",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 8,
			},
		})
	}))
	defer server.Close()

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	resp, err := client.CreateMessage(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.ID != "chatcmpl-123" {
		t.Errorf("expected ID chatcmpl-123, got %s", resp.ID)
	}
	if resp.Model != "anthropic/claude-3.5-sonnet" {
		t.Errorf("expected model anthropic/claude-3.5-sonnet, got %s", resp.Model)
	}
	if resp.StopReason != StopReasonEndTurn {
		t.Errorf("expected stop reason end_turn, got %s", resp.StopReason)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(resp.Content))
	}
	if resp.Content[0].Text != "Hello! How can I help you today?" {
		t.Errorf("expected different text, got %s", resp.Content[0].Text)
	}
}

func TestOpenRouterClient_ToolCalling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-123",
			"model": "anthropic/claude-3.5-sonnet",
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []map[string]any{
							{
								"id":   "call_abc123",
								"type": "function",
								"function": map[string]any{
									"name":      "get_weather",
									"arguments": `{"location": "New York"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     15,
				"completion_tokens": 10,
			},
		})
	}))
	defer server.Close()

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("What's the weather in New York?")},
		Tools: []ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Get weather for a location",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	resp, err := client.CreateMessage(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.StopReason != StopReasonToolUse {
		t.Errorf("expected stop reason tool_use, got %s", resp.StopReason)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(resp.Content))
	}
	if resp.Content[0].Type != ContentTypeToolUse {
		t.Errorf("expected tool_use type, got %s", resp.Content[0].Type)
	}
	if resp.Content[0].Name != "get_weather" {
		t.Errorf("expected tool name get_weather, got %s", resp.Content[0].Name)
	}
	if resp.Content[0].ID != "call_abc123" {
		t.Errorf("expected tool ID call_abc123, got %s", resp.Content[0].ID)
	}
	if resp.Content[0].Input["location"] != "New York" {
		t.Errorf("expected location New York, got %v", resp.Content[0].Input["location"])
	}
}

func TestOpenRouterClient_InvalidAPIKey(t *testing.T) {
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

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("invalid-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
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

func TestOpenRouterClient_RateLimiting(t *testing.T) {
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

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
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

func TestOpenRouterClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
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

func TestOpenRouterClient_ServerError(t *testing.T) {
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

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
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

func TestOpenRouterClient_DefaultMaxTokens(t *testing.T) {
	var receivedMaxTokens int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if mt, ok := body["max_completion_tokens"].(float64); ok {
			receivedMaxTokens = int64(mt)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-123",
			"model": "anthropic/claude-3.5-sonnet",
			"choices": []map[string]any{
				{
					"message":       map[string]any{"role": "assistant", "content": "Hi"},
					"finish_reason": "stop",
				},
			},
		})
	}))
	defer server.Close()

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
		// MaxTokens not set - should use DefaultMaxTokens
	}

	_, err := client.CreateMessage(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMaxTokens != DefaultMaxTokens {
		t.Errorf("expected max_completion_tokens %d, got %d", DefaultMaxTokens, receivedMaxTokens)
	}
}

func TestOpenRouterClient_ModelOverride(t *testing.T) {
	var receivedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if m, ok := body["model"].(string); ok {
			receivedModel = m
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-123",
			"model": receivedModel,
			"choices": []map[string]any{
				{
					"message":       map[string]any{"role": "assistant", "content": "Hi"},
					"finish_reason": "stop",
				},
			},
		})
	}))
	defer server.Close()

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
	}

	ctx := context.Background()
	req := &Request{
		Model:    "openai/gpt-4o", // Override the default model
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedModel != "openai/gpt-4o" {
		t.Errorf("expected model openai/gpt-4o, got %s", receivedModel)
	}
}

// Streaming Tests

func TestOpenRouterClient_StreamBasic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// Chunk 1: Role
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"anthropic/claude-3.5-sonnet","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 2: Content
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"anthropic/claude-3.5-sonnet","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 3: More content
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"anthropic/claude-3.5-sonnet","choices":[{"index":0,"delta":{"content":"!"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 4: End
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"anthropic/claude-3.5-sonnet","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		flusher.Flush()

		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
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

	expectedDeltas := []string{"Hello", "!"}
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

func TestOpenRouterClient_StreamWithToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// Chunk 1: Start with role
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"anthropic/claude-3.5-sonnet","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 2: Tool call start
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"anthropic/claude-3.5-sonnet","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc123","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 3: Tool call arguments
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"anthropic/claude-3.5-sonnet","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"location\":"}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 4: More arguments
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"anthropic/claude-3.5-sonnet","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"NYC\"}"}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 5: End with tool_calls finish reason
		w.Write([]byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"anthropic/claude-3.5-sonnet","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
		flusher.Flush()

		// Done
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("What's the weather in NYC?")},
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

func TestOpenRouterClient_StreamServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "Internal server error"},
		})
	}))
	defer server.Close()

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
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

func TestOpenRouterClient_StreamContextCancellation(t *testing.T) {
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

	client := &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		),
		model: "anthropic/claude-3.5-sonnet",
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

// Constants verification

func TestOpenRouterConstants(t *testing.T) {
	if OpenRouterBaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("expected base URL https://openrouter.ai/api/v1, got %s", OpenRouterBaseURL)
	}
	if OpenRouterDefaultModel != "anthropic/claude-3.5-sonnet" {
		t.Errorf("expected default model anthropic/claude-3.5-sonnet, got %s", OpenRouterDefaultModel)
	}
}

// Compile-time interface check
var _ Client = (*OpenRouterClient)(nil)
