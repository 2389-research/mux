package orchestrator_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/session"
	"github.com/2389-research/mux/tool"
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

// Add mock LLM client for testing
type mockLLMClient struct {
	responses []*llm.Response
	callIndex int
}

func (m *mockLLMClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if m.callIndex >= len(m.responses) {
		return &llm.Response{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		}, nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

func (m *mockLLMClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		resp, _ := m.CreateMessage(ctx, req)
		ch <- llm.StreamEvent{Type: llm.EventMessageStop, Response: resp}
		close(ch)
	}()
	return ch, nil
}

func (m *mockLLMClient) Capabilities() llm.Capabilities { return llm.FullCapabilities() }

// mockTool for orchestrator tests
type mockTool struct {
	name     string
	execFunc func(ctx context.Context, params map[string]any) (*tool.Result, error)
}

func (m *mockTool) Name() string                                { return m.name }
func (m *mockTool) Description() string                         { return "mock" }
func (m *mockTool) RequiresApproval(params map[string]any) bool { return false }
func (m *mockTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	if m.execFunc != nil {
		return m.execFunc(ctx, params)
	}
	return tool.NewResult(m.name, true, "executed", ""), nil
}

func TestOrchestratorSimpleResponse(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Hello!"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	ctx := context.Background()
	err := orch.Run(ctx, "Say hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hasText, hasComplete bool
	for event := range events {
		switch event.Type {
		case orchestrator.EventText:
			hasText = true
		case orchestrator.EventComplete:
			hasComplete = true
		}
	}

	if !hasText {
		t.Error("expected text event")
	}
	if !hasComplete {
		t.Error("expected complete event")
	}
}

func TestOrchestratorWithToolUse(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type:  llm.ContentTypeToolUse,
					ID:    "tool_1",
					Name:  "test_tool",
					Input: map[string]any{"arg": "value"},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done!"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	registry := tool.NewRegistry()
	testTool := &mockTool{name: "test_tool"}
	registry.Register(testTool)
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	ctx := context.Background()
	err := orch.Run(ctx, "Use the tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var toolCalled, toolResult bool
	for event := range events {
		switch event.Type {
		case orchestrator.EventToolCall:
			toolCalled = true
		case orchestrator.EventToolResult:
			toolResult = true
		}
	}

	if !toolCalled {
		t.Error("expected tool call event")
	}
	if !toolResult {
		t.Error("expected tool result event")
	}
}

// TestContextCancellationDuringToolExecution tests that context cancellation
// is properly handled when a tool is executing.
func TestContextCancellationDuringToolExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	toolExecuting := make(chan struct{})
	toolBlocked := make(chan struct{})

	client := &mockLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type:  llm.ContentTypeToolUse,
					ID:    "tool_1",
					Name:  "blocking_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
		},
	}

	registry := tool.NewRegistry()
	blockingTool := &mockTool{
		name: "blocking_tool",
		execFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			close(toolExecuting)
			<-toolBlocked // Block until test unblocks us
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				return tool.NewResult("blocking_tool", true, "done", ""), nil
			}
		},
	}
	registry.Register(blockingTool)
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	errCh := make(chan error, 1)
	go func() {
		errCh <- orch.Run(ctx, "Use blocking tool")
	}()

	// Wait for tool to start executing
	<-toolExecuting
	// Cancel context while tool is executing
	cancel()
	// Unblock the tool
	close(toolBlocked)

	// Wait for orchestrator to finish
	err := <-errCh
	if err != context.Canceled {
		t.Errorf("expected context.Canceled error, got %v", err)
	}

	// Verify error event was published
	var gotError bool
	for event := range events {
		if event.Type == orchestrator.EventError {
			gotError = true
			if event.Error != context.Canceled {
				t.Errorf("expected context.Canceled in event, got %v", event.Error)
			}
		}
	}
	if !gotError {
		t.Error("expected error event")
	}
}

// TestContextCancellationBeforeToolExecution tests cancellation before tool starts.
func TestContextCancellationBeforeToolExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

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
		},
	}

	registry := tool.NewRegistry()
	testTool := &mockTool{name: "test_tool"}
	registry.Register(testTool)
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	err := orch.Run(ctx, "Use tool")
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// Consume events
	for range events {
	}
}

// TestToolExecutionTimeout tests handling of tool execution timeouts.
func TestToolExecutionTimeout(t *testing.T) {
	ctx := context.Background()

	client := &mockLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type:  llm.ContentTypeToolUse,
					ID:    "tool_1",
					Name:  "timeout_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Handled timeout"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	registry := tool.NewRegistry()
	timeoutTool := &mockTool{
		name: "timeout_tool",
		execFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return nil, context.DeadlineExceeded
		},
	}
	registry.Register(timeoutTool)
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	err := orch.Run(ctx, "Use timeout tool")
	if err != nil {
		t.Fatalf("orchestrator should handle tool timeout gracefully: %v", err)
	}

	// Verify we got a tool result with error
	var gotErrorResult bool
	for event := range events {
		if event.Type == orchestrator.EventToolResult {
			if event.Result != nil && !event.Result.Success {
				gotErrorResult = true
			}
		}
	}
	if !gotErrorResult {
		t.Error("expected error tool result event")
	}
}

