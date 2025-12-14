# Agent Tool Registration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Enable different agents to have different tool sets via filtering on a shared registry.

**Architecture:** Agent type wraps orchestrator with per-agent tool filtering. FilteredRegistry provides a view of the shared registry with allow/deny lists. Child agents inherit and can further restrict parent's tools.

**Tech Stack:** Go 1.21+, standard library only, TDD with table-driven tests

---

## Task 1: FilteredRegistry - Core Filtering Logic

**Files:**
- Create: `tool/filter.go`
- Create: `tool/filter_test.go`

### Step 1: Write failing test for FilteredRegistry construction

```go
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
```

### Step 2: Run test to verify it fails

Run: `go test ./tool -run TestNewFilteredRegistry -v`
Expected: FAIL - undefined: tool.NewFilteredRegistry

### Step 3: Write minimal implementation

```go
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
```

### Step 4: Run test to verify it passes

Run: `go test ./tool -run TestNewFilteredRegistry -v`
Expected: PASS

### Step 5: Commit

```bash
git add tool/filter.go tool/filter_test.go
git commit -m "feat(tool): add FilteredRegistry constructor"
```

---

## Task 2: FilteredRegistry.isAllowed helper

**Files:**
- Modify: `tool/filter.go`
- Modify: `tool/filter_test.go`

### Step 1: Write failing tests for isAllowed logic

```go
// tool/filter_test.go (append)

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
```

### Step 2: Run test to verify it fails

Run: `go test ./tool -run TestFilteredRegistry_isAllowed -v`
Expected: FAIL - f.IsAllowed undefined

### Step 3: Write implementation

```go
// tool/filter.go (append)

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
```

### Step 4: Run test to verify it passes

Run: `go test ./tool -run TestFilteredRegistry_isAllowed -v`
Expected: PASS

### Step 5: Commit

```bash
git add tool/filter.go tool/filter_test.go
git commit -m "feat(tool): add FilteredRegistry.IsAllowed method"
```

---

## Task 3: FilteredRegistry.Get and All methods

**Files:**
- Modify: `tool/filter.go`
- Modify: `tool/filter_test.go`

### Step 1: Write failing tests

```go
// tool/filter_test.go (append)

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
```

### Step 2: Run test to verify it fails

Run: `go test ./tool -run "TestFilteredRegistry_(Get|All)" -v`
Expected: FAIL - f.Get undefined, f.All undefined

### Step 3: Write implementation

```go
// tool/filter.go (append)

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
```

### Step 4: Run test to verify it passes

Run: `go test ./tool -run "TestFilteredRegistry_(Get|All)" -v`
Expected: PASS

### Step 5: Commit

```bash
git add tool/filter.go tool/filter_test.go
git commit -m "feat(tool): add FilteredRegistry.Get and All methods"
```

---

## Task 4: FilteredRegistry remaining Registry methods

**Files:**
- Modify: `tool/filter.go`
- Modify: `tool/filter_test.go`

### Step 1: Write failing tests

```go
// tool/filter_test.go (append)

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
```

### Step 2: Run test to verify it fails

Run: `go test ./tool -run "TestFilteredRegistry_(List|Count)" -v`
Expected: FAIL - f.List undefined, f.Count undefined

### Step 3: Write implementation

```go
// tool/filter.go (append)

// List returns names of all tools that pass the filter, sorted alphabetically.
func (f *FilteredRegistry) List() []string {
	all := f.All()
	names := make([]string, 0, len(all))
	for _, t := range all {
		names = append(names, t.Name())
	}
	sort.Strings(names)
	return names
}

// Count returns the number of tools that pass the filter.
func (f *FilteredRegistry) Count() int {
	return len(f.All())
}
```

Also add import at top of filter.go:
```go
import "sort"
```

### Step 4: Run test to verify it passes

Run: `go test ./tool -run "TestFilteredRegistry_(List|Count)" -v`
Expected: PASS

### Step 5: Commit

