// ABOUTME: Defines the Client interface - the abstraction layer that allows
// ABOUTME: mux to work with any LLM provider (Anthropic, OpenAI, etc.)

// Package llm defines the provider-agnostic Client interface and shared types
// for LLM communication, with concrete implementations for Anthropic, OpenAI,
// Gemini, Ollama, and OpenRouter.
package llm

import "context"

// Client is the interface for LLM communication.
type Client interface {
	CreateMessage(ctx context.Context, req *Request) (*Response, error)
	CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error)
	Capabilities() Capabilities
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

	// BlockID identifies the content block this event concerns, stable
	// across its start/delta*/stop sequence within one provider stream
	// attempt (for example "anthropic:0"). It is local to this attempt: the
	// orchestrator owns durable message/event identity separately. A
	// metadata-free legacy provider client leaves it empty.
	BlockID string
	// DeltaKind says how to interpret Text on a content_block_delta event.
	DeltaKind StreamDeltaKind
	// FinalBlocks maps every block in the final Response.Content to the
	// BlockID that produced it. Set once, on the terminal message_stop
	// event.
	FinalBlocks []StreamBlockRef
}
