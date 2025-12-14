// tool/filter_test.go
package tool_test

import (
	"context"
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

func TestFilteredRegistry_Get(t *testing.T) {
	source := tool.NewRegistry()
	mock1 := &mockTool{name: "allowed_tool"}
	mock2 := &mockTool{name: "denied_tool"}
	source.Register(mock1)
	source.Register(mock2)

	f := tool.NewFilteredRegistry(source, []string{"allowed_tool"}, nil)

	// Should get allowed tool
	got, ok := f.Get("allowed_tool")
	if !ok {
		t.Fatal("expected to find allowed_tool")
	}
	if got.Name() != "allowed_tool" {
		t.Errorf("got name %q, want allowed_tool", got.Name())
	}

	// Should not get filtered-out tool
	_, ok = f.Get("denied_tool")
	if ok {
		t.Error("expected denied_tool to be filtered out")
	}
}

func TestFilteredRegistry_All(t *testing.T) {
	source := tool.NewRegistry()
	source.Register(&mockTool{name: "bash"})
	source.Register(&mockTool{name: "read_file"})
	source.Register(&mockTool{name: "write_file"})

	f := tool.NewFilteredRegistry(source, []string{"read_file", "write_file"}, nil)

	all := f.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(all))
	}

	names := make(map[string]bool)
	for _, t := range all {
		names[t.Name()] = true
	}
	if !names["read_file"] || !names["write_file"] {
		t.Errorf("expected read_file and write_file, got %v", names)
	}
	if names["bash"] {
		t.Error("bash should be filtered out")
	}
}

func TestFilteredRegistry_List(t *testing.T) {
	source := tool.NewRegistry()
	source.Register(&mockTool{name: "bash"})
	source.Register(&mockTool{name: "read_file"})

	f := tool.NewFilteredRegistry(source, []string{"read_file"}, nil)

	names := f.List()
	if len(names) != 1 || names[0] != "read_file" {
		t.Errorf("expected [read_file], got %v", names)
	}
}

func TestFilteredRegistry_Count(t *testing.T) {
	source := tool.NewRegistry()
	source.Register(&mockTool{name: "a"})
	source.Register(&mockTool{name: "b"})
	source.Register(&mockTool{name: "c"})

	f := tool.NewFilteredRegistry(source, []string{"a", "b"}, nil)

	if f.Count() != 2 {
		t.Errorf("expected count 2, got %d", f.Count())
	}
}

func TestFilteredRegistryWithExecutor(t *testing.T) {
	source := tool.NewRegistry()
	source.Register(&mockTool{name: "allowed"})
	source.Register(&mockTool{name: "denied"})

	filtered := tool.NewFilteredRegistry(source, []string{"allowed"}, nil)
	exec := tool.NewExecutorWithSource(filtered)

	// Should execute allowed tool
	_, err := exec.Execute(context.Background(), "allowed", nil)
	if err != nil {
		t.Errorf("expected allowed tool to execute, got error: %v", err)
	}

	// Should not find denied tool
	_, err = exec.Execute(context.Background(), "denied", nil)
	if err == nil {
		t.Error("expected error for denied tool")
	}
}