```bash
git add tool/filter.go tool/filter_test.go
git commit -m "feat(tool): add FilteredRegistry.List and Count methods"
```

---

## Task 5: ToolSource interface for Executor compatibility

**Files:**
- Modify: `tool/tool.go`
- Modify: `tool/executor.go`
- Modify: `tool/filter.go`

### Step 1: Write failing test - Executor with FilteredRegistry

```go
// tool/filter_test.go (append)

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
```

### Step 2: Run test to verify it fails

Run: `go test ./tool -run TestFilteredRegistryWithExecutor -v`
Expected: FAIL - NewExecutorWithSource undefined

### Step 3: Write implementation

```go
// tool/tool.go (append after SchemaProvider interface)

// ToolSource provides access to tools. Both Registry and FilteredRegistry implement this.
type ToolSource interface {
	Get(name string) (Tool, bool)
	All() []Tool
	List() []string
	Count() int
}
```

```go
// tool/executor.go - modify Executor struct and add new constructor

// Executor struct change: registry *Registry -> source ToolSource
type Executor struct {
	source       ToolSource
	approvalFunc ApprovalFunc
	beforeHooks  []BeforeHook
	afterHooks   []AfterHook
}

// NewExecutor creates a new Executor with the given Registry.
// Panics if registry is nil.
func NewExecutor(registry *Registry) *Executor {
	if registry == nil {
		panic("mux: registry must not be nil")
	}
	return &Executor{
		source:      registry,
		beforeHooks: make([]BeforeHook, 0),
		afterHooks:  make([]AfterHook, 0),
	}
}

// NewExecutorWithSource creates a new Executor with any ToolSource.
// Panics if source is nil.
func NewExecutorWithSource(source ToolSource) *Executor {
	if source == nil {
		panic("mux: source must not be nil")
	}
	return &Executor{
		source:      source,
		beforeHooks: make([]BeforeHook, 0),
		afterHooks:  make([]AfterHook, 0),
	}
}

// Source returns the underlying tool source.
func (e *Executor) Source() ToolSource {
	return e.source
}

// Execute method: change e.registry.Get to e.source.Get
func (e *Executor) Execute(ctx context.Context, toolName string, params map[string]any) (*Result, error) {
	t, ok := e.source.Get(toolName)
	// ... rest stays the same
}
```

Also update Registry() method to handle backwards compatibility:
```go
// Registry returns the underlying tool registry if source is a Registry.
// Returns nil if source is a different ToolSource type.
func (e *Executor) Registry() *Registry {
	if reg, ok := e.source.(*Registry); ok {
		return reg
	}
	return nil
}
```

### Step 4: Run test to verify it passes

Run: `go test ./tool -run TestFilteredRegistryWithExecutor -v`
Expected: PASS

Also run all tool tests to ensure no regressions:
Run: `go test ./tool -v`
Expected: All PASS

### Step 5: Commit

```bash
git add tool/tool.go tool/executor.go tool/filter.go tool/filter_test.go
git commit -m "feat(tool): add ToolSource interface for executor compatibility"
```

---

## Task 6: Create agent package with Config

**Files:**
- Create: `agent/config.go`
- Create: `agent/config_test.go`

### Step 1: Write failing test for AgentConfig

```go
// agent/config_test.go
package agent_test

import (
	"testing"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/tool"
)

func TestAgentConfig(t *testing.T) {
	registry := tool.NewRegistry()

	cfg := agent.Config{
		Name:         "test-agent",
		AllowedTools: []string{"read_file"},
		DeniedTools:  []string{"bash"},
		Registry:     registry,
	}

	if cfg.Name != "test-agent" {
		t.Errorf("expected name test-agent, got %s", cfg.Name)
	}
	if len(cfg.AllowedTools) != 1 {
		t.Errorf("expected 1 allowed tool, got %d", len(cfg.AllowedTools))
	}
}
```

### Step 2: Run test to verify it fails

