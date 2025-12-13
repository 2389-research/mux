package orchestrator_test

import (
	"fmt"
	"testing"

	"github.com/2389-research/mux/orchestrator"
)

func TestStateMachine(t *testing.T) {
	sm := orchestrator.NewStateMachine()

	// Initial state should be Idle
	if sm.Current() != orchestrator.StateIdle {
		t.Errorf("expected initial state Idle, got %s", sm.Current())
	}

	// Valid: Idle -> Streaming
	if err := sm.Transition(orchestrator.StateStreaming); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Valid: Streaming -> AwaitingApproval
	if err := sm.Transition(orchestrator.StateAwaitingApproval); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Valid: AwaitingApproval -> ExecutingTool
	if err := sm.Transition(orchestrator.StateExecutingTool); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Valid: ExecutingTool -> Streaming
	if err := sm.Transition(orchestrator.StateStreaming); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Valid: Streaming -> Complete
	if err := sm.Transition(orchestrator.StateComplete); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStateMachineInvalidTransition(t *testing.T) {
	sm := orchestrator.NewStateMachine()

	// Invalid: Idle -> Complete
	err := sm.Transition(orchestrator.StateComplete)
	if err == nil {
		t.Error("expected error for Idle -> Complete")
	}

	// Invalid: Idle -> ExecutingTool
	err = sm.Transition(orchestrator.StateExecutingTool)
	if err == nil {
		t.Error("expected error for Idle -> ExecutingTool")
	}
}

func TestStateMachineErrorFromAnyState(t *testing.T) {
	states := []orchestrator.State{
		orchestrator.StateIdle,
		orchestrator.StateStreaming,
		orchestrator.StateAwaitingApproval,
		orchestrator.StateExecutingTool,
	}

	for _, state := range states {
		sm := orchestrator.NewStateMachine()
		sm.ForceState(state)

		if err := sm.Transition(orchestrator.StateError); err != nil {
			t.Errorf("Error should be reachable from %s: %v", state, err)
		}
	}
}

func TestStateMachineReset(t *testing.T) {
	sm := orchestrator.NewStateMachine()
	sm.Transition(orchestrator.StateStreaming)
	sm.Reset()

	if sm.Current() != orchestrator.StateIdle {
		t.Errorf("expected Idle after reset, got %s", sm.Current())
	}
}

func TestEventTypes(t *testing.T) {
	textEvent := orchestrator.NewTextEvent("Hello world")
	if textEvent.Type != orchestrator.EventText {
		t.Errorf("expected EventText, got %s", textEvent.Type)
	}
	if textEvent.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", textEvent.Text)
	}

	toolCallEvent := orchestrator.NewToolCallEvent("tool_123", "read_file", map[string]any{"path": "/tmp"})
	if toolCallEvent.Type != orchestrator.EventToolCall {
		t.Errorf("expected EventToolCall, got %s", toolCallEvent.Type)
	}

	errorEvent := orchestrator.NewErrorEvent(fmt.Errorf("test error"))
	if errorEvent.Type != orchestrator.EventError {
		t.Errorf("expected EventError, got %s", errorEvent.Type)
	}
}

func TestEventBus(t *testing.T) {
	bus := orchestrator.NewEventBus()
	sub := bus.Subscribe()

	event := orchestrator.NewTextEvent("test")
	bus.Publish(event)

	select {
	case received := <-sub:
		if received.Text != "test" {
			t.Errorf("expected text 'test', got %q", received.Text)
		}
	default:
		t.Error("expected to receive event")
	}

	bus.Close()
	_, ok := <-sub
	if ok {
		t.Error("expected channel to be closed")
	}
}

func TestEventBusMultipleSubscribers(t *testing.T) {
	bus := orchestrator.NewEventBus()
	sub1 := bus.Subscribe()
	sub2 := bus.Subscribe()

	bus.Publish(orchestrator.NewTextEvent("broadcast"))

	for i, sub := range []<-chan orchestrator.Event{sub1, sub2} {
		select {
		case e := <-sub:
			if e.Text != "broadcast" {
				t.Errorf("subscriber %d: expected 'broadcast', got %q", i, e.Text)
			}
		default:
			t.Errorf("subscriber %d: expected to receive event", i)
		}
	}

	bus.Close()
}
