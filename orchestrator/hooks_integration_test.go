// ABOUTME: Integration tests for hook lifecycle events in the orchestrator.
// ABOUTME: Verifies hooks fire correctly during real orchestrator execution.
package orchestrator_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/2389-research/mux/hooks"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/tool"
)

// hookRecorder records all hook events for verification.
type hookRecorder struct {
	mu              sync.Mutex
	sessionStarts   []hooks.SessionStartEvent
	sessionEnds     []hooks.SessionEndEvent
	iterations      []hooks.IterationEvent
	stops           []hooks.StopEvent
	subagentStarts  []hooks.SubagentStartEvent
	subagentStops   []hooks.SubagentStopEvent
	continueOnStop  bool // If true, Stop hook sets Continue=true
	stopAfterNIters int  // If > 0, only continue for N iterations via Stop hook
}

func newHookRecorder() *hookRecorder {
	return &hookRecorder{}
}

// drainEvents spawns a goroutine to consume all events and returns a function
// to wait for completion. This prevents goroutine leaks in tests.
func drainEvents(events <-chan orchestrator.Event) func() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range events {
		}
	}()
	return wg.Wait
}

func (r *hookRecorder) registerAll(m *hooks.Manager) {
	m.OnSessionStart(func(ctx context.Context, e *hooks.SessionStartEvent) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.sessionStarts = append(r.sessionStarts, *e)
		return nil
	})

	m.OnSessionEnd(func(ctx context.Context, e *hooks.SessionEndEvent) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.sessionEnds = append(r.sessionEnds, *e)
		return nil
	})

	m.OnIteration(func(ctx context.Context, e *hooks.IterationEvent) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.iterations = append(r.iterations, *e)
		return nil
	})

	m.OnStop(func(ctx context.Context, e *hooks.StopEvent) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.stops = append(r.stops, *e)

		if r.continueOnStop {
			// Check if we should stop continuing after N iterations
			if r.stopAfterNIters > 0 && len(r.iterations) >= r.stopAfterNIters {
				e.Continue = false
			} else {
				e.Continue = true
			}
		}
		return nil
	})

	m.OnSubagentStart(func(ctx context.Context, e *hooks.SubagentStartEvent) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.subagentStarts = append(r.subagentStarts, *e)
		return nil
	})

	m.OnSubagentStop(func(ctx context.Context, e *hooks.SubagentStopEvent) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.subagentStops = append(r.subagentStops, *e)
		return nil
	})
}