Run: `go test ./agent -run TestAgentConfig -v`
Expected: FAIL - package agent does not exist

### Step 3: Write implementation

```go
// agent/config.go
// ABOUTME: Defines Config - the configuration struct for creating agents with
// ABOUTME: specific tool access, LLM clients, and execution settings.
package agent

import (
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

// Config holds configuration for creating an Agent.
type Config struct {
	// Name identifies this agent (becomes part of hierarchical ID)
	Name string

	// AllowedTools lists tools this agent can use.
	// Empty means inherit from parent or allow all (for root agents).
	AllowedTools []string

	// DeniedTools lists tools this agent cannot use.
	// Takes precedence over AllowedTools.
	DeniedTools []string

	// Registry is the shared tool registry.
	Registry *tool.Registry

	// LLMClient is the LLM provider for this agent.
	LLMClient llm.Client

	// SystemPrompt is an optional custom system prompt.
	SystemPrompt string

	// ApprovalFunc is called when tools require approval.
	ApprovalFunc tool.ApprovalFunc

	// MaxIterations limits the think-act loop (0 = default).
	MaxIterations int
}
```

### Step 4: Run test to verify it passes

Run: `go test ./agent -run TestAgentConfig -v`
Expected: PASS

### Step 5: Commit

```bash
git add agent/config.go agent/config_test.go
git commit -m "feat(agent): add Config struct"
```

---

## Task 7: Agent type with constructor

**Files:**
- Create: `agent/agent.go`
- Modify: `agent/config_test.go` -> rename to `agent/agent_test.go`

### Step 1: Write failing test for NewAgent

```go
// agent/agent_test.go (replace config_test.go content)
package agent_test

import (
	"context"
	"testing"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

// mockClient implements llm.Client for testing
type mockClient struct {
	response *llm.Response
}

func (m *mockClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return m.response, nil
}

func (m *mockClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, nil
}

// mockTool implements tool.Tool for testing
type mockTool struct {
	name string
}

func (m *mockTool) Name() string                                                     { return m.name }
func (m *mockTool) Description() string                                              { return "mock" }
func (m *mockTool) RequiresApproval(params map[string]any) bool                      { return false }
func (m *mockTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	return tool.NewResult(m.name, true, "ok", ""), nil
}

func TestNewAgent(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "hello"}},
		},
	}

	a := agent.New(agent.Config{
		Name:      "test-agent",
		Registry:  registry,
		LLMClient: client,
	})

	if a == nil {
		t.Fatal("expected non-nil agent")
	}
	if a.ID() != "test-agent" {
		t.Errorf("expected ID test-agent, got %s", a.ID())
	}
}
```

### Step 2: Run test to verify it fails

Run: `go test ./agent -run TestNewAgent -v`
Expected: FAIL - agent.New undefined

### Step 3: Write implementation

```go
// agent/agent.go
// ABOUTME: Implements the Agent type - the top-level abstraction that wraps
// ABOUTME: orchestrator with per-agent tool filtering and hierarchy support.
package agent

import (
	"context"
	"sync"

	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/tool"
)

// Agent represents an autonomous agent with its own tool access and config.
type Agent struct {
	mu       sync.RWMutex
	id       string
	config   Config
	parent   *Agent
	children []*Agent

	filtered *tool.FilteredRegistry
	executor *tool.Executor
	orch     *orchestrator.Orchestrator
}

// New creates a new root Agent with the given configuration.
func New(cfg Config) *Agent {
	a := &Agent{
		id:       cfg.Name,
		config:   cfg,
		children: make([]*Agent, 0),
	}
	a.init()
	return a
}

// ID returns the hierarchical agent ID.
func (a *Agent) ID() string {
	return a.id
}

func (a *Agent) init() {
	// Create filtered view of registry
	a.filtered = tool.NewFilteredRegistry(
		a.config.Registry,
		a.config.AllowedTools,
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

	// Create orchestrator
	a.orch = orchestrator.NewWithConfig(a.config.LLMClient, a.executor, orchConfig)
}
```

