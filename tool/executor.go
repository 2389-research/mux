// ABOUTME: Implements the Executor - the permission-gated execution engine
// ABOUTME: that orchestrates tool calls with approval flows and hooks.
package tool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
)

var (
	ErrToolNotFound     = errors.New("tool not found")
	ErrApprovalDenied   = errors.New("tool execution denied")
	ErrApprovalRequired = errors.New("tool requires approval but no approval function set")
)

// ApprovalFunc is called when a tool requires approval before execution.
type ApprovalFunc func(ctx context.Context, t Tool, params map[string]any) (bool, error)

// BeforeHook is called before tool execution.
type BeforeHook func(ctx context.Context, toolName string, params map[string]any)

// AfterHook is called after tool execution.
type AfterHook func(ctx context.Context, toolName string, params map[string]any, result *Result, err error)

// Executor manages tool execution with permission checking and hooks.
type Executor struct {
	source       ToolSource
	approvalFunc ApprovalFunc
	beforeHooks  []BeforeHook
	afterHooks   []AfterHook
}

// NewExecutor creates a new Executor with the given Registry.
// Panics if registry is nil.
func NewExecutor(registry *Registry) *Executor {
	if registry == nil {
		panic("mux: registry must not be nil")
	}
	return &Executor{
		source:      registry,
		beforeHooks: make([]BeforeHook, 0),
		afterHooks:  make([]AfterHook, 0),
	}
}

// NewExecutorWithSource creates a new Executor with any ToolSource.
// Panics if source is nil.
func NewExecutorWithSource(source ToolSource) *Executor {
	if source == nil {
		panic("mux: source must not be nil")
	}
	return &Executor{
		source:      source,
		beforeHooks: make([]BeforeHook, 0),
		afterHooks:  make([]AfterHook, 0),
	}
}

// SetApprovalFunc sets the function used to request approval.
func (e *Executor) SetApprovalFunc(fn ApprovalFunc) {
	e.approvalFunc = fn
}

// ApprovalFunc returns the currently configured approval function (may be nil).
func (e *Executor) ApprovalFunc() ApprovalFunc {
	return e.approvalFunc
}

// NeedsApproval reports whether the named tool would require approval for these
// params, without executing it. Unknown tools return false; Execute will surface
// ErrToolNotFound when actually invoked.
func (e *Executor) NeedsApproval(toolName string, params map[string]any) bool {
	t, ok := e.source.Get(toolName)
	if !ok {
		return false
	}
	return t.RequiresApproval(params)
}

// AddBeforeHook adds a hook that runs before tool execution.
func (e *Executor) AddBeforeHook(hook BeforeHook) {
	e.beforeHooks = append(e.beforeHooks, hook)
}

// AddAfterHook adds a hook that runs after tool execution.
func (e *Executor) AddAfterHook(hook AfterHook) {
	e.afterHooks = append(e.afterHooks, hook)
}

// Source returns the underlying tool source.
func (e *Executor) Source() ToolSource {
	return e.source
}

// Registry returns the underlying tool registry if source is a Registry.
// Returns nil if source is a different ToolSource type.
func (e *Executor) Registry() *Registry {
	if reg, ok := e.source.(*Registry); ok {
		return reg
	}
	return nil
}

// ExecuteOption customizes one Execute call without touching the executor's own
// configuration, so a per-call fact cannot leak into a concurrent or nested call.
type ExecuteOption func(*executeOptions)

// executeOptions carries the per-call overrides an ExecuteOption sets.
type executeOptions struct {
	// decision, when set, is the approval answer for this call alone. It takes
	// precedence over the executor's approval func for this call only; a call
	// that passes no option — a nested Execute, say — still consults the func.
	decision *bool
}

// WithApprovalDecision answers this call's approval check with the given
// decision instead of consulting the executor's approval func. It binds the
// answer to the call it is passed to: unlike SetApprovalFunc, the override is
// not executor-wide state, so a tool that calls back into the same executor
// during its own Execute does not inherit the answer.
func WithApprovalDecision(approved bool) ExecuteOption {
	return func(o *executeOptions) {
		o.decision = &approved
	}
}

// Execute runs a tool by name with the given parameters.
func (e *Executor) Execute(ctx context.Context, toolName string, params map[string]any, opts ...ExecuteOption) (*Result, error) {
	var options executeOptions
	for _, opt := range opts {
		opt(&options)
	}

	t, ok := e.source.Get(toolName)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrToolNotFound, toolName)
	}

	// Check approval
	if t.RequiresApproval(params) {
		approved := false
		switch {
		case options.decision != nil:
			approved = *options.decision
		case e.approvalFunc == nil:
			return nil, fmt.Errorf("%w: %s", ErrApprovalRequired, toolName)
		default:
			ok, err := e.approvalFunc(ctx, t, params)
			if err != nil {
				return nil, fmt.Errorf("approval check failed: %w", err)
			}
			approved = ok
		}
		if !approved {
			return nil, fmt.Errorf("%w: %s", ErrApprovalDenied, toolName)
		}
	}

	// Run before hooks with panic recovery to prevent hook failures from crashing execution
	for _, hook := range e.beforeHooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "Warning: before hook panicked for tool %s: %v\n%s\n", toolName, r, debug.Stack())
				}
			}()
			hook(ctx, toolName, params)
		}()
	}

	// Execute tool
	result, err := t.Execute(ctx, params)

	// Run after hooks with panic recovery to prevent hook failures from crashing execution
	for _, hook := range e.afterHooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "Warning: after hook panicked for tool %s: %v\n%s\n", toolName, r, debug.Stack())
				}
			}()
			hook(ctx, toolName, params, result, err)
		}()
	}

	return result, err
}