// TestRecoveryFromFailedToolExecution tests that orchestrator continues after tool failure.
func TestRecoveryFromFailedToolExecution(t *testing.T) {
	ctx := context.Background()

	client := &mockLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type:  llm.ContentTypeToolUse,
					ID:    "tool_1",
					Name:  "failing_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Recovered from failure"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	registry := tool.NewRegistry()
	failingTool := &mockTool{
		name: "failing_tool",
		execFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return nil, fmt.Errorf("tool execution failed")
		},
	}
	registry.Register(failingTool)
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	err := orch.Run(ctx, "Use failing tool")
	if err != nil {
		t.Fatalf("orchestrator should recover from tool failure: %v", err)
	}

	var gotToolError, gotComplete bool
	for event := range events {
		switch event.Type {
		case orchestrator.EventToolResult:
			if event.Result != nil && !event.Result.Success {
				gotToolError = true
			}
		case orchestrator.EventComplete:
			gotComplete = true
		}
	}

	if !gotToolError {
		t.Error("expected error tool result")
	}
	if !gotComplete {
		t.Error("expected orchestrator to complete after recovery")
	}
}

// TestMaxIterationLimit tests that orchestrator respects max iterations.
func TestMaxIterationLimit(t *testing.T) {
	ctx := context.Background()

	// Create a client that always returns tool use (infinite loop scenario)
	loopingClient := &mockLoopingLLMClient{}

	registry := tool.NewRegistry()
	loopTool := &mockTool{name: "loop_tool"}
	registry.Register(loopTool)
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.MaxIterations = 3
	orch := orchestrator.NewWithConfig(loopingClient, executor, config)
	events := orch.Subscribe()

	err := orch.Run(ctx, "Start infinite loop")
	if err == nil {
		t.Fatal("expected max iterations error")
	}
	if !contains(err.Error(), "exceeded max iterations") {
		t.Errorf("expected 'exceeded max iterations' error, got: %v", err)
	}

	// Count how many tool calls happened
	toolCallCount := 0
	for event := range events {
		if event.Type == orchestrator.EventToolCall {
			toolCallCount++
		}
	}

	if toolCallCount != 3 {
		t.Errorf("expected 3 tool calls (max iterations), got %d", toolCallCount)
	}
}

// TestMaxIterationLimitEdgeCaseZero tests behavior with zero max iterations.
func TestMaxIterationLimitEdgeCaseZero(t *testing.T) {
	ctx := context.Background()

	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Hello"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}

	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.MaxIterations = 0
	orch := orchestrator.NewWithConfig(client, executor, config)
	events := orch.Subscribe()

	err := orch.Run(ctx, "Test zero iterations")
	if err == nil {
		t.Fatal("expected max iterations error")
	}

	// Consume events
	for range events {
	}
}

// TestEventBusCleanup tests that event bus properly cleans up subscribers.
func TestEventBusCleanup(t *testing.T) {
	bus := orchestrator.NewEventBus()

	// Subscribe multiple times
	sub1 := bus.Subscribe()
	sub2 := bus.Subscribe()
	sub3 := bus.Subscribe()

	// Publish an event
	bus.Publish(orchestrator.NewTextEvent("test"))

	// All subscribers should receive it
	for i, sub := range []<-chan orchestrator.Event{sub1, sub2, sub3} {
		select {
		case event := <-sub:
			if event.Text != "test" {
				t.Errorf("subscriber %d: expected 'test', got %q", i, event.Text)
			}
		default:
			t.Errorf("subscriber %d: expected to receive event", i)
		}
	}

	// Close the bus
	bus.Close()

	// All channels should be closed
	for i, sub := range []<-chan orchestrator.Event{sub1, sub2, sub3} {
		if _, ok := <-sub; ok {
			t.Errorf("subscriber %d: channel should be closed", i)
		}
	}

	// Publishing after close should not panic
	bus.Publish(orchestrator.NewTextEvent("after close"))

	// Subscribing after close should return closed channel
	sub4 := bus.Subscribe()
	if _, ok := <-sub4; ok {
		t.Error("subscription after close should return closed channel")
	}
}

// TestEventBusMultipleClose tests that multiple Close calls don't panic.
func TestEventBusMultipleClose(t *testing.T) {
	bus := orchestrator.NewEventBus()
	sub := bus.Subscribe()

	bus.Close()
	bus.Close() // Should not panic

	if _, ok := <-sub; ok {
		t.Error("channel should be closed")
	}
}

