# Skills (Gene #2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a mux caller drop a folder of `SKILL.md` files on disk and have an agent advertise them via a catalog injected into the system prompt and load any one's full instructions on demand through a `load_skill` tool.

**Architecture:** A new self-contained `skill` package (`agent → skill → tool`, orchestrator untouched) holds a `Skill` type, a frontmatter parser, a `Registry` with directory loading, a rendered `Catalog()`, and the `load_skill` `tool.Tool`. The `agent` layer gains one `Config.Skills *skill.Registry` field; `agent.init()` injects the catalog, registers the tool, and auto-allows it.

**Tech Stack:** Go 1.24; `gopkg.in/yaml.v3` (promoted from transitive to direct for frontmatter parsing); stdlib `os`/`io/fs`/`path/filepath`/`sort`/`strings`.

## Global Constraints

- Module is `github.com/2389-research/mux`, Go 1.24. The orchestrator package is **not** modified by any task.
- Dependency direction is `agent → skill → tool`. The `skill` package imports only `tool`, stdlib, and `gopkg.in/yaml.v3`. It must never import `agent` or `orchestrator` (no import cycle).
- The only new dependency is `gopkg.in/yaml.v3 v3.0.1` (already present in `go.sum`; promoted to a direct `require`). Add no other dependencies.
- Every new `.go` file starts with exactly two `// ABOUTME: ` comment lines describing what the file does.
- TDD: write the failing test first, watch it fail, then write minimal code. Test output must be pristine (use canonical `go test`, no `-v` in the suite run).
- The only test doubles allowed are types defined in `_test.go` files (the existing house pattern, e.g. `mockClient` in `agent/agent_test.go`). No production mock modes.
- Conventional commit messages, imperative present tense.
- `gofmt`, `go vet ./...`, and `golangci-lint` must pass (the pre-commit hook enforces all three plus `go mod tidy`); never use `--no-verify`.
- The `load_skill` tool name is the string literal `"load_skill"`, defined once as a constant and referenced everywhere.

---

## File Structure

| File | Responsibility |
|---|---|
| `skill/skill.go` (create) | `Skill` struct + `parseSkill` frontmatter parser |
| `skill/skill_test.go` (create) | white-box tests for `parseSkill` |
| `skill/registry.go` (create) | `Registry`, `LoadDir`, accessors, `Catalog()` |
| `skill/registry_test.go` (create) | tests for loading, accessors, catalog |
| `skill/tool.go` (create) | `load_skill` tool + `Registry.Tool()` |
| `skill/tool_test.go` (create) | tests for the `load_skill` tool |
| `agent/config.go` (modify) | add `Skills *skill.Registry` field |
| `agent/agent.go` (modify) | wire skills in `init()` |
| `agent/skills_test.go` (create) | agent-level integration tests |

---

### Task 1: Skill type and frontmatter parser

Parses a `SKILL.md` byte slice into a `Skill`. Pure function, no filesystem — the unit of risk a reviewer gates independently. Promotes `yaml.v3` to a direct dependency.

**Files:**
- Create: `skill/skill.go`
- Test: `skill/skill_test.go`
- Modify: `go.mod`, `go.sum` (via `go get` / `go mod tidy` — automatic)

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  - `type Skill struct { Name string; Description string; Body string }`
  - `func parseSkill(data []byte) (Skill, error)` — unexported; tested white-box from `package skill`.

- [ ] **Step 1: Promote yaml.v3 to a direct dependency**

Run:
```bash
go get gopkg.in/yaml.v3@v3.0.1
```
Expected: no network fetch (already in `go.sum`); `go.mod` gains `gopkg.in/yaml.v3 v3.0.1` in the require block. (It moves from absent/indirect to direct once `skill.go` imports it and `go mod tidy` runs in Step 5.)

- [ ] **Step 2: Write the failing test**

