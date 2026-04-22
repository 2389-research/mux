// ABOUTME: OpenAI API client implementing the llm.Client interface.
// ABOUTME: Handles both streaming and non-streaming chat completions with tool calling.
package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// OpenAIClient implements Client for the OpenAI API.
type OpenAIClient struct {
	client openai.Client
	model  string
}

// NewOpenAIClient creates a new OpenAI API client.
// Default model is gpt-5.2 (Thinking variant for agentic work).
func NewOpenAIClient(apiKey, model string) *OpenAIClient {
	if model == "" {
		model = "gpt-5.2"
	}
	return &OpenAIClient{
		client: openai.NewClient(option.WithAPIKey(apiKey)),
		model:  model,
	}
}

// NewOpenAIClientWithBaseURL creates an OpenAI API client with a custom base URL.
// Useful for Azure OpenAI, proxies, or OpenAI-compatible endpoints.
func NewOpenAIClientWithBaseURL(apiKey, model, baseURL string) *OpenAIClient {
	if model == "" {
		model = "gpt-5.2"
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &OpenAIClient{
		client: openai.NewClient(opts...),
		model:  model,
	}
}

// convertOpenAIRequest converts our Request to OpenAI's ChatCompletionNewParams.
func convertOpenAIRequest(req *Request) openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model: req.Model,
	}

	if req.MaxTokens > 0 {
		// Use MaxCompletionTokens for newer models (gpt-5.x, o1, etc.)
		params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
	}

	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}

	// Map thinking config to reasoning effort
	if req.Thinking != nil && req.Thinking.Enabled {
		switch {
		case req.Thinking.Budget <= 4096:
			params.ReasoningEffort = openai.ReasoningEffortLow
		case req.Thinking.Budget <= 16384:
			params.ReasoningEffort = openai.ReasoningEffortMedium
		default:
			params.ReasoningEffort = openai.ReasoningEffortHigh
		}
	}

	// Build messages
	messages := []openai.ChatCompletionMessageParamUnion{}

	// System prompt becomes a system message
	if req.System != "" {
		messages = append(messages, openai.SystemMessage(req.System))
	}

	// Convert conversation messages
	for _, msg := range req.Messages {
		switch msg.Role {
		case RoleUser:
			messages = append(messages, convertUserMessages(msg)...)
		case RoleAssistant:
			messages = append(messages, convertAssistantMessage(msg))
		}
	}
	params.Messages = messages

	// Convert tools
	if len(req.Tools) > 0 {
		tools := make([]openai.ChatCompletionToolParam, 0, len(req.Tools))
		for _, tool := range req.Tools {
			toolParam := openai.ChatCompletionToolParam{
				Type: "function",
				Function: openai.FunctionDefinitionParam{
					Name:        tool.Name,
					Description: openai.String(tool.Description),
					Parameters:  openai.FunctionParameters(tool.InputSchema),
				},
			}
			tools = append(tools, toolParam)
		}
		params.Tools = tools
	}

	return params
}

// convertUserMessage converts a mux user message to OpenAI format.
func convertUserMessage(msg Message) openai.ChatCompletionMessageParamUnion {
	// Tool result routes to a tool message (OpenAI's required shape).
	for _, block := range msg.Blocks {
		if block.Type == ContentTypeToolResult {
			return openai.ToolMessage(block.Text, block.ToolUseID)
		}
	}

	var parts []openai.ChatCompletionContentPartUnionParam
	if msg.Content != "" {
		parts = append(parts, openai.TextContentPart(msg.Content))
	}
	for _, block := range msg.Blocks {
		switch block.Type {
		case ContentTypeText:
			parts = append(parts, openai.TextContentPart(block.Text))
		case ContentTypeImage:
			parts = append(parts, convertOpenAIImage(block))
		case ContentTypePDF:
			parts = append(parts, convertOpenAIPDF(block))
		case ContentTypeAudio:
			parts = append(parts, convertOpenAIAudio(block))
		}
	}

	if len(parts) == 0 {
		return openai.UserMessage("")
	}
	// Keep plain text in string form so we don't force array form unnecessarily.
	if len(parts) == 1 && len(msg.Blocks) == 0 {
		return openai.UserMessage(msg.Content)
	}
	return openai.UserMessage(parts)
}

// convertOpenAIPDF translates a PDF block to an OpenAI file content part.
// URL form and nil Source are rejected pre-flight by validateOpenAISources /
// validateRequest, but we guard here defensively.
func convertOpenAIPDF(block ContentBlock) openai.ChatCompletionContentPartUnionParam {
	if block.Source == nil {
		return openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{})
	}
	encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
	filename := "file.pdf"
	if block.Source.Path != "" {
		filename = filepath.Base(block.Source.Path)
	}
	return openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
		FileData: openai.String(encoded),
		Filename: openai.String(filename),
	})
}

