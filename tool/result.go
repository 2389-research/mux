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

// NewResult creates a new Result with the given values.
func NewResult(toolName string, success bool, output, errMsg string) *Result {
	return &Result{
		ToolName: toolName,
		Success:  success,
		Output:   output,
		Error:    errMsg,
		Metadata: make(map[string]any),
	}
}

// NewErrorResult creates a failed Result with an error message.
func NewErrorResult(toolName string, errMsg string) *Result {
	return &Result{
		ToolName: toolName,
		Success:  false,
		Error:    errMsg,
		Metadata: make(map[string]any),
	}
}
