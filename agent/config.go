// ABOUTME: Defines Config - the configuration struct for creating agents with
// ABOUTME: specific tool access, LLM clients, and execution settings.
package agent

import (
	"github.com/2389-research/mux/hooks"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

// Config holds configuration for creating an Agent.
type Config struct {
	// Name identifies this agent (becomes part of hierarchical ID)
	Name string

	// AllowedTools lists tools this agent can use.
	// Empty means inherit from parent or allow all (for root agents).
	AllowedTools []string

	// DeniedTools lists tools this agent cannot use.
	// Takes precedence over AllowedTools.
	DeniedTools []string

	// Registry is the shared tool registry.
	Registry *tool.Registry

	// LLMClient is the LLM provider for this agent.
	LLMClient llm.Client

	// SystemPrompt is an optional custom system prompt.
	SystemPrompt string

	// ApprovalFunc is called when tools require approval.
	ApprovalFunc tool.ApprovalFunc

	// MaxIterations limits the think-act loop (0 = default).
	MaxIterations int

	// HookManager enables lifecycle hooks for this agent and its children.
	// If set, fires SubagentStart/SubagentStop events when children are spawned.
	HookManager *hooks.Manager
}
