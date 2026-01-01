// ABOUTME: Tests for the Gemini LLM client implementation.
// ABOUTME: Verifies request/response conversion, message handling, and tool calling.
package llm

import (
	"context"
	"testing"
)

func TestNewGeminiClient(t *testing.T) {
	ctx := context.Background()
	client, err := NewGeminiClient(ctx, "test-api-key", "gemini-2.0-flash")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.model != "gemini-2.0-flash" {
		t.Errorf("expected model gemini-2.0-flash, got %s", client.model)
	}
}

func TestNewGeminiClientDefaultModel(t *testing.T) {
	ctx := context.Background()
	client, err := NewGeminiClient(ctx, "test-api-key", "")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if client.model != "gemini-2.0-flash" {
		t.Errorf("expected default model gemini-2.0-flash, got %s", client.model)
	}
}

func TestConvertGeminiRequest_Basic(t *testing.T) {
	temp := 0.7
	req := &Request{
		Model:       "gemini-2.0-flash",
		MaxTokens:   1024,
		System:      "You are helpful.",
		Temperature: &temp,
		Messages: []Message{
			{Role: RoleUser, Content: "Hello"},
		},
	}

	contents, config := convertGeminiRequest(req)

	if config.MaxOutputTokens != 1024 {
		t.Errorf("expected MaxOutputTokens 1024, got %d", config.MaxOutputTokens)
	}
	if config.Temperature == nil || *config.Temperature != 0.7 {
		t.Errorf("expected Temperature 0.7, got %v", config.Temperature)
	}
	if config.SystemInstruction == nil {
		t.Error("expected SystemInstruction to be set")
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}
	if contents[0].Role != "user" {
		t.Errorf("expected role 'user', got '%s'", contents[0].Role)
	}
}

func TestConvertGeminiRequest_WithTools(t *testing.T) {
	req := &Request{
		Model:    "gemini-2.0-flash",
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

	_, config := convertGeminiRequest(req)

	if len(config.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(config.Tools))
	}
	if len(config.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("expected 1 function declaration, got %d", len(config.Tools[0].FunctionDeclarations))
	}
	funcDecl := config.Tools[0].FunctionDeclarations[0]
	if funcDecl.Name != "get_weather" {
		t.Errorf("expected function name get_weather, got %s", funcDecl.Name)
	}
	if funcDecl.Description != "Get the weather for a location" {
		t.Errorf("expected function description, got %s", funcDecl.Description)
	}
}

func TestConvertGeminiRequest_MultipleTools(t *testing.T) {
	req := &Request{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewUserMessage("Hello")},
		Tools: []ToolDefinition{
			{Name: "tool1", Description: "First tool", InputSchema: map[string]any{"type": "object"}},
			{Name: "tool2", Description: "Second tool", InputSchema: map[string]any{"type": "object"}},
			{Name: "tool3", Description: "Third tool", InputSchema: map[string]any{"type": "object"}},
		},
	}

	_, config := convertGeminiRequest(req)

	if len(config.Tools) != 1 {
		t.Fatalf("expected 1 tool container, got %d", len(config.Tools))
	}
	if len(config.Tools[0].FunctionDeclarations) != 3 {
		t.Fatalf("expected 3 function declarations, got %d", len(config.Tools[0].FunctionDeclarations))
	}
}

func TestConvertMessage_UserText(t *testing.T) {
	msg := Message{Role: RoleUser, Content: "Hello, world!"}
	content := convertMessage(msg)

	if content == nil {
		t.Fatal("expected non-nil content")
	}
	if content.Role != "user" {
		t.Errorf("expected role 'user', got '%s'", content.Role)
	}
	if len(content.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(content.Parts))
	}
	if content.Parts[0].Text != "Hello, world!" {
		t.Errorf("expected text 'Hello, world!', got '%s'", content.Parts[0].Text)
	}
}

