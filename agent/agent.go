// ABOUTME: Implements the Agent type - the top-level abstraction that wraps
// ABOUTME: orchestrator with per-agent tool filtering and hierarchy support.
package agent

import (
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
