// ABOUTME: Implements background agent execution with async result retrieval.
// ABOUTME: Provides RunAsync, status tracking, and result polling/waiting.
package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// RunStatus represents the current state of an async agent run.
type RunStatus int32

const (
	RunStatusPending RunStatus = iota
	RunStatusRunning
	RunStatusCompleted
	RunStatusFailed
	RunStatusCancelled
)

func (s RunStatus) String() string {
	switch s {
	case RunStatusPending:
		return "pending"
	case RunStatusRunning:
		return "running"
	case RunStatusCompleted:
		return "completed"
	case RunStatusFailed:
		return "failed"
	case RunStatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// RunHandle provides access to a background agent execution.
type RunHandle struct {
	agent     *Agent
	status    int32 // atomic RunStatus
	err       error
	done      chan struct{}
	startTime time.Time
	endTime   time.Time
	mu        sync.RWMutex
}

// Status returns the current execution status.
func (h *RunHandle) Status() RunStatus {
	return RunStatus(atomic.LoadInt32(&h.status))
}

// Err returns the error from execution, if any.
// Returns nil if still running or completed successfully.
func (h *RunHandle) Err() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.err
}

// Done returns a channel that closes when execution completes.
func (h *RunHandle) Done() <-chan struct{} {
	return h.done
}

// Wait blocks until execution completes and returns the error (if any).
func (h *RunHandle) Wait() error {
	<-h.done
	return h.Err()
}

// WaitWithTimeout blocks until execution completes or timeout expires.
// Returns context.DeadlineExceeded if timeout is reached.
func (h *RunHandle) WaitWithTimeout(timeout time.Duration) error {
	select {
	case <-h.done:
		return h.Err()
	case <-time.After(timeout):
		return context.DeadlineExceeded
	}
}

// Poll returns the current status and error without blocking.
// If the agent is still running, err will be nil.
func (h *RunHandle) Poll() (RunStatus, error) {
	return h.Status(), h.Err()
}

// IsComplete returns true if the agent has finished (success, failure, or cancelled).
func (h *RunHandle) IsComplete() bool {
	status := h.Status()
	return status == RunStatusCompleted || status == RunStatusFailed || status == RunStatusCancelled
}

// Duration returns how long the agent has been running (or ran for if complete).
func (h *RunHandle) Duration() time.Duration {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.endTime.IsZero() {
		return time.Since(h.startTime)
	}
	return h.endTime.Sub(h.startTime)
}

// Agent returns the underlying agent.
func (h *RunHandle) Agent() *Agent {
	return h.agent
}

// Cancel attempts to cancel the running agent via context cancellation.
// This requires the original context to have been cancellable.
// Returns true if the agent was still running when cancel was called.
func (h *RunHandle) Cancel() bool {
	status := h.Status()
	if status == RunStatusPending || status == RunStatusRunning {
		atomic.CompareAndSwapInt32(&h.status, int32(status), int32(RunStatusCancelled))
		return true
	}
	return false
}

// setComplete updates the handle to completed state.
func (h *RunHandle) setComplete(err error) {
	h.mu.Lock()
	h.err = err
	h.endTime = time.Now()
	h.mu.Unlock()

	if err != nil {
		if err == context.Canceled {
			atomic.StoreInt32(&h.status, int32(RunStatusCancelled))
		} else {
			atomic.StoreInt32(&h.status, int32(RunStatusFailed))
		}
	} else {
		atomic.StoreInt32(&h.status, int32(RunStatusCompleted))
	}
	close(h.done)
}

// RunAsync executes the agent in the background and returns immediately.
// The returned RunHandle can be used to check status, wait for completion, or cancel.
func (a *Agent) RunAsync(ctx context.Context, prompt string) *RunHandle {
	handle := &RunHandle{
		agent:     a,
		status:    int32(RunStatusPending),
		done:      make(chan struct{}),
		startTime: time.Now(),
	}

	go func() {
		atomic.StoreInt32(&handle.status, int32(RunStatusRunning))
		err := a.Run(ctx, prompt)
		handle.setComplete(err)
	}()

	return handle
}

// ContinueAsync continues the agent in the background and returns immediately.
// Similar to RunAsync but uses Continue() which preserves conversation history.
func (a *Agent) ContinueAsync(ctx context.Context, prompt string) *RunHandle {
	handle := &RunHandle{
		agent:     a,
		status:    int32(RunStatusPending),
		done:      make(chan struct{}),
		startTime: time.Now(),
	}

	go func() {
		atomic.StoreInt32(&handle.status, int32(RunStatusRunning))
		err := a.Continue(ctx, prompt)
		handle.setComplete(err)
	}()

	return handle
}

// RunChildAsync runs a child agent in the background, firing SubagentStop when complete.
// Combines SpawnChild semantics with async execution.
func (a *Agent) RunChildAsync(ctx context.Context, child *Agent, prompt string) *RunHandle {
	handle := &RunHandle{
		agent:     child,
		status:    int32(RunStatusPending),
		done:      make(chan struct{}),
		startTime: time.Now(),
	}

	go func() {
		atomic.StoreInt32(&handle.status, int32(RunStatusRunning))
		err := a.RunChild(ctx, child, prompt)
		handle.setComplete(err)
	}()

	return handle
}
