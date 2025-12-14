// tool/filter_test.go
package tool_test

import (
	"testing"

	"github.com/2389-research/mux/tool"
)

func TestNewFilteredRegistry(t *testing.T) {
	source := tool.NewRegistry()

	filtered := tool.NewFilteredRegistry(source, nil, nil)

	if filtered == nil {
		t.Fatal("expected non-nil FilteredRegistry")
	}
}

func TestFilteredRegistry_isAllowed(t *testing.T) {
	tests := []struct {
		name         string
		allowedTools []string
		deniedTools  []string
		toolName     string
		want         bool
	}{
		{
			name:         "empty lists allows all",
			allowedTools: nil,
			deniedTools:  nil,
			toolName:     "any_tool",
			want:         true,
		},
		{
			name:         "denied takes precedence over allowed",
			allowedTools: []string{"bash"},
			deniedTools:  []string{"bash"},
			toolName:     "bash",
			want:         false,
		},
		{
			name:         "allowed list restricts to listed tools",
			allowedTools: []string{"read_file", "write_file"},
			deniedTools:  nil,
			toolName:     "bash",
			want:         false,
		},
		{
			name:         "allowed list permits listed tools",
			allowedTools: []string{"read_file", "write_file"},
			deniedTools:  nil,
			toolName:     "read_file",
			want:         true,
		},
		{
			name:         "denied list blocks specific tools",
			allowedTools: nil,
			deniedTools:  []string{"dangerous"},
			toolName:     "dangerous",
			want:         false,
		},
		{
			name:         "denied list allows unlisted tools",
			allowedTools: nil,
			deniedTools:  []string{"dangerous"},
			toolName:     "safe",
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := tool.NewRegistry()
			f := tool.NewFilteredRegistry(source, tt.allowedTools, tt.deniedTools)

			got := f.IsAllowed(tt.toolName)
			if got != tt.want {
				t.Errorf("IsAllowed(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}
