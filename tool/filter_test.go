// tool/filter_test.go
package tool_test

import (
	"context"
	"fmt"
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

// Issue 8: Large tool sets performance test (1000+ tools)
func TestFilteredRegistry_LargeToolSet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large tool set test in short mode")
	}

	const toolCount = 1000
	source := tool.NewRegistry()

	// Register 1000 tools
	for i := 0; i < toolCount; i++ {
		name := fmt.Sprintf("tool_%d", i)
		source.Register(&mockTool{name: name})
	}

	tests := []struct {
		name         string
		allowedTools []string
		deniedTools  []string
		expectCount  int
	}{
		{
			name:         "no filters returns all tools",
			allowedTools: nil,
			deniedTools:  nil,
			expectCount:  toolCount,
		},
		{
			name:         "allow first 100 tools",
			allowedTools: generateToolNames(0, 100),
			deniedTools:  nil,
			expectCount:  100,
		},
		{
			name:         "deny first 100 tools",
			allowedTools: nil,
			deniedTools:  generateToolNames(0, 100),
			expectCount:  toolCount - 100,
		},
		{
			name:         "allow 500 deny 50",
			allowedTools: generateToolNames(0, 500),
			deniedTools:  generateToolNames(0, 50),
			expectCount:  450, // 500 allowed - 50 denied
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tool.NewFilteredRegistry(source, tt.allowedTools, tt.deniedTools)

			// Test Count performance
			count := f.Count()
			if count != tt.expectCount {
				t.Errorf("Count() = %d, want %d", count, tt.expectCount)
			}

			// Test All performance
			all := f.All()
			if len(all) != tt.expectCount {
				t.Errorf("All() returned %d tools, want %d", len(all), tt.expectCount)
			}

			// Test List performance
			names := f.List()
			if len(names) != tt.expectCount {
				t.Errorf("List() returned %d names, want %d", len(names), tt.expectCount)
			}

			// Verify List is sorted
			for i := 1; i < len(names); i++ {
				if names[i-1] >= names[i] {
					t.Errorf("List() not sorted at index %d: %q >= %q", i, names[i-1], names[i])
					break
				}
			}
		})
	}
}

// Issue 8: Empty source registry handling
func TestFilteredRegistry_EmptySource(t *testing.T) {
	source := tool.NewRegistry() // Empty registry

	tests := []struct {
		name         string
		allowedTools []string
		deniedTools  []string
	}{
		{
			name:         "no filters on empty registry",
			allowedTools: nil,
			deniedTools:  nil,
		},
		{
			name:         "with allowed list on empty registry",
			allowedTools: []string{"tool1", "tool2"},
			deniedTools:  nil,
		},
		{
			name:         "with denied list on empty registry",
			allowedTools: nil,
			deniedTools:  []string{"tool1", "tool2"},
		},
		{
			name:         "with both lists on empty registry",
			allowedTools: []string{"tool1"},
			deniedTools:  []string{"tool2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tool.NewFilteredRegistry(source, tt.allowedTools, tt.deniedTools)

			// All operations should work on empty registry
			if f.Count() != 0 {
				t.Errorf("Count() = %d, want 0", f.Count())
			}

			all := f.All()
			if len(all) != 0 {
				t.Errorf("All() returned %d tools, want 0", len(all))
			}

			names := f.List()
			if len(names) != 0 {
				t.Errorf("List() returned %d names, want 0", len(names))
			}

			// Get should return false for any tool
			_, ok := f.Get("nonexistent")
			if ok {
				t.Error("Get() should return false for nonexistent tool")
			}
		})
	}
}

