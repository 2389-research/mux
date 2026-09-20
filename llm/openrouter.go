// ABOUTME: OpenRouter API client implementing the llm.Client interface.
// ABOUTME: Uses OpenAI-compatible API with custom base URL and optional OpenRouter headers.
package llm

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
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

// NewOpenRouterClientWithBaseURL creates an OpenRouter client with a custom base URL.
// Empty model falls back to OpenRouterDefaultModel; empty baseURL falls back to OpenRouterBaseURL.
func NewOpenRouterClientWithBaseURL(apiKey, model, baseURL string) *OpenRouterClient {
	if model == "" {
		model = OpenRouterDefaultModel
	}
	if baseURL == "" {
		baseURL = OpenRouterBaseURL
	}
	return &OpenRouterClient{
		client: openai.NewClient(
			option.WithAPIKey(apiKey),
			option.WithBaseURL(baseURL),
		),
		model: model,
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
	if err := validateRequest("openrouter", o.Capabilities(), req); err != nil {
		return nil, err
	}
	// OpenRouter uses Chat Completions on both transports, which has no URL
	// form for PDFs.
	if err := validateOpenAISources("openrouter", false, req); err != nil {
		return nil, err
	}

	params := convertOpenAIRequest(req)
	resp, err := o.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}
	// Validate every returned call's argument document before converting.
	// convertOpenAIResponse silently drops a call whose arguments do not
	// parse, which is only safe once this path has refused the response
	// outright: a malformed document must not be erased here and then
	// reported as a successful turn with the call missing.
	if err := validateChatToolInputs("openrouter", resp); err != nil {
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
	if err := validateRequest("openrouter", o.Capabilities(), req); err != nil {
		return nil, err
	}
	// OpenRouter uses Chat Completions on both transports, which has no URL
	// form for PDFs.
	if err := validateOpenAISources("openrouter", false, req); err != nil {
		return nil, err
	}

	params := convertOpenAIRequest(req)
	stream := o.client.Chat.Completions.NewStreaming(ctx, params)

	eventChan := make(chan StreamEvent, 100)

	go func() {
		defer stream.Close()
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Error: panic recovered in OpenRouter CreateMessageStream: %v\n", r)
				sendStreamEvent(ctx, eventChan, StreamEvent{
					Type:  EventError,
					Error: fmt.Errorf("panic in stream processing: %v", r),
				})
			}
			close(eventChan)
		}()

		acc := newChatStreamAccumulator("openrouter")

		// Send message start
		if !sendStreamEvent(ctx, eventChan, StreamEvent{
			Type: EventMessageStart,
		}) {
			return
		}

		for stream.Next() {
			events, err := acc.Add(stream.Current())
			if err != nil {
				sendStreamEvent(ctx, eventChan, StreamEvent{Type: EventError, Error: err})
				return
			}
			for _, event := range events {
				if !sendStreamEvent(ctx, eventChan, event) {
					return
				}
			}
		}

		if err := stream.Err(); err != nil {
			sendStreamEvent(ctx, eventChan, StreamEvent{
				Type:  EventError,
				Error: err,
			})
			return
		}

		// The terminal event is emitted once, and only after Finish has
		// validated every accumulated fragment: a stream that ended early or
		// carried an unexecutable call must fail instead of reporting a
		// successful turn.
		response, refs, err := acc.Finish()
		if err != nil {
			sendStreamEvent(ctx, eventChan, StreamEvent{Type: EventError, Error: err})
			return
		}
		sendStreamEvent(ctx, eventChan, StreamEvent{
			Type:        EventMessageStop,
			Response:    response,
			FinalBlocks: refs,
		})
	}()

	return eventChan, nil
}

// Capabilities reports which media types OpenRouter supports as input.
// OpenRouter uses the Chat Completions wire format, which carries image,
// PDF, and audio parts natively, so all three are enabled at the provider
// level. Per the design doc's "provider-level static + provider 400
// fall-through" decision, model-level mismatches surface as upstream 400s
// rather than being preflighted here. Video is rejected because no major
// OpenRouter route currently accepts video on the chat endpoint.
func (o *OpenRouterClient) Capabilities() Capabilities {
	return Capabilities{Image: true, PDF: true, Audio: true}
}

// Compile-time interface assertion.
var _ Client = (*OpenRouterClient)(nil)