// TestEventBusSlowSubscriber tests that slow subscribers don't block others.
func TestEventBusSlowSubscriber(t *testing.T) {
	bus := orchestrator.NewEventBus()

	// Create a subscriber but don't read from it (simulating slow consumer)
	_ = bus.Subscribe()

	// Create a normal subscriber
	fastSub := bus.Subscribe()

	// Publish many events (more than buffer size of 100)
	for i := 0; i < 200; i++ {
		bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
	}

	// Fast subscriber should still receive events (non-blocking)
	eventCount := 0
	for {
		select {
		case <-fastSub:
			eventCount++
		default:
			goto done
		}
	}
done:

	if eventCount == 0 {
		t.Error("fast subscriber should have received some events")
	}

	bus.Close()
}

// TestConcurrentOrchestratorExecution tests multiple concurrent orchestrator runs.
func TestConcurrentOrchestratorExecution(t *testing.T) {
	const numGoroutines = 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			client := &mockLLMClient{
				responses: []*llm.Response{{
					Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: fmt.Sprintf("Response %d", id)}},
					StopReason: llm.StopReasonEndTurn,
				}},
			}

			registry := tool.NewRegistry()
			executor := tool.NewExecutor(registry)
			orch := orchestrator.New(client, executor)
			events := orch.Subscribe()

			ctx := context.Background()
			err := orch.Run(ctx, fmt.Sprintf("Prompt %d", id))
			if err != nil {
				t.Errorf("goroutine %d: unexpected error: %v", id, err)
			}

			// Consume all events
			for range events {
			}
		}(i)
	}

	wg.Wait()
}

// TestLLMStreamInterruption tests handling of stream interruption.
func TestLLMStreamInterruption(t *testing.T) {
	ctx := context.Background()

	// Client that returns an error immediately
	errorClient := &mockLLMClientWithError{
		err: fmt.Errorf("stream interrupted"),
	}

	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)
	orch := orchestrator.New(errorClient, executor)
	events := orch.Subscribe()

	err := orch.Run(ctx, "Test interruption")
	if err == nil {
		t.Fatal("expected error from stream interruption")
	}
	if !contains(err.Error(), "stream interrupted") {
		t.Errorf("expected 'stream interrupted' error, got: %v", err)
	}

	// Verify error event
	var gotError bool
	for event := range events {
		if event.Type == orchestrator.EventError {
			gotError = true
		}
	}
	if !gotError {
		t.Error("expected error event")
	}
}

// TestEventBusSubscriberManagement tests dynamic subscriber management.
func TestEventBusSubscriberManagement(t *testing.T) {
	bus := orchestrator.NewEventBus()

	// Add subscribers dynamically
	subs := make([]<-chan orchestrator.Event, 0)
	for i := 0; i < 5; i++ {
		subs = append(subs, bus.Subscribe())
	}

	// Publish event
	bus.Publish(orchestrator.NewTextEvent("broadcast"))

	// All should receive
	for i, sub := range subs {
		select {
		case event := <-sub:
			if event.Text != "broadcast" {
				t.Errorf("subscriber %d: expected 'broadcast', got %q", i, event.Text)
			}
		default:
			t.Errorf("subscriber %d: expected to receive event", i)
		}
	}

	// Add more subscribers after publishing
	newSub := bus.Subscribe()

	// Publish another event
	bus.Publish(orchestrator.NewTextEvent("second"))

	// New subscriber should receive it
	select {
	case event := <-newSub:
		if event.Text != "second" {
			t.Errorf("new subscriber: expected 'second', got %q", event.Text)
		}
	default:
		t.Error("new subscriber: expected to receive event")
	}

	bus.Close()
}

// mockLLMClientWithError always returns an error.
type mockLLMClientWithError struct {
	err error
}

func (m *mockLLMClientWithError) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return nil, m.err
}

func (m *mockLLMClientWithError) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, m.err
}

func (m *mockLLMClientWithError) Capabilities() llm.Capabilities { return llm.FullCapabilities() }

// mockLoopingLLMClient always returns tool use to test max iterations.
type mockLoopingLLMClient struct{}

func (m *mockLoopingLLMClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Content: []llm.ContentBlock{{
			Type:  llm.ContentTypeToolUse,
			ID:    "tool_loop",
			Name:  "loop_tool",
			Input: map[string]any{},
		}},
		StopReason: llm.StopReasonToolUse,
	}, nil
}

func (m *mockLoopingLLMClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		resp, _ := m.CreateMessage(ctx, req)
		ch <- llm.StreamEvent{Type: llm.EventMessageStop, Response: resp}
		close(ch)
	}()
	return ch, nil
}

func (m *mockLoopingLLMClient) Capabilities() llm.Capabilities { return llm.FullCapabilities() }

// mockDynamicLLMClient returns responses from a function for dynamic testing.
type mockDynamicLLMClient struct {
	responseFn func() *llm.Response
}

func (m *mockDynamicLLMClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return m.responseFn(), nil
}

func (m *mockDynamicLLMClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		resp := m.responseFn()
		ch <- llm.StreamEvent{Type: llm.EventMessageStop, Response: resp}
		close(ch)
	}()
	return ch, nil
}

func (m *mockDynamicLLMClient) Capabilities() llm.Capabilities { return llm.FullCapabilities() }