### Step 4: Run test to verify it passes

Run: `go test ./agent -run TestNewAgent -v`
Expected: PASS

### Step 5: Commit

```bash
git add agent/agent.go agent/agent_test.go
git rm agent/config_test.go 2>/dev/null || true
git commit -m "feat(agent): add Agent type with constructor"
```

---

## Task 8: Agent.Run and Subscribe methods

**Files:**
- Modify: `agent/agent.go`
- Modify: `agent/agent_test.go`

### Step 1: Write failing test

```go
// agent/agent_test.go (append)

func TestAgentRun(t *testing.T) {
	registry := tool.NewRegistry()
	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "response"}},
		},
	}

	a := agent.New(agent.Config{
		Name:      "runner",
		Registry:  registry,
		LLMClient: client,
	})

	events := a.Subscribe()

	err := a.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Drain events
	for range events {
	}
}
```

### Step 2: Run test to verify it fails

Run: `go test ./agent -run TestAgentRun -v`
Expected: FAIL - a.Subscribe undefined, a.Run undefined

### Step 3: Write implementation

```go
// agent/agent.go (append)

// Run executes the agent's think-act loop with the given prompt.
func (a *Agent) Run(ctx context.Context, prompt string) error {
	return a.orch.Run(ctx, prompt)
}

// Subscribe returns a channel for receiving orchestrator events.
func (a *Agent) Subscribe() <-chan orchestrator.Event {
	return a.orch.Subscribe()
}

// Config returns the agent's configuration.
func (a *Agent) Config() Config {
	return a.config
}

// Parent returns the parent agent, or nil for root agents.
func (a *Agent) Parent() *Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.parent
}
```

### Step 4: Run test to verify it passes

Run: `go test ./agent -run TestAgentRun -v`
Expected: PASS

### Step 5: Commit

```bash
git add agent/agent.go agent/agent_test.go
git commit -m "feat(agent): add Run and Subscribe methods"
```

---

## Task 9: Agent.SpawnChild with inheritance

**Files:**
- Modify: `agent/agent.go`
- Modify: `agent/agent_test.go`

### Step 1: Write failing tests

```go
// agent/agent_test.go (append)

func TestAgentSpawnChild(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})
	registry.Register(&mockTool{name: "write_file"})
	registry.Register(&mockTool{name: "bash"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:         "parent",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read_file", "write_file", "bash"},
	})

	// Spawn child with restricted tools
	child, err := parent.SpawnChild(agent.Config{
		Name:         "child",
		AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check hierarchical ID
	if child.ID() != "parent.child" {
		t.Errorf("expected ID parent.child, got %s", child.ID())
	}

	// Check parent reference
	if child.Parent() != parent {
		t.Error("expected child.Parent() to return parent")
	}
}

func TestAgentSpawnChildInheritsTools(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})
	registry.Register(&mockTool{name: "bash"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:         "parent",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read_file", "bash"},
	})

	// Child with empty AllowedTools inherits parent's tools
	child, err := parent.SpawnChild(agent.Config{
		Name: "child",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Child should inherit parent's allowed tools
	cfg := child.Config()
	if len(cfg.AllowedTools) != 2 {
		t.Errorf("expected child to inherit 2 tools, got %d", len(cfg.AllowedTools))
	}
}

func TestAgentSpawnChildDeniedAccumulates(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "bash"})
	registry.Register(&mockTool{name: "rm"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "ok"}},
		},
	}

	parent := agent.New(agent.Config{
		Name:        "parent",
		Registry:    registry,
		LLMClient:   client,
		DeniedTools: []string{"rm"},
	})

	child, err := parent.SpawnChild(agent.Config{
		Name:        "child",
		DeniedTools: []string{"bash"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Child should have both rm and bash denied
	cfg := child.Config()
	if len(cfg.DeniedTools) != 2 {
		t.Errorf("expected 2 denied tools, got %d: %v", len(cfg.DeniedTools), cfg.DeniedTools)
	}
}
```

