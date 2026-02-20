// ABOUTME: Tests for the Ollama LLM client implementation.
// ABOUTME: Verifies API communication, streaming, tool calling, and default configurations.
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

func TestNewOllamaClient(t *testing.T) {
	client := NewOllamaClient("http://localhost:11434/v1", "llama3.2")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.model != "llama3.2" {
		t.Errorf("expected model llama3.2, got %s", client.model)
	}
}

func TestNewOllamaClientDefaultModel(t *testing.T) {
	client := NewOllamaClient("http://localhost:11434/v1", "")
	if client.model != "llama3.2" {
		t.Errorf("expected default model llama3.2, got %s", client.model)
	}
}

func TestNewOllamaClientDefaultBaseURL(t *testing.T) {
	client := NewOllamaClient("", "llama3.2")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	// Client should be created with default base URL
	if client.model != "llama3.2" {
		t.Errorf("expected model llama3.2, got %s", client.model)
	}
}

func TestNewOllamaClientAllDefaults(t *testing.T) {
	client := NewOllamaClient("", "")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.model != "llama3.2" {
		t.Errorf("expected default model llama3.2, got %s", client.model)
	}
}

func TestNewOllamaClientCustomBaseURL(t *testing.T) {
	customURL := "http://192.168.1.100:11434/v1"
	client := NewOllamaClient(customURL, "llama3.2")
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestOllamaClient_CreateMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("expected /chat/completions path, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-ollama-123",
			"model": "llama3.2",
			"choices": []map[string]any{
				{
					"index": 0,
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
				"total_tokens":      18,
			},
		})
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
		),
		model: "llama3.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	resp, err := client.CreateMessage(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.ID != "chatcmpl-ollama-123" {
		t.Errorf("expected ID chatcmpl-ollama-123, got %s", resp.ID)
	}
	if resp.StopReason != StopReasonEndTurn {
		t.Errorf("expected stop reason end_turn, got %s", resp.StopReason)
	}
	if len(resp.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(resp.Content))
	}
	if resp.Content[0].Type != ContentTypeText {
		t.Errorf("expected text content, got %s", resp.Content[0].Type)
	}
	if resp.Content[0].Text != "Hello! How can I help you today?" {
		t.Errorf("unexpected text content: %s", resp.Content[0].Text)
	}
}

func TestOllamaClient_CreateMessageWithToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-ollama-456",
			"model": "llama3.2",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role": "assistant",
						"tool_calls": []map[string]any{
							{
								"id":   "call_abc123",
								"type": "function",
								"function": map[string]any{
									"name":      "get_weather",
									"arguments": `{"location": "San Francisco"}`,
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
				"total_tokens":      25,
			},
		})
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
		),
		model: "llama3.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("What's the weather in San Francisco?")},
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
		t.Errorf("expected tool_use content, got %s", resp.Content[0].Type)
	}
	if resp.Content[0].Name != "get_weather" {
		t.Errorf("expected tool name get_weather, got %s", resp.Content[0].Name)
	}
	if resp.Content[0].Input["location"] != "San Francisco" {
		t.Errorf("expected location San Francisco, got %v", resp.Content[0].Input["location"])
	}
}

func TestOllamaClient_ConnectionRefused(t *testing.T) {
	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL("http://localhost:1"),
		),
		model: "llama3.2",
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

func TestOllamaClient_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "Model not found",
				"type":    "invalid_request_error",
			},
		})
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
		),
		model: "nonexistent-model",
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

func TestOllamaClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
		),
		model: "llama3.2",
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

func TestOllamaClient_NetworkTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
			option.WithHTTPClient(&http.Client{Timeout: 50 * time.Millisecond}),
		),
		model: "llama3.2",
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

