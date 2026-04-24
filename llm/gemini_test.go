// ABOUTME: Tests for the Gemini LLM client implementation.
// ABOUTME: Verifies request/response conversion, message handling, and tool calling.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tidwall/gjson"
	"google.golang.org/genai"
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

func TestConvertGeminiRequest_WithThinking(t *testing.T) {
	req := &Request{
		Model: "gemini-2.5-pro", Messages: []Message{NewUserMessage("Think hard")},
		Thinking: &ThinkingConfig{Enabled: true, Budget: 8000},
	}
	_, config := convertGeminiRequest(req)
	if config.ThinkingConfig == nil {
		t.Fatal("expected ThinkingConfig to be set")
	}
	if config.ThinkingConfig.ThinkingBudget == nil || *config.ThinkingConfig.ThinkingBudget != 8000 {
		t.Errorf("expected ThinkingBudget 8000, got %v", config.ThinkingConfig.ThinkingBudget)
	}
	if !config.ThinkingConfig.IncludeThoughts {
		t.Error("expected IncludeThoughts to be true")
	}
}

func TestConvertGeminiRequest_WithoutThinking(t *testing.T) {
	req := &Request{
		Model: "gemini-2.0-flash", Messages: []Message{NewUserMessage("Hello")},
	}
	_, config := convertGeminiRequest(req)
	if config.ThinkingConfig != nil {
		t.Error("expected ThinkingConfig to be nil")
	}
}

func TestConvertGeminiResponse_ThinkingParts(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Role: "model",
					Parts: []*genai.Part{
						{Text: "Let me think...", Thought: true},
						{Text: "Here is the answer."},
					},
				},
				FinishReason: genai.FinishReasonStop,
			},
		},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount: 10, CandidatesTokenCount: 50,
		},
	}
	result := convertGeminiResponse(resp, "gemini-2.5-pro")
	if len(result.Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(result.Content))
	}
	if result.Content[0].Type != ContentTypeThinking {
		t.Errorf("expected thinking, got %s", result.Content[0].Type)
	}
	if result.Content[0].Thinking != "Let me think..." {
		t.Errorf("wrong thinking text: %s", result.Content[0].Thinking)
	}
	if result.Content[1].Type != ContentTypeText {
		t.Errorf("expected text, got %s", result.Content[1].Type)
	}
}

func TestConvertGeminiResponse_ThinkingTokens(t *testing.T) {
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "answer"}}},
				FinishReason: genai.FinishReasonStop,
			},
		},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount: 10, CandidatesTokenCount: 50, ThoughtsTokenCount: 200,
		},
	}
	result := convertGeminiResponse(resp, "gemini-2.5-pro")
	if result.Usage.ThinkingTokens != 200 {
		t.Errorf("expected ThinkingTokens 200, got %d", result.Usage.ThinkingTokens)
	}
}

func TestGeminiCapabilities(t *testing.T) {
	c, err := NewGeminiClient(context.Background(), "fake-key", "")
	if err != nil {
		t.Fatal(err)
	}
	caps := c.Capabilities()
	if !caps.Image || !caps.PDF || !caps.Audio || !caps.Video {
		t.Errorf("expected all true, got %+v", caps)
	}
}

func TestGeminiConvertRequest_ImageBytes(t *testing.T) {
	img, err := NewImageFromBytes("image/png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(img)},
	})
	if len(contents) != 1 || len(contents[0].Parts) != 1 {
		t.Fatalf("expected 1 content with 1 part, got %+v", contents)
	}
	part := contents[0].Parts[0]
	if part.InlineData == nil {
		t.Fatalf("expected InlineData, got %+v", part)
	}
	if part.InlineData.MIMEType != "image/png" {
		t.Errorf("MIMEType: %q", part.InlineData.MIMEType)
	}
	if len(part.InlineData.Data) == 0 {
		t.Error("Data should be non-empty")
	}
}

func TestGeminiConvertRequest_PDFBytes(t *testing.T) {
	pdf, err := NewPDFFromBytes([]byte("%PDF-1.4"))
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(pdf)},
	})
	part := contents[0].Parts[0]
	if part.InlineData == nil || part.InlineData.MIMEType != "application/pdf" {
		t.Errorf("expected application/pdf inline data, got %+v", part)
	}
}

func TestGeminiConvertRequest_AudioBytes(t *testing.T) {
	audio, err := NewAudioFromBytes("audio/mpeg", []byte{0xff, 0xfb})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(audio)},
	})
	part := contents[0].Parts[0]
	if part.InlineData == nil || part.InlineData.MIMEType != "audio/mpeg" {
		t.Errorf("expected audio/mpeg inline data, got %+v", part)
	}
}