// TestEventBusSlowSubscriberBufferOverflow tests that slow subscribers don't block
// publishing and that events are dropped when the buffer overflows.
func TestEventBusSlowSubscriberBufferOverflow(t *testing.T) {
	bus := orchestrator.NewEventBus()

	// Create a slow subscriber that doesn't read from its channel
	slowSub := bus.Subscribe()

	// Create a fast subscriber that reads immediately
	fastSub := bus.Subscribe()

	// Publish more events than the buffer size (buffer is 100)
	const eventCount = 200
	for i := 0; i < eventCount; i++ {
		bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
	}

	// Fast subscriber should receive events
	fastEvents := make([]orchestrator.Event, 0)
	for {
		select {
		case event := <-fastSub:
			fastEvents = append(fastEvents, event)
		default:
			goto fastDone
		}
	}
fastDone:

	if len(fastEvents) == 0 {
		t.Error("fast subscriber should have received events")
	}

	// Slow subscriber should have exactly buffer size (100) events waiting
	slowEvents := make([]orchestrator.Event, 0)
	for {
		select {
		case event := <-slowSub:
			slowEvents = append(slowEvents, event)
		default:
			goto slowDone
		}
	}
slowDone:

	if len(slowEvents) != 100 {
		t.Errorf("slow subscriber should have exactly 100 buffered events, got %d", len(slowEvents))
	}

	// Verify no blocking occurred - test should complete quickly
	bus.Close()
}

// TestEventBusSubscriberRemovalDuringPublishing tests concurrent subscriber
// operations while events are being published.
func TestEventBusSubscriberRemovalDuringPublishing(t *testing.T) {
	bus := orchestrator.NewEventBus()

	// Create initial subscribers
	sub1 := bus.Subscribe()
	sub2 := bus.Subscribe()
	sub3 := bus.Subscribe()

	// Start publishing events in a goroutine
	publishDone := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
		}
		close(publishDone)
	}()

	// Add new subscribers concurrently while publishing
	var wg sync.WaitGroup
	wg.Add(5)
	newSubs := make([]<-chan orchestrator.Event, 5)
	for i := 0; i < 5; i++ {
		go func(idx int) {
			defer wg.Done()
			newSubs[idx] = bus.Subscribe()
		}(i)
	}
	wg.Wait()

	// Wait for publishing to complete
	<-publishDone

	// Verify all subscribers received at least some events
	allSubs := []<-chan orchestrator.Event{sub1, sub2, sub3}
	allSubs = append(allSubs, newSubs...)

	for i, sub := range allSubs {
		eventCount := 0
		for {
			select {
			case <-sub:
				eventCount++
			default:
				goto done
			}
		}
	done:
		if i < 3 && eventCount == 0 {
			t.Errorf("original subscriber %d should have received events", i)
		}
		// New subscribers may or may not receive events depending on timing
	}

	bus.Close()
}

// TestEventBusEventOrderingGuarantees tests that events are received in the order
// they are published for non-blocked subscribers.
func TestEventBusEventOrderingGuarantees(t *testing.T) {
	bus := orchestrator.NewEventBus()

	// Create multiple subscribers
	sub1 := bus.Subscribe()
	sub2 := bus.Subscribe()
	sub3 := bus.Subscribe()

	// Publish events in a specific order
	const eventCount = 50
	for i := 0; i < eventCount; i++ {
		bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
	}

	// Verify all subscribers receive events in order
	subs := []<-chan orchestrator.Event{sub1, sub2, sub3}
	for subIdx, sub := range subs {
		receivedEvents := make([]string, 0)
		for i := 0; i < eventCount; i++ {
			select {
			case event := <-sub:
				receivedEvents = append(receivedEvents, event.Text)
			default:
				t.Fatalf("subscriber %d: expected event %d but channel was empty", subIdx, i)
			}
		}

		// Verify order
		for i, text := range receivedEvents {
			expected := fmt.Sprintf("event_%d", i)
			if text != expected {
				t.Errorf("subscriber %d: event %d out of order, expected %s got %s", subIdx, i, expected, text)
			}
		}
	}

	bus.Close()
}

// TestEventBusEventOrderingWithSlowSubscriber tests event ordering when some
// subscribers are slow and drop events.
func TestEventBusEventOrderingWithSlowSubscriber(t *testing.T) {
	bus := orchestrator.NewEventBus()

	// Create a slow subscriber (never reads)
	_ = bus.Subscribe()

	// Create a fast subscriber
	fastSub := bus.Subscribe()

	// Publish events
	const eventCount = 150 // More than buffer size
	for i := 0; i < eventCount; i++ {
		bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
	}

	// Fast subscriber should receive events in order (whatever it receives)
	receivedEvents := make([]string, 0)
	for {
		select {
		case event := <-fastSub:
			receivedEvents = append(receivedEvents, event.Text)
		default:
			goto done
		}
	}
done:

	if len(receivedEvents) == 0 {
		t.Fatal("fast subscriber should have received some events")
	}

	// Verify the events received are in order
	for i := 0; i < len(receivedEvents)-1; i++ {
		current := receivedEvents[i]
		next := receivedEvents[i+1]

		// Extract event numbers
		var currentNum, nextNum int
		fmt.Sscanf(current, "event_%d", &currentNum)
		fmt.Sscanf(next, "event_%d", &nextNum)

		if nextNum <= currentNum {
			t.Errorf("events out of order: %s followed by %s", current, next)
		}
	}

	bus.Close()
}

