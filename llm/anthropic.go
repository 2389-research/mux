// ABOUTME: Anthropic API client implementing the llm.Client interface.
// ABOUTME: Handles both streaming and non-streaming message creation.
package llm

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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

// CreateMessage sends a message and returns the complete response.
func (a *AnthropicClient) CreateMessage(ctx context.Context, req *Request) (*Response, error) {
	// TODO: implement in next task
	return nil, nil
}

// CreateMessageStream sends a message and returns a channel of streaming events.
func (a *AnthropicClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error) {
	// TODO: implement in next task
	return nil, nil
}

// Compile-time interface assertion.
var _ Client = (*AnthropicClient)(nil)