### Step 2: Run test to verify it fails

Run: `go test ./agent -run "TestAgentSpawnChild" -v`
Expected: FAIL - parent.SpawnChild undefined

### Step 3: Write implementation

```go
// agent/agent.go (append)

import "errors"

var (
	ErrInvalidChildTools = errors.New("child cannot have tools parent doesn't have")
)

// SpawnChild creates a child agent with inherited configuration.
// Child tools are restricted to parent's allowed tools.
// Child denied tools accumulate (union of parent and child denied).
func (a *Agent) SpawnChild(cfg Config) (*Agent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Inherit registry if not specified
	if cfg.Registry == nil {
		cfg.Registry = a.config.Registry
	}

	// Inherit LLM client if not specified
	if cfg.LLMClient == nil {
		cfg.LLMClient = a.config.LLMClient
	}

	// Empty AllowedTools = inherit parent's allowed tools
	if len(cfg.AllowedTools) == 0 {
		cfg.AllowedTools = make([]string, len(a.config.AllowedTools))
		copy(cfg.AllowedTools, a.config.AllowedTools)
	} else {
		// Validate child tools are subset of parent tools (if parent has restrictions)
		if len(a.config.AllowedTools) > 0 {
			if err := a.validateChildTools(cfg.AllowedTools); err != nil {
				return nil, err
			}
		}
	}

	// Merge denied tools (union)
	cfg.DeniedTools = unionStrings(a.config.DeniedTools, cfg.DeniedTools)

	child := &Agent{
		id:       a.id + "." + cfg.Name,
		config:   cfg,
		parent:   a,
		children: make([]*Agent, 0),
	}
	child.init()

	a.children = append(a.children, child)
	return child, nil
}

// validateChildTools ensures child's tools are a subset of parent's allowed tools.
func (a *Agent) validateChildTools(childTools []string) error {
	parentSet := make(map[string]bool)
	for _, t := range a.config.AllowedTools {
		parentSet[t] = true
	}

	for _, t := range childTools {
		if !parentSet[t] {
			return ErrInvalidChildTools
		}
	}
	return nil
}

// unionStrings returns the union of two string slices without duplicates.
func unionStrings(a, b []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(a)+len(b))

	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// Children returns the list of child agents.
func (a *Agent) Children() []*Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	children := make([]*Agent, len(a.children))
	copy(children, a.children)
	return children
}
```

### Step 4: Run test to verify it passes

Run: `go test ./agent -run "TestAgentSpawnChild" -v`
Expected: PASS

### Step 5: Commit

```bash
git add agent/agent.go agent/agent_test.go
git commit -m "feat(agent): add SpawnChild with inheritance"
```

---

## Task 10: Agent tool filtering integration test

**Files:**
- Create: `agent/integration_test.go`

### Step 1: Write integration test

