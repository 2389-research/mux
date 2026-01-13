// ABOUTME: Defines the hook system for lifecycle events in mux.
// ABOUTME: Enables observability and control over session, agent, and tool lifecycle.
package hooks

import (
	"context"
	"sync"
)

// EventType identifies the kind of lifecycle event.
type EventType string

const (
	// Session lifecycle events
	EventSessionStart EventType = "SessionStart"
	EventSessionEnd   EventType = "SessionEnd"

	// Agent stop event (before returning from Run)
	EventStop EventType = "Stop"

	// Iteration event (fired at start of each think-act loop iteration)
	EventIteration EventType = "Iteration"

	// Subagent lifecycle events
	EventSubagentStart EventType = "SubagentStart"
	EventSubagentStop  EventType = "SubagentStop"

	// Compaction event (fired when context is compacted)
	EventCompaction EventType = "Compaction"
)

// SessionStartEvent is fired when Run() or Continue() is called.
type SessionStartEvent struct {
	SessionID string
	Source    string // "run", "continue"
	Prompt    string
}

// SessionEndEvent is fired when the agentic loop completes.
type SessionEndEvent struct {
	SessionID string
	Error     error  // nil if successful
	Reason    string // "complete", "error", "cancelled"
}

// StopEvent is fired before the orchestrator returns.
// Hooks can set Continue=true to force the loop to continue.
// Once any hook sets Continue=true, the loop will continue regardless
// of subsequent hooks setting it to false.
// When Continue=true, a "continue" user message is automatically injected
// into the conversation to prompt the LLM for another response.
type StopEvent struct {
	SessionID string
	FinalText string
	Continue  bool // Set to true to prevent stopping
}

// IterationEvent is fired at the start of each think-act loop iteration.
type IterationEvent struct {
	SessionID string
	Iteration int // 0-indexed: 0 = first LLM call, increments each loop cycle
}

// SubagentStartEvent is fired when a child agent is created.
type SubagentStartEvent struct {
	ParentID string
	ChildID  string
	Name     string
}

// SubagentStopEvent is fired when a child agent's Run() completes.
type SubagentStopEvent struct {
	ParentID string
	ChildID  string
	Name     string
	Error    error
}

// CompactionEvent is fired when conversation history is compacted.
type CompactionEvent struct {
	SessionID       string
	OriginalTokens  int
	CompactedTokens int
	MessagesRemoved int
	Summary         string // The generated summary
}

// Hook is the interface for all lifecycle hooks.
// Each hook type has its own signature for type safety.
type Hook interface {
	// Type returns the event type this hook handles.
	Type() EventType
}

// SessionStartHook handles SessionStart events.
type SessionStartHook func(ctx context.Context, event *SessionStartEvent) error

func (h SessionStartHook) Type() EventType { return EventSessionStart }

// SessionEndHook handles SessionEnd events.
type SessionEndHook func(ctx context.Context, event *SessionEndEvent) error

func (h SessionEndHook) Type() EventType { return EventSessionEnd }

// StopHook handles Stop events. Can modify event.Continue to prevent stopping.
type StopHook func(ctx context.Context, event *StopEvent) error

func (h StopHook) Type() EventType { return EventStop }

// IterationHook handles Iteration events.
type IterationHook func(ctx context.Context, event *IterationEvent) error

func (h IterationHook) Type() EventType { return EventIteration }

// SubagentStartHook handles SubagentStart events.
type SubagentStartHook func(ctx context.Context, event *SubagentStartEvent) error

func (h SubagentStartHook) Type() EventType { return EventSubagentStart }

// SubagentStopHook handles SubagentStop events.
type SubagentStopHook func(ctx context.Context, event *SubagentStopEvent) error

func (h SubagentStopHook) Type() EventType { return EventSubagentStop }

// CompactionHook handles Compaction events.
type CompactionHook func(ctx context.Context, event *CompactionEvent) error

func (h CompactionHook) Type() EventType { return EventCompaction }

// Manager manages hook registration and dispatch.
type Manager struct {
	mu    sync.RWMutex
	hooks map[EventType][]Hook
}

// NewManager creates a new hook manager.
func NewManager() *Manager {
	return &Manager{
		hooks: make(map[EventType][]Hook),
	}
}

// Register adds a hook for its event type.
func (m *Manager) Register(hook Hook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks[hook.Type()] = append(m.hooks[hook.Type()], hook)
}

// OnSessionStart registers a SessionStart hook.
func (m *Manager) OnSessionStart(fn func(ctx context.Context, event *SessionStartEvent) error) {
	m.Register(SessionStartHook(fn))
}

// OnSessionEnd registers a SessionEnd hook.
func (m *Manager) OnSessionEnd(fn func(ctx context.Context, event *SessionEndEvent) error) {
	m.Register(SessionEndHook(fn))
}

