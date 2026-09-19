// ABOUTME: Defines Config - the configuration struct for creating agents with
// ABOUTME: specific tool access, LLM clients, and execution settings.
package agent

import (
	"github.com/2389-research/mux/hooks"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/skill"
	"github.com/2389-research/mux/tool"
)

// ChildConfigPolicy states how one Config field crosses the SpawnChild boundary.
// Every Config field has exactly one policy, recorded in ConfigFieldChildPolicies.
type ChildConfigPolicy string

const (
	// ChildConfigInheritIfNil: a nil child value copies the parent's value.
	ChildConfigInheritIfNil ChildConfigPolicy = "inherit-if-nil"
	// ChildConfigInheritIfEmpty: an empty child slice copies the parent's slice.
	ChildConfigInheritIfEmpty ChildConfigPolicy = "inherit-if-empty"
	// ChildConfigUnion: the child value is unioned with the parent's; the caller
	// cannot remove a parent entry.
	ChildConfigUnion ChildConfigPolicy = "union"
	// ChildConfigCallerRestates: the value never propagates; the child gets its
	// zero value unless the caller restates it.
	ChildConfigCallerRestates ChildConfigPolicy = "caller-restates"
)

// childConfigPolicies is the single declaration site for every Config field's
// child inheritance policy. deriveChildConfig implements it, and
// TestConfigFieldPoliciesAreComplete fails when a Config field is added here
// without a decision.
var childConfigPolicies = map[string]ChildConfigPolicy{
	"Name":             ChildConfigCallerRestates,
	"AllowedTools":     ChildConfigInheritIfEmpty,
	"DeniedTools":      ChildConfigUnion,
	"Registry":         ChildConfigInheritIfNil,
	"LLMClient":        ChildConfigInheritIfNil,
	"SystemPrompt":     ChildConfigCallerRestates,
	"ApprovalFunc":     ChildConfigInheritIfNil,
	"MaxIterations":    ChildConfigCallerRestates,
	"Stream":           ChildConfigCallerRestates,
	"HookManager":      ChildConfigInheritIfNil,
	"ThinkingSettings": ChildConfigCallerRestates,
	"SessionStore":     ChildConfigCallerRestates,
	"ApprovalMode":     ChildConfigCallerRestates,
	"Skills":           ChildConfigCallerRestates,
}

// ConfigFieldChildPolicies lists every Config field's child inheritance policy.
// It is the one place a field's crossing-the-spawn-boundary behaviour is
// declared, so adding a Config field forces a decision there. The returned map
// is a copy and safe for callers to keep or modify.
func ConfigFieldChildPolicies() map[string]ChildConfigPolicy {
	policies := make(map[string]ChildConfigPolicy, len(childConfigPolicies))
	for field, policy := range childConfigPolicies {
		policies[field] = policy
	}
	return policies
}

// Config holds configuration for creating an Agent.
//
// Each field's behaviour when a child Config omits it is recorded in
// ConfigFieldChildPolicies and implemented by deriveChildConfig. In summary:
//
//   - inherited when nil/empty: Registry, LLMClient, HookManager, AllowedTools
//   - unioned with the parent's: DeniedTools (parent denials cannot be removed)
//   - inherited when nil, overridden by an explicit value: ApprovalFunc
//   - never inherited; the caller restates them on the child (empty or zero
//     selects the ordinary child default): Name, SystemPrompt, MaxIterations,
//     Stream, ThinkingSettings, SessionStore, ApprovalMode, Skills
//
// SessionStore and ApprovalMode are deliberately not inherited: a child never
// silently shares the parent's session writer. A child that sets
// ApprovalMode: ApprovalSuspend must also supply a SessionStore, or SpawnChild
// returns ErrInvalidChildConfig.
type Config struct {
	// Name identifies this agent (becomes part of hierarchical ID).
	Name string

	// AllowedTools lists tools this agent can use.
	// Empty means inherit from parent or allow all (for root agents).
	AllowedTools []string

	// DeniedTools lists tools this agent cannot use.
	// Takes precedence over AllowedTools. Children union this with the parent's.
	DeniedTools []string

	// Registry is the shared tool registry. Children inherit it when nil.
	Registry *tool.Registry

	// LLMClient is the LLM provider for this agent. Children inherit it when nil.
	LLMClient llm.Client

	// SystemPrompt is an optional custom system prompt. Not inherited by children.
	SystemPrompt string

	// ApprovalFunc is called when tools require approval. A child that omits it
	// inherits the parent's, so an inherited approval-gated tool stays usable.
	ApprovalFunc tool.ApprovalFunc

	// MaxIterations limits the think-act loop (0 = default). Not inherited.
	MaxIterations int

	// Stream uses streaming LLM responses while preserving the final response.
	// Not inherited.
	Stream bool

	// HookManager enables lifecycle hooks for this agent and its children.
	// If set, fires SubagentStart/SubagentStop events when children are spawned.
	// Children inherit it when nil.
	HookManager *hooks.Manager

	// ThinkingSettings configures per-call thinking behavior (nil = no thinking).
	// Not inherited; a non-nil child value is copied.
	ThinkingSettings *orchestrator.ThinkingSettings

	// SessionStore enables durable suspend/resume; passed through to the
	// orchestrator. Not inherited: a child never shares the parent's store
	// unless it restates one.
	SessionStore orchestrator.Store
	// ApprovalMode selects synchronous approval (default) or suspend-on-approval.
	// Not inherited. ApprovalSuspend requires SessionStore on the same child
	// config.
	ApprovalMode orchestrator.ApprovalMode

	// Skills, when non-nil, exposes its skills to this agent: the catalog is
	// injected into the system prompt and the load_skill tool is registered and
	// permitted. Construct it with skill.LoadDir at startup. Not inherited:
	// distinct skill catalogs belong to distinct Registry objects.
	Skills *skill.Registry
}
