// ABOUTME: Ollama API client implementing the llm.Client interface.
// ABOUTME: Uses OpenAI-compatible API for local LLM inference via Ollama.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
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

	params := convertOpenAIRequest(req)
	resp, err := o.client.Chat.Completions.New(ctx, params)
	if err != nil {
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

	params := convertOpenAIRequest(req)
	stream := o.client.Chat.Completions.NewStreaming(ctx, params)

	eventChan := make(chan StreamEvent, 100)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Error: panic recovered in Ollama CreateMessageStream: %v\n", r)
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
var _ Client = (*OllamaClient)(nil)
