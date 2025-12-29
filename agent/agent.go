// ABOUTME: Implements the Agent type - the top-level abstraction that wraps
// ABOUTME: orchestrator with per-agent tool filtering and hierarchy support.
package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/tool"
)

// Agent represents an autonomous agent with its own tool access and config.
type Agent struct {
	mu       sync.RWMutex
	id       string
	config   Config
	parent   *Agent
	children []*Agent

	filtered *tool.FilteredRegistry
	executor *tool.Executor
	orch     *orchestrator.Orchestrator

	// cachedConfig is lazily initialized to avoid expensive deep copies
	cachedConfig     *Config
	cachedConfigOnce sync.Once
}

// New creates a new root Agent with the given configuration.
// Panics if Registry or LLMClient is nil.
func New(cfg Config) *Agent {
	if cfg.Registry == nil {
		panic("mux: agent registry must not be nil")
	}
	if cfg.LLMClient == nil {
		panic("mux: agent LLMClient must not be nil")
	}
	if cfg.Name == "" {
		panic("mux: agent name must not be empty")
	}

	a := &Agent{
		id:       cfg.Name,
		config:   cfg,
		children: make([]*Agent, 0),
	}
	a.init()
	return a
}

// ID returns the hierarchical agent ID.
func (a *Agent) ID() string {
	return a.id
}

func (a *Agent) init() {
	// Create filtered view of registry
	a.filtered = tool.NewFilteredRegistry(
		a.config.Registry,
		a.config.AllowedTools,
		a.config.DeniedTools,
	)

	// Create executor with filtered registry
	a.executor = tool.NewExecutorWithSource(a.filtered)
	if a.config.ApprovalFunc != nil {
		a.executor.SetApprovalFunc(a.config.ApprovalFunc)
	}

	// Create orchestrator config
	orchConfig := orchestrator.DefaultConfig()
	if a.config.SystemPrompt != "" {
		orchConfig.SystemPrompt = a.config.SystemPrompt
	}
	if a.config.MaxIterations > 0 {
		orchConfig.MaxIterations = a.config.MaxIterations
	}

	// Create orchestrator
	a.orch = orchestrator.NewWithConfig(a.config.LLMClient, a.executor, orchConfig)
}

// Run executes the agent's think-act loop with the given prompt.
// Each call starts fresh with only the new prompt (no conversation history).
// Use Continue() for multi-turn conversations that preserve history.
func (a *Agent) Run(ctx context.Context, prompt string) error {
	return a.orch.Run(ctx, prompt)
}

// Continue appends the prompt to existing conversation history and runs the think-act loop.
// Use this for multi-turn conversations where the agent should remember previous exchanges.
// Use SetMessages() to restore history from persistence before calling Continue().
func (a *Agent) Continue(ctx context.Context, prompt string) error {
	return a.orch.Continue(ctx, prompt)
}

// Subscribe returns a channel for receiving orchestrator events.
func (a *Agent) Subscribe() <-chan orchestrator.Event {
	return a.orch.Subscribe()
}

// Messages returns the current conversation history.
func (a *Agent) Messages() []llm.Message {
	return a.orch.Messages()
}

// SetMessages sets the conversation history.
// Use this to restore conversation state from persistence.
func (a *Agent) SetMessages(messages []llm.Message) {
	a.orch.SetMessages(messages)
}

// ClearMessages resets the conversation history.
func (a *Agent) ClearMessages() {
	a.orch.ClearMessages()
}

// Config returns a copy of the agent's configuration.
// The returned config is safe to inspect but should not be modified.
// Uses lazy initialization to avoid expensive deep copies on every call.
// Note: The cached config is computed once and never invalidated.
// Agent configuration is considered immutable after creation.
func (a *Agent) Config() Config {
	a.cachedConfigOnce.Do(func() {
		cfg := a.config
		// Deep copy slices to prevent external modification
		if a.config.AllowedTools != nil {
			cfg.AllowedTools = make([]string, len(a.config.AllowedTools))
			copy(cfg.AllowedTools, a.config.AllowedTools)
		}
		if a.config.DeniedTools != nil {
			cfg.DeniedTools = make([]string, len(a.config.DeniedTools))
			copy(cfg.DeniedTools, a.config.DeniedTools)
		}
		a.cachedConfig = &cfg
	})
	return *a.cachedConfig
}

// Parent returns the parent agent, or nil for root agents.
func (a *Agent) Parent() *Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.parent
}

var (
	ErrInvalidChildTools = errors.New("child cannot have tools parent doesn't have")
)

// SpawnChild creates a child agent with inherited configuration.
// Child tools are restricted to parent's allowed tools.
// Child denied tools accumulate (union of parent and child denied).
func (a *Agent) SpawnChild(cfg Config) (*Agent, error) {
	// First, prepare the child config while holding a read lock
	a.mu.RLock()
	// Inherit registry if not specified
	if cfg.Registry == nil {
		cfg.Registry = a.config.Registry
	}

	// Inherit LLM client if not specified
	if cfg.LLMClient == nil {
		cfg.LLMClient = a.config.LLMClient
	}

	// Empty AllowedTools = inherit parent's allowed tools
	if len(cfg.AllowedTools) == 0 {
		cfg.AllowedTools = make([]string, len(a.config.AllowedTools))
		copy(cfg.AllowedTools, a.config.AllowedTools)
	} else {
		// Validate child tools are subset of parent tools (if parent has restrictions)
		if len(a.config.AllowedTools) > 0 {
			if err := a.validateChildTools(cfg.AllowedTools); err != nil {
				a.mu.RUnlock()
				return nil, err
			}
		}
	}

	// Merge denied tools (union)
	cfg.DeniedTools = unionStrings(a.config.DeniedTools, cfg.DeniedTools)

	// Create child ID
	childID := a.id + "." + cfg.Name
	a.mu.RUnlock()

	// Initialize child without holding parent lock (expensive operation)
	child := &Agent{
		id:       childID,
		config:   cfg,
		parent:   a,
		children: make([]*Agent, 0),
	}
	child.init()

	// Now acquire write lock just to append to children list
	a.mu.Lock()
	a.children = append(a.children, child)
	a.mu.Unlock()

	return child, nil
}

// validateChildTools ensures child's tools are a subset of parent's allowed tools.
func (a *Agent) validateChildTools(childTools []string) error {
	parentSet := make(map[string]bool)
	for _, t := range a.config.AllowedTools {
		parentSet[t] = true
	}

	for _, t := range childTools {
		if !parentSet[t] {
			return ErrInvalidChildTools
		}
	}
	return nil
}

// unionStrings returns the union of two string slices without duplicates.
func unionStrings(a, b []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(a)+len(b))

	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// Children returns the list of child agents.
func (a *Agent) Children() []*Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	children := make([]*Agent, len(a.children))
	copy(children, a.children)
	return children
}

// RemoveChild removes a specific child agent from the parent's children list.
// Returns true if the child was found and removed, false otherwise.
// This is useful for cleaning up terminated child agents.
func (a *Agent) RemoveChild(child *Agent) bool {
	if child == nil {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i, c := range a.children {
		if c == child {
			// Remove child from slice by replacing it with the last element
			// and truncating the slice
			a.children[i] = a.children[len(a.children)-1]
			a.children = a.children[:len(a.children)-1]
			return true
		}
	}
	return false
}

// RemoveAllChildren removes all child agents from this agent.
// This is useful for bulk cleanup operations.
func (a *Agent) RemoveAllChildren() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.children = make([]*Agent, 0)
}

// Executor returns the agent's tool executor.
func (a *Agent) Executor() *tool.Executor {
	return a.executor
}
