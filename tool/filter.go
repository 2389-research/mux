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

// IsAllowed returns whether a tool name passes the filter.
// Denied list takes precedence over allowed list.
// Empty allowed list means all tools are allowed (unless denied).
func (f *FilteredRegistry) IsAllowed(name string) bool {
	// Denied always wins
	for _, denied := range f.deniedTools {
		if denied == name {
			return false
		}
	}
	// Empty allowed = all allowed
	if len(f.allowedTools) == 0 {
		return true
	}
	// Check if in allowed list
	for _, allowed := range f.allowedTools {
		if allowed == name {
			return true
		}
	}
	return false
}

// Get retrieves a tool by name if it passes the filter.
func (f *FilteredRegistry) Get(name string) (Tool, bool) {
	if !f.IsAllowed(name) {
		return nil, false
	}
	return f.source.Get(name)
}

// All returns all tools that pass the filter.
func (f *FilteredRegistry) All() []Tool {
	all := f.source.All()
	filtered := make([]Tool, 0, len(all))
	for _, t := range all {
		if f.IsAllowed(t.Name()) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}
