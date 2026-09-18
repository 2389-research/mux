// ABOUTME: Tests for the Gemini LLM client implementation.
// ABOUTME: Verifies request/response conversion, message handling, and tool calling.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	contents, config := sigGeminiContents(t, req)

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

	_, config := sigGeminiContents(t, req)

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

	_, config := sigGeminiContents(t, req)

	if len(config.Tools) != 1 {
		t.Fatalf("expected 1 tool container, got %d", len(config.Tools))
	}
	if len(config.Tools[0].FunctionDeclarations) != 3 {
		t.Fatalf("expected 3 function declarations, got %d", len(config.Tools[0].FunctionDeclarations))
	}
}

func TestConvertMessage_UserText(t *testing.T) {
	msg := Message{Role: RoleUser, Content: "Hello, world!"}
	content := sigGeminiContent(t, msg)

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
	content := sigGeminiContent(t, msg)

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
	content := sigGeminiContent(t, msg)

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
	content := sigGeminiContent(t, msg)

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
	content := sigGeminiContent(t, msg)

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
	content := sigGeminiContent(t, msg)

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
	content := sigGeminiContent(t, msg)

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
	content := sigGeminiContent(t, msg)

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

	contents, _ := sigGeminiContents(t, req)

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

	_, config := sigGeminiContents(t, req)

	if config.SystemInstruction != nil {
		t.Error("expected nil SystemInstruction when no system prompt provided")
	}
}

func TestConvertGeminiRequest_NoTools(t *testing.T) {
	req := &Request{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, config := sigGeminiContents(t, req)

	if len(config.Tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(config.Tools))
	}
}

func TestConvertGeminiRequest_NoTemperature(t *testing.T) {
	req := &Request{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, config := sigGeminiContents(t, req)

	if config.Temperature != nil {
		t.Error("expected nil Temperature when not provided")
	}
}

func TestConvertGeminiRequest_NoMaxTokens(t *testing.T) {
	req := &Request{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewUserMessage("Hello")},
	}

	_, config := sigGeminiContents(t, req)

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
	content := sigGeminiContent(t, msg)

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
	content := sigGeminiContent(t, msg)

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
	_, config := sigGeminiContents(t, req)
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
	_, config := sigGeminiContents(t, req)
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
	result := sigGeminiResult(t, resp, "gemini-2.5-pro")
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
	result := sigGeminiResult(t, resp, "gemini-2.5-pro")
	if result.Usage.ThinkingTokens != 200 {
		t.Errorf("expected ThinkingTokens 200, got %d", result.Usage.ThinkingTokens)
	}
}