// OnStop registers a Stop hook.
func (m *Manager) OnStop(fn func(ctx context.Context, event *StopEvent) error) {
	m.Register(StopHook(fn))
}

// OnIteration registers an Iteration hook.
func (m *Manager) OnIteration(fn func(ctx context.Context, event *IterationEvent) error) {
	m.Register(IterationHook(fn))
}

// OnSubagentStart registers a SubagentStart hook.
func (m *Manager) OnSubagentStart(fn func(ctx context.Context, event *SubagentStartEvent) error) {
	m.Register(SubagentStartHook(fn))
}

// OnSubagentStop registers a SubagentStop hook.
func (m *Manager) OnSubagentStop(fn func(ctx context.Context, event *SubagentStopEvent) error) {
	m.Register(SubagentStopHook(fn))
}

// OnCompaction registers a Compaction hook.
func (m *Manager) OnCompaction(fn func(ctx context.Context, event *CompactionEvent) error) {
	m.Register(CompactionHook(fn))
}

// FireSessionStart dispatches SessionStart event to all registered hooks.
func (m *Manager) FireSessionStart(ctx context.Context, event *SessionStartEvent) error {
	m.mu.RLock()
	original := m.hooks[EventSessionStart]
	hooks := make([]Hook, len(original))
	copy(hooks, original)
	m.mu.RUnlock()

	for _, h := range hooks {
		if fn, ok := h.(SessionStartHook); ok {
			if err := fn(ctx, event); err != nil {
				return err
			}
		}
	}
	return nil
}

// FireSessionEnd dispatches SessionEnd event to all registered hooks.
func (m *Manager) FireSessionEnd(ctx context.Context, event *SessionEndEvent) error {
	m.mu.RLock()
	original := m.hooks[EventSessionEnd]
	hooks := make([]Hook, len(original))
	copy(hooks, original)
	m.mu.RUnlock()

	for _, h := range hooks {
		if fn, ok := h.(SessionEndHook); ok {
			if err := fn(ctx, event); err != nil {
				return err
			}
		}
	}
	return nil
}

// FireStop dispatches Stop event to all registered hooks.
// Returns true if any hook set event.Continue = true.
// Once any hook sets Continue=true, the loop will continue regardless of subsequent hooks.
func (m *Manager) FireStop(ctx context.Context, event *StopEvent) (continueLoop bool, err error) {
	m.mu.RLock()
	original := m.hooks[EventStop]
	hooks := make([]Hook, len(original))
	copy(hooks, original)
	m.mu.RUnlock()

	for _, h := range hooks {
		if fn, ok := h.(StopHook); ok {
			if err := fn(ctx, event); err != nil {
				return false, err
			}
			if event.Continue {
				continueLoop = true
			}
		}
	}
	return continueLoop, nil
}

// FireIteration dispatches Iteration event to all registered hooks.
func (m *Manager) FireIteration(ctx context.Context, event *IterationEvent) error {
	m.mu.RLock()
	original := m.hooks[EventIteration]
	hooks := make([]Hook, len(original))
	copy(hooks, original)
	m.mu.RUnlock()

	for _, h := range hooks {
		if fn, ok := h.(IterationHook); ok {
			if err := fn(ctx, event); err != nil {
				return err
			}
		}
	}
	return nil
}

// FireSubagentStart dispatches SubagentStart event to all registered hooks.
func (m *Manager) FireSubagentStart(ctx context.Context, event *SubagentStartEvent) error {
	m.mu.RLock()
	original := m.hooks[EventSubagentStart]
	hooks := make([]Hook, len(original))
	copy(hooks, original)
	m.mu.RUnlock()

	for _, h := range hooks {
		if fn, ok := h.(SubagentStartHook); ok {
			if err := fn(ctx, event); err != nil {
				return err
			}
		}
	}
	return nil
}

// FireSubagentStop dispatches SubagentStop event to all registered hooks.
func (m *Manager) FireSubagentStop(ctx context.Context, event *SubagentStopEvent) error {
	m.mu.RLock()
	original := m.hooks[EventSubagentStop]
	hooks := make([]Hook, len(original))
	copy(hooks, original)
	m.mu.RUnlock()

	for _, h := range hooks {
		if fn, ok := h.(SubagentStopHook); ok {
			if err := fn(ctx, event); err != nil {
				return err
			}
		}
	}
	return nil
}

// FireCompaction dispatches Compaction event to all registered hooks.
func (m *Manager) FireCompaction(ctx context.Context, event *CompactionEvent) error {
	m.mu.RLock()
	original := m.hooks[EventCompaction]
	hooks := make([]Hook, len(original))
	copy(hooks, original)
	m.mu.RUnlock()

	for _, h := range hooks {
		if fn, ok := h.(CompactionHook); ok {
			if err := fn(ctx, event); err != nil {
				return err
			}
		}
	}
	return nil
}