Create `skill/skill_test.go`:
```go
// ABOUTME: White-box tests for the SKILL.md frontmatter parser, covering valid
// ABOUTME: parses, ignored extra keys, and every validation error path.
package skill

import "testing"

func TestParseSkill(t *testing.T) {
	data := []byte("---\nname: commit-message\ndescription: Write a commit. Use when asked.\n---\n\n# Commit\n\n1. Do the thing.\n")
	s, err := parseSkill(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "commit-message" {
		t.Errorf("Name = %q, want commit-message", s.Name)
	}
	if s.Description != "Write a commit. Use when asked." {
		t.Errorf("Description = %q", s.Description)
	}
	if s.Body != "# Commit\n\n1. Do the thing." {
		t.Errorf("Body = %q", s.Body)
	}
}

func TestParseSkillIgnoresExtraKeys(t *testing.T) {
	data := []byte("---\nname: x\ndescription: y\nversion: 2\nextra: stuff\n---\nbody\n")
	s, err := parseSkill(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Name != "x" || s.Description != "y" || s.Body != "body" {
		t.Errorf("got %+v", s)
	}
}

func TestParseSkillErrors(t *testing.T) {
	cases := map[string]string{
		"no frontmatter":    "# Just a heading\n",
		"unterminated":      "---\nname: x\ndescription: y\n",
		"empty name":        "---\nname: \"\"\ndescription: y\n---\nbody\n",
		"missing name":      "---\ndescription: y\n---\nbody\n",
		"empty description": "---\nname: x\ndescription: \"\"\n---\nbody\n",
		"empty body":        "---\nname: x\ndescription: y\n---\n\n",
		"bad yaml":          "---\nname: [unclosed\n---\nbody\n",
	}
	for label, in := range cases {
		if _, err := parseSkill([]byte(in)); err == nil {
			t.Errorf("%s: expected error, got nil", label)
		}
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./skill/ -run TestParseSkill`
Expected: build failure — `undefined: parseSkill` (and `undefined: Skill`).

- [ ] **Step 4: Write the minimal implementation**

Create `skill/skill.go`:
```go
// ABOUTME: Defines the Skill type and parseSkill, which reads a SKILL.md byte
// ABOUTME: slice (YAML frontmatter + markdown body) into a validated Skill.

// Package skill loads file-authored procedures ("skills") and exposes them to an
// agent via a system-prompt catalog and an on-demand load_skill tool.
package skill

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill is a single file-authored procedure: frontmatter metadata + markdown body.
type Skill struct {
	Name        string // unique identifier, from frontmatter `name`
	Description string // one-line "what + when", from frontmatter `description`
	Body        string // the markdown instructions following the frontmatter
}

// parseSkill reads a SKILL.md document: a YAML frontmatter block delimited by
// `---` fences, followed by a markdown body. Name, description, and body must all
// be non-empty. Extra frontmatter keys are ignored so skills carrying additional
// fields still load.
func parseSkill(data []byte) (Skill, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return Skill{}, errors.New("missing frontmatter: file must begin with '---'")
	}

	// Drop the opening fence line, then find the closing fence.
	rest := text[strings.IndexByte(text, '\n')+1:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Skill{}, errors.New("unterminated frontmatter: no closing '---'")
	}
	frontmatter := rest[:end]

	// Body is everything after the closing fence line.
	body := rest[end+len("\n---"):]
	if nl := strings.IndexByte(body, '\n'); nl >= 0 {
		body = body[nl+1:]
	} else {
		body = ""
	}

	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &meta); err != nil {
		return Skill{}, fmt.Errorf("invalid frontmatter yaml: %w", err)
	}

	name := strings.TrimSpace(meta.Name)
	desc := strings.TrimSpace(meta.Description)
	body = strings.TrimSpace(body)
	if name == "" {
		return Skill{}, errors.New("frontmatter 'name' is required")
	}
	if desc == "" {
		return Skill{}, errors.New("frontmatter 'description' is required")
	}
	if body == "" {
		return Skill{}, errors.New("skill body is empty")
	}
	return Skill{Name: name, Description: desc, Body: body}, nil
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./skill/ && go mod tidy && go vet ./skill/`
Expected: `ok  github.com/2389-research/mux/skill`; `go mod tidy` leaves `gopkg.in/yaml.v3 v3.0.1` as a direct require; vet clean.

- [ ] **Step 6: Commit**

```bash
git add skill/skill.go skill/skill_test.go go.mod go.sum
git commit -m "feat: add skill type and frontmatter parser"
```

---

### Task 2: Registry with directory loading and catalog

Holds parsed skills, loads them from a directory, and renders the system-prompt catalog.

