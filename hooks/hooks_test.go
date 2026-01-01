// ABOUTME: Tests for the hook system - registration, dispatch, and lifecycle events.
// ABOUTME: Validates that hooks fire correctly and can modify behavior (e.g., Stop.Continue).
package hooks

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.hooks == nil {
		t.Fatal("hooks map not initialized")
	}
}

func TestSessionStartHook(t *testing.T) {
	m := NewManager()
	var called int32

	m.OnSessionStart(func(ctx context.Context, event *SessionStartEvent) error {
		atomic.AddInt32(&called, 1)
		if event.SessionID != "test-session" {
			t.Errorf("expected session ID 'test-session', got %q", event.SessionID)
		}
		if event.Source != "run" {
			t.Errorf("expected source 'run', got %q", event.Source)
		}
		if event.Prompt != "hello" {
			t.Errorf("expected prompt 'hello', got %q", event.Prompt)
		}
		return nil
	})

	event := &SessionStartEvent{
		SessionID: "test-session",
		Source:    "run",
		Prompt:    "hello",
	}

	err := m.FireSessionStart(context.Background(), event)
	if err != nil {
		t.Fatalf("FireSessionStart failed: %v", err)
	}

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("hook called %d times, expected 1", called)
	}
}

func TestSessionStartHook_Error(t *testing.T) {
	m := NewManager()
	expectedErr := errors.New("hook failed")

	m.OnSessionStart(func(ctx context.Context, event *SessionStartEvent) error {
		return expectedErr
	})

	event := &SessionStartEvent{SessionID: "test"}
	err := m.FireSessionStart(context.Background(), event)

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestSessionEndHook(t *testing.T) {
	m := NewManager()
	var called int32

	m.OnSessionEnd(func(ctx context.Context, event *SessionEndEvent) error {
		atomic.AddInt32(&called, 1)
		if event.SessionID != "test-session" {
			t.Errorf("expected session ID 'test-session', got %q", event.SessionID)
		}
		if event.Reason != "complete" {
			t.Errorf("expected reason 'complete', got %q", event.Reason)
		}
		if event.Error != nil {
			t.Errorf("expected nil error, got %v", event.Error)
		}
		return nil
	})

	event := &SessionEndEvent{
		SessionID: "test-session",
		Reason:    "complete",
	}

	err := m.FireSessionEnd(context.Background(), event)
	if err != nil {
		t.Fatalf("FireSessionEnd failed: %v", err)
	}

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("hook called %d times, expected 1", called)
	}
}

func TestStopHook_NoContinue(t *testing.T) {
	m := NewManager()

	m.OnStop(func(ctx context.Context, event *StopEvent) error {
		// Do not set Continue
		return nil
	})

	event := &StopEvent{
		SessionID: "test-session",
		FinalText: "Done!",
	}

	continueLoop, err := m.FireStop(context.Background(), event)
	if err != nil {
		t.Fatalf("FireStop failed: %v", err)
	}

	if continueLoop {
		t.Error("expected continueLoop=false when hook doesn't set Continue")
	}
}

func TestStopHook_WithContinue(t *testing.T) {
	m := NewManager()

	m.OnStop(func(ctx context.Context, event *StopEvent) error {
		event.Continue = true
		return nil
	})

	event := &StopEvent{
		SessionID: "test-session",
		FinalText: "Not done yet",
	}

	continueLoop, err := m.FireStop(context.Background(), event)
	if err != nil {
		t.Fatalf("FireStop failed: %v", err)
	}

	if !continueLoop {
		t.Error("expected continueLoop=true when hook sets Continue=true")
	}
}

func TestStopHook_Error(t *testing.T) {
	m := NewManager()
	expectedErr := errors.New("stop hook failed")

	m.OnStop(func(ctx context.Context, event *StopEvent) error {
		return expectedErr
	})

	event := &StopEvent{SessionID: "test"}
	continueLoop, err := m.FireStop(context.Background(), event)

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
	if continueLoop {
		t.Error("expected continueLoop=false on error")
	}
}

func TestSubagentStartHook(t *testing.T) {
	m := NewManager()
	var called int32

	m.OnSubagentStart(func(ctx context.Context, event *SubagentStartEvent) error {
		atomic.AddInt32(&called, 1)
		if event.ParentID != "parent" {
			t.Errorf("expected parent ID 'parent', got %q", event.ParentID)
		}
		if event.ChildID != "parent.child" {
			t.Errorf("expected child ID 'parent.child', got %q", event.ChildID)
		}
		if event.Name != "child" {
			t.Errorf("expected name 'child', got %q", event.Name)
		}
		return nil
	})

	event := &SubagentStartEvent{
		ParentID: "parent",
		ChildID:  "parent.child",
		Name:     "child",
	}

	err := m.FireSubagentStart(context.Background(), event)
	if err != nil {
		t.Fatalf("FireSubagentStart failed: %v", err)
	}

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("hook called %d times, expected 1", called)
	}
}

