// ABOUTME: Defines the Result type - a unified structure for tool execution
// ABOUTME: outcomes, used consistently across all tool types.
package tool

// Result represents the outcome of a tool execution.
type Result struct {
	ToolName string
	Success  bool
	Output   string
	Error    string
	Metadata map[string]any
}