// TestEventBusMemoryCleanupForClosedSubscribers tests that closing the bus
// properly cleans up all subscriber channels.
func TestEventBusMemoryCleanupForClosedSubscribers(t *testing.T) {
	bus := orchestrator.NewEventBus()

	// Create many subscribers
	const subCount = 100
	subs := make([]<-chan orchestrator.Event, subCount)
	for i := 0; i < subCount; i++ {
		subs[i] = bus.Subscribe()
	}

	// Publish some events
	for i := 0; i < 10; i++ {
		bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
	}

	// Close the bus
	bus.Close()

	// Verify all subscriber channels are eventually closed (after draining buffered events)
	for i, sub := range subs {
		// Drain any buffered events first, then verify the channel is closed
		eventCount := 0
		for event := range sub {
			eventCount++
			_ = event
		}
		// Each subscriber should have received the 10 published events
		if eventCount != 10 {
			t.Errorf("subscriber %d: expected 10 buffered events, got %d", i, eventCount)
		}
	}

	// Verify new subscriptions after close return closed channels
	newSub := bus.Subscribe()
	_, ok := <-newSub
	if ok {
		t.Error("new subscription after close should return closed channel")
	}
}

// TestEventBusMemoryCleanupWithPartialConsumption tests cleanup when subscribers
// have only partially consumed their buffered events.
func TestEventBusMemoryCleanupWithPartialConsumption(t *testing.T) {
	bus := orchestrator.NewEventBus()

	// Create subscribers
	sub1 := bus.Subscribe()
	sub2 := bus.Subscribe()
	sub3 := bus.Subscribe()

	// Publish many events
	for i := 0; i < 100; i++ {
		bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
	}

	// Partially consume from sub1 only
	for i := 0; i < 10; i++ {
		<-sub1
	}

	// Close the bus - this should close all channels even if events are buffered
	bus.Close()

	// All channels should be closed now
	for i, sub := range []<-chan orchestrator.Event{sub1, sub2, sub3} {
		// Drain the channel and verify it eventually closes
		eventCount := 0
		for event := range sub {
			eventCount++
			_ = event
		}
		// sub1 should have consumed 10, so 90 remain
		// sub2 and sub3 should have 100 each
		if i == 0 && eventCount != 90 {
			t.Errorf("sub1: expected 90 remaining events, got %d", eventCount)
		} else if i > 0 && eventCount != 100 {
			t.Errorf("sub%d: expected 100 events, got %d", i+1, eventCount)
		}
	}
}

// TestEventBusConcurrentPublishAndClose tests that concurrent publishing and
// closing doesn't cause panics or data races.
func TestEventBusConcurrentPublishAndClose(t *testing.T) {
	const iterations = 10
	for iter := 0; iter < iterations; iter++ {
		bus := orchestrator.NewEventBus()

		// Create subscribers
		sub1 := bus.Subscribe()
		sub2 := bus.Subscribe()

		// Start publishing in goroutines
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
			}
		}()

		go func() {
			defer wg.Done()
			for i := 50; i < 100; i++ {
				bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
			}
		}()

		// Concurrently close the bus
		go func() {
			bus.Close()
		}()

		wg.Wait()

		// Drain any remaining events
		for range sub1 {
		}
		for range sub2 {
		}
	}
}

// TestEventBusNoSubscribers tests that publishing with no subscribers doesn't panic.
func TestEventBusNoSubscribers(t *testing.T) {
	bus := orchestrator.NewEventBus()

	// Publish events with no subscribers
	for i := 0; i < 100; i++ {
		bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
	}

	bus.Close()
}

// TestEventBusConcurrentSubscribers tests that many concurrent subscribers work correctly.
func TestEventBusConcurrentSubscribers(t *testing.T) {
	bus := orchestrator.NewEventBus()

	const subCount = 50
	subs := make([]<-chan orchestrator.Event, subCount)
	var wg sync.WaitGroup
	wg.Add(subCount)

	// Create subscribers concurrently
	for i := 0; i < subCount; i++ {
		go func(idx int) {
			defer wg.Done()
			subs[idx] = bus.Subscribe()
		}(i)
	}
	wg.Wait()

	// Publish events
	const eventCount = 20
	for i := 0; i < eventCount; i++ {
		bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
	}

	// Verify all subscribers received all events
	for i, sub := range subs {
		received := 0
		for {
			select {
			case <-sub:
				received++
			default:
				goto done
			}
		}
	done:
		if received != eventCount {
			t.Errorf("subscriber %d: expected %d events, got %d", i, eventCount, received)
		}
	}

	bus.Close()
}

