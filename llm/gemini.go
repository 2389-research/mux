// ABOUTME: Gemini API client implementing the llm.Client interface.
// ABOUTME: Handles both streaming and non-streaming content generation with tool calling.
package llm

import (
	"context"
	"fmt"
	"math"
	"os"

	"google.golang.org/genai"
)

// GeminiClient implements Client for the Gemini API.
type GeminiClient struct {
	client *genai.Client
	model  string
}

// NewGeminiClient creates a new Gemini API client.
// Default model is gemini-2.0-flash.
func NewGeminiClient(ctx context.Context, apiKey, model string) (*GeminiClient, error) {
	if model == "" {
		model = "gemini-2.0-flash"
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &GeminiClient{
		client: client,
		model:  model,
	}, nil
}

// NewGeminiClientWithBaseURL creates a Gemini API client with a custom base URL.
// Useful for proxies or compatible endpoints.
func NewGeminiClientWithBaseURL(ctx context.Context, apiKey, model, baseURL string) (*GeminiClient, error) {
	if model == "" {
		model = "gemini-2.0-flash"
	}

	config := &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	}
	if baseURL != "" {
		config.HTTPOptions = genai.HTTPOptions{
			BaseURL: baseURL,
		}
	}

	client, err := genai.NewClient(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	return &GeminiClient{
		client: client,
		model:  model,
	}, nil
}

// convertGeminiRequest converts our Request to Gemini's content and config.
func convertGeminiRequest(req *Request) ([]*genai.Content, *genai.GenerateContentConfig) {
	config := &genai.GenerateContentConfig{}

	if req.MaxTokens > 0 && req.MaxTokens <= math.MaxInt32 {
		config.MaxOutputTokens = int32(req.MaxTokens) //nolint:gosec // bounds checked above
	}

	if req.Temperature != nil {
		temp := float32(*req.Temperature)
		config.Temperature = &temp
	}

	// System instruction
	if req.System != "" {
		config.SystemInstruction = genai.NewContentFromText(req.System, genai.RoleUser)
	}

	// Convert tools
	if len(req.Tools) > 0 {
		tools := make([]*genai.Tool, 0, 1)
		funcDecls := make([]*genai.FunctionDeclaration, 0, len(req.Tools))
		for _, tool := range req.Tools {
			funcDecl := &genai.FunctionDeclaration{
				Name:        tool.Name,
				Description: tool.Description,
			}
			// Use ParametersJsonSchema to pass the raw schema
			if tool.InputSchema != nil {
				funcDecl.ParametersJsonSchema = tool.InputSchema
			}
			funcDecls = append(funcDecls, funcDecl)
		}
		tools = append(tools, &genai.Tool{FunctionDeclarations: funcDecls})
		config.Tools = tools
	}

	// Convert messages
	contents := make([]*genai.Content, 0, len(req.Messages))
	for _, msg := range req.Messages {
		content := convertMessage(msg)
		if content != nil {
			contents = append(contents, content)
		}
	}

	return contents, config
}

// convertMessage converts a mux Message to Gemini Content.
func convertMessage(msg Message) *genai.Content {
	role := genai.RoleUser
	if msg.Role == RoleAssistant {
		role = genai.RoleModel
	}

	var parts []*genai.Part

	// Handle simple text content
	if msg.Content != "" {
		parts = append(parts, &genai.Part{Text: msg.Content})
	}

	// Handle blocks
	for _, block := range msg.Blocks {
		switch block.Type {
		case ContentTypeText:
			parts = append(parts, &genai.Part{Text: block.Text})
		case ContentTypeToolUse:
			// Assistant's tool call - represented as FunctionCall
			parts = append(parts, genai.NewPartFromFunctionCall(block.Name, block.Input))
		case ContentTypeToolResult:
			// User's tool result - represented as FunctionResponse
			response := map[string]any{"output": block.Text}
			if block.IsError {
				response = map[string]any{"error": block.Text}
			}
			parts = append(parts, genai.NewPartFromFunctionResponse(block.Name, response))
		}
	}

	if len(parts) == 0 {
		return nil
	}

	return &genai.Content{
		Role:  role,
		Parts: parts,
	}
}

// convertGeminiResponse converts Gemini's GenerateContentResponse to our Response.
func convertGeminiResponse(resp *genai.GenerateContentResponse, model string) *Response {
	result := &Response{
		Model: model,
	}

	if resp.ResponseID != "" {
		result.ID = resp.ResponseID
	}

	// Usage metadata
	if resp.UsageMetadata != nil {
		result.Usage = Usage{
			InputTokens:  int(resp.UsageMetadata.PromptTokenCount),
			OutputTokens: int(resp.UsageMetadata.CandidatesTokenCount),
		}
	}

	if len(resp.Candidates) == 0 {
		return result
	}

	candidate := resp.Candidates[0]

	// Map finish reason
	switch candidate.FinishReason {
	case genai.FinishReasonStop:
		result.StopReason = StopReasonEndTurn
	case genai.FinishReasonMaxTokens:
		result.StopReason = StopReasonMaxTokens
	default:
		// Check if we have function calls - that indicates tool use
		if resp.FunctionCalls() != nil && len(resp.FunctionCalls()) > 0 {
			result.StopReason = StopReasonToolUse
		} else {
			result.StopReason = StopReasonEndTurn
		}
	}

	// Extract content from candidate
	if candidate.Content != nil {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				result.Content = append(result.Content, ContentBlock{
					Type: ContentTypeText,
					Text: part.Text,
				})
			}
			if part.FunctionCall != nil {
				result.Content = append(result.Content, ContentBlock{
					Type:  ContentTypeToolUse,
					ID:    part.FunctionCall.ID,
					Name:  part.FunctionCall.Name,
					Input: part.FunctionCall.Args,
				})
			}
		}
	}

	return result
}

