// ABOUTME: Tests for Result.ModelText, the accessor that determines which field
// ABOUTME: of a tool Result the model actually sees in a tool_result block.
package tool_test

import (
	"testing"

	"github.com/2389-research/mux/tool"
)

func TestResultModelText(t *testing.T) {
	tests := []struct {
		name   string
		result *tool.Result
		want   string
	}{
		{
			name:   "success with output",
			result: tool.NewResult("t", true, "ok", ""),
			want:   "ok",
		},
		{
			name:   "failure with both output and error prefers output",
			result: tool.NewResult("t", false, "partial", "bad"),
			want:   "partial",
		},
		{
			name:   "failure with only error falls back to error",
			result: tool.NewResult("t", false, "", "bad"),
			want:   "bad",
		},
		{
			name:   "success with empty output ignores stale error",
			result: tool.NewResult("t", true, "", "stale"),
			want:   "",
		},
		{
			name:   "failure with duplicated output and error is not doubled",
			result: tool.NewResult("t", false, "bad", "bad"),
			want:   "bad",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.ModelText(); got != tt.want {
				t.Errorf("ModelText() = %q, want %q", got, tt.want)
			}
		})
	}
}