func TestConvertGeminiResponse_EmptyCandidatesStopReason(t *testing.T) {
	resp := &genai.GenerateContentResponse{}

	result := sigGeminiResult(t, resp, "gemini-2.5-pro")
	if result.StopReason != StopReasonOther {
		t.Errorf("expected stop reason other for empty candidates, got %q", result.StopReason)
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
	contents, _ := sigGeminiContents(t, &Request{
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
	contents, _ := sigGeminiContents(t, &Request{
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
	contents, _ := sigGeminiContents(t, &Request{
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
	contents, _ := sigGeminiContents(t, &Request{
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
	contents, _ := sigGeminiContents(t, &Request{
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
	contents, _ := sigGeminiContents(t, &Request{
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
	contents, _ := sigGeminiContents(t, &Request{
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
	contents, _ := sigGeminiContents(t, &Request{
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

// TestConvertGeminiResponse_BlockedPrompt covers mux#a2j0: a prompt Gemini
// blocks before generation carries no candidates, only PromptFeedback. That
// must surface as a typed, errors.As-detectable failure instead of the empty
// successful Response the zero-candidate branch produces for an ordinary
// empty turn.
func TestConvertGeminiResponse_BlockedPrompt(t *testing.T) {
	resp := sigGeminiResponse(t, `{"promptFeedback":{"blockReason":"SAFETY"}}`)
	result, err := convertGeminiResponse(resp, "gemini-2.5-pro")
	if result != nil {
		t.Fatalf("expected no Response for a blocked prompt, got %+v", result)
	}
	var pe *ErrProviderResponse
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ErrProviderResponse, got %T: %v", err, err)
	}
	if pe.Provider != "gemini" {
		t.Errorf("expected provider gemini, got %q", pe.Provider)
	}
	if pe.Reason != "blocked: SAFETY" {
		t.Errorf("expected reason %q, got %q", "blocked: SAFETY", pe.Reason)
	}
}

// TestConvertGeminiResponse_BlockReasonUnspecifiedNotBlocked guards the
// boundary the acceptance text draws: an explicit but meaningless
// BLOCKED_REASON_UNSPECIFIED must not be treated as a block, or every
// prompt carrying an empty-but-present PromptFeedback object would start
// erroring. This is a genuinely distinct fixture from
// TestConvertGeminiResponse_EmptyCandidatesStopReason (no PromptFeedback at
// all): both must reach StopReasonOther, not an error.
func TestConvertGeminiResponse_BlockReasonUnspecifiedNotBlocked(t *testing.T) {
	resp := sigGeminiResponse(t, `{"promptFeedback":{"blockReason":"BLOCKED_REASON_UNSPECIFIED"}}`)
	result, err := convertGeminiResponse(resp, "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected error for an unspecified block reason: %v", err)
	}
	if result.StopReason != StopReasonOther {
		t.Errorf("expected stop reason other, got %q", result.StopReason)
	}
}

// TestConvertGeminiResponse_CandidateSafetyFinishIsNotBlocked confirms the
// new prompt-level block check does not overreach into per-candidate content
// filtering: a candidate whose own FinishReason is SAFETY (no PromptFeedback
// at all) stays a successful Response carrying StopReasonContentFilter, per
// the status policy documented on Response — content filtering is never an
// error.
func TestConvertGeminiResponse_CandidateSafetyFinishIsNotBlocked(t *testing.T) {
	resp := sigGeminiResponse(t, `{"candidates":[{"finishReason":"SAFETY","content":{"role":"model","parts":[]}}]}`)
	result, err := convertGeminiResponse(resp, "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("candidate-level content filtering must not be an error: %v", err)
	}
	if result.StopReason != StopReasonContentFilter {
		t.Errorf("expected stop reason content_filter, got %q", result.StopReason)
	}
}

// TestConvertGeminiResponse_MaxTokensPartialText closes the acceptance gap
// noted for mux#a2j0: MAX_TOKENS truncation already preserves partial text
// today, since content extraction in convertGeminiResponse is never gated on
// finish reason. This pins that behavior with a regression test rather than
// a code change.
func TestConvertGeminiResponse_MaxTokensPartialText(t *testing.T) {
	resp := sigGeminiResponse(t, `{"candidates":[{"finishReason":"MAX_TOKENS","content":{"role":"model","parts":[{"text":"partial answer"}]}}]}`)
	result, err := convertGeminiResponse(resp, "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("unexpected error for max_tokens truncation: %v", err)
	}
	if result.StopReason != StopReasonMaxTokens {
		t.Errorf("expected stop reason max_tokens, got %q", result.StopReason)
	}
	if result.TextContent() != "partial answer" {
		t.Errorf("expected partial text preserved, got %q", result.TextContent())
	}
}

// TestGeminiCreateMessageStream_BlockedPrompt covers the streaming half of
// mux#a2j0: a blocked prompt must end the stream with a typed EventError
// instead of an EventMessageStop carrying an empty successful Response.
func TestGeminiCreateMessageStream_BlockedPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`data: {"promptFeedback":{"blockReason":"SAFETY"}}` + "\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client, err := NewGeminiClientWithBaseURL(context.Background(), "test-key", "gemini-2.5-pro", server.URL)
	if err != nil {
		t.Fatalf("NewGeminiClientWithBaseURL: %v", err)
	}

	events, err := client.CreateMessageStream(context.Background(), &Request{
		Messages: []Message{NewUserMessage("blocked prompt")},
	})
	if err != nil {
		t.Fatalf("CreateMessageStream: %v", err)
	}

	var gotError error
	var gotStop bool
	for event := range events {
		switch event.Type {
		case EventError:
			gotError = event.Error
		case EventMessageStop:
			gotStop = true
		}
	}
	if gotError == nil {
		t.Fatal("expected an EventError for a blocked prompt, got none")
	}
	var pe *ErrProviderResponse
	if !errors.As(gotError, &pe) {
		t.Fatalf("expected *ErrProviderResponse, got %T: %v", gotError, gotError)
	}
	if pe.Provider != "gemini" || pe.Reason != "blocked: SAFETY" {
		t.Errorf("err fields: %+v", pe)
	}
	if gotStop {
		t.Error("a blocked prompt must not also emit a successful EventMessageStop")
	}
}

// TestGeminiCreateMessageStream_TrailerChunkNotBlocked guards against the
// false-positive mux#a2j0's streaming fix could introduce: a trailer/usage
// chunk with zero candidates is normal at the end of an ordinary stream and
// carries no PromptFeedback at all. Checking every chunk for a block reason
// (rather than only the last-seen chunk) must not misfire on it.
func TestGeminiCreateMessageStream_TrailerChunkNotBlocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`data: {"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"hi"}]}}]}` + "\n\n"))
		flusher.Flush()
		// A trailer chunk carrying only usage metadata: zero candidates, no
		// PromptFeedback, exactly what a normal stream's final chunk looks
		// like.
		_, _ = w.Write([]byte(`data: {"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}` + "\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client, err := NewGeminiClientWithBaseURL(context.Background(), "test-key", "gemini-2.5-pro", server.URL)
	if err != nil {
		t.Fatalf("NewGeminiClientWithBaseURL: %v", err)
	}

	events, err := client.CreateMessageStream(context.Background(), &Request{
		Messages: []Message{NewUserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("CreateMessageStream: %v", err)
	}

	var gotStop bool
	for event := range events {
		switch event.Type {
		case EventError:
			t.Fatalf("unexpected error event on an ordinary trailer chunk: %v", event.Error)
		case EventMessageStop:
			gotStop = true
		}
	}
	if !gotStop {
		t.Error("expected a successful EventMessageStop, got none")
	}
}

// TestGeminiCreateMessageStream_BlockedThenTrailerChunkStillErrors guards the
// per-chunk PromptFeedback check itself, not just its outcome: per
// geminiBlockedPromptError's doc comment, PromptFeedback is carried on
// whichever chunk reports the block, not necessarily the last one seen. This
// sends the block on the first chunk, then a second chunk shaped exactly
// like TestGeminiCreateMessageStream_TrailerChunkNotBlocked's trailer — zero
// candidates, no PromptFeedback — which becomes lastResp. The final
// convertGeminiResponse(lastResp, model) fallback run after the loop sees no
// PromptFeedback on that trailer and would report success on its own, so
// this only passes when the in-loop check catches the block before the
// trailer arrives.
func TestGeminiCreateMessageStream_BlockedThenTrailerChunkStillErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`data: {"promptFeedback":{"blockReason":"SAFETY"}}` + "\n\n"))
		flusher.Flush()
		// A trailer chunk carrying only usage metadata: zero candidates, no
		// PromptFeedback, becoming lastResp — the same shape
		// TestGeminiCreateMessageStream_TrailerChunkNotBlocked uses to prove
		// trailers are a normal occurrence.
		_, _ = w.Write([]byte(`data: {"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":0}}` + "\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client, err := NewGeminiClientWithBaseURL(context.Background(), "test-key", "gemini-2.5-pro", server.URL)
	if err != nil {
		t.Fatalf("NewGeminiClientWithBaseURL: %v", err)
	}

	events, err := client.CreateMessageStream(context.Background(), &Request{
		Messages: []Message{NewUserMessage("blocked prompt")},
	})
	if err != nil {
		t.Fatalf("CreateMessageStream: %v", err)
	}

	var gotError error
	var gotStop bool
	for event := range events {
		switch event.Type {
		case EventError:
			gotError = event.Error
		case EventMessageStop:
			gotStop = true
		}
	}
	if gotError == nil {
		t.Fatal("expected an EventError for a prompt blocked on a non-final chunk, got none")
	}
	var pe *ErrProviderResponse
	if !errors.As(gotError, &pe) {
		t.Fatalf("expected *ErrProviderResponse, got %T: %v", gotError, gotError)
	}
	if pe.Provider != "gemini" || pe.Reason != "blocked: SAFETY" {
		t.Errorf("err fields: %+v", pe)
	}
	if gotStop {
		t.Error("a blocked prompt must not also emit a successful EventMessageStop")
	}
}

// geminiSSEServer serves each payload as a data: SSE event in order.
func geminiSSEServer(t *testing.T, payloads ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, p := range payloads {
			fmt.Fprintf(w, "data: %s\n\n", p)
		}
	}))
}

func collectGeminiStream(t *testing.T, c *GeminiClient, req *Request) (delta string, final *Response) {
	t.Helper()
	ch, err := c.CreateMessageStream(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateMessageStream: %v", err)
	}
	for e := range ch {
		if e.Error != nil {
			t.Fatalf("stream error: %v", e.Error)
		}
		delta += e.Text
		if e.Type == EventMessageStop {
			final = e.Response
		}
	}
	return delta, final
}

// TestAuditGeminiStreamAccumulates reproduces mux#ac2b: multi-chunk text plus a
// usage/finish trailer must yield a final response whose text is every delta.
func TestAuditGeminiStreamAccumulates(t *testing.T) {
	s := geminiSSEServer(t,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello "}]}}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"world"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2}}`,
	)
	defer s.Close()
	c, err := NewGeminiClientWithBaseURL(context.Background(), "key", "gemini-test", s.URL)
	if err != nil {
		t.Fatal(err)
	}
	delta, final := collectGeminiStream(t, c, &Request{Messages: []Message{NewUserMessage("hi")}})
	if final == nil {
		t.Fatal("no EventMessageStop response")
	}
	if final.TextContent() != "Hello world" {
		t.Errorf("final text %q, want %q (deltas %q)", final.TextContent(), "Hello world", delta)
	}
	if final.Usage.InputTokens != 5 || final.Usage.OutputTokens != 2 {
		t.Errorf("usage = %+v, want 5/2", final.Usage)
	}
	if final.StopReason != StopReasonEndTurn {
		t.Errorf("stop reason %q, want end_turn", final.StopReason)
	}
}

func TestGeminiStreamAccumulates(t *testing.T) {
	toolCall := `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"location":"Boston"}}}]}}]}`
	t.Run("tool call survives usage-only trailer", func(t *testing.T) {
		s := geminiSSEServer(t,
			toolCall,
			`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3}}`,
		)
		defer s.Close()
		c, err := NewGeminiClientWithBaseURL(context.Background(), "key", "gemini-test", s.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, final := collectGeminiStream(t, c, &Request{Messages: []Message{NewUserMessage("hi")}})
		if final == nil {
			t.Fatal("no EventMessageStop response")
		}
		uses := final.ToolUses()
		if len(uses) != 1 {
			t.Fatalf("final tool uses = %d, want 1; content=%+v", len(uses), final.Content)
		}
		if uses[0].Name != "get_weather" || uses[0].Input["location"] != "Boston" {
			t.Errorf("tool use = %+v", uses[0])
		}
		if final.StopReason != StopReasonToolUse {
			t.Errorf("stop reason %q, want tool_use", final.StopReason)
		}
		if final.Usage.InputTokens != 7 || final.Usage.OutputTokens != 3 {
			t.Errorf("usage = %+v, want 7/3", final.Usage)
		}
		if final.TextContent() != "" {
			t.Errorf("text %q, want empty", final.TextContent())
		}
	})
	t.Run("thinking separated from text", func(t *testing.T) {
		s := geminiSSEServer(t,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"reasoning","thought":true}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":6}}`,
		)
		defer s.Close()
		c, err := NewGeminiClientWithBaseURL(context.Background(), "key", "gemini-test", s.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, final := collectGeminiStream(t, c, &Request{Messages: []Message{NewUserMessage("hi")}})
		if final == nil {
			t.Fatal("no EventMessageStop response")
		}
		var think, text string
		for _, b := range final.Content {
			switch b.Type {
			case ContentTypeThinking:
				think += b.Thinking
			case ContentTypeText:
				text += b.Text
			}
		}
		if think != "reasoning" {
			t.Errorf("thinking = %q, want %q", think, "reasoning")
		}
		if text != "answer" {
			t.Errorf("text = %q, want %q", text, "answer")
		}
	})
	t.Run("multi-part chunks accumulate in order", func(t *testing.T) {
		s := geminiSSEServer(t,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"one"},{"functionCall":{"name":"t1","args":{"a":1}}}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"two"},{"text":"three"}]},"finishReason":"STOP"}]}`,
		)
		defer s.Close()
		c, err := NewGeminiClientWithBaseURL(context.Background(), "key", "gemini-test", s.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, final := collectGeminiStream(t, c, &Request{Messages: []Message{NewUserMessage("hi")}})
		if final == nil {
			t.Fatal("no EventMessageStop response")
		}
		types := make([]ContentType, 0, len(final.Content))
		for _, b := range final.Content {
			types = append(types, b.Type)
		}
		want := []ContentType{ContentTypeText, ContentTypeToolUse, ContentTypeText, ContentTypeText}
		if len(types) != len(want) {
			t.Fatalf("block types %v, want %v", types, want)
		}
		for i := range want {
			if types[i] != want[i] {
				t.Fatalf("block types %v, want %v", types, want)
			}
		}
		if final.TextContent() != "onetwothree" {
			t.Errorf("text = %q, want %q", final.TextContent(), "onetwothree")
		}
	})
}
