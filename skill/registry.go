// ABOUTME: Implements the skill Registry — a name-keyed store of loaded skills with
// ABOUTME: directory loading, sorted accessors, and system-prompt catalog rendering.
package skill

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Registry is a thread-safe, name-keyed collection of skills.
type Registry struct {
	mu     sync.RWMutex
	skills map[string]Skill
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{skills: make(map[string]Skill)}
}

// LoadDir scans dir for <name>/SKILL.md files, parses each, and returns a populated
// Registry. A subdirectory without a SKILL.md is ignored. A missing dir, a malformed
// SKILL.md, or two skills declaring the same name are errors.
func LoadDir(dir string) (*Registry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("skill: reading skills dir: %w", err)
	}
	r := NewRegistry()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // subdirectory is not a skill
			}
			return nil, fmt.Errorf("skill: reading %s: %w", path, err)
		}
		s, err := parseSkill(data)
		if err != nil {
			return nil, fmt.Errorf("skill: parsing %s: %w", path, err)
		}
		if err := r.Register(s); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Register adds a skill. It returns an error if a skill with the same name exists,
// because duplicate names indicate a misconfigured skills directory.
func (r *Registry) Register(s Skill) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.skills[s.Name]; exists {
		return fmt.Errorf("skill: duplicate skill name %q", s.Name)
	}
	r.skills[s.Name] = s
	return nil
}

// Get returns the skill with the given name.
func (r *Registry) Get(name string) (Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[name]
	return s, ok
}

// All returns every skill, sorted by name.
func (r *Registry) All() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// List returns the names of all skills, sorted alphabetically.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.skills))
	for name := range r.skills {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Count returns the number of registered skills.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.skills)
}

// Catalog renders the progressive-disclosure menu injected into the system prompt:
// one line per skill (name + description), in sorted order. It returns "" when the
// registry is empty so callers never inject a dangling header.
func (r *Registry) Catalog() string {
	if r.Count() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Available Skills\n\n")
	b.WriteString("Load full instructions with the load_skill tool before acting on one.\n\n")
	for _, s := range r.All() {
		fmt.Fprintf(&b, "- **%s** — %s\n", s.Name, s.Description)
	}
	return strings.TrimRight(b.String(), "\n")
}
