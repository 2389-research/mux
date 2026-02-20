// ABOUTME: Anthropic API client implementing the llm.Client interface.
// ABOUTME: Handles both streaming and non-streaming message creation.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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

// NewAnthropicClientWithBaseURL creates an Anthropic API client with a custom base URL.
// Useful for proxies, API gateways, or compatible endpoints.
func NewAnthropicClientWithBaseURL(apiKey, model, baseURL string) *AnthropicClient {
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &AnthropicClient{
		client: anthropic.NewClient(opts...),
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
			case ContentTypeToolUse:
				// Serialize assistant's tool_use blocks for conversation history
				content = append(content, anthropic.NewToolUseBlock(block.ID, block.Input, block.Name))
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
			inputSchema := anthropic.ToolInputSchemaParam{}

			// Extract properties from the schema
			if props, ok := tool.InputSchema["properties"]; ok {
				inputSchema.Properties = props
			}

			// Extract required fields from the schema
			if req, ok := tool.InputSchema["required"]; ok {
				if reqSlice, ok := req.([]string); ok {
					inputSchema.Required = reqSlice
				} else if reqSlice, ok := req.([]any); ok {
					// Handle []any (common from JSON unmarshal)
					required := make([]string, 0, len(reqSlice))
					for _, r := range reqSlice {
						if s, ok := r.(string); ok {
							required = append(required, s)
						} else {
							fmt.Fprintf(os.Stderr, "Warning: failed to convert required field element to string for tool %s: got %T\n", tool.Name, r)
						}
					}
					inputSchema.Required = required
				} else {
					fmt.Fprintf(os.Stderr, "Warning: failed to convert required field to []string or []any for tool %s: got %T\n", tool.Name, req)
				}
			}

			toolParam := anthropic.ToolParam{
				Name:        tool.Name,
				Description: param.NewOpt(tool.Description),
				InputSchema: inputSchema,
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
				if err := json.Unmarshal(block.Input, &input); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to parse tool input for %s: %v\n", block.Name, err)
					input = make(map[string]any)
				}
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
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Error: panic recovered in CreateMessageStream: %v\n", r)
				eventChan <- StreamEvent{
					Type:  EventError,
					Error: fmt.Errorf("panic in stream processing: %v", r),
				}
			}
			close(eventChan)
		}()

		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "message_start":
				eventChan <- StreamEvent{
					Type:     EventMessageStart,
					Response: convertResponse(&event.Message),
				}
			case "content_block_start":
				se := StreamEvent{
					Type:  EventContentStart,
					Index: int(event.Index),
				}
				// Populate Block so consumers can distinguish text from tool_use
				if event.ContentBlock.Type != "" {
					se.Block = &ContentBlock{
						Type: ContentType(event.ContentBlock.Type),
						ID:   event.ContentBlock.ID,
						Name: event.ContentBlock.Name,
					}
				}
				eventChan <- se
			case "content_block_delta":
				var text string
				switch event.Delta.Type {
				case "text_delta":
					text = event.Delta.Text
				case "input_json_delta":
					text = event.Delta.PartialJSON
				case "thinking_delta":
					text = event.Delta.Thinking
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
				se := StreamEvent{
					Type: EventMessageDelta,
				}
				// Carry stop_reason and usage from the final message_delta
				if event.Delta.StopReason != "" || event.Usage.OutputTokens > 0 {
					se.Response = &Response{
						StopReason: StopReason(event.Delta.StopReason),
						Usage: Usage{
							OutputTokens: int(event.Usage.OutputTokens),
						},
					}
				}
				eventChan <- se
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
