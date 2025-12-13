// ABOUTME: Implements the state machine for agent orchestration - ensures valid
// ABOUTME: state transitions and prevents illegal operation sequences.
package orchestrator

import (
	"fmt"
	"sync"
)

// State represents the current state of the orchestrator.
type State string

const (
	StateIdle             State = "idle"
	StateStreaming        State = "streaming"
	StateAwaitingApproval State = "awaiting_approval"
	StateExecutingTool    State = "executing_tool"
	StateComplete         State = "complete"
	StateError            State = "error"
)

var validTransitions = map[State][]State{
	StateIdle:             {StateStreaming, StateError},
	StateStreaming:        {StateAwaitingApproval, StateComplete, StateError},
	StateAwaitingApproval: {StateExecutingTool, StateStreaming, StateError},
	StateExecutingTool:    {StateStreaming, StateComplete, StateError},
	StateComplete:         {StateIdle},
	StateError:            {StateIdle},
}

// StateMachine manages orchestrator state with validation.
type StateMachine struct {
	mu      sync.RWMutex
	current State
}

// NewStateMachine creates a new StateMachine in Idle state.
func NewStateMachine() *StateMachine {
	return &StateMachine{current: StateIdle}
}

// Current returns the current state.
func (sm *StateMachine) Current() State {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current
}

// Transition attempts to move to a new state.
func (sm *StateMachine) Transition(to State) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	valid := validTransitions[sm.current]
	for _, allowed := range valid {
		if allowed == to {
			sm.current = to
			return nil
		}
	}
	return fmt.Errorf("invalid transition: %s -> %s", sm.current, to)
}

// ForceState sets state without validation (testing only).
func (sm *StateMachine) ForceState(state State) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.current = state
}

// IsTerminal returns true if in Complete or Error state.
func (sm *StateMachine) IsTerminal() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current == StateComplete || sm.current == StateError
}

// Reset returns to Idle state.
func (sm *StateMachine) Reset() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.current = StateIdle
}
