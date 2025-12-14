// ABOUTME: Implements the Agent type - the top-level abstraction that wraps
// ABOUTME: orchestrator with per-agent tool filtering and hierarchy support.
package agent

import (
	"context"
	"errors"
	"sync"

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
}

// New creates a new root Agent with the given configuration.
func New(cfg Config) *Agent {
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
func (a *Agent) Run(ctx context.Context, prompt string) error {
	return a.orch.Run(ctx, prompt)
}

// Subscribe returns a channel for receiving orchestrator events.
func (a *Agent) Subscribe() <-chan orchestrator.Event {
	return a.orch.Subscribe()
}

// Config returns the agent's configuration.
func (a *Agent) Config() Config {
	return a.config
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
	a.mu.Lock()
	defer a.mu.Unlock()

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
				return nil, err
			}
		}
	}

	// Merge denied tools (union)
	cfg.DeniedTools = unionStrings(a.config.DeniedTools, cfg.DeniedTools)

	child := &Agent{
		id:       a.id + "." + cfg.Name,
		config:   cfg,
		parent:   a,
		children: make([]*Agent, 0),
	}
	child.init()

	a.children = append(a.children, child)
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
