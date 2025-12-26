// ABOUTME: OpenAI API client implementing the llm.Client interface.
// ABOUTME: Handles both streaming and non-streaming chat completions with tool calling.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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
			messages = append(messages, convertUserMessage(msg))
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
	// Check for tool results in blocks
	for _, block := range msg.Blocks {
		if block.Type == ContentTypeToolResult {
			return openai.ToolMessage(block.Text, block.ToolUseID)
		}
	}

	// Regular text message
	if msg.Content != "" {
		return openai.UserMessage(msg.Content)
	}

	// Text from blocks
	for _, block := range msg.Blocks {
		if block.Type == ContentTypeText {
			return openai.UserMessage(block.Text)
		}
	}

	return openai.UserMessage("")
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
			InputTokens:  int(resp.Usage.PromptTokens),
			OutputTokens: int(resp.Usage.CompletionTokens),
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
		req.MaxTokens = 4096
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
		req.MaxTokens = 4096
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

// Compile-time interface assertion.
var _ Client = (*OpenAIClient)(nil)
