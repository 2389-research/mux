// ABOUTME: OpenRouter API client implementing the llm.Client interface.
// ABOUTME: Uses OpenAI-compatible API with custom base URL and optional OpenRouter headers.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const (
	// OpenRouterBaseURL is the base URL for OpenRouter's OpenAI-compatible API.
	OpenRouterBaseURL = "https://openrouter.ai/api/v1"

	// OpenRouterDefaultModel is the default model used when none is specified.
	OpenRouterDefaultModel = "anthropic/claude-3.5-sonnet"
)

// OpenRouterClient implements Client for the OpenRouter API.
// OpenRouter provides a unified API that routes to various LLM providers.
type OpenRouterClient struct {
	client openai.Client
	model  string
}

// NewOpenRouterClient creates a new OpenRouter API client.
// Default model is anthropic/claude-3.5-sonnet.
func NewOpenRouterClient(apiKey, model string) *OpenRouterClient {
	if model == "" {
		model = OpenRouterDefaultModel
	}

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL(OpenRouterBaseURL),
	}

	return &OpenRouterClient{
		client: openai.NewClient(opts...),
		model:  model,
	}
}

// NewOpenRouterClientWithHeaders creates an OpenRouter client with custom headers.
// Use this to set HTTP-Referer and X-Title headers for app identification.
func NewOpenRouterClientWithHeaders(apiKey, model, referer, appTitle string) *OpenRouterClient {
	if model == "" {
		model = OpenRouterDefaultModel
	}

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL(OpenRouterBaseURL),
	}

	// Add OpenRouter-specific headers if provided
	if referer != "" {
		opts = append(opts, option.WithHeader("HTTP-Referer", referer))
	}
	if appTitle != "" {
		opts = append(opts, option.WithHeader("X-Title", appTitle))
	}

	return &OpenRouterClient{
		client: openai.NewClient(opts...),
		model:  model,
	}
}

// CreateMessage sends a message and returns the complete response.
func (o *OpenRouterClient) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	if req.Model == "" {
		req.Model = o.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}

	params := convertOpenAIRequest(req)
	resp, err := o.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}

	return convertOpenAIResponse(resp), nil
}

// CreateMessageStream sends a message and returns a channel of streaming events.
func (o *OpenRouterClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	if req.Model == "" {
		req.Model = o.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}

	params := convertOpenAIRequest(req)
	stream := o.client.Chat.Completions.NewStreaming(ctx, params)

	eventChan := make(chan StreamEvent, 100)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Error: panic recovered in OpenRouter CreateMessageStream: %v\n", r)
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
var _ Client = (*OpenRouterClient)(nil)