// TestHookSessionStartOnRun verifies SessionStart fires with correct data on Run().
func TestHookSessionStartOnRun(t *testing.T) {
	recorder := newHookRecorder()
	hookMgr := hooks.NewManager()
	recorder.registerAll(hookMgr)

	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Hello"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(client, executor, config)

	// Drain events and ensure cleanup
	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(context.Background(), "Test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify SessionStart was fired
	if len(recorder.sessionStarts) != 1 {
		t.Fatalf("expected 1 SessionStart event, got %d", len(recorder.sessionStarts))
	}

	start := recorder.sessionStarts[0]
	if start.Source != "run" {
		t.Errorf("expected source 'run', got %q", start.Source)
	}
	if start.Prompt != "Test prompt" {
		t.Errorf("expected prompt 'Test prompt', got %q", start.Prompt)
	}
	if start.SessionID == "" {
		t.Error("expected non-empty SessionID")
	}
	if start.SessionID != orch.SessionID() {
		t.Errorf("SessionID mismatch: event=%q, orch=%q", start.SessionID, orch.SessionID())
	}
}

// TestHookSessionStartOnContinue verifies SessionStart fires with source="continue".
func TestHookSessionStartOnContinue(t *testing.T) {
	recorder := newHookRecorder()
	hookMgr := hooks.NewManager()
	recorder.registerAll(hookMgr)

	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Hello"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(client, executor, config)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Continue(context.Background(), "Continue prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(recorder.sessionStarts) != 1 {
		t.Fatalf("expected 1 SessionStart event, got %d", len(recorder.sessionStarts))
	}

	start := recorder.sessionStarts[0]
	if start.Source != "continue" {
		t.Errorf("expected source 'continue', got %q", start.Source)
	}
}

// TestHookSessionEndOnComplete verifies SessionEnd fires with reason="complete".
func TestHookSessionEndOnComplete(t *testing.T) {
	recorder := newHookRecorder()
	hookMgr := hooks.NewManager()
	recorder.registerAll(hookMgr)

	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(client, executor, config)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(context.Background(), "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(recorder.sessionEnds) != 1 {
		t.Fatalf("expected 1 SessionEnd event, got %d", len(recorder.sessionEnds))
	}

	end := recorder.sessionEnds[0]
	if end.Reason != "complete" {
		t.Errorf("expected reason 'complete', got %q", end.Reason)
	}
	if end.Error != nil {
		t.Errorf("expected nil error, got %v", end.Error)
	}
}

// TestHookSessionEndOnError verifies SessionEnd fires with reason="error".
func TestHookSessionEndOnError(t *testing.T) {
	recorder := newHookRecorder()
	hookMgr := hooks.NewManager()
	recorder.registerAll(hookMgr)

	client := &mockLLMClientWithError{err: fmt.Errorf("LLM failure")}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(client, executor, config)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(context.Background(), "Test")
	if err == nil {
		t.Fatal("expected error")
	}

	if len(recorder.sessionEnds) != 1 {
		t.Fatalf("expected 1 SessionEnd event, got %d", len(recorder.sessionEnds))
	}

	end := recorder.sessionEnds[0]
	if end.Reason != "error" {
		t.Errorf("expected reason 'error', got %q", end.Reason)
	}
	if end.Error == nil {
		t.Error("expected non-nil error in event")
	}
}

// TestHookSessionEndOnCancelled verifies SessionEnd fires with reason="cancelled".
func TestHookSessionEndOnCancelled(t *testing.T) {
	recorder := newHookRecorder()
	hookMgr := hooks.NewManager()
	recorder.registerAll(hookMgr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Never"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(client, executor, config)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(ctx, "Test")
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	if len(recorder.sessionEnds) != 1 {
		t.Fatalf("expected 1 SessionEnd event, got %d", len(recorder.sessionEnds))
	}

	end := recorder.sessionEnds[0]
	if end.Reason != "cancelled" {
		t.Errorf("expected reason 'cancelled', got %q", end.Reason)
	}
}

// TestHookIterationFiringCorrectly verifies Iteration hooks fire with correct numbers.
func TestHookIterationFiringCorrectly(t *testing.T) {
	recorder := newHookRecorder()
	hookMgr := hooks.NewManager()
	recorder.registerAll(hookMgr)

	// Client that requires 3 iterations: tool -> tool -> done
	client := &mockLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type:  llm.ContentTypeToolUse,
					ID:    "tool_1",
					Name:  "test_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content: []llm.ContentBlock{{
					Type:  llm.ContentTypeToolUse,
					ID:    "tool_2",
					Name:  "test_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	registry := tool.NewRegistry()
	testTool := &mockTool{name: "test_tool"}
	registry.Register(testTool)
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(client, executor, config)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(context.Background(), "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 3 iterations: 0, 1, 2
	if len(recorder.iterations) != 3 {
		t.Fatalf("expected 3 Iteration events, got %d", len(recorder.iterations))
	}

	for i, iter := range recorder.iterations {
		if iter.Iteration != i {
			t.Errorf("iteration %d: expected Iteration=%d, got %d", i, i, iter.Iteration)
		}
		if iter.SessionID != orch.SessionID() {
			t.Errorf("iteration %d: SessionID mismatch", i)
		}
	}
}

// TestHookIterationSingleIteration verifies single iteration scenario.
func TestHookIterationSingleIteration(t *testing.T) {
	recorder := newHookRecorder()
	hookMgr := hooks.NewManager()
	recorder.registerAll(hookMgr)

	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(client, executor, config)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(context.Background(), "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have exactly 1 iteration (iteration 0)
	if len(recorder.iterations) != 1 {
		t.Fatalf("expected 1 Iteration event, got %d", len(recorder.iterations))
	}

	if recorder.iterations[0].Iteration != 0 {
		t.Errorf("expected iteration 0, got %d", recorder.iterations[0].Iteration)
	}
}

// TestHookStopFires verifies Stop hook fires with final text.
func TestHookStopFires(t *testing.T) {
	recorder := newHookRecorder()
	hookMgr := hooks.NewManager()
	recorder.registerAll(hookMgr)

	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Final response"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(client, executor, config)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(context.Background(), "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(recorder.stops) != 1 {
		t.Fatalf("expected 1 Stop event, got %d", len(recorder.stops))
	}

	stop := recorder.stops[0]
	if stop.FinalText != "Final response" {
		t.Errorf("expected FinalText 'Final response', got %q", stop.FinalText)
	}
	if stop.SessionID != orch.SessionID() {
		t.Errorf("SessionID mismatch")
	}
}

// TestHookStopContinue verifies Stop hook can force continuation.
func TestHookStopContinue(t *testing.T) {
	recorder := newHookRecorder()
	recorder.continueOnStop = true
	recorder.stopAfterNIters = 3 // Continue until 3 iterations, then stop

	hookMgr := hooks.NewManager()
	recorder.registerAll(hookMgr)

	// Client that always returns text (would normally stop immediately)
	callCount := 0
	client := &mockDynamicLLMClient{
		responseFn: func() *llm.Response {
			callCount++
			return &llm.Response{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: fmt.Sprintf("Response %d", callCount)}},
				StopReason: llm.StopReasonEndTurn,
			}
		},
	}

	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(client, executor, config)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(context.Background(), "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 3 iterations because Stop hook forced continuation twice
	if len(recorder.iterations) != 3 {
		t.Fatalf("expected 3 iterations due to Stop.Continue, got %d", len(recorder.iterations))
	}

	// Should have 3 Stop events (one per attempt to stop)
	if len(recorder.stops) != 3 {
		t.Fatalf("expected 3 Stop events, got %d", len(recorder.stops))
	}
}

// TestHookOrderingWithinIteration verifies hooks fire in correct order.
func TestHookOrderingWithinIteration(t *testing.T) {
	var events []string
	var mu sync.Mutex

	hookMgr := hooks.NewManager()

	hookMgr.OnSessionStart(func(ctx context.Context, e *hooks.SessionStartEvent) error {
		mu.Lock()
		events = append(events, "SessionStart")
		mu.Unlock()
		return nil
	})

	hookMgr.OnIteration(func(ctx context.Context, e *hooks.IterationEvent) error {
		mu.Lock()
		events = append(events, fmt.Sprintf("Iteration:%d", e.Iteration))
		mu.Unlock()
		return nil
	})

	hookMgr.OnStop(func(ctx context.Context, e *hooks.StopEvent) error {
		mu.Lock()
		events = append(events, "Stop")
		mu.Unlock()
		return nil
	})

	hookMgr.OnSessionEnd(func(ctx context.Context, e *hooks.SessionEndEvent) error {
		mu.Lock()
		events = append(events, "SessionEnd")
		mu.Unlock()
		return nil
	})

	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(client, executor, config)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(context.Background(), "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected order: SessionStart -> Iteration:0 -> Stop -> SessionEnd
	expected := []string{"SessionStart", "Iteration:0", "Stop", "SessionEnd"}
	if len(events) != len(expected) {
		t.Fatalf("expected %d events, got %d: %v", len(expected), len(events), events)
	}

	for i, exp := range expected {
		if events[i] != exp {
			t.Errorf("event %d: expected %q, got %q", i, exp, events[i])
		}
	}
}

// TestHookMaxIterationsWithIterationHook verifies iteration count at max iterations error.
func TestHookMaxIterationsWithIterationHook(t *testing.T) {
	recorder := newHookRecorder()
	hookMgr := hooks.NewManager()
	recorder.registerAll(hookMgr)

	// Client that always returns tool use (infinite loop)
	loopingClient := &mockLoopingLLMClient{}

	registry := tool.NewRegistry()
	loopTool := &mockTool{name: "loop_tool"}
	registry.Register(loopTool)
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.MaxIterations = 5
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(loopingClient, executor, config)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(context.Background(), "Test")
	if err == nil {
		t.Fatal("expected max iterations error")
	}

	// Should have exactly 5 iterations (0, 1, 2, 3, 4)
	if len(recorder.iterations) != 5 {
		t.Fatalf("expected 5 Iteration events at max iterations, got %d", len(recorder.iterations))
	}

	// Verify iteration numbers
	for i, iter := range recorder.iterations {
		if iter.Iteration != i {
			t.Errorf("iteration %d: expected Iteration=%d, got %d", i, i, iter.Iteration)
		}
	}

	// SessionEnd should fire with error reason
	if len(recorder.sessionEnds) != 1 {
		t.Fatalf("expected 1 SessionEnd event, got %d", len(recorder.sessionEnds))
	}
	if recorder.sessionEnds[0].Reason != "error" {
		t.Errorf("expected reason 'error', got %q", recorder.sessionEnds[0].Reason)
	}
}

// TestHookIterationError verifies that iteration hook errors propagate correctly.
func TestHookIterationError(t *testing.T) {
	expectedErr := fmt.Errorf("iteration hook failed")
	var sessionEnds []hooks.SessionEndEvent
	var mu sync.Mutex

	hookMgr := hooks.NewManager()

	// Register an iteration hook that fails on iteration 1
	hookMgr.OnIteration(func(ctx context.Context, e *hooks.IterationEvent) error {
		if e.Iteration == 1 {
			return expectedErr
		}
		return nil
	})

	hookMgr.OnSessionEnd(func(ctx context.Context, e *hooks.SessionEndEvent) error {
		mu.Lock()
		defer mu.Unlock()
		sessionEnds = append(sessionEnds, *e)
		return nil
	})

	// Client that requires 2 iterations (tool use then text)
	client := &mockLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type:  llm.ContentTypeToolUse,
					ID:    "tool_1",
					Name:  "test_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	registry := tool.NewRegistry()
	testTool := &mockTool{name: "test_tool"}
	registry.Register(testTool)
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.HookManager = hookMgr
	orch := orchestrator.NewWithConfig(client, executor, config)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(context.Background(), "Test")

	// Should get the iteration hook error
	if err != expectedErr {
		t.Fatalf("expected iteration hook error, got %v", err)
	}

	// SessionEnd should fire with error reason
	mu.Lock()
	defer mu.Unlock()
	if len(sessionEnds) != 1 {
		t.Fatalf("expected 1 SessionEnd event, got %d", len(sessionEnds))
	}
	if sessionEnds[0].Reason != "error" {
		t.Errorf("expected reason 'error', got %q", sessionEnds[0].Reason)
	}
	if sessionEnds[0].Error != expectedErr {
		t.Errorf("expected error %v in SessionEnd, got %v", expectedErr, sessionEnds[0].Error)
	}
}

// TestHookNoHookManager verifies orchestrator works without hook manager.
func TestHookNoHookManager(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	// No hook manager configured
	orch := orchestrator.New(client, executor)

	wait := drainEvents(orch.Subscribe())
	defer wait()

	err := orch.Run(context.Background(), "Test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should complete without issues
	if orch.State() != orchestrator.StateComplete {
		t.Errorf("expected StateComplete, got %s", orch.State())
	}
}
