// tool/filter.go
// ABOUTME: Implements FilteredRegistry - a filtered view of a Registry that
// ABOUTME: applies allow/deny lists to control which tools are visible.
package tool

// FilteredRegistry wraps a Registry with allow/deny filtering.
type FilteredRegistry struct {
	source       *Registry
	allowedTools []string
	deniedTools  []string
}

// NewFilteredRegistry creates a filtered view of the source registry.
// allowedTools: if non-empty, only these tools are visible (allowlist)
// deniedTools: these tools are never visible (denylist, takes precedence)
func NewFilteredRegistry(source *Registry, allowedTools, deniedTools []string) *FilteredRegistry {
	return &FilteredRegistry{
		source:       source,
		allowedTools: allowedTools,
		deniedTools:  deniedTools,
	}
}