**Files:**
- Create: `skill/registry.go`
- Test: `skill/registry_test.go`

**Interfaces:**
- Consumes: `Skill`, `parseSkill` (Task 1).
- Produces:
  - `func NewRegistry() *Registry`
  - `func LoadDir(dir string) (*Registry, error)`
  - `func (r *Registry) Register(s Skill) error` — errors on duplicate name
  - `func (r *Registry) Get(name string) (Skill, bool)`
  - `func (r *Registry) All() []Skill` — sorted by name
  - `func (r *Registry) List() []string` — sorted names
  - `func (r *Registry) Count() int`
  - `func (r *Registry) Catalog() string` — menu text, `""` when empty

- [ ] **Step 1: Write the failing test**

Create `skill/registry_test.go`:
```go
// ABOUTME: Tests for the skill Registry: directory loading, duplicate detection,
// ABOUTME: accessors, and catalog rendering.
package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkillDir creates <root>/<name>/SKILL.md with the given frontmatter + body.
func writeSkillDir(t *testing.T, root, name, desc, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDir(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "beta", "Second skill.", "do beta")
	writeSkillDir(t, root, "alpha", "First skill.", "do alpha")
	// A subdirectory without a SKILL.md is ignored.
	if err := os.MkdirAll(filepath.Join(root, "notaskill"), 0o755); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadDir(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.Count() != 2 {
		t.Fatalf("Count = %d, want 2", reg.Count())
	}
	if got := reg.List(); got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("List = %v, want [alpha beta]", got)
	}
	s, ok := reg.Get("alpha")
	if !ok || s.Body != "do alpha" {
		t.Errorf("Get(alpha) = %+v, %v", s, ok)
	}
}

func TestLoadDirMissingDirErrors(t *testing.T) {
	if _, err := LoadDir(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("expected error for missing directory")
	}
}

func TestLoadDirDuplicateNameErrors(t *testing.T) {
	root := t.TempDir()
	// Two different directories whose frontmatter declares the same name.
	writeSkillDir(t, root, "dir-one", "First.", "body one")
	dir2 := filepath.Join(root, "dir-two")
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatal(err)
	}
	dup := "---\nname: dir-one\ndescription: Clash.\n---\n\nbody two\n"
	if err := os.WriteFile(filepath.Join(dir2, "SKILL.md"), []byte(dup), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(root); err == nil {
		t.Error("expected duplicate-name error")
	}
}

func TestLoadDirMalformedSkillErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("no frontmatter here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDir(root); err == nil {
		t.Error("expected parse error to propagate")
	}
}

func TestCatalog(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "beta", "Second skill.", "do beta")
	writeSkillDir(t, root, "alpha", "First skill.", "do alpha")
	reg, err := LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	cat := reg.Catalog()
	if !strings.Contains(cat, "## Available Skills") {
		t.Errorf("catalog missing header:\n%s", cat)
	}
	if !strings.Contains(cat, "- **alpha** — First skill.") {
		t.Errorf("catalog missing alpha entry:\n%s", cat)
	}
	if !strings.Contains(cat, "- **beta** — Second skill.") {
		t.Errorf("catalog missing beta entry:\n%s", cat)
	}
	// alpha sorts before beta.
	if strings.Index(cat, "alpha") > strings.Index(cat, "beta") {
		t.Errorf("catalog not sorted:\n%s", cat)
	}
}

func TestCatalogEmpty(t *testing.T) {
	if got := NewRegistry().Catalog(); got != "" {
		t.Errorf("empty catalog = %q, want \"\"", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./skill/ -run 'TestLoadDir|TestCatalog'`
Expected: build failure — `undefined: LoadDir`, `undefined: NewRegistry`.

- [ ] **Step 3: Write the minimal implementation**

Create `skill/registry.go`:
```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./skill/ && go vet ./skill/`
Expected: `ok  github.com/2389-research/mux/skill`; vet clean.

- [ ] **Step 5: Commit**

```bash
git add skill/registry.go skill/registry_test.go
git commit -m "feat: add skill registry with directory loading and catalog"
```

---

### Task 3: The load_skill tool

The on-demand half of progressive disclosure: a `tool.Tool` that returns a skill's body.

