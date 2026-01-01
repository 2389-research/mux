// ABOUTME: Tests for background agent execution - RunAsync, status tracking, waiting.
// ABOUTME: Validates async execution, completion detection, and timeout handling.
package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

func TestRunStatusString(t *testing.T) {
	tests := []struct {
		status   RunStatus
		expected string
	}{
		{RunStatusPending, "pending"},
		{RunStatusRunning, "running"},
		{RunStatusCompleted, "completed"},
		{RunStatusFailed, "failed"},
		{RunStatusCancelled, "cancelled"},
		{RunStatus(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.status.String(); got != tt.expected {
			t.Errorf("RunStatus(%d).String() = %q, want %q", tt.status, got, tt.expected)
		}
	}
}

func TestRunAsync_Success(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockLLMClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	agent := New(Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	handle := agent.RunAsync(context.Background(), "test prompt")

	// Should start as pending or running
	status := handle.Status()
	if status != RunStatusPending && status != RunStatusRunning {
		t.Errorf("initial status = %v, want pending or running", status)
	}

	// Wait for completion
	err := handle.Wait()
	if err != nil {
		t.Errorf("Wait() error = %v, want nil", err)
	}

	if handle.Status() != RunStatusCompleted {
		t.Errorf("final status = %v, want completed", handle.Status())
	}

	if !handle.IsComplete() {
		t.Error("IsComplete() = false, want true")
	}
}

func TestRunAsync_Failure(t *testing.T) {
	registry := tool.NewRegistry()
	expectedErr := errors.New("LLM error")
	client := &mockLLMClient{
		err: expectedErr,
	}

	agent := New(Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	handle := agent.RunAsync(context.Background(), "test prompt")

	err := handle.Wait()
	if err == nil {
		t.Error("Wait() error = nil, want error")
	}

	if handle.Status() != RunStatusFailed {
		t.Errorf("final status = %v, want failed", handle.Status())
	}
}

func TestRunAsync_Cancellation(t *testing.T) {
	registry := tool.NewRegistry()
	// Client that blocks until context is cancelled
	client := &mockLLMClient{
		blockUntilCancel: true,
	}

	agent := New(Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	ctx, cancel := context.WithCancel(context.Background())
	handle := agent.RunAsync(ctx, "test prompt")

	// Wait a bit for the agent to start
	time.Sleep(10 * time.Millisecond)

	// Cancel the context
	cancel()

	err := handle.Wait()
	if err != context.Canceled {
		t.Errorf("Wait() error = %v, want context.Canceled", err)
	}

	if handle.Status() != RunStatusCancelled {
		t.Errorf("final status = %v, want cancelled", handle.Status())
	}
}

func TestRunAsync_Poll(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockLLMClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	agent := New(Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	handle := agent.RunAsync(context.Background(), "test prompt")

	// Poll should not block
	status, _ := handle.Poll()
	if status != RunStatusPending && status != RunStatusRunning && status != RunStatusCompleted {
		t.Errorf("Poll() status = %v, unexpected", status)
	}

	// Wait for completion
	handle.Wait()

	var err error
	status, err = handle.Poll()
	if status != RunStatusCompleted {
		t.Errorf("Poll() after complete status = %v, want completed", status)
	}
	if err != nil {
		t.Errorf("Poll() after complete err = %v, want nil", err)
	}
}

func TestRunAsync_WaitWithTimeout(t *testing.T) {
	registry := tool.NewRegistry()
	// Client that blocks for a while
	client := &mockLLMClient{
		delay: 500 * time.Millisecond,
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	agent := New(Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	handle := agent.RunAsync(context.Background(), "test prompt")

	// Short timeout should fail
	err := handle.WaitWithTimeout(10 * time.Millisecond)
	if err != context.DeadlineExceeded {
		t.Errorf("WaitWithTimeout(short) = %v, want DeadlineExceeded", err)
	}

	// Long timeout should succeed
	err = handle.WaitWithTimeout(1 * time.Second)
	if err != nil {
		t.Errorf("WaitWithTimeout(long) = %v, want nil", err)
	}
}

func TestRunAsync_Done(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockLLMClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	agent := New(Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	handle := agent.RunAsync(context.Background(), "test prompt")

	// Done channel should close when complete
	select {
	case <-handle.Done():
		// Good
	case <-time.After(1 * time.Second):
		t.Error("Done() channel did not close within timeout")
	}
}

func TestRunAsync_Duration(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockLLMClient{
		delay: 50 * time.Millisecond,
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	agent := New(Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	handle := agent.RunAsync(context.Background(), "test prompt")
	handle.Wait()

	duration := handle.Duration()
	if duration < 50*time.Millisecond {
		t.Errorf("Duration() = %v, want >= 50ms", duration)
	}
}

func TestRunAsync_Agent(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockLLMClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	agent := New(Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	handle := agent.RunAsync(context.Background(), "test prompt")

	if handle.Agent() != agent {
		t.Error("Agent() did not return the correct agent")
	}
}

func TestContinueAsync(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockLLMClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	agent := New(Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	// First run
	handle1 := agent.RunAsync(context.Background(), "first")
	handle1.Wait()

	// Continue async
	handle2 := agent.ContinueAsync(context.Background(), "second")
	err := handle2.Wait()
	if err != nil {
		t.Errorf("ContinueAsync Wait() = %v, want nil", err)
	}

	if handle2.Status() != RunStatusCompleted {
		t.Errorf("ContinueAsync status = %v, want completed", handle2.Status())
	}
}

func TestRunChildAsync(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockLLMClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	parent := New(Config{
		Name:      "parent",
		Registry:  registry,
		LLMClient: client,
	})

	child, err := parent.SpawnChild(Config{Name: "child"})
	if err != nil {
		t.Fatalf("SpawnChild failed: %v", err)
	}

	handle := parent.RunChildAsync(context.Background(), child, "test prompt")

	err = handle.Wait()
	if err != nil {
		t.Errorf("RunChildAsync Wait() = %v, want nil", err)
	}

	if handle.Agent() != child {
		t.Error("RunChildAsync Agent() should return child agent")
	}
}

// mockLLMClient with additional control for async tests
type mockLLMClient struct {
	response         *llm.Response
	err              error
	delay            time.Duration
	blockUntilCancel bool
}

func (m *mockLLMClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if m.blockUntilCancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func (m *mockLLMClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}