// convertOpenAIAudio translates an audio block to an OpenAI input_audio part.
// URL form and nil Source are rejected pre-flight by validateOpenAISources /
// validateRequest, but we guard here defensively.
func convertOpenAIAudio(block ContentBlock) openai.ChatCompletionContentPartUnionParam {
	if block.Source == nil {
		return openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{})
	}
	format, _ := openaiAudioFormat(block.MediaType) // validateOpenAISources already checked.
	encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
	return openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{
		Data:   encoded,
		Format: format,
	})
}

// openaiAudioFormat maps a MIME type to OpenAI's input_audio format.
// audio/mpeg → "mp3"; audio/wav and audio/x-wav → "wav". Unknown MIME types
// return ok=false so callers can reject the request with a clear local error
// rather than relying on an upstream 400.
func openaiAudioFormat(mediaType string) (format string, ok bool) {
	switch mediaType {
	case "audio/mpeg", "audio/mp3":
		return "mp3", true
	case "audio/wav", "audio/x-wav":
		return "wav", true
	default:
		return "", false
	}
}

// validateOpenAISources checks every user-message block for source-form
// compatibility. Returns *ErrUnsupportedSource for PDF/audio via URL and for
// audio MIME types OpenAI's input_audio doesn't accept.
func validateOpenAISources(req *Request) error {
	for _, msg := range req.Messages {
		if msg.Role != RoleUser {
			continue
		}
		for _, block := range msg.Blocks {
			if block.Source == nil {
				continue
			}
			if block.Source.Kind == SourceKindURL {
				switch block.Type {
				case ContentTypePDF:
					return &ErrUnsupportedSource{Provider: "openai", Media: "pdf", Kind: "url"}
				case ContentTypeAudio:
					return &ErrUnsupportedSource{Provider: "openai", Media: "audio", Kind: "url"}
				}
			}
			if block.Type == ContentTypeAudio {
				if _, ok := openaiAudioFormat(block.MediaType); !ok {
					return &ErrUnsupportedSource{Provider: "openai", Media: "audio", Kind: block.MediaType}
				}
			}
		}
	}
	return nil
}

// convertOpenAIImage builds an OpenAI image content part from a mux image
// block, encoding inline bytes as a data URL.
func convertOpenAIImage(block ContentBlock) openai.ChatCompletionContentPartUnionParam {
	if block.Source == nil {
		return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{})
	}
	var url string
	switch block.Source.Kind {
	case SourceKindURL:
		url = block.Source.URL
	default:
		encoded := base64.StdEncoding.EncodeToString(block.Source.Bytes)
		url = "data:" + block.MediaType + ";base64," + encoded
	}
	return openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: url})
}

// convertUserMessages converts a mux user message to one or more OpenAI messages.
// When a user message contains multiple tool results (packed by the orchestrator),
// each tool result becomes a separate ToolMessage. Any text content is emitted as
// a UserMessage before the tool messages.
func convertUserMessages(msg Message) []openai.ChatCompletionMessageParamUnion {
	var toolMessages []openai.ChatCompletionMessageParamUnion
	var hasText bool

	for _, block := range msg.Blocks {
		if block.Type == ContentTypeToolResult {
			toolMessages = append(toolMessages, openai.ToolMessage(block.Text, block.ToolUseID))
		}
	}

	if len(toolMessages) == 0 {
		// No tool results — fall back to single user message behavior
		return []openai.ChatCompletionMessageParamUnion{convertUserMessage(msg)}
	}

	// Check if there is also text content alongside tool results
	if msg.Content != "" {
		hasText = true
	} else {
		for _, block := range msg.Blocks {
			if block.Type == ContentTypeText {
				hasText = true
				break
			}
		}
	}

	var result []openai.ChatCompletionMessageParamUnion
	if hasText {
		text := msg.Content
		if text == "" {
			for _, block := range msg.Blocks {
				if block.Type == ContentTypeText {
					text = block.Text
					break
				}
			}
		}
		result = append(result, openai.UserMessage(text))
	}
	result = append(result, toolMessages...)
	return result
}