func TestConvertMessage_AssistantText(t *testing.T) {
	msg := Message{Role: RoleAssistant, Content: "I can help with that."}
	content := convertMessage(msg)

	if content == nil {
		t.Fatal("expected non-nil content")
	}
	if content.Role != "model" {
		t.Errorf("expected role 'model', got '%s'", content.Role)
	}
}

func TestConvertMessage_TextBlock(t *testing.T) {
	msg := Message{
		Role: RoleUser,
		Blocks: []ContentBlock{
			{Type: ContentTypeText, Text: "Block text content"},
		},
	}
	content := convertMessage(msg)

	if content == nil {
		t.Fatal("expected non-nil content")
	}
	if len(content.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(content.Parts))
	}
	if content.Parts[0].Text != "Block text content" {
		t.Errorf("expected text 'Block text content', got '%s'", content.Parts[0].Text)
	}
}

func TestConvertMessage_ToolUseBlock(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Blocks: []ContentBlock{
			{
				Type:  ContentTypeToolUse,
				ID:    "call_123",
				Name:  "get_weather",
				Input: map[string]any{"location": "New York"},
			},
		},
	}
	content := convertMessage(msg)

	if content == nil {
		t.Fatal("expected non-nil content")
	}
	if content.Role != "model" {
		t.Errorf("expected role 'model', got '%s'", content.Role)
	}
	if len(content.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(content.Parts))
	}
	if content.Parts[0].FunctionCall == nil {
		t.Fatal("expected FunctionCall part")
	}
	if content.Parts[0].FunctionCall.Name != "get_weather" {
		t.Errorf("expected function name 'get_weather', got '%s'", content.Parts[0].FunctionCall.Name)
	}
	if content.Parts[0].FunctionCall.Args["location"] != "New York" {
		t.Errorf("expected location 'New York', got %v", content.Parts[0].FunctionCall.Args["location"])
	}
}

func TestConvertMessage_ToolResultBlock(t *testing.T) {
	msg := Message{
		Role: RoleUser,
		Blocks: []ContentBlock{
			{
				Type:      ContentTypeToolResult,
				ToolUseID: "call_123",
				Name:      "get_weather",
				Text:      "Sunny, 75F",
				IsError:   false,
			},
		},
	}
	content := convertMessage(msg)

	if content == nil {
		t.Fatal("expected non-nil content")
	}
	if content.Role != "user" {
		t.Errorf("expected role 'user', got '%s'", content.Role)
	}
	if len(content.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(content.Parts))
	}
	if content.Parts[0].FunctionResponse == nil {
		t.Fatal("expected FunctionResponse part")
	}
	if content.Parts[0].FunctionResponse.Name != "get_weather" {
		t.Errorf("expected function name 'get_weather', got '%s'", content.Parts[0].FunctionResponse.Name)
	}
	if content.Parts[0].FunctionResponse.Response["output"] != "Sunny, 75F" {
		t.Errorf("expected output 'Sunny, 75F', got %v", content.Parts[0].FunctionResponse.Response["output"])
	}
}

func TestConvertMessage_ToolResultError(t *testing.T) {
	msg := Message{
		Role: RoleUser,
		Blocks: []ContentBlock{
			{
				Type:      ContentTypeToolResult,
				ToolUseID: "call_123",
				Name:      "get_weather",
				Text:      "API error: rate limit exceeded",
				IsError:   true,
			},
		},
	}
	content := convertMessage(msg)

	if content == nil {
		t.Fatal("expected non-nil content")
	}
	if content.Parts[0].FunctionResponse == nil {
		t.Fatal("expected FunctionResponse part")
	}
	if content.Parts[0].FunctionResponse.Response["error"] != "API error: rate limit exceeded" {
		t.Errorf("expected error message, got %v", content.Parts[0].FunctionResponse.Response["error"])
	}
}

func TestConvertMessage_EmptyMessage(t *testing.T) {
	msg := Message{Role: RoleUser}
	content := convertMessage(msg)

	if content != nil {
		t.Error("expected nil content for empty message")
	}
}

