# Agent Tool Registration Design

## Problem

Mux needs to support different agent types with different tool sets:
- Productivity agents (email, calendar, contacts)
- Code agents (read file, write file, bash)
- Social media agents (post, read feed)

Currently, all tools in a registry are available to every orchestrator. There's no mechanism for per-agent tool filtering.

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Filtering approach | Tool filtering (not separate registries) | Hex/jeff pattern; MCP loads once |
| Access control | Both allowlist and denylist | Maximum flexibility |
| Configuration | Struct-based (not builder) | Explicit, serializable |
| Empty AllowedTools | Inherit from parent | Intuitive spawn behavior |
| Registry ownership | Shared registry, agent filters | MCP efficiency |

## Core Types

### AgentConfig

```go
// agent/config.go
type AgentConfig struct {
    // Identity
    Name string // Agent name (becomes part of hierarchical ID)

    // Tool access control
    AllowedTools []string // Tools agent CAN use (empty = inherit or all)
    DeniedTools  []string // Tools agent CANNOT use (takes precedence)

    // Dependencies
    Registry  *tool.Registry // Shared tool registry
    LLMClient llm.Client     // LLM for this agent

    // Optional
    SystemPrompt string                              // Custom system prompt
    ApprovalFunc func(...) (bool, error)            // Permission callback
}
```

### Agent

```go
// agent/agent.go
type Agent struct {
    id       string       // Hierarchical ID (e.g., "root.researcher.1")
    config   AgentConfig
    parent   *Agent       // nil for root agents
    children []*Agent     // Spawned child agents

    executor     *tool.Executor
    orchestrator *orchestrator.Orchestrator
}
```

## Tool Filtering

FilteredRegistry wraps a registry with allow/deny logic:

```go
// tool/filter.go
type FilteredRegistry struct {
    source       *Registry
    allowedTools []string // empty = all allowed
    deniedTools  []string // takes precedence over allowed
}

func (f *FilteredRegistry) isAllowed(name string) bool {
    // Denied always wins
    if contains(f.deniedTools, name) {
        return false
    }
    // Empty allowed = all allowed
    if len(f.allowedTools) == 0 {
        return true
    }
    return contains(f.allowedTools, name)
}
```

## Agent Hierarchy

Child agents inherit from parents with restricted permissions:

```go
func (a *Agent) SpawnChild(cfg AgentConfig) (*Agent, error) {
    // Validate: child can't have tools parent doesn't have
    if err := a.validateChildTools(cfg); err != nil {
        return nil, err
    }

    child := &Agent{
        id:     a.id + "." + cfg.Name, // "parent.child"
        config: a.mergeConfig(cfg),
        parent: a,
    }
    child.init()

    a.children = append(a.children, child)
    return child, nil
}
```

### Inheritance Rules

- Child ID = `parent.id + "." + child.name` (for tracing)
- Empty `AllowedTools` = inherit parent's list
- `DeniedTools` accumulate (union of parent + child)
- Child cannot allow tools parent doesn't have

## Integration

Agent composes existing mux components:

```go
func (a *Agent) init() {
    // Create filtered view of registry
    filtered := tool.NewFilteredRegistry(
        a.config.Registry,
        a.config.AllowedTools,
        a.config.DeniedTools,
    )

    // Create executor with filtered registry
    a.executor = tool.NewExecutor(filtered)

    // Create orchestrator with executor
    a.orchestrator = orchestrator.New(a.config.LLMClient, a.executor)
}
```

### What Stays the Same

- `tool.Registry`, `tool.Executor`, `orchestrator.Orchestrator` unchanged
- Event system unchanged
- Permission hooks unchanged

### What's New

- `agent.Agent` type composes existing pieces
- `tool.FilteredRegistry` wraps registry with allow/deny logic
- Hierarchy tracking via parent/children

## MCP Integration

MCP tools load once into shared registry, agents filter:

```go
// Shared registry
registry := tool.NewRegistry()

// Load MCP tools once (expensive stdio processes)
mcpClient, _ := mcp.NewClient(mcp.ClientConfig{
    Command: "npx",
    Args:    []string{"-y", "@modelcontextprotocol/server-github"},
})
mcpManager := mcp.NewToolManager(mcpClient)
mcpManager.RegisterAll(registry)

// Agents filter what they need
codeAgent := agent.NewAgent(agent.AgentConfig{
    Name:         "code-agent",
    Registry:     registry,
    AllowedTools: []string{"read_file", "github_create_issue"},
})
```

## Example: Multi-Agent System

```go
func main() {
    registry := tool.NewRegistry()
    // ... register all tools ...

    // Root orchestrator - has broad access
    orch := agent.NewAgent(agent.AgentConfig{
        Name:         "orchestrator",
        Registry:     registry,
        AllowedTools: []string{"task", "read_file", "write_file", "bash"},
        LLMClient:    client,
    })

    // Spawn specialized children
    researcher, _ := orch.SpawnChild(agent.AgentConfig{
        Name:         "researcher",
        AllowedTools: []string{"read_file"}, // Restricted
    })
    // researcher.ID() = "orchestrator.researcher"

    coder, _ := orch.SpawnChild(agent.AgentConfig{
        Name:         "coder",
        AllowedTools: []string{"read_file", "write_file", "bash"},
        DeniedTools:  []string{"rm_rf"},
    })
    // coder.ID() = "orchestrator.coder"

    // Nested hierarchy
    tester, _ := coder.SpawnChild(agent.AgentConfig{
        Name: "tester",
        // Inherits coder's tools, inherits denied: ["rm_rf"]
    })
    // tester.ID() = "orchestrator.coder.tester"
}
```

## Migration Path

### From Jeff

Replace jeff's `Agent` with mux's `agent.Agent`. The spawn pattern and permission inheritance map directly.

### From Hex

Hex's subagent types become AgentConfig presets:

```go
var ExploreAgentConfig = agent.AgentConfig{
    AllowedTools: []string{"Read", "Grep", "Glob", "Bash"},
}
```

## Files to Create/Modify

| File | Action |
|------|--------|
| `agent/config.go` | New - AgentConfig struct |
| `agent/agent.go` | New - Agent type with spawn |
| `tool/filter.go` | New - FilteredRegistry |
| `tool/registry.go` | Modify - implement ToolSource interface |