// TestEventBusClosedBusPublish tests that publishing to a closed bus doesn't panic.
func TestEventBusClosedBusPublish(t *testing.T) {
	bus := orchestrator.NewEventBus()
	sub := bus.Subscribe()

	bus.Close()

	// Publishing after close should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("publishing to closed bus caused panic: %v", r)
		}
	}()

	for i := 0; i < 10; i++ {
		bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d", i)))
	}

	// Channel should be closed with no new events
	eventCount := 0
	for range sub {
		eventCount++
	}
	if eventCount != 0 {
		t.Errorf("expected 0 events after close, got %d", eventCount)
	}
}

// TestEventBusRaceConditions tests for race conditions using -race flag.
// This test is designed to be run with: go test -race
func TestEventBusRaceConditions(t *testing.T) {
	bus := orchestrator.NewEventBus()

	var publisherWg sync.WaitGroup
	const goroutines = 10

	// Concurrent publishers
	publisherWg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer publisherWg.Done()
			for j := 0; j < 10; j++ {
				bus.Publish(orchestrator.NewTextEvent(fmt.Sprintf("event_%d_%d", idx, j)))
			}
		}(i)
	}

	// Concurrent subscribers that drain events
	var subscriberWg sync.WaitGroup
	subscriberWg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer subscriberWg.Done()
			sub := bus.Subscribe()
			// Drain events until channel is closed
			for range sub {
			}
		}()
	}

	// Wait for all publishers to finish
	publisherWg.Wait()

	// Close the bus to signal subscribers
	bus.Close()

	// Wait for all subscribers to finish draining
	subscriberWg.Wait()
}

func TestOrchestratorThinkingEvent(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content: []llm.ContentBlock{
				{Type: llm.ContentTypeThinking, Thinking: "Let me reason about this..."},
				{Type: llm.ContentTypeText, Text: "Hello!"},
			},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	ctx := context.Background()
	err := orch.Run(ctx, "Think about something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hasThinking bool
	var thinkingText string
	for event := range events {
		if event.Type == orchestrator.EventThinking {
			hasThinking = true
			thinkingText = event.Thinking
		}
	}

	if !hasThinking {
		t.Error("expected thinking event")
	}
	if thinkingText != "Let me reason about this..." {
		t.Errorf("thinking text = %q, want 'Let me reason about this...'", thinkingText)
	}
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// capturingLLMClient records the requests it receives for assertion.
type capturingLLMClient struct {
	requests  []*llm.Request
	responses []*llm.Response
	callIndex int
	mu        sync.Mutex
}

func (c *capturingLLMClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	if c.callIndex >= len(c.responses) {
		return &llm.Response{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		}, nil
	}
	resp := c.responses[c.callIndex]
	c.callIndex++
	return resp, nil
}

func (c *capturingLLMClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		resp, _ := c.CreateMessage(ctx, req)
		ch <- llm.StreamEvent{Type: llm.EventMessageStop, Response: resp}
		close(ch)
	}()
	return ch, nil
}

func (c *capturingLLMClient) Capabilities() llm.Capabilities { return llm.FullCapabilities() }

func TestThinkingStrategyOff(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)
	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy: orchestrator.ThinkingOff,
		Budget:   8192,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()
	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(client.requests))
	}
	if client.requests[0].Thinking != nil {
		t.Error("ThinkingOff: expected Thinking to be nil")
	}
}

func TestThinkingStrategyAlways(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type: llm.ContentTypeToolUse, ID: "t1", Name: "test_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "test_tool"})
	executor := tool.NewExecutor(registry)
	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy: orchestrator.ThinkingAlways,
		Budget:   8192,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()
	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(client.requests))
	}
	for i, req := range client.requests {
		if req.Thinking == nil {
			t.Errorf("request %d: ThinkingAlways: expected Thinking to be set", i)
		} else if req.Thinking.Budget != 8192 {
			t.Errorf("request %d: Budget = %d, want 8192", i, req.Thinking.Budget)
		}
	}
}

func TestThinkingStrategyFirstOnly(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type: llm.ContentTypeToolUse, ID: "t1", Name: "test_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "test_tool"})
	executor := tool.NewExecutor(registry)
	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy: orchestrator.ThinkingFirstOnly,
		Budget:   8192,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()
	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(client.requests))
	}
	if client.requests[0].Thinking == nil {
		t.Error("request 0: ThinkingFirstOnly: expected Thinking to be set")
	}
	if client.requests[1].Thinking != nil {
		t.Error("request 1: ThinkingFirstOnly: expected Thinking to be nil")
	}
}

func TestThinkingStrategyAdaptiveFirstCall(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type: llm.ContentTypeToolUse, ID: "t1", Name: "test_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "test_tool"})
	executor := tool.NewExecutor(registry)
	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy:                 orchestrator.ThinkingAdaptive,
		Budget:                   8192,
		ConsecutiveToolThreshold: 5,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()
	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.requests[0].Thinking == nil {
		t.Error("request 0: Adaptive: expected Thinking on first call")
	}
	if client.requests[1].Thinking != nil {
		t.Error("request 1: Adaptive: expected no Thinking on second call")
	}
}