**Files:**
- Create: `skill/tool.go`
- Test: `skill/tool_test.go`

**Interfaces:**
- Consumes: `Registry`, `(*Registry).Get` (Task 2); `tool.Tool`, `tool.Result`, `tool.NewResult`, `tool.NewErrorResult` from `github.com/2389-research/mux/tool`.
- Produces:
  - `func (r *Registry) Tool() tool.Tool` — returns the `load_skill` tool bound to `r`
  - the tool's `Name()` is `"load_skill"`, `RequiresApproval` is always `false`, and it implements `tool.SchemaProvider`.

- [ ] **Step 1: Write the failing test**

Create `skill/tool_test.go`:
```go
// ABOUTME: Tests for the load_skill tool: metadata, schema, and Execute behavior
// ABOUTME: across found, unknown, and malformed-argument cases.
package skill

import (
	"context"
	"testing"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.Register(Skill{Name: "greet", Description: "Say hi.", Body: "Say hello to the user."}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLoadSkillToolMetadata(t *testing.T) {
	tl := newTestRegistry(t).Tool()
	if tl.Name() != "load_skill" {
		t.Errorf("Name = %q, want load_skill", tl.Name())
	}
	if tl.Description() == "" {
		t.Error("Description is empty")
	}
	if tl.RequiresApproval(nil) {
		t.Error("load_skill must not require approval")
	}
	sp, ok := tl.(interface{ InputSchema() map[string]any })
	if !ok {
		t.Fatal("load_skill tool does not implement InputSchema")
	}
	schema := sp.InputSchema()
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}
}

func TestLoadSkillToolExecuteFound(t *testing.T) {
	tl := newTestRegistry(t).Tool()
	res, err := tl.Execute(context.Background(), map[string]any{"name": "greet"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Success {
		t.Errorf("Success = false, Error = %q", res.Error)
	}
	if res.Output != "Say hello to the user." {
		t.Errorf("Output = %q", res.Output)
	}
}

func TestLoadSkillToolExecuteUnknown(t *testing.T) {
	tl := newTestRegistry(t).Tool()
	res, err := tl.Execute(context.Background(), map[string]any{"name": "missing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Error("Success = true for unknown skill, want false")
	}
}

func TestLoadSkillToolExecuteBadArgs(t *testing.T) {
	tl := newTestRegistry(t).Tool()
	cases := []map[string]any{
		{},                  // missing name
		{"name": ""},        // empty name
		{"name": 42},        // non-string name
	}
	for i, params := range cases {
		res, err := tl.Execute(context.Background(), params)
		if err != nil {
			t.Fatalf("case %d: unexpected error: %v", i, err)
		}
		if res.Success {
			t.Errorf("case %d: Success = true, want false", i)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./skill/ -run TestLoadSkillTool`
Expected: build failure — `r.Tool undefined (type *Registry has no field or method Tool)`.

- [ ] **Step 3: Write the minimal implementation**

Create `skill/tool.go`:
```go
// ABOUTME: Implements the load_skill tool — an ordinary tool.Tool that returns a
// ABOUTME: skill's markdown body so the model can follow it as a tool_result.
package skill

import (
	"context"
	"strings"

	"github.com/2389-research/mux/tool"
)

// loadSkillToolName is the registered name of the load_skill tool.
const loadSkillToolName = "load_skill"

// Tool returns the load_skill tool bound to this registry. Registering it and
// injecting Catalog() into the system prompt is what makes the registry's skills
// available to an agent.
func (r *Registry) Tool() tool.Tool {
	return &loadSkillTool{reg: r}
}

// loadSkillTool is the tool.Tool implementation backing load_skill.
type loadSkillTool struct {
	reg *Registry
}

func (t *loadSkillTool) Name() string { return loadSkillToolName }

func (t *loadSkillTool) Description() string {
	return "Load the full instructions for a skill by name. Call this with a skill " +
		"name from the Available Skills list before acting on that skill."
}

// RequiresApproval is always false: loading a skill is a pure read with no side effects.
func (t *loadSkillTool) RequiresApproval(map[string]any) bool { return false }

// InputSchema advertises the single required string parameter, name.
func (t *loadSkillTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "The name of the skill to load, from the Available Skills list.",
			},
		},
		"required": []string{"name"},
	}
}

// Execute returns the named skill's body. An unknown, missing, empty, or non-string
// name yields a failed Result (a recoverable error tool_result), never a Go error.
func (t *loadSkillTool) Execute(_ context.Context, params map[string]any) (*tool.Result, error) {
	name, ok := params["name"].(string)
	if !ok || strings.TrimSpace(name) == "" {
		return tool.NewErrorResult(loadSkillToolName, "load_skill requires a non-empty string 'name' parameter"), nil
	}
	s, ok := t.reg.Get(name)
	if !ok {
		return tool.NewErrorResult(loadSkillToolName, "unknown skill: "+name), nil
	}
	return tool.NewResult(loadSkillToolName, true, s.Body, ""), nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./skill/ && go vet ./skill/`
