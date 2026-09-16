// ABOUTME: Defines the Result type - a unified structure for tool execution
// ABOUTME: outcomes, used consistently across all tool types.
package tool

// Result represents the outcome of a tool execution.
//
// See ModelText for which field the model actually sees.
type Result struct {
	ToolName string
	Success  bool
	Output   string
	Error    string
	Metadata map[string]any
}

// NewResult creates a new Result with the given values. See Result.ModelText
// for which of output and errMsg reaches the model.
func NewResult(toolName string, success bool, output, errMsg string) *Result {
	return &Result{
		ToolName: toolName,
		Success:  success,
		Output:   output,
		Error:    errMsg,
		Metadata: make(map[string]any),
	}
}

// NewErrorResult creates a failed Result with an error message. errMsg reaches
// the model through Result.ModelText, since Output is left empty here.
func NewErrorResult(toolName string, errMsg string) *Result {
	return &Result{
		ToolName: toolName,
		Success:  false,
		Error:    errMsg,
		Metadata: make(map[string]any),
	}
}

// ModelText returns the text the model sees for this Result in the
// tool_result block: Output when it is non-empty; otherwise, for a failed
// Result, Error; otherwise the empty string. Output always wins when both are
// set, so a producer that duplicates its message into both fields (as some
// adapters once did) does not render it twice.
func (r *Result) ModelText() string {
	if r.Output != "" {
		return r.Output
	}
	if !r.Success {
		return r.Error
	}
	return ""
}