func TestThinkingStrategyAdaptiveToolError(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type: llm.ContentTypeToolUse, ID: "t1", Name: "error_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{
		name: "error_tool",
		execFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return nil, fmt.Errorf("something went wrong")
		},
	})
	executor := tool.NewExecutor(registry)
	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy:                 orchestrator.ThinkingAdaptive,
		Budget:                   8192,
		ConsecutiveToolThreshold: 5,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()
	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(client.requests))
	}
	if client.requests[0].Thinking == nil {
		t.Error("request 0: expected Thinking on first call")
	}
	if client.requests[1].Thinking == nil {
		t.Error("request 1: expected Thinking re-enabled after tool error")
	}
}

func TestThinkingStrategyAdaptiveConsecutiveTools(t *testing.T) {
	responses := make([]*llm.Response, 0, 7)
	for i := 0; i < 6; i++ {
		responses = append(responses, &llm.Response{
			Content: []llm.ContentBlock{{
				Type: llm.ContentTypeToolUse, ID: fmt.Sprintf("t%d", i), Name: "test_tool",
				Input: map[string]any{},
			}},
			StopReason: llm.StopReasonToolUse,
		})
	}
	responses = append(responses, &llm.Response{
		Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		StopReason: llm.StopReasonEndTurn,
	})
	client := &capturingLLMClient{responses: responses}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "test_tool"})
	executor := tool.NewExecutor(registry)
	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy:                 orchestrator.ThinkingAdaptive,
		Budget:                   8192,
		ConsecutiveToolThreshold: 3,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()
	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.requests) != 7 {
		t.Fatalf("expected 7 requests, got %d", len(client.requests))
	}
	// Request 0: thinking (first call)
	// Request 1: no thinking (1 consecutive tool)
	// Request 2: no thinking (2 consecutive tools)
	// Request 3: thinking (3 consecutive tools >= threshold)
	// Request 4: thinking (4 >= threshold)
	// Request 5: thinking (5 >= threshold)
	// Request 6: thinking (6 >= threshold)
	expected := []bool{true, false, false, true, true, true, true}
	for i, req := range client.requests {
		hasThinking := req.Thinking != nil
		if hasThinking != expected[i] {
			t.Errorf("request %d: thinking=%v, want %v", i, hasThinking, expected[i])
		}
	}
}

func TestThinkingNilSettings(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)
	orch := orchestrator.New(client, executor)
	_ = orch.Subscribe()
	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.requests[0].Thinking != nil {
		t.Error("nil ThinkingSettings: expected Thinking to be nil")
	}
}

// TestOrchestratorLockContract pins the orchestrator's locking contract so a
// future lock rework cannot silently change it: SetMessages/Messages round-trip,
// Run replaces history, Continue appends, and concurrent Run calls on a single
// instance are serialized by the lock (no data race under -race).
func TestOrchestratorLockContract(t *testing.T) {
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	// A mockLLMClient with no canned responses is stateless: it always returns a
	// terminal "done" response, so each Run/Continue settles in one iteration.
	orch := orchestrator.New(&mockLLMClient{}, executor)

	// 1. SetMessages -> Messages round-trips.
	seed := []llm.Message{
		llm.NewUserMessage("seed-1"),
		llm.NewUserMessage("seed-2"),
	}
	orch.SetMessages(seed)
	got := orch.Messages()
	if len(got) != 2 || got[0].Content != "seed-1" || got[1].Content != "seed-2" {
		t.Fatalf("SetMessages/Messages round-trip failed: %+v", got)
	}

	// 2. Run replaces history with a fresh prompt.
	if err := orch.Run(context.Background(), "fresh-run"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	afterRun := orch.Messages()
	if len(afterRun) == 0 || afterRun[0].Role != llm.RoleUser || afterRun[0].Content != "fresh-run" {
		t.Fatalf("Run did not start fresh with the new prompt: %+v", afterRun)
	}
	for _, m := range afterRun {
		if m.Content == "seed-1" || m.Content == "seed-2" {
			t.Fatalf("Run did not replace prior history; a seed message survived: %+v", afterRun)
		}
	}

	// 3. Continue appends to existing history.
	beforeLen := len(orch.Messages())
	if err := orch.Continue(context.Background(), "continued"); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	afterContinue := orch.Messages()
	if len(afterContinue) <= beforeLen {
		t.Fatalf("Continue did not append: before=%d after=%d", beforeLen, len(afterContinue))
	}
	foundContinued := false
	for _, m := range afterContinue {
		if m.Role == llm.RoleUser && m.Content == "continued" {
			foundContinued = true
		}
	}
	if !foundContinued {
		t.Fatalf("Continue did not append the new user prompt: %+v", afterContinue)
	}

	// 4. Concurrent Run on the SAME instance is serialized by the lock: no data
	// race under -race, and the final history is a valid single-run result.
	creg := tool.NewRegistry()
	cexec := tool.NewExecutor(creg)
	concurrent := orchestrator.New(&mockLLMClient{}, cexec)
	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			if err := concurrent.Run(context.Background(), fmt.Sprintf("run-%d", id)); err != nil {
				t.Errorf("concurrent Run %d: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	final := concurrent.Messages()
	if len(final) == 0 || final[0].Role != llm.RoleUser {
		t.Fatalf("concurrent runs left inconsistent history (lock not serializing?): %+v", final)
	}
}

func TestNewWithConfig_SuspendRequiresStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when ApprovalSuspend has no SessionStore")
		} else if r != "mux: ApprovalSuspend requires a SessionStore" {
			t.Fatalf("wrong panic value: %v", r)
		}
	}()
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)
	orchestrator.NewWithConfig(&mockLLMClient{}, executor, orchestrator.Config{
		MaxIterations: 5,
		ApprovalMode:  orchestrator.ApprovalSuspend,
		// SessionStore intentionally nil
	})
}

