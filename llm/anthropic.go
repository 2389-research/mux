// ABOUTME: Anthropic API client implementing the llm.Client interface.
// ABOUTME: Handles both streaming and non-streaming message creation.
package llm

import (
	"context"
	"encoding/json"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// AnthropicClient implements Client for the Anthropic API.
type AnthropicClient struct {
	client anthropic.Client
	model  string
}

// NewAnthropicClient creates a new Anthropic API client.
func NewAnthropicClient(apiKey, model string) *AnthropicClient {
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &AnthropicClient{
		client: anthropic.NewClient(option.WithAPIKey(apiKey)),
		model:  model,
	}
}

// convertRequest converts our Request to Anthropic's MessageNewParams.
func convertRequest(req *Request) anthropic.MessageNewParams {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: int64(req.MaxTokens),
	}

	// Convert messages
	messages := make([]anthropic.MessageParam, 0, len(req.Messages))
	for _, msg := range req.Messages {
		var content []anthropic.ContentBlockParamUnion
		if msg.Content != "" {
			content = append(content, anthropic.NewTextBlock(msg.Content))
		}
		for _, block := range msg.Blocks {
			switch block.Type {
			case ContentTypeText:
				content = append(content, anthropic.NewTextBlock(block.Text))
			case ContentTypeToolResult:
				content = append(content, anthropic.NewToolResultBlock(block.ToolUseID, block.Text, block.IsError))
			}
		}
		messages = append(messages, anthropic.MessageParam{
			Role:    anthropic.MessageParamRole(msg.Role),
			Content: content,
		})
	}
	params.Messages = messages

	// Set system prompt
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}

	// Convert tools
	if len(req.Tools) > 0 {
		tools := make([]anthropic.ToolUnionParam, 0, len(req.Tools))
		for _, tool := range req.Tools {
			toolParam := anthropic.ToolParam{
				Name:        tool.Name,
				Description: param.NewOpt(tool.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: tool.InputSchema,
				},
			}
			tools = append(tools, anthropic.ToolUnionParam{OfTool: &toolParam})
		}
		params.Tools = tools
	}

	return params
}

// convertResponse converts Anthropic's Message to our Response.
func convertResponse(msg *anthropic.Message) *Response {
	resp := &Response{
		ID:         msg.ID,
		Model:      string(msg.Model),
		StopReason: StopReason(msg.StopReason),
		Usage: Usage{
			InputTokens:  int(msg.Usage.InputTokens),
			OutputTokens: int(msg.Usage.OutputTokens),
		},
	}

	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			resp.Content = append(resp.Content, ContentBlock{
				Type: ContentTypeText,
				Text: block.Text,
			})
		case "tool_use":
			// Unmarshal the raw JSON input to map[string]any
			var input map[string]any
			if block.Input != nil {
				json.Unmarshal(block.Input, &input)
			}
			resp.Content = append(resp.Content, ContentBlock{
				Type:  ContentTypeToolUse,
				ID:    block.ID,
				Name:  block.Name,
				Input: input,
			})
		}
	}

	return resp
}

// CreateMessage sends a message and returns the complete response.
func (a *AnthropicClient) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	if req.Model == "" {
		req.Model = a.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = 4096
	}

	params := convertRequest(req)
	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return nil, err
	}

	return convertResponse(msg), nil
}

// CreateMessageStream sends a message and returns a channel of streaming events.
func (a *AnthropicClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	if req.Model == "" {
		req.Model = a.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = 4096
	}

	params := convertRequest(req)
	stream := a.client.Messages.NewStreaming(ctx, params)

	eventChan := make(chan StreamEvent, 100)

	go func() {
		defer close(eventChan)

		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "message_start":
				eventChan <- StreamEvent{
					Type:     EventMessageStart,
					Response: convertResponse(&event.Message),
				}
			case "content_block_start":
				eventChan <- StreamEvent{
					Type:  EventContentStart,
					Index: int(event.Index),
				}
			case "content_block_delta":
				var text string
				if event.Delta.Type == "text_delta" {
					text = event.Delta.Text
				}
				eventChan <- StreamEvent{
					Type:  EventContentDelta,
					Index: int(event.Index),
					Text:  text,
				}
			case "content_block_stop":
				eventChan <- StreamEvent{
					Type:  EventContentStop,
					Index: int(event.Index),
				}
			case "message_delta":
				eventChan <- StreamEvent{
					Type: EventMessageDelta,
				}
			case "message_stop":
				eventChan <- StreamEvent{
					Type: EventMessageStop,
				}
			}
		}

		if err := stream.Err(); err != nil {
			eventChan <- StreamEvent{
				Type:  EventError,
				Error: err,
			}
		}
	}()

	return eventChan, nil
}

// Compile-time interface assertion.
var _ Client = (*AnthropicClient)(nil)