// CreateMessage sends a message and returns the complete response.
func (g *GeminiClient) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	model := req.Model
	if model == "" {
		model = g.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}

	contents, config := convertGeminiRequest(req)
	resp, err := g.client.Models.GenerateContent(ctx, model, contents, config)
	if err != nil {
		return nil, err
	}

	return convertGeminiResponse(resp, model), nil
}

// CreateMessageStream sends a message and returns a channel of streaming events.
func (g *GeminiClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = g.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}

	contents, config := convertGeminiRequest(req)

	eventChan := make(chan StreamEvent, 100)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Error: panic recovered in Gemini CreateMessageStream: %v\n", r)
				eventChan <- StreamEvent{
					Type:  EventError,
					Error: fmt.Errorf("panic in stream processing: %v", r),
				}
			}
			close(eventChan)
		}()

		// Send message start
		eventChan <- StreamEvent{
			Type: EventMessageStart,
		}

		var lastResp *genai.GenerateContentResponse

		// Iterate over the streaming response
		for resp, err := range g.client.Models.GenerateContentStream(ctx, model, contents, config) {
			if err != nil {
				eventChan <- StreamEvent{
					Type:  EventError,
					Error: err,
				}
				return
			}

			lastResp = resp

			// Process each candidate's content
			if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
				for _, part := range resp.Candidates[0].Content.Parts {
					if part.Text != "" {
						eventChan <- StreamEvent{
							Type: EventContentDelta,
							Text: part.Text,
						}
					}
					if part.FunctionCall != nil {
						eventChan <- StreamEvent{
							Type: EventContentStop,
							Block: &ContentBlock{
								Type:  ContentTypeToolUse,
								ID:    part.FunctionCall.ID,
								Name:  part.FunctionCall.Name,
								Input: part.FunctionCall.Args,
							},
						}
					}
				}
			}
		}

		// Send final message stop with complete response
		if lastResp != nil {
			eventChan <- StreamEvent{
				Type:     EventMessageStop,
				Response: convertGeminiResponse(lastResp, model),
			}
		} else {
			eventChan <- StreamEvent{
				Type: EventMessageStop,
			}
		}
	}()

	return eventChan, nil
}

// Compile-time interface assertion.
var _ Client = (*GeminiClient)(nil)
