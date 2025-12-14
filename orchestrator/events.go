// ABOUTME: Defines the event system for orchestrator - enables decoupled
// ABOUTME: communication between the orchestration loop and UI/consumers.
package orchestrator

import (
	"sync"

	"github.com/2389-research/mux/tool"
)

// EventType identifies the kind of orchestrator event.
type EventType string

const (
	EventText        EventType = "text"
	EventToolCall    EventType = "tool_call"
	EventToolResult  EventType = "tool_result"
	EventStateChange EventType = "state_change"
	EventComplete    EventType = "complete"
	EventError       EventType = "error"
)

// Event represents an orchestrator lifecycle event.
type Event struct {
	Type EventType

	// For EventText
	Text string

	// For EventToolCall
	ToolID     string
	ToolName   string
	ToolParams map[string]any

	// For EventToolResult
	Result *tool.Result

	// For EventStateChange
	FromState State
	ToState   State

	// For EventError
	Error error

	// For EventComplete
	FinalText string
}

// NewTextEvent creates a text content event.
func NewTextEvent(text string) Event {
	return Event{Type: EventText, Text: text}
}

// NewToolCallEvent creates a tool call event.
func NewToolCallEvent(id, name string, params map[string]any) Event {
	return Event{Type: EventToolCall, ToolID: id, ToolName: name, ToolParams: params}
}

// NewToolResultEvent creates a tool result event.
func NewToolResultEvent(result *tool.Result) Event {
	return Event{Type: EventToolResult, Result: result}
}

// NewStateChangeEvent creates a state transition event.
func NewStateChangeEvent(from, to State) Event {
	return Event{Type: EventStateChange, FromState: from, ToState: to}
}

// NewCompleteEvent creates a completion event.
func NewCompleteEvent(finalText string) Event {
	return Event{Type: EventComplete, FinalText: finalText}
}

// NewErrorEvent creates an error event.
func NewErrorEvent(err error) Event {
	return Event{Type: EventError, Error: err}
}

// EventBus manages event distribution to subscribers.
type EventBus struct {
	mu          sync.RWMutex
	subscribers []chan Event
	closed      bool
}

// NewEventBus creates a new EventBus.
func NewEventBus() *EventBus {
	return &EventBus{subscribers: make([]chan Event, 0)}
}

// Subscribe returns a channel that receives events.
// The channel is buffered with 100 events to handle typical burst scenarios where
// the orchestrator generates multiple rapid events (text chunks, tool calls, state changes).
// This size balances memory usage against the ability to absorb event bursts without blocking.
func (eb *EventBus) Subscribe() <-chan Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	ch := make(chan Event, 100)
	if eb.closed {
		close(ch)
		return ch
	}
	eb.subscribers = append(eb.subscribers, ch)
	return ch
}

// Publish sends an event to all subscribers.
// Events are sent non-blocking: if a subscriber's channel is full, the event is dropped
// for that subscriber. This prevents slow consumers from blocking the orchestrator's
// event loop. Subscribers should size their buffers appropriately or consume events quickly.
func (eb *EventBus) Publish(event Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	if eb.closed {
		return
	}
	for _, ch := range eb.subscribers {
		select {
		case ch <- event:
		default:
			// Drop event if channel is full - non-blocking publish prevents slow
			// consumers from blocking orchestrator progress
		}
	}
}

// Close shuts down the event bus.
func (eb *EventBus) Close() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if eb.closed {
		return
	}
	eb.closed = true
	for _, ch := range eb.subscribers {
		close(ch)
	}
}