Expected: `ok  github.com/2389-research/mux/skill`; vet clean.

- [ ] **Step 5: Commit**

```bash
git add skill/tool.go skill/tool_test.go
git commit -m "feat: add load_skill tool"
```

---

### Task 4: Wire skills into the agent

Adds `Config.Skills` and makes `agent.init()` inject the catalog, register `load_skill`, and auto-allow it — without mutating the stored config.

**Files:**
- Modify: `agent/config.go` (add the field), `agent/agent.go` (`init()` + two helpers)
- Test: `agent/skills_test.go`

**Interfaces:**
- Consumes: `skill.Registry`, `(*skill.Registry).Tool()`, `(*skill.Registry).Catalog()` (Tasks 2–3); existing `tool.NewFilteredRegistry`, `(*tool.Registry).Register`, `orchestrator` config fields. Tests reuse the existing `capturingClient` and `scriptedClient` doubles from `agent/agent_test.go`, plus `(*Agent).Messages()` and the `llm.ContentBlock{Type: ContentTypeToolResult, Name, Text}` shape the orchestrator produces.
- Produces: `agent.Config.Skills *skill.Registry`; wiring behavior in `init()`.

- [ ] **Step 1: Write the failing test**

Create `agent/skills_test.go`. **Reuse the existing test doubles** `capturingClient` (captures `lastRequest`) and `scriptedClient` (plays back a fixed response sequence), both already defined in `agent/agent_test.go` in this same `agent_test` package — do **not** redefine them (duplicate-type compile error) and do **not** add a new client double.
```go
// ABOUTME: Agent-level integration tests for skills: catalog injection into the
// ABOUTME: system prompt, load_skill registration, and allowlist auto-reachability.
package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/skill"
	"github.com/2389-research/mux/tool"
)

// skillsDir builds a one-skill registry (greet) from a temp directory.
func skillsDir(t *testing.T) *skill.Registry {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "greet")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: greet\ndescription: Say hi to the user.\n---\n\nSay hello warmly.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := skill.LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// toolResultText returns the Text of the first tool_result block for the named
// tool in the conversation history, or "" if none is present.
func toolResultText(msgs []llm.Message, toolName string) string {
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.ContentTypeToolResult && b.Name == toolName {
				return b.Text
			}
		}
	}
	return ""
}

func TestAgentInjectsSkillCatalog(t *testing.T) {
	client := &capturingClient{
		response: &llm.Response{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}}},
	}
	a := agent.New(agent.Config{
		Name:         "root",
		Registry:     tool.NewRegistry(),
		LLMClient:    client,
		SystemPrompt: "Base prompt.",
		Skills:       skillsDir(t),
	})
	if err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.lastRequest == nil {
		t.Fatal("no request captured")
	}
	sys := client.lastRequest.System
	if !strings.Contains(sys, "Base prompt.") {
		t.Errorf("system prompt lost the base:\n%s", sys)
	}
	if !strings.Contains(sys, "## Available Skills") || !strings.Contains(sys, "- **greet** — Say hi to the user.") {
		t.Errorf("system prompt missing catalog:\n%s", sys)
	}
}

func TestAgentLoadSkillRoundTrip(t *testing.T) {
	client := &scriptedClient{responses: []*llm.Response{
		{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "load_skill", Input: map[string]any{"name": "greet"}}},
			StopReason: llm.StopReasonToolUse,
		},
		{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		},
	}}
	a := agent.New(agent.Config{
		Name:      "root",
		Registry:  tool.NewRegistry(),
		LLMClient: client,
		Skills:    skillsDir(t),
	})
	if err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := toolResultText(a.Messages(), "load_skill"); got != "Say hello warmly." {
		t.Errorf("load_skill result = %q, want skill body", got)
	}
}

func TestAgentLoadSkillReachableWithAllowlist(t *testing.T) {
	client := &scriptedClient{responses: []*llm.Response{
		{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeToolUse, ID: "call-1", Name: "load_skill", Input: map[string]any{"name": "greet"}}},
			StopReason: llm.StopReasonToolUse,
		},
		{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		},
	}}
	// Non-empty allowlist that omits load_skill: wiring must auto-allow it.
	a := agent.New(agent.Config{
		Name:         "root",
		Registry:     tool.NewRegistry(),
		LLMClient:    client,
		AllowedTools: []string{"some_other_tool"},
		Skills:       skillsDir(t),
	})
	if err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := toolResultText(a.Messages(), "load_skill"); got != "Say hello warmly." {
		t.Errorf("load_skill not reachable under allowlist: got %q", got)
	}
	// The caller's stored allowlist is unchanged (no silent mutation).
	if got := a.Config().AllowedTools; len(got) != 1 || got[0] != "some_other_tool" {
		t.Errorf("Config().AllowedTools = %v, want [some_other_tool]", got)
	}
}

func TestAgentNoSkillsUnaffected(t *testing.T) {
	client := &capturingClient{
		response: &llm.Response{Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}}},
	}
	reg := tool.NewRegistry()
	a := agent.New(agent.Config{
		Name:      "root",
		Registry:  reg,
		LLMClient: client,
	})
	if err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if client.lastRequest != nil && strings.Contains(client.lastRequest.System, "## Available Skills") {
		t.Error("catalog injected without Skills set")
	}
	if _, ok := reg.Get("load_skill"); ok {
		t.Error("load_skill registered without Skills set")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./agent/ -run TestAgent`