func TestSubagentStopHook(t *testing.T) {
	m := NewManager()
	var called int32
	expectedErr := errors.New("child failed")

	m.OnSubagentStop(func(ctx context.Context, event *SubagentStopEvent) error {
		atomic.AddInt32(&called, 1)
		if event.ParentID != "parent" {
			t.Errorf("expected parent ID 'parent', got %q", event.ParentID)
		}
		if event.ChildID != "parent.child" {
			t.Errorf("expected child ID 'parent.child', got %q", event.ChildID)
		}
		if event.Error != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, event.Error)
		}
		return nil
	})

	event := &SubagentStopEvent{
		ParentID: "parent",
		ChildID:  "parent.child",
		Name:     "child",
		Error:    expectedErr,
	}

	err := m.FireSubagentStop(context.Background(), event)
	if err != nil {
		t.Fatalf("FireSubagentStop failed: %v", err)
	}

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("hook called %d times, expected 1", called)
	}
}

func TestMultipleHooks(t *testing.T) {
	m := NewManager()
	var order []int

	m.OnSessionStart(func(ctx context.Context, event *SessionStartEvent) error {
		order = append(order, 1)
		return nil
	})

	m.OnSessionStart(func(ctx context.Context, event *SessionStartEvent) error {
		order = append(order, 2)
		return nil
	})

	m.OnSessionStart(func(ctx context.Context, event *SessionStartEvent) error {
		order = append(order, 3)
		return nil
	})

	event := &SessionStartEvent{SessionID: "test"}
	err := m.FireSessionStart(context.Background(), event)
	if err != nil {
		t.Fatalf("FireSessionStart failed: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 hooks called, got %d", len(order))
	}

	// Hooks should be called in registration order
	for i, v := range order {
		if v != i+1 {
			t.Errorf("order[%d] = %d, expected %d", i, v, i+1)
		}
	}
}

func TestMultipleHooks_StopOnFirstError(t *testing.T) {
	m := NewManager()
	expectedErr := errors.New("hook 2 failed")
	var called []int

	m.OnSessionStart(func(ctx context.Context, event *SessionStartEvent) error {
		called = append(called, 1)
		return nil
	})

	m.OnSessionStart(func(ctx context.Context, event *SessionStartEvent) error {
		called = append(called, 2)
		return expectedErr
	})

	m.OnSessionStart(func(ctx context.Context, event *SessionStartEvent) error {
		called = append(called, 3)
		return nil
	})

	event := &SessionStartEvent{SessionID: "test"}
	err := m.FireSessionStart(context.Background(), event)

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}

	// Hook 3 should not be called because hook 2 returned an error
	if len(called) != 2 {
		t.Errorf("expected 2 hooks called, got %d: %v", len(called), called)
	}
}

func TestNoHooksRegistered(t *testing.T) {
	m := NewManager()

	// Should not panic when no hooks are registered
	err := m.FireSessionStart(context.Background(), &SessionStartEvent{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	err = m.FireSessionEnd(context.Background(), &SessionEndEvent{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	continueLoop, err := m.FireStop(context.Background(), &StopEvent{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if continueLoop {
		t.Error("expected continueLoop=false when no hooks registered")
	}

	err = m.FireSubagentStart(context.Background(), &SubagentStartEvent{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	err = m.FireSubagentStop(context.Background(), &SubagentStopEvent{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHookType(t *testing.T) {
	// Verify each hook type returns the correct EventType
	tests := []struct {
		name     string
		hook     Hook
		expected EventType
	}{
		{"SessionStartHook", SessionStartHook(func(ctx context.Context, e *SessionStartEvent) error { return nil }), EventSessionStart},
		{"SessionEndHook", SessionEndHook(func(ctx context.Context, e *SessionEndEvent) error { return nil }), EventSessionEnd},
		{"StopHook", StopHook(func(ctx context.Context, e *StopEvent) error { return nil }), EventStop},
		{"SubagentStartHook", SubagentStartHook(func(ctx context.Context, e *SubagentStartEvent) error { return nil }), EventSubagentStart},
		{"SubagentStopHook", SubagentStopHook(func(ctx context.Context, e *SubagentStopEvent) error { return nil }), EventSubagentStop},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.hook.Type(); got != tt.expected {
				t.Errorf("Type() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRegisterDirect(t *testing.T) {
	m := NewManager()
	var called bool

	hook := SessionStartHook(func(ctx context.Context, e *SessionStartEvent) error {
		called = true
		return nil
	})

	m.Register(hook)

	err := m.FireSessionStart(context.Background(), &SessionStartEvent{})
	if err != nil {
		t.Fatalf("FireSessionStart failed: %v", err)
	}

	if !called {
		t.Error("hook was not called")
	}
}