func TestGeminiConvertRequest_VideoBytes(t *testing.T) {
	video, err := NewVideoFromBytes("video/mp4", []byte{0x00, 0x00, 0x00, 0x18})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(video)},
	})
	part := contents[0].Parts[0]
	if part.InlineData == nil || part.InlineData.MIMEType != "video/mp4" {
		t.Errorf("expected video/mp4 inline data, got %+v", part)
	}
}

func TestGeminiCreateMessage_ImageURL_ErrUnsupportedSource(t *testing.T) {
	ctx := context.Background()
	c, err := NewGeminiClient(ctx, "fake-key", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateMessage(ctx, &Request{
		Messages: []Message{NewUserMessageWithBlocks(NewImageFromURL("https://example.com/x.png"))},
	})
	var ue *ErrUnsupportedSource
	if !errors.As(err, &ue) {
		t.Fatalf("expected *ErrUnsupportedSource, got %T: %v", err, err)
	}
	if ue.Provider != "gemini" || ue.Media != "image" || ue.Kind != "url" {
		t.Errorf("err fields: %+v", ue)
	}
}

func TestGeminiCreateMessage_VideoURL_ErrUnsupportedSource(t *testing.T) {
	ctx := context.Background()
	c, err := NewGeminiClient(ctx, "fake-key", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateMessage(ctx, &Request{
		Messages: []Message{NewUserMessageWithBlocks(NewVideoFromURL("https://example.com/v.mp4"))},
	})
	var ue *ErrUnsupportedSource
	if !errors.As(err, &ue) {
		t.Fatalf("expected *ErrUnsupportedSource, got %T: %v", err, err)
	}
	if ue.Provider != "gemini" || ue.Media != "video" || ue.Kind != "url" {
		t.Errorf("err fields: %+v", ue)
	}
}

// Compile-time interface check
var _ Client = (*GeminiClient)(nil)

func TestGeminiWireFormat_ImageBytes(t *testing.T) {
	img, err := NewImageFromBytes("image/png", []byte{0x89, 'P', 'N', 'G'})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(img)},
	})
	body, err := json.Marshal(contents[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if v := gjson.GetBytes(body, "parts.0.inlineData.mimeType").String(); v != "image/png" {
		t.Errorf("mimeType: got %q want image/png; body=%s", v, body)
	}
	if v := gjson.GetBytes(body, "parts.0.inlineData.data").String(); v == "" {
		t.Errorf("data should be non-empty base64; body=%s", body)
	}
}

func TestGeminiWireFormat_VideoBytes(t *testing.T) {
	video, err := NewVideoFromBytes("video/mp4", []byte{0x00, 0x00, 0x00, 0x18})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(video)},
	})
	body, err := json.Marshal(contents[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if v := gjson.GetBytes(body, "parts.0.inlineData.mimeType").String(); v != "video/mp4" {
		t.Errorf("mimeType: got %q; body=%s", v, body)
	}
	if v := gjson.GetBytes(body, "parts.0.inlineData.data").String(); v == "" {
		t.Errorf("data should be non-empty base64; body=%s", body)
	}
}

func TestGeminiWireFormat_PDFBytes(t *testing.T) {
	pdf, err := NewPDFFromBytes([]byte("%PDF-1.4"))
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(pdf)},
	})
	body, err := json.Marshal(contents[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if v := gjson.GetBytes(body, "parts.0.inlineData.mimeType").String(); v != "application/pdf" {
		t.Errorf("mimeType: %q; body=%s", v, body)
	}
	if v := gjson.GetBytes(body, "parts.0.inlineData.data").String(); v == "" {
		t.Errorf("data should be non-empty base64; body=%s", body)
	}
}

func TestGeminiWireFormat_AudioBytes(t *testing.T) {
	audio, err := NewAudioFromBytes("audio/mpeg", []byte{0xff, 0xfb})
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := convertGeminiRequest(&Request{
		Messages: []Message{NewUserMessageWithBlocks(audio)},
	})
	body, err := json.Marshal(contents[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if v := gjson.GetBytes(body, "parts.0.inlineData.mimeType").String(); v != "audio/mpeg" {
		t.Errorf("mimeType: %q; body=%s", v, body)
	}
	if v := gjson.GetBytes(body, "parts.0.inlineData.data").String(); v == "" {
		t.Errorf("data should be non-empty base64; body=%s", body)
	}
}
