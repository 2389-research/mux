// ABOUTME: Defines the Client interface - the abstraction layer that allows
// ABOUTME: mux to work with any LLM provider (Anthropic, OpenAI, etc.)
package llm

import "context"

// Client is the interface for LLM communication.
type Client interface {
	CreateMessage(ctx context.Context, req *Request) (*Response, error)
	CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error)
}

// EventType identifies stream event types.
type EventType string

const (
	EventMessageStart EventType = "message_start"
	EventContentStart EventType = "content_block_start"
	EventContentDelta EventType = "content_block_delta"
	EventContentStop  EventType = "content_block_stop"
	EventMessageDelta EventType = "message_delta"
	EventMessageStop  EventType = "message_stop"
	EventError        EventType = "error"
)

// StreamEvent represents a streaming response event.
type StreamEvent struct {
	Type     EventType
	Index    int
	Text     string
	Block    *ContentBlock
	Response *Response
	Error    error
}