// convertAssistantMessage converts a mux assistant message to OpenAI format.
func convertAssistantMessage(msg Message) openai.ChatCompletionMessageParamUnion {
	// Check for tool use blocks
	var toolCalls []openai.ChatCompletionMessageToolCallParam
	var textContent string

	if msg.Content != "" {
		textContent = msg.Content
	}

	for _, block := range msg.Blocks {
		switch block.Type {
		case ContentTypeText:
			textContent = block.Text
		case ContentTypeToolUse:
			argsJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
				ID:   block.ID,
				Type: "function",
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	if len(toolCalls) > 0 {
		msg := openai.ChatCompletionAssistantMessageParam{
			Role:      "assistant",
			ToolCalls: toolCalls,
		}
		if textContent != "" {
			msg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
				OfString: openai.String(textContent),
			}
		}
		return openai.ChatCompletionMessageParamUnion{OfAssistant: &msg}
	}

	return openai.AssistantMessage(textContent)
}

// convertOpenAIResponse converts OpenAI's ChatCompletion to our Response.
func convertOpenAIResponse(resp *openai.ChatCompletion) *Response {
	result := &Response{
		ID:    resp.ID,
		Model: resp.Model,
		Usage: Usage{
			InputTokens:    int(resp.Usage.PromptTokens),
			OutputTokens:   int(resp.Usage.CompletionTokens),
			ThinkingTokens: int(resp.Usage.CompletionTokensDetails.ReasoningTokens),
		},
	}

	if len(resp.Choices) == 0 {
		return result
	}

	choice := resp.Choices[0]

	// Map stop reason
	switch choice.FinishReason {
	case "stop":
		result.StopReason = StopReasonEndTurn
	case "tool_calls":
		result.StopReason = StopReasonToolUse
	case "length":
		result.StopReason = StopReasonMaxTokens
	default:
		result.StopReason = StopReasonEndTurn
	}

	// Text content
	if choice.Message.Content != "" {
		result.Content = append(result.Content, ContentBlock{
			Type: ContentTypeText,
			Text: choice.Message.Content,
		})
	}

	// Tool calls
	for _, tc := range choice.Message.ToolCalls {
		var input map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &input); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse tool call arguments for %s: %v\n", tc.Function.Name, err)
			input = make(map[string]any)
		}

		result.Content = append(result.Content, ContentBlock{
			Type:  ContentTypeToolUse,
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	return result
}

// CreateMessage sends a message and returns the complete response.
func (o *OpenAIClient) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	if req.Model == "" {
		req.Model = o.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}

	if err := validateRequest("openai", o.Capabilities(), req); err != nil {
		return nil, err
	}
	if err := validateOpenAISources(req); err != nil {
		return nil, err
	}

	params := convertOpenAIRequest(req)
	resp, err := o.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}

	return convertOpenAIResponse(resp), nil
}

// CreateMessageStream sends a message and returns a channel of streaming events.
func (o *OpenAIClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	if req.Model == "" {
		req.Model = o.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}

	if err := validateRequest("openai", o.Capabilities(), req); err != nil {
		return nil, err
	}
	if err := validateOpenAISources(req); err != nil {
		return nil, err
	}

	params := convertOpenAIRequest(req)
	stream := o.client.Chat.Completions.NewStreaming(ctx, params)

	eventChan := make(chan StreamEvent, 100)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Error: panic recovered in OpenAI CreateMessageStream: %v\n", r)
				eventChan <- StreamEvent{
					Type:  EventError,
					Error: fmt.Errorf("panic in stream processing: %v", r),
				}
			}
			close(eventChan)
		}()

		var acc openai.ChatCompletionAccumulator

		// Send message start
		eventChan <- StreamEvent{
			Type: EventMessageStart,
		}

		for stream.Next() {
			chunk := stream.Current()
			acc.AddChunk(chunk)

			// Emit text deltas
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				eventChan <- StreamEvent{
					Type: EventContentDelta,
					Text: chunk.Choices[0].Delta.Content,
				}
			}

			// Check for completed tool calls
			if toolCall, ok := acc.JustFinishedToolCall(); ok {
				var input map[string]any
				if err := json.Unmarshal([]byte(toolCall.Arguments), &input); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to parse tool call arguments for %s: %v\n", toolCall.Name, err)
					input = make(map[string]any)
				}

				eventChan <- StreamEvent{
					Type: EventContentStop,
					Block: &ContentBlock{
						Type:  ContentTypeToolUse,
						ID:    toolCall.ID,
						Name:  toolCall.Name,
						Input: input,
					},
				}
			}
		}

		if err := stream.Err(); err != nil {
			eventChan <- StreamEvent{
				Type:  EventError,
				Error: err,
			}
			return
		}

		// Final message with complete response
		eventChan <- StreamEvent{
			Type:     EventMessageStop,
			Response: convertOpenAIResponse(&acc.ChatCompletion),
		}
	}()

	return eventChan, nil
}

// Capabilities reports which media types OpenAI's Chat Completions supports.
// Audio is provider-level enabled; specific models (gpt-4o-audio-preview class)
// are required at send time — the API rejects on mismatch.
func (o *OpenAIClient) Capabilities() Capabilities {
	return Capabilities{Image: true, PDF: true, Audio: true, Video: false}
}

// Compile-time interface assertion.
var _ Client = (*OpenAIClient)(nil)
