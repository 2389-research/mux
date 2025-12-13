// ABOUTME: Defines the core Tool interface - the universal abstraction for all
// ABOUTME: executable capabilities (built-in tools, MCP tools, skills, etc.)
package tool

import "context"

// Tool is the universal interface for executable capabilities.
// All tools (built-in, MCP, skills) implement this interface.
type Tool interface {
	// Name returns the unique identifier for this tool.
	Name() string

	// Description returns a human-readable description for the LLM.
	Description() string

	// RequiresApproval returns true if execution needs user approval.
	// The params are provided to allow context-sensitive decisions.
	RequiresApproval(params map[string]any) bool

	// Execute runs the tool with the given parameters.
	Execute(ctx context.Context, params map[string]any) (*Result, error)
}