```go
// agent/integration_test.go
package agent_test

import (
	"context"
	"testing"

	"github.com/2389-research/mux/agent"
	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

func TestAgentToolFiltering(t *testing.T) {
	// Setup registry with multiple tools
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read_file"})
	registry.Register(&mockTool{name: "write_file"})
	registry.Register(&mockTool{name: "bash"})
	registry.Register(&mockTool{name: "dangerous"})

	// Mock client that uses a tool
	toolUsed := ""
	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	// Create agent with restricted tools
	a := agent.New(agent.Config{
		Name:         "restricted",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read_file"},
		DeniedTools:  []string{"dangerous"},
	})

	// Verify agent only sees allowed tools
	// We can check this through the executor's source
	exec := a.Executor()
	source := exec.Source()

	// read_file should be accessible
	if _, ok := source.Get("read_file"); !ok {
		t.Error("expected read_file to be accessible")
	}

	// write_file should NOT be accessible (not in allowed list)
	if _, ok := source.Get("write_file"); ok {
		t.Error("expected write_file to be filtered out")
	}

	// dangerous should NOT be accessible (in denied list)
	if _, ok := source.Get("dangerous"); ok {
		t.Error("expected dangerous to be filtered out")
	}

	// Verify tool count
	if source.Count() != 1 {
		t.Errorf("expected 1 visible tool, got %d", source.Count())
	}

	_ = toolUsed // silence unused warning
}

func TestAgentHierarchyToolRestriction(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "read"})
	registry.Register(&mockTool{name: "write"})
	registry.Register(&mockTool{name: "exec"})

	client := &mockClient{
		response: &llm.Response{
			Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		},
	}

	// Root agent has all tools
	root := agent.New(agent.Config{
		Name:         "root",
		Registry:     registry,
		LLMClient:    client,
		AllowedTools: []string{"read", "write", "exec"},
	})

	// Level 1: restrict to read + write
	level1, _ := root.SpawnChild(agent.Config{
		Name:         "level1",
		AllowedTools: []string{"read", "write"},
	})

	// Level 2: further restrict to read only
	level2, _ := level1.SpawnChild(agent.Config{
		Name:         "level2",
		AllowedTools: []string{"read"},
	})

	// Verify IDs
	if root.ID() != "root" {
		t.Errorf("expected root ID, got %s", root.ID())
	}
	if level1.ID() != "root.level1" {
		t.Errorf("expected root.level1 ID, got %s", level1.ID())
	}
	if level2.ID() != "root.level1.level2" {
		t.Errorf("expected root.level1.level2 ID, got %s", level2.ID())
	}

	// Verify tool counts at each level
	if root.Executor().Source().Count() != 3 {
		t.Errorf("root should have 3 tools, got %d", root.Executor().Source().Count())
	}
	if level1.Executor().Source().Count() != 2 {
		t.Errorf("level1 should have 2 tools, got %d", level1.Executor().Source().Count())
	}
	if level2.Executor().Source().Count() != 1 {
		t.Errorf("level2 should have 1 tool, got %d", level2.Executor().Source().Count())
	}
}
```

### Step 2: Run test to verify it fails

Run: `go test ./agent -run "TestAgent(ToolFiltering|Hierarchy)" -v`
Expected: FAIL - a.Executor undefined

### Step 3: Add Executor accessor to Agent

```go
// agent/agent.go (append)

// Executor returns the agent's tool executor.
func (a *Agent) Executor() *tool.Executor {
	return a.executor
}
```

### Step 4: Run test to verify it passes

Run: `go test ./agent -run "TestAgent(ToolFiltering|Hierarchy)" -v`
Expected: PASS

### Step 5: Run all tests

Run: `go test ./... -v`
Expected: All PASS

### Step 6: Commit

```bash
git add agent/agent.go agent/integration_test.go
git commit -m "feat(agent): add integration tests for tool filtering"
```

---

## Task 11: Final cleanup and documentation

**Files:**
- Update: `docs/plans/2025-01-13-agent-tool-registration-design.md` (mark complete)

### Step 1: Run full test suite

Run: `go test ./... -v`
Expected: All PASS

### Step 2: Update design doc

Add "Status: Implemented" at the top of the design document.

### Step 3: Final commit

```bash
git add docs/plans/2025-01-13-agent-tool-registration-design.md
git commit -m "docs: mark agent tool registration as implemented"
```

---

## Summary

| Task | Component | Tests |
|------|-----------|-------|
| 1 | FilteredRegistry constructor | 1 |
| 2 | FilteredRegistry.IsAllowed | 6 |
| 3 | FilteredRegistry.Get/All | 2 |
| 4 | FilteredRegistry.List/Count | 2 |
| 5 | ToolSource interface | 1 |
| 6 | Config struct | 1 |
| 7 | Agent constructor | 1 |
| 8 | Agent.Run/Subscribe | 1 |
| 9 | Agent.SpawnChild | 3 |
| 10 | Integration tests | 2 |
| 11 | Final cleanup | - |

**Total: 11 tasks, ~20 tests**