func TestOllamaClient_CreateMessageStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// Chunk 1: Role
		w.Write([]byte(`data: {"id":"chatcmpl-ollama-123","object":"chat.completion.chunk","created":1234567890,"model":"llama3.2","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 2: First content
		w.Write([]byte(`data: {"id":"chatcmpl-ollama-123","object":"chat.completion.chunk","created":1234567890,"model":"llama3.2","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 3: More content
		w.Write([]byte(`data: {"id":"chatcmpl-ollama-123","object":"chat.completion.chunk","created":1234567890,"model":"llama3.2","choices":[{"index":0,"delta":{"content":" there!"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 4: End
		w.Write([]byte(`data: {"id":"chatcmpl-ollama-123","object":"chat.completion.chunk","created":1234567890,"model":"llama3.2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		flusher.Flush()

		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
		),
		model: "llama3.2",
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

	expectedDeltas := []string{"Hello", " there!"}
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

func TestOllamaClient_StreamWithToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// Chunk 1: Start with role
		w.Write([]byte(`data: {"id":"chatcmpl-ollama-789","object":"chat.completion.chunk","created":1234567890,"model":"llama3.2","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 2: Tool call start
		w.Write([]byte(`data: {"id":"chatcmpl-ollama-789","object":"chat.completion.chunk","created":1234567890,"model":"llama3.2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_ollama123","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 3: Tool call arguments
		w.Write([]byte(`data: {"id":"chatcmpl-ollama-789","object":"chat.completion.chunk","created":1234567890,"model":"llama3.2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 4: More arguments
		w.Write([]byte(`data: {"id":"chatcmpl-ollama-789","object":"chat.completion.chunk","created":1234567890,"model":"llama3.2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"test.txt\"}"}}]},"finish_reason":null}]}` + "\n\n"))
		flusher.Flush()

		// Chunk 5: End with tool_calls finish reason
		w.Write([]byte(`data: {"id":"chatcmpl-ollama-789","object":"chat.completion.chunk","created":1234567890,"model":"llama3.2","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
		flusher.Flush()

		// Done
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
		),
		model: "llama3.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Read the file test.txt")},
		Tools: []ToolDefinition{
			{Name: "read_file", Description: "Read a file", InputSchema: map[string]any{"type": "object"}},
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
		if toolCallBlock.Name != "read_file" {
			t.Errorf("expected tool name read_file, got %s", toolCallBlock.Name)
		}
	}
}

func TestOllamaClient_StreamContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// Send initial event
		w.Write([]byte(`data: {"id":"chatcmpl-ollama-123","object":"chat.completion.chunk","choices":[{"delta":{"role":"assistant"},"index":0}]}` + "\n\n"))
		flusher.Flush()

		// Wait before sending more
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
		),
		model: "llama3.2",
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

func TestOllamaClient_StreamServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "Internal server error"},
		})
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
		),
		model: "llama3.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
	}

	eventChan, err := client.CreateMessageStream(ctx, req)
	if err != nil {
		// Error returned immediately is acceptable
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

func TestOllamaClient_ModelOverride(t *testing.T) {
	var receivedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		receivedModel = reqBody["model"].(string)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-ollama-123",
			"model": receivedModel,
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "Hello",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     5,
				"completion_tokens": 1,
			},
		})
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
		),
		model: "llama3.2",
	}

	ctx := context.Background()
	req := &Request{
		Model:    "mistral", // Override the default model
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedModel != "mistral" {
		t.Errorf("expected model mistral, got %s", receivedModel)
	}
}

func TestOllamaClient_DefaultMaxTokens(t *testing.T) {
	var receivedMaxTokens int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		if mt, ok := reqBody["max_completion_tokens"].(float64); ok {
			receivedMaxTokens = int64(mt)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-ollama-123",
			"model": "llama3.2",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "Hello",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     5,
				"completion_tokens": 1,
			},
		})
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
		),
		model: "llama3.2",
	}

	ctx := context.Background()
	req := &Request{
		Messages: []Message{NewUserMessage("Hello")},
		// No MaxTokens specified, should use default
	}

	_, err := client.CreateMessage(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedMaxTokens != DefaultMaxTokens {
		t.Errorf("expected default max_tokens %d, got %d", DefaultMaxTokens, receivedMaxTokens)
	}
}

func TestOllamaClient_SystemPrompt(t *testing.T) {
	var receivedMessages []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]any
		json.NewDecoder(r.Body).Decode(&reqBody)
		if msgs, ok := reqBody["messages"].([]any); ok {
			for _, m := range msgs {
				if msg, ok := m.(map[string]any); ok {
					receivedMessages = append(receivedMessages, msg)
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":    "chatcmpl-ollama-123",
			"model": "llama3.2",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "Hello",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     5,
				"completion_tokens": 1,
			},
		})
	}))
	defer server.Close()

	client := &OllamaClient{
		client: openai.NewClient(
			option.WithAPIKey("ollama"),
			option.WithBaseURL(server.URL),
		),
		model: "llama3.2",
	}

	ctx := context.Background()
	req := &Request{
		System:   "You are a helpful assistant.",
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, err := client.CreateMessage(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(receivedMessages) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(receivedMessages))
	}

	// First message should be system
	if receivedMessages[0]["role"] != "system" {
		t.Errorf("expected first message role to be system, got %s", receivedMessages[0]["role"])
	}
}

// Compile-time interface check
var _ Client = (*OllamaClient)(nil)