func TestConvertMessage_MixedBlocks(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Blocks: []ContentBlock{
			{Type: ContentTypeText, Text: "Let me check the weather."},
			{
				Type:  ContentTypeToolUse,
				ID:    "call_456",
				Name:  "get_weather",
				Input: map[string]any{"location": "Boston"},
			},
		},
	}
	content := convertMessage(msg)

	if content == nil {
		t.Fatal("expected non-nil content")
	}
	if len(content.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(content.Parts))
	}
	if content.Parts[0].Text != "Let me check the weather." {
		t.Errorf("expected text part, got '%s'", content.Parts[0].Text)
	}
	if content.Parts[1].FunctionCall == nil {
		t.Error("expected FunctionCall part")
	}
}

func TestConvertGeminiRequest_ConversationHistory(t *testing.T) {
	req := &Request{
		Model: "gemini-2.0-flash",
		Messages: []Message{
			{Role: RoleUser, Content: "Hello"},
			{Role: RoleAssistant, Content: "Hi there!"},
			{Role: RoleUser, Content: "How are you?"},
		},
	}

	contents, _ := convertGeminiRequest(req)

	if len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(contents))
	}
	if contents[0].Role != "user" {
		t.Errorf("expected first content role 'user', got '%s'", contents[0].Role)
	}
	if contents[1].Role != "model" {
		t.Errorf("expected second content role 'model', got '%s'", contents[1].Role)
	}
	if contents[2].Role != "user" {
		t.Errorf("expected third content role 'user', got '%s'", contents[2].Role)
	}
}

func TestConvertGeminiRequest_NoSystem(t *testing.T) {
	req := &Request{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, config := convertGeminiRequest(req)

	if config.SystemInstruction != nil {
		t.Error("expected nil SystemInstruction when no system prompt provided")
	}
}

func TestConvertGeminiRequest_NoTools(t *testing.T) {
	req := &Request{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, config := convertGeminiRequest(req)

	if len(config.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(config.Tools))
	}
}

func TestConvertGeminiRequest_NoTemperature(t *testing.T) {
	req := &Request{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, config := convertGeminiRequest(req)

	if config.Temperature != nil {
		t.Error("expected nil Temperature when not provided")
	}
}

func TestConvertGeminiRequest_NoMaxTokens(t *testing.T) {
	req := &Request{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, config := convertGeminiRequest(req)

	if config.MaxOutputTokens != 0 {
		t.Errorf("expected 0 MaxOutputTokens when not provided, got %d", config.MaxOutputTokens)
	}
}

func TestConvertMessage_ContentAndBlocks(t *testing.T) {
	// When both Content and Blocks are set, both should be included
	msg := Message{
		Role:    RoleUser,
		Content: "Text from content",
		Blocks: []ContentBlock{
			{Type: ContentTypeText, Text: "Text from block"},
		},
	}
	content := convertMessage(msg)

	if content == nil {
		t.Fatal("expected non-nil content")
	}
	if len(content.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(content.Parts))
	}
}

func TestConvertMessage_ToolWithNilInput(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Blocks: []ContentBlock{
			{
				Type:  ContentTypeToolUse,
				ID:    "call_789",
				Name:  "simple_tool",
				Input: nil,
			},
		},
	}
	content := convertMessage(msg)

	if content == nil {
		t.Fatal("expected non-nil content")
	}
	if len(content.Parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(content.Parts))
	}
	if content.Parts[0].FunctionCall == nil {
		t.Fatal("expected FunctionCall part")
	}
	// Should handle nil input gracefully
}

func TestGeminiClient_InterfaceCompliance(t *testing.T) {
	// Compile-time check that GeminiClient implements Client
	var _ Client = (*GeminiClient)(nil)
}

// Compile-time interface check
var _ Client = (*GeminiClient)(nil)