func TestNewWithConfig_DefaultsDoNotPanic(t *testing.T) {
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)
	// ApprovalSync (zero value) + nil store must remain today's behavior: no panic.
	_ = orchestrator.NewWithConfig(&mockLLMClient{}, executor, orchestrator.Config{MaxIterations: 5})
}

func TestThinkingAdaptiveFullCycle(t *testing.T) {
	// Scenario:
	// Call 0: first call → thinking ON (iteration 0)
	// Call 1: tool success → thinking OFF (1 consecutive)
	// Call 2: tool error → thinking ON next call (error trigger)
	// Call 3: thinking ON (re-enabled by error) → done

	responses := []*llm.Response{
		// Call 0 → tool use (success)
		{
			Content: []llm.ContentBlock{{
				Type: llm.ContentTypeToolUse, ID: "t0", Name: "good_tool",
				Input: map[string]any{},
			}},
			StopReason: llm.StopReasonToolUse,
		},
		// Call 1 → tool use (will error)
		{
			Content: []llm.ContentBlock{{
				Type: llm.ContentTypeToolUse, ID: "t1", Name: "bad_tool",
				Input: map[string]any{},
			}},
			StopReason: llm.StopReasonToolUse,
		},
		// Call 2 → tool use (success, after error re-enable)
		{
			Content: []llm.ContentBlock{{
				Type: llm.ContentTypeToolUse, ID: "t2", Name: "good_tool",
				Input: map[string]any{},
			}},
			StopReason: llm.StopReasonToolUse,
		},
		// Call 3 → done
		{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		},
	}

	client := &capturingLLMClient{responses: responses}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "good_tool"})
	registry.Register(&mockTool{
		name: "bad_tool",
		execFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return nil, fmt.Errorf("tool failed")
		},
	})
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy:                 orchestrator.ThinkingAdaptive,
		Budget:                   8192,
		ConsecutiveToolThreshold: 5,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()

	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.requests) != 4 {
		t.Fatalf("expected 4 requests, got %d", len(client.requests))
	}

	// Call 0: thinking ON (first call)
	// Call 1: thinking OFF (1 consecutive tool, no error)
	// Call 2: thinking ON (previous tool had error)
	// Call 3: thinking OFF (no error from previous, 1 consecutive tool)
	expected := []bool{true, false, true, false}
	for i, req := range client.requests {
		hasThinking := req.Thinking != nil
		if hasThinking != expected[i] {
			t.Errorf("request %d: thinking=%v, want %v", i, hasThinking, expected[i])
		}
	}
}

func TestRun_CheckpointsWithStore(t *testing.T) {
	// One tool round-trip then end_turn. A store is configured in the default
	// ApprovalSync mode: the loop must persist a running checkpoint after the
	// tool batch and a complete checkpoint at the end.
	store := session.NewFileStore(t.TempDir())
	registry := tool.NewRegistry()
	executed := false
	registry.Register(&mockTool{name: "noop", execFunc: func(_ context.Context, _ map[string]any) (*tool.Result, error) {
		executed = true
		return tool.NewResult("noop", true, "done", ""), nil
	}})
	executor := tool.NewExecutor(registry)
	client := &mockLLMClient{responses: []*llm.Response{
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "u1", Name: "noop", Input: map[string]any{}}}, StopReason: llm.StopReasonToolUse},
		{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "all done"}}, StopReason: llm.StopReasonEndTurn},
	}}
	orch := orchestrator.NewWithConfig(client, executor, orchestrator.Config{
		MaxIterations: 5,
		SessionStore:  store,
	})
	if err := orch.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !executed {
		t.Fatal("tool was not executed")
	}
	snap, err := store.Load(context.Background(), orch.SessionID())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snap.Status != orchestrator.StatusComplete {
		t.Errorf("final snapshot Status = %q, want %q", snap.Status, orchestrator.StatusComplete)
	}
}