Expected: build failure — `unknown field 'Skills' in struct literal of type agent.Config`.

- [ ] **Step 3: Add the Config field**

In `agent/config.go`, add the import and the field. Add to the import block:
```go
	"github.com/2389-research/mux/skill"
```
Add this field at the end of the `Config` struct (after `ApprovalMode`):
```go
	// Skills, when non-nil, exposes its skills to this agent: the catalog is
	// injected into the system prompt and the load_skill tool is registered and
	// permitted. Construct it with skill.LoadDir at startup.
	Skills *skill.Registry
```

- [ ] **Step 4: Wire skills in init()**

In `agent/agent.go`, replace the `init` method (currently at lines 66-95) with this version. The only additions are the skills block before building the filtered registry, the catalog injection after the system prompt is set, and the two helpers below.
```go
func (a *Agent) init() {
	// When skills are configured, register the load_skill tool and ensure it is
	// reachable through this agent's filtered registry.
	allowed := a.config.AllowedTools
	if a.config.Skills != nil {
		loadTool := a.config.Skills.Tool()
		a.config.Registry.Register(loadTool)
		allowed = ensureAllowed(allowed, loadTool.Name())
	}

	// Create filtered view of registry
	a.filtered = tool.NewFilteredRegistry(
		a.config.Registry,
		allowed,
		a.config.DeniedTools,
	)

	// Create executor with filtered registry
	a.executor = tool.NewExecutorWithSource(a.filtered)
	if a.config.ApprovalFunc != nil {
		a.executor.SetApprovalFunc(a.config.ApprovalFunc)
	}

	// Create orchestrator config
	orchConfig := orchestrator.DefaultConfig()
	if a.config.SystemPrompt != "" {
		orchConfig.SystemPrompt = a.config.SystemPrompt
	}
	if a.config.MaxIterations > 0 {
		orchConfig.MaxIterations = a.config.MaxIterations
	}
	orchConfig.HookManager = a.hookManager
	orchConfig.ThinkingSettings = a.config.ThinkingSettings
	orchConfig.SessionStore = a.config.SessionStore
	orchConfig.ApprovalMode = a.config.ApprovalMode

	// Inject the skills catalog into the effective system prompt (default or custom).
	if a.config.Skills != nil {
		if cat := a.config.Skills.Catalog(); cat != "" {
			orchConfig.SystemPrompt = appendSystemSection(orchConfig.SystemPrompt, cat)
		}
	}

	// Create orchestrator
	a.orch = orchestrator.NewWithConfig(a.config.LLMClient, a.executor, orchConfig)
}

// ensureAllowed returns an allowlist that permits name. An empty allowlist already
// permits everything, so it is returned unchanged. Otherwise name is appended to a
// copy — the caller's stored Config.AllowedTools is never mutated.
func ensureAllowed(allowed []string, name string) []string {
	if len(allowed) == 0 {
		return allowed
	}
	for _, a := range allowed {
		if a == name {
			return allowed
		}
	}
	out := make([]string, len(allowed)+1)
	copy(out, allowed)
	out[len(allowed)] = name
	return out
}

// appendSystemSection joins a base system prompt and an added section with a blank
// line, returning the section alone when the base is empty.
func appendSystemSection(base, section string) string {
	if base == "" {
		return section
	}
	return base + "\n\n" + section
}
```
Do **not** add a `skill` import to `agent/agent.go`. Go imports are per-file, and `init()` only calls methods on `a.config.Skills` (`.Tool()`, `.Catalog()`) without ever naming the `skill` package identifier — adding the import would be an unused-import compile error. The `skill` import belongs only in `config.go` (Step 3), which names the type `*skill.Registry`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./agent/ ./skill/ && go vet ./...`
Expected: `ok  github.com/2389-research/mux/agent` and `ok  github.com/2389-research/mux/skill`; vet clean across all packages.

- [ ] **Step 6: Run the full suite**

Run: `go test ./...`
Expected: every package `ok` (or cached), pristine output, no failures.

- [ ] **Step 7: Commit**

```bash
git add agent/config.go agent/agent.go agent/skills_test.go
git commit -m "feat: wire skills into agent via Config.Skills"
```

---

## Self-Review

**1. Spec coverage:**
- `skill` package, `agent → skill → tool` direction, orchestrator untouched → Tasks 1–4 (no orchestrator file is modified).
- Claude-Code `<dir>/<name>/SKILL.md` format, yaml.v3 frontmatter, extra keys ignored → Task 1 (`parseSkill`, `TestParseSkillIgnoresExtraKeys`) + Task 2 (`LoadDir`).
- yaml.v3 promoted from transitive → direct → Task 1 Step 1.
- `Skill{Name,Description,Body}` → Task 1.
- `Registry` with `Get/All/List/Count` + `LoadDir` → Task 2.
- `Catalog()` cheap menu, `""` when empty → Task 2 (`TestCatalog`, `TestCatalogEmpty`).
- `load_skill` tool: name, no approval, schema, body as output, recoverable errors → Task 3.
- `agent.Config.Skills`, catalog injection, tool registration, auto-allow without mutating stored config → Task 4 (incl. `TestAgentLoadSkillReachableWithAllowlist` asserting `Config().AllowedTools` unchanged).
- Fail-fast load errors (missing dir, malformed, duplicate, empty fields) → Tasks 1–2.
- Backward compatibility (Skills nil ⇒ no catalog, no tool) → Task 4 (`TestAgentNoSkillsUnaffected`).
- Tests use real files + scripted `llm` client (house pattern) → Tasks 1–4.

**2. Placeholder scan:** No `TBD`/`TODO`/"handle edge cases" — every code and test step contains complete code, exact commands, and expected output.

**3. Type consistency:** `Skill{Name,Description,Body}`, `parseSkill([]byte) (Skill, error)`, `LoadDir(string) (*Registry, error)`, `Register(Skill) error`, `Catalog() string`, `Tool() tool.Tool`, and `Config.Skills *skill.Registry` are referenced identically across tasks. The tool name `"load_skill"` is the constant `loadSkillToolName` everywhere in production code; tests assert the literal. The Task 4 round-trip asserts via `(*Agent).Messages()`, matching the orchestrator's tool-result block shape `llm.ContentBlock{Type: ContentTypeToolResult, Name: use.Name, Text: result.Output}` (orchestrator.go:540-546) — no dependency on event-channel close semantics. Task 4 reuses `capturingClient`/`scriptedClient` from `agent_test.go` rather than defining new doubles.
