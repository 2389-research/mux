// ABOUTME: Ollama API client implementing the llm.Client interface.
// ABOUTME: Uses OpenAI-compatible API for local LLM inference via Ollama.
package llm

import (
	"context"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// DefaultOllamaBaseURL is the default Ollama API endpoint.
const DefaultOllamaBaseURL = "http://localhost:11434/v1"

// DefaultOllamaModel is the default model for Ollama.
const DefaultOllamaModel = "llama3.2"

// OllamaClient implements Client for the Ollama API using OpenAI-compatible endpoints.
type OllamaClient struct {
	client openai.Client
	model  string
}

// NewOllamaClient creates a new Ollama API client.
// If baseURL is empty, defaults to http://localhost:11434/v1.
// If model is empty, defaults to llama3.2.
func NewOllamaClient(baseURL, model string) *OllamaClient {
	if baseURL == "" {
		baseURL = DefaultOllamaBaseURL
	}
	if model == "" {
		model = DefaultOllamaModel
	}
	return &OllamaClient{
		client: openai.NewClient(
			option.WithBaseURL(baseURL),
			option.WithAPIKey("ollama"), // Ollama ignores API key but SDK requires it
		),
		model: model,
	}
}

// CreateMessage sends a message and returns the complete response.
func (o *OllamaClient) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	if req.Model == "" {
		req.Model = o.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}
	if err := validateRequest("ollama", o.Capabilities(), req); err != nil {
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
	if err := validateChatToolInputs("ollama", resp); err != nil {
		return nil, err
	}

	return convertOpenAIResponse(resp), nil
}

// CreateMessageStream sends a message and returns a channel of streaming events.
func (o *OllamaClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	if req.Model == "" {
		req.Model = o.model
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = DefaultMaxTokens
	}
	if err := validateRequest("ollama", o.Capabilities(), req); err != nil {
		return nil, err
	}

	params := convertOpenAIRequest(req)
	stream := o.client.Chat.Completions.NewStreaming(ctx, params)

	eventChan := make(chan StreamEvent, 100)

	go func() {
		defer stream.Close()
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Error: panic recovered in Ollama CreateMessageStream: %v\n", r)
				sendStreamEvent(ctx, eventChan, StreamEvent{
					Type:  EventError,
					Error: fmt.Errorf("panic in stream processing: %v", r),
				})
			}
			close(eventChan)
		}()

		acc := newChatStreamAccumulator("ollama")

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

// Capabilities reports which media types Ollama supports as input.
// Ollama's OpenAI-compatible endpoint accepts image_url for vision-capable
// models (LLaVA, Llama 3.2 Vision, etc.). Documents, audio, and video are not
// supported on the chat endpoint regardless of model.
func (o *OllamaClient) Capabilities() Capabilities {
	return Capabilities{Image: true}
}

// Compile-time interface assertion.
var _ Client = (*OllamaClient)(nil)