// Issue 8: Tool filter updates during execution
func TestFilteredRegistry_FilterUpdatesDuringExecution(t *testing.T) {
	source := tool.NewRegistry()

	// Register tools
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("tool_%d", i)
		source.Register(&mockTool{name: name})
	}

	// Create filtered registry
	allowed := []string{"tool_0", "tool_1", "tool_2"}
	denied := []string{"tool_3"}
	f := tool.NewFilteredRegistry(source, allowed, denied)

	// Initial state
	if f.Count() != 3 {
		t.Errorf("initial Count() = %d, want 3", f.Count())
	}

	// Modify the source registry by adding more tools
	for i := 10; i < 20; i++ {
		name := fmt.Sprintf("tool_%d", i)
		source.Register(&mockTool{name: name})
	}

	// Filtered registry should still respect original filters
	// but see new tools from source
	newCount := f.Count()
	// Should still be 3 because new tools aren't in allowed list
	if newCount != 3 {
		t.Errorf("after adding tools, Count() = %d, want 3", newCount)
	}

	// Test that we can still access originally allowed tools
	for _, name := range allowed {
		if !f.IsAllowed(name) {
			t.Errorf("IsAllowed(%q) = false, want true", name)
		}
		_, ok := f.Get(name)
		if !ok {
			t.Errorf("Get(%q) failed, expected success", name)
		}
	}

	// Test that denied tool is still denied
	if f.IsAllowed("tool_3") {
		t.Error("IsAllowed(tool_3) = true, want false (should be denied)")
	}
}

// Issue 8: Memory usage with large filter lists
func TestFilteredRegistry_LargeFilterLists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large filter list test in short mode")
	}

	const filterSize = 5000
	source := tool.NewRegistry()

	// Register some tools (smaller than filter list)
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("tool_%d", i)
		source.Register(&mockTool{name: name})
	}

	// Create very large filter lists
	largeAllowed := generateToolNames(0, filterSize)
	largeDenied := generateToolNames(filterSize, filterSize*2)

	tests := []struct {
		name         string
		allowedTools []string
		deniedTools  []string
	}{
		{
			name:         "large allowed list",
			allowedTools: largeAllowed,
			deniedTools:  nil,
		},
		{
			name:         "large denied list",
			allowedTools: nil,
			deniedTools:  largeDenied,
		},
		{
			name:         "both lists large",
			allowedTools: largeAllowed,
			deniedTools:  largeDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tool.NewFilteredRegistry(source, tt.allowedTools, tt.deniedTools)

			// Operations should complete without hanging or excessive memory
			count := f.Count()
			if count < 0 || count > 100 {
				t.Errorf("Count() = %d, expected between 0 and 100", count)
			}

			all := f.All()
			if len(all) != count {
				t.Errorf("All() returned %d tools, expected %d", len(all), count)
			}

			names := f.List()
			if len(names) != count {
				t.Errorf("List() returned %d names, expected %d", len(names), count)
			}

			// Test Get operations
			_, ok := f.Get("tool_0")
			// Result depends on filters, just verify no panic
			_ = ok
		})
	}
}

// Issue 8: Edge case - filter with duplicate entries
func TestFilteredRegistry_DuplicateFilterEntries(t *testing.T) {
	source := tool.NewRegistry()
	source.Register(&mockTool{name: "tool_a"})
	source.Register(&mockTool{name: "tool_b"})
	source.Register(&mockTool{name: "tool_c"})

	// Create filters with duplicates
	allowed := []string{"tool_a", "tool_a", "tool_b", "tool_b"}
	denied := []string{"tool_c", "tool_c"}

	f := tool.NewFilteredRegistry(source, allowed, denied)

	// Should handle duplicates gracefully
	if f.Count() != 2 {
		t.Errorf("Count() = %d, want 2 (tool_a and tool_b)", f.Count())
	}

	if !f.IsAllowed("tool_a") {
		t.Error("IsAllowed(tool_a) = false, want true")
	}
	if !f.IsAllowed("tool_b") {
		t.Error("IsAllowed(tool_b) = false, want true")
	}
	if f.IsAllowed("tool_c") {
		t.Error("IsAllowed(tool_c) = true, want false")
	}
}

// Issue 8: Concurrent access to filtered registry
func TestFilteredRegistry_ConcurrentAccess(t *testing.T) {
	source := tool.NewRegistry()

	// Register tools
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("tool_%d", i)
		source.Register(&mockTool{name: name})
	}

	f := tool.NewFilteredRegistry(source, nil, nil)

	// Spawn multiple goroutines to access the filtered registry
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() {
				done <- true
			}()

			// Perform various operations
			_ = f.Count()
			_ = f.All()
			_ = f.List()

			for j := 0; j < 10; j++ {
				name := fmt.Sprintf("tool_%d", j)
				_ = f.IsAllowed(name)
				_, _ = f.Get(name)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Helper function to generate tool names
func generateToolNames(start, count int) []string {
	names := make([]string, count)
	for i := 0; i < count; i++ {
		names[i] = fmt.Sprintf("tool_%d", start+i)
	}
	return names
}
