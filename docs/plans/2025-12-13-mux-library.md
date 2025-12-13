# Mux Library Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Extract the common agentic middle layer from jeff and hex into a reusable Go library that handles tool execution, MCP integration, permission-gated approval flows, and agent orchestration.

**Architecture:** Interface-driven design with pluggable components. The library provides: (1) a universal Tool interface with Registry and Executor, (2) MCP client with automatic Tool adaptation, (3) event-driven Orchestrator with state machine, (4) permission system with approval hooks. Consumers provide their own LLM client implementation and UI layer.

**Tech Stack:** Go 1.23+, no external dependencies for core (stdlib only), optional MCP support via JSON-RPC 2.0 over stdio.

---

## How Mux Differs from Fantasy

| Aspect | Fantasy | Mux |
|--------|---------|-----------|
| **Focus** | Multi-provider LLM abstraction | Agentic execution infrastructure |
| **Tool System** | Simple function wrapping with generics | Registry + Executor + Permission gates |
| **MCP Support** | None | First-class MCP protocol integration |
| **Permissions** | None | Full approval flow with hooks |
| **State Machine** | Implicit in loop | Explicit, validated state transitions |
| **Event Model** | Callbacks | Channel-based event streaming |
| **Multi-Agent** | None | Composable (voting, decomposition) |
| **LLM Coupling** | Tightly coupled to providers | LLM-agnostic via interface |

**Key Differentiator:** Fantasy answers "how do I call different LLMs?" Mux answers "how do I build a robust agentic system with tools, permissions, and orchestration?"

---

## Package Structure

```
mux/
├── go.mod
├── tool/                    # Core tool abstractions
│   ├── tool.go              # Tool interface
│   ├── registry.go          # Tool discovery and management
│   ├── executor.go          # Permission-gated execution
│   ├── result.go            # Unified result type
│   └── tool_test.go
├── mcp/                     # Model Context Protocol
│   ├── client.go            # JSON-RPC 2.0 stdio client
│   ├── adapter.go           # MCP→Tool interface wrapper
│   ├── config.go            # Server configuration
│   ├── types.go             # MCP protocol types
│   └── mcp_test.go
├── orchestrator/            # Agent loop orchestration
│   ├── orchestrator.go      # Main orchestration logic
│   ├── state.go             # State machine
│   ├── events.go            # Event types and channels
│   ├── loop.go              # The agentic think-act loop
│   └── orchestrator_test.go
├── permission/              # Permission system
│   ├── checker.go           # Permission interface
│   ├── rules.go             # Rule-based permissions
│   ├── mode.go              # Permission modes (auto/ask/deny)
│   └── permission_test.go
├── llm/                     # LLM abstraction (interfaces only)
│   ├── client.go            # Client interface
│   ├── types.go             # Message, ContentBlock, ToolUse types
│   └── stream.go            # Streaming interface
├── agent/                   # Optional: Multi-agent composition
│   ├── voting.go            # Parallel consensus execution
│   ├── decomposition.go     # Task breakdown
│   └── agent_test.go
└── examples/
    ├── simple/              # Minimal usage example
    ├── mcp/                  # MCP integration example
    └── multiagent/          # Multi-agent example
```

---

## Phase 1: Core Tool System

### Task 1: Initialize Go Module

**Files:**
- Create: `go.mod`
- Create: `README.md`

**Step 1: Initialize module**

```bash
cd /Users/harper/Public/src/2389/mux
go mod init github.com/2389-research/mux
```

**Step 2: Create README**

```markdown
# mux

Agentic infrastructure for Go. Tool execution, MCP integration, permission-gated approval flows, and orchestration.

## Installation

go get github.com/2389-research/mux

## Quick Start

See examples/ directory.
```

**Step 3: Commit**

```bash
git init
git add go.mod README.md
git commit -m "chore: initialize mux module"
```

---

### Task 2: Define Tool Interface

**Files:**
- Create: `tool/tool.go`
- Create: `tool/tool_test.go`

**Step 1: Write the failing test**

```go
// tool/tool_test.go
package tool_test

import (
	"context"
	"testing"

	"github.com/2389-research/mux/tool"
)

// mockTool implements Tool interface for testing
type mockTool struct {
	name             string
	description      string
	requiresApproval bool
	executeFunc      func(ctx context.Context, params map[string]any) (*tool.Result, error)
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string { return m.description }
func (m *mockTool) RequiresApproval(params map[string]any) bool {
	return m.requiresApproval
}
func (m *mockTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	if m.executeFunc != nil {
		return m.executeFunc(ctx, params)
	}
	return &tool.Result{ToolName: m.name, Success: true, Output: "executed"}, nil
}

func TestToolInterface(t *testing.T) {
	mock := &mockTool{
		name:             "test_tool",
		description:      "A test tool",
		requiresApproval: true,
	}

	// Verify interface compliance
	var _ tool.Tool = mock

	if mock.Name() != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", mock.Name())
	}
	if mock.Description() != "A test tool" {
		t.Errorf("expected description 'A test tool', got %q", mock.Description())
	}
	if !mock.RequiresApproval(nil) {
		t.Error("expected RequiresApproval to return true")
	}

	result, err := mock.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success to be true")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./tool/... -v`
Expected: FAIL - package not found

**Step 3: Write minimal implementation**

```go
// tool/tool.go
// ABOUTME: Defines the core Tool interface - the universal abstraction for all
// ABOUTME: executable capabilities (built-in tools, MCP tools, skills, etc.)
package tool

import "context"

// Tool is the universal interface for executable capabilities.
// All tools (built-in, MCP, skills) implement this interface.
type Tool interface {
	// Name returns the unique identifier for this tool.
	Name() string

	// Description returns a human-readable description for the LLM.
	Description() string

	// RequiresApproval returns true if execution needs user approval.
	// The params are provided to allow context-sensitive decisions
	// (e.g., "bash" might require approval for destructive commands).
	RequiresApproval(params map[string]any) bool

	// Execute runs the tool with the given parameters.
	// Returns a Result containing output or error information.
	Execute(ctx context.Context, params map[string]any) (*Result, error)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./tool/... -v`
Expected: FAIL - Result type not defined (expected, we'll fix in next task)

**Step 5: Commit (partial - we'll commit with Result)**

Hold commit until Task 3.

---

### Task 3: Define Result Type

**Files:**
- Create: `tool/result.go`

**Step 1: Write the failing test (add to tool_test.go)**

```go
// Add to tool/tool_test.go
func TestResultCreation(t *testing.T) {
	// Test successful result
	success := tool.NewResult("test_tool", true, "output data", "")
	if success.ToolName != "test_tool" {
		t.Errorf("expected ToolName 'test_tool', got %q", success.ToolName)
	}
	if !success.Success {
		t.Error("expected Success to be true")
	}
	if success.Output != "output data" {
		t.Errorf("expected Output 'output data', got %q", success.Output)
	}

	// Test error result
	failure := tool.NewErrorResult("test_tool", "something went wrong")
	if failure.Success {
		t.Error("expected Success to be false for error result")
	}
	if failure.Error != "something went wrong" {
		t.Errorf("expected Error message, got %q", failure.Error)
	}

	// Test metadata
	withMeta := tool.NewResult("test_tool", true, "ok", "")
	withMeta.SetMetadata("key", "value")
	if v, ok := withMeta.Metadata["key"]; !ok || v != "value" {
		t.Error("expected metadata to contain key=value")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./tool/... -v`
Expected: FAIL - NewResult, NewErrorResult not defined

**Step 3: Write minimal implementation**

```go
// tool/result.go
// ABOUTME: Defines the Result type - a unified structure for tool execution
// ABOUTME: outcomes, used consistently across all tool types.
package tool

// Result represents the outcome of a tool execution.
// This is the universal result type used by all tools.
type Result struct {
	// ToolName identifies which tool produced this result.
	ToolName string

	// Success indicates whether the tool executed successfully.
	Success bool

	// Output contains the tool's output on success.
	Output string

	// Error contains the error message on failure.
	Error string

	// Metadata holds additional tool-specific data.
	Metadata map[string]any
}

// NewResult creates a new Result with the given values.
func NewResult(toolName string, success bool, output, errMsg string) *Result {
	return &Result{
		ToolName: toolName,
		Success:  success,
		Output:   output,
		Error:    errMsg,
		Metadata: make(map[string]any),
	}
}

// NewErrorResult creates a failed Result with an error message.
func NewErrorResult(toolName string, errMsg string) *Result {
	return &Result{
		ToolName: toolName,
		Success:  false,
		Error:    errMsg,
		Metadata: make(map[string]any),
	}
}

// SetMetadata adds a key-value pair to the result metadata.
func (r *Result) SetMetadata(key string, value any) {
	if r.Metadata == nil {
		r.Metadata = make(map[string]any)
	}
	r.Metadata[key] = value
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./tool/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add tool/
git commit -m "feat(tool): add Tool interface and Result type"
```

---

### Task 4: Implement Tool Registry

**Files:**
- Create: `tool/registry.go`
- Modify: `tool/tool_test.go`

**Step 1: Write the failing test**

```go
// Add to tool/tool_test.go
func TestRegistry(t *testing.T) {
	reg := tool.NewRegistry()

	// Test empty registry
	if names := reg.List(); len(names) != 0 {
		t.Errorf("expected empty registry, got %d tools", len(names))
	}

	// Register a tool
	mock := &mockTool{name: "test_tool", description: "Test"}
	reg.Register(mock)

	// Test retrieval
	retrieved, ok := reg.Get("test_tool")
	if !ok {
		t.Fatal("expected to find registered tool")
	}
	if retrieved.Name() != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", retrieved.Name())
	}

	// Test list
	names := reg.List()
	if len(names) != 1 || names[0] != "test_tool" {
		t.Errorf("expected ['test_tool'], got %v", names)
	}

	// Test not found
	_, ok = reg.Get("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent tool")
	}

	// Test unregister
	reg.Unregister("test_tool")
	_, ok = reg.Get("test_tool")
	if ok {
		t.Error("expected tool to be unregistered")
	}
}

func TestRegistryConcurrency(t *testing.T) {
	reg := tool.NewRegistry()
	done := make(chan bool)

	// Concurrent writes
	for i := 0; i < 100; i++ {
		go func(n int) {
			mock := &mockTool{name: "tool_" + string(rune('a'+n%26))}
			reg.Register(mock)
			done <- true
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		go func() {
			reg.List()
			reg.Get("tool_a")
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 200; i++ {
		<-done
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./tool/... -v`
Expected: FAIL - NewRegistry not defined

**Step 3: Write minimal implementation**

```go
// tool/registry.go
// ABOUTME: Implements the Registry - a thread-safe container for discovering
// ABOUTME: and managing available tools at runtime.
package tool

import (
	"sort"
	"sync"
)

// Registry manages a collection of tools with thread-safe access.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry creates a new empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
// If a tool with the same name exists, it is replaced.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Unregister removes a tool from the registry.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// Get retrieves a tool by name.
// Returns the tool and true if found, nil and false otherwise.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns the names of all registered tools, sorted alphabetically.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// All returns all registered tools as a slice.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	return tools
}

// Count returns the number of registered tools.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./tool/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add tool/registry.go tool/tool_test.go
git commit -m "feat(tool): add thread-safe Registry"
```

---

### Task 5: Implement Tool Executor with Approval Flow

**Files:**
- Create: `tool/executor.go`
- Modify: `tool/tool_test.go`

**Step 1: Write the failing test**

```go
// Add to tool/tool_test.go
func TestExecutor(t *testing.T) {
	reg := tool.NewRegistry()
	mock := &mockTool{
		name:             "test_tool",
		requiresApproval: false,
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return tool.NewResult("test_tool", true, "executed", ""), nil
		},
	}
	reg.Register(mock)

	exec := tool.NewExecutor(reg)

	// Execute tool that doesn't require approval
	result, err := exec.Execute(context.Background(), "test_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	if result.Output != "executed" {
		t.Errorf("expected output 'executed', got %q", result.Output)
	}

	// Execute nonexistent tool
	_, err = exec.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Error("expected error for nonexistent tool")
	}
}

func TestExecutorApprovalFlow(t *testing.T) {
	reg := tool.NewRegistry()
	mock := &mockTool{
		name:             "dangerous_tool",
		requiresApproval: true,
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return tool.NewResult("dangerous_tool", true, "danger executed", ""), nil
		},
	}
	reg.Register(mock)

	// Test with approval granted
	exec := tool.NewExecutor(reg)
	approvalCalled := false
	exec.SetApprovalFunc(func(ctx context.Context, t tool.Tool, params map[string]any) (bool, error) {
		approvalCalled = true
		return true, nil // approve
	})

	result, err := exec.Execute(context.Background(), "dangerous_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approvalCalled {
		t.Error("expected approval function to be called")
	}
	if !result.Success {
		t.Error("expected success after approval")
	}

	// Test with approval denied
	exec.SetApprovalFunc(func(ctx context.Context, t tool.Tool, params map[string]any) (bool, error) {
		return false, nil // deny
	})

	_, err = exec.Execute(context.Background(), "dangerous_tool", nil)
	if err == nil {
		t.Error("expected error when approval denied")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./tool/... -v`
Expected: FAIL - NewExecutor not defined

**Step 3: Write minimal implementation**

```go
// tool/executor.go
// ABOUTME: Implements the Executor - the permission-gated execution engine
// ABOUTME: that orchestrates tool calls with approval flows and hooks.
package tool

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrToolNotFound is returned when a requested tool doesn't exist.
	ErrToolNotFound = errors.New("tool not found")

	// ErrApprovalDenied is returned when tool execution is denied.
	ErrApprovalDenied = errors.New("tool execution denied")

	// ErrApprovalRequired is returned when no approval function is set
	// but a tool requires approval.
	ErrApprovalRequired = errors.New("tool requires approval but no approval function set")
)

// ApprovalFunc is called when a tool requires approval before execution.
// It receives the tool and parameters, and returns whether execution is approved.
type ApprovalFunc func(ctx context.Context, t Tool, params map[string]any) (bool, error)

// ExecutionHook is called before or after tool execution.
type ExecutionHook func(ctx context.Context, toolName string, params map[string]any, result *Result, err error)

// Executor manages tool execution with permission checking and hooks.
type Executor struct {
	registry     *Registry
	approvalFunc ApprovalFunc
	beforeHooks  []ExecutionHook
	afterHooks   []ExecutionHook
}

// NewExecutor creates a new Executor with the given Registry.
func NewExecutor(registry *Registry) *Executor {
	return &Executor{
		registry:    registry,
		beforeHooks: make([]ExecutionHook, 0),
		afterHooks:  make([]ExecutionHook, 0),
	}
}

// SetApprovalFunc sets the function used to request approval for tools
// that require it.
func (e *Executor) SetApprovalFunc(fn ApprovalFunc) {
	e.approvalFunc = fn
}

// AddBeforeHook adds a hook that runs before tool execution.
func (e *Executor) AddBeforeHook(hook ExecutionHook) {
	e.beforeHooks = append(e.beforeHooks, hook)
}

// AddAfterHook adds a hook that runs after tool execution.
func (e *Executor) AddAfterHook(hook ExecutionHook) {
	e.afterHooks = append(e.afterHooks, hook)
}

// Execute runs a tool by name with the given parameters.
// It handles approval flow, before/after hooks, and error handling.
func (e *Executor) Execute(ctx context.Context, toolName string, params map[string]any) (*Result, error) {
	// Get tool from registry
	t, ok := e.registry.Get(toolName)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrToolNotFound, toolName)
	}

	// Check if approval is required
	if t.RequiresApproval(params) {
		if e.approvalFunc == nil {
			return nil, fmt.Errorf("%w: %s", ErrApprovalRequired, toolName)
		}

		approved, err := e.approvalFunc(ctx, t, params)
		if err != nil {
			return nil, fmt.Errorf("approval check failed: %w", err)
		}
		if !approved {
			return nil, fmt.Errorf("%w: %s", ErrApprovalDenied, toolName)
		}
	}

	// Run before hooks
	for _, hook := range e.beforeHooks {
		hook(ctx, toolName, params, nil, nil)
	}

	// Execute tool
	result, err := t.Execute(ctx, params)

	// Run after hooks
	for _, hook := range e.afterHooks {
		hook(ctx, toolName, params, result, err)
	}

	return result, err
}

// ExecuteAll runs multiple tools sequentially, stopping on first error.
func (e *Executor) ExecuteAll(ctx context.Context, calls []ToolCall) ([]*Result, error) {
	results := make([]*Result, 0, len(calls))
	for _, call := range calls {
		result, err := e.Execute(ctx, call.Name, call.Params)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

// ToolCall represents a pending tool invocation.
type ToolCall struct {
	Name   string
	Params map[string]any
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./tool/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add tool/executor.go tool/tool_test.go
git commit -m "feat(tool): add Executor with approval flow and hooks"
```

---

## Phase 2: LLM Abstraction Layer

### Task 6: Define LLM Client Interface

**Files:**
- Create: `llm/client.go`
- Create: `llm/types.go`
- Create: `llm/llm_test.go`

**Step 1: Write the failing test**

```go
// llm/llm_test.go
package llm_test

import (
	"context"
	"testing"

	"github.com/2389-research/mux/llm"
)

type mockClient struct{}

func (m *mockClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	return &llm.Response{
		Content: []llm.ContentBlock{
			{Type: llm.ContentTypeText, Text: "Hello"},
		},
		StopReason: llm.StopReasonEndTurn,
	}, nil
}

func (m *mockClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		ch <- llm.StreamEvent{Type: llm.EventContentDelta, Text: "Hello"}
		ch <- llm.StreamEvent{Type: llm.EventMessageStop}
		close(ch)
	}()
	return ch, nil
}

func TestClientInterface(t *testing.T) {
	var client llm.Client = &mockClient{}

	resp, err := client.CreateMessage(context.Background(), &llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "Hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(resp.Content))
	}
	if resp.Content[0].Text != "Hello" {
		t.Errorf("expected 'Hello', got %q", resp.Content[0].Text)
	}
}

func TestMessageConstruction(t *testing.T) {
	msg := llm.NewUserMessage("Hello world")
	if msg.Role != llm.RoleUser {
		t.Errorf("expected role User, got %s", msg.Role)
	}

	assistantMsg := llm.NewAssistantMessage("Hi there")
	if assistantMsg.Role != llm.RoleAssistant {
		t.Errorf("expected role Assistant, got %s", assistantMsg.Role)
	}
}

func TestToolDefinition(t *testing.T) {
	def := llm.ToolDefinition{
		Name:        "read_file",
		Description: "Read a file from disk",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	}

	if def.Name != "read_file" {
		t.Errorf("expected name 'read_file', got %q", def.Name)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./llm/... -v`
Expected: FAIL - package not found

**Step 3: Write minimal implementation**

```go
// llm/types.go
// ABOUTME: Defines the core types for LLM communication - messages, content
// ABOUTME: blocks, tool definitions, and tool use/results.
package llm

// Role represents the role of a message sender.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// ContentType identifies the type of content in a block.
type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
)

// StopReason indicates why the model stopped generating.
type StopReason string

const (
	StopReasonEndTurn   StopReason = "end_turn"
	StopReasonToolUse   StopReason = "tool_use"
	StopReasonMaxTokens StopReason = "max_tokens"
)

// Message represents a conversation message.
type Message struct {
	Role    Role           `json:"role"`
	Content string         `json:"content,omitempty"`
	Blocks  []ContentBlock `json:"blocks,omitempty"`
}

// NewUserMessage creates a user message with text content.
func NewUserMessage(text string) Message {
	return Message{Role: RoleUser, Content: text}
}

// NewAssistantMessage creates an assistant message with text content.
func NewAssistantMessage(text string) Message {
	return Message{Role: RoleAssistant, Content: text}
}

// ContentBlock represents a piece of content in a message.
type ContentBlock struct {
	Type ContentType `json:"type"`

	// For text content
	Text string `json:"text,omitempty"`

	// For tool use
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`

	// For tool result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ToolUse extracts tool use information from a content block.
func (cb ContentBlock) ToolUse() (id, name string, input map[string]any, ok bool) {
	if cb.Type != ContentTypeToolUse {
		return "", "", nil, false
	}
	return cb.ID, cb.Name, cb.Input, true
}

// ToolDefinition describes a tool for the LLM.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Request is the input for CreateMessage.
type Request struct {
	Model       string           `json:"model,omitempty"`
	Messages    []Message        `json:"messages"`
	Tools       []ToolDefinition `json:"tools,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	System      string           `json:"system,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
}

// Response is the output from CreateMessage.
type Response struct {
	ID         string         `json:"id"`
	Content    []ContentBlock `json:"content"`
	StopReason StopReason     `json:"stop_reason"`
	Model      string         `json:"model"`
	Usage      Usage          `json:"usage"`
}

// Usage tracks token consumption.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// HasToolUse returns true if the response contains tool use blocks.
func (r *Response) HasToolUse() bool {
	for _, block := range r.Content {
		if block.Type == ContentTypeToolUse {
			return true
		}
	}
	return false
}

// ToolUses extracts all tool use blocks from the response.
func (r *Response) ToolUses() []ContentBlock {
	var uses []ContentBlock
	for _, block := range r.Content {
		if block.Type == ContentTypeToolUse {
			uses = append(uses, block)
		}
	}
	return uses
}

// TextContent extracts concatenated text from the response.
func (r *Response) TextContent() string {
	var text string
	for _, block := range r.Content {
		if block.Type == ContentTypeText {
			text += block.Text
		}
	}
	return text
}
```

```go
// llm/client.go
// ABOUTME: Defines the Client interface - the abstraction layer that allows
// ABOUTME: mux to work with any LLM provider (Anthropic, OpenAI, etc.)
package llm

import "context"

// Client is the interface for LLM communication.
// Implementations handle provider-specific details.
type Client interface {
	// CreateMessage sends a request and returns the complete response.
	CreateMessage(ctx context.Context, req *Request) (*Response, error)

	// CreateMessageStream sends a request and streams the response.
	CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error)
}

// EventType identifies stream event types.
type EventType string

const (
	EventMessageStart  EventType = "message_start"
	EventContentStart  EventType = "content_block_start"
	EventContentDelta  EventType = "content_block_delta"
	EventContentStop   EventType = "content_block_stop"
	EventMessageDelta  EventType = "message_delta"
	EventMessageStop   EventType = "message_stop"
	EventError         EventType = "error"
)

// StreamEvent represents a streaming response event.
type StreamEvent struct {
	Type EventType

	// For content events
	Index int
	Text  string
	Block *ContentBlock

	// For message events
	Response *Response

	// For errors
	Error error
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./llm/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add llm/
git commit -m "feat(llm): add LLM client interface and types"
```

---

## Phase 3: Orchestrator

### Task 7: Define Orchestrator State Machine

**Files:**
- Create: `orchestrator/state.go`
- Create: `orchestrator/orchestrator_test.go`

**Step 1: Write the failing test**

```go
// orchestrator/orchestrator_test.go
package orchestrator_test

import (
	"testing"

	"github.com/2389-research/mux/orchestrator"
)

func TestStateMachine(t *testing.T) {
	sm := orchestrator.NewStateMachine()

	// Initial state should be Idle
	if sm.Current() != orchestrator.StateIdle {
		t.Errorf("expected initial state Idle, got %s", sm.Current())
	}

	// Valid transition: Idle -> Streaming
	if err := sm.Transition(orchestrator.StateStreaming); err != nil {
		t.Fatalf("unexpected error transitioning to Streaming: %v", err)
	}
	if sm.Current() != orchestrator.StateStreaming {
		t.Errorf("expected state Streaming, got %s", sm.Current())
	}

	// Valid transition: Streaming -> AwaitingApproval
	if err := sm.Transition(orchestrator.StateAwaitingApproval); err != nil {
		t.Fatalf("unexpected error transitioning to AwaitingApproval: %v", err)
	}

	// Valid transition: AwaitingApproval -> ExecutingTool
	if err := sm.Transition(orchestrator.StateExecutingTool); err != nil {
		t.Fatalf("unexpected error transitioning to ExecutingTool: %v", err)
	}

	// Valid transition: ExecutingTool -> Streaming (continue)
	if err := sm.Transition(orchestrator.StateStreaming); err != nil {
		t.Fatalf("unexpected error transitioning back to Streaming: %v", err)
	}

	// Valid transition: Streaming -> Complete
	if err := sm.Transition(orchestrator.StateComplete); err != nil {
		t.Fatalf("unexpected error transitioning to Complete: %v", err)
	}
}

func TestStateMachineInvalidTransition(t *testing.T) {
	sm := orchestrator.NewStateMachine()

	// Invalid: Idle -> Complete (must stream first)
	err := sm.Transition(orchestrator.StateComplete)
	if err == nil {
		t.Error("expected error for invalid transition Idle -> Complete")
	}

	// Invalid: Idle -> ExecutingTool (must stream first)
	err = sm.Transition(orchestrator.StateExecutingTool)
	if err == nil {
		t.Error("expected error for invalid transition Idle -> ExecutingTool")
	}
}

func TestStateMachineErrorFromAnyState(t *testing.T) {
	// Error should be reachable from any state
	states := []orchestrator.State{
		orchestrator.StateIdle,
		orchestrator.StateStreaming,
		orchestrator.StateAwaitingApproval,
		orchestrator.StateExecutingTool,
	}

	for _, state := range states {
		sm := orchestrator.NewStateMachine()
		sm.ForceState(state) // Test helper

		if err := sm.Transition(orchestrator.StateError); err != nil {
			t.Errorf("expected Error to be reachable from %s, got error: %v", state, err)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./orchestrator/... -v`
Expected: FAIL - package not found

**Step 3: Write minimal implementation**

```go
// orchestrator/state.go
// ABOUTME: Implements the state machine for agent orchestration - ensures valid
// ABOUTME: state transitions and prevents illegal operation sequences.
package orchestrator

import (
	"fmt"
	"sync"
)

// State represents the current state of the orchestrator.
type State string

const (
	// StateIdle is the initial state, waiting for work.
	StateIdle State = "idle"

	// StateStreaming is actively receiving LLM response.
	StateStreaming State = "streaming"

	// StateAwaitingApproval is waiting for user approval on tool execution.
	StateAwaitingApproval State = "awaiting_approval"

	// StateExecutingTool is running an approved tool.
	StateExecutingTool State = "executing_tool"

	// StateComplete indicates successful completion.
	StateComplete State = "complete"

	// StateError indicates an error occurred.
	StateError State = "error"
)

// validTransitions defines allowed state transitions.
var validTransitions = map[State][]State{
	StateIdle:             {StateStreaming, StateError},
	StateStreaming:        {StateAwaitingApproval, StateComplete, StateError},
	StateAwaitingApproval: {StateExecutingTool, StateStreaming, StateError}, // Streaming if denied
	StateExecutingTool:    {StateStreaming, StateComplete, StateError},
	StateComplete:         {StateIdle}, // Reset for next run
	StateError:            {StateIdle}, // Reset for retry
}

// StateMachine manages orchestrator state with validation.
type StateMachine struct {
	mu      sync.RWMutex
	current State
}

// NewStateMachine creates a new StateMachine in the Idle state.
func NewStateMachine() *StateMachine {
	return &StateMachine{current: StateIdle}
}

// Current returns the current state.
func (sm *StateMachine) Current() State {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current
}

// Transition attempts to move to a new state.
// Returns an error if the transition is invalid.
func (sm *StateMachine) Transition(to State) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	valid := validTransitions[sm.current]
	for _, allowed := range valid {
		if allowed == to {
			sm.current = to
			return nil
		}
	}

	return fmt.Errorf("invalid state transition: %s -> %s", sm.current, to)
}

// ForceState sets the state without validation (for testing only).
func (sm *StateMachine) ForceState(state State) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.current = state
}

// IsTerminal returns true if the current state is terminal (Complete or Error).
func (sm *StateMachine) IsTerminal() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current == StateComplete || sm.current == StateError
}

// Reset returns the state machine to Idle.
func (sm *StateMachine) Reset() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.current = StateIdle
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./orchestrator/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add orchestrator/
git commit -m "feat(orchestrator): add state machine with validated transitions"
```

---

### Task 8: Define Orchestrator Events

**Files:**
- Create: `orchestrator/events.go`
- Modify: `orchestrator/orchestrator_test.go`

**Step 1: Write the failing test**

```go
// Add to orchestrator/orchestrator_test.go
func TestEventTypes(t *testing.T) {
	// Test event creation
	textEvent := orchestrator.NewTextEvent("Hello world")
	if textEvent.Type != orchestrator.EventText {
		t.Errorf("expected EventText, got %s", textEvent.Type)
	}
	if textEvent.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", textEvent.Text)
	}

	toolCallEvent := orchestrator.NewToolCallEvent("tool_123", "read_file", map[string]any{"path": "/tmp"})
	if toolCallEvent.Type != orchestrator.EventToolCall {
		t.Errorf("expected EventToolCall, got %s", toolCallEvent.Type)
	}
	if toolCallEvent.ToolID != "tool_123" {
		t.Errorf("expected tool ID 'tool_123', got %q", toolCallEvent.ToolID)
	}

	errorEvent := orchestrator.NewErrorEvent(fmt.Errorf("test error"))
	if errorEvent.Type != orchestrator.EventError {
		t.Errorf("expected EventError, got %s", errorEvent.Type)
	}
}

func TestEventBus(t *testing.T) {
	bus := orchestrator.NewEventBus()

	// Subscribe
	sub := bus.Subscribe()

	// Publish
	event := orchestrator.NewTextEvent("test")
	bus.Publish(event)

	// Receive
	select {
	case received := <-sub:
		if received.Text != "test" {
			t.Errorf("expected text 'test', got %q", received.Text)
		}
	default:
		t.Error("expected to receive event")
	}

	// Close
	bus.Close()

	// Channel should be closed
	_, ok := <-sub
	if ok {
		t.Error("expected channel to be closed")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./orchestrator/... -v`
Expected: FAIL - NewTextEvent not defined

**Step 3: Write minimal implementation**

```go
// orchestrator/events.go
// ABOUTME: Defines the event system for orchestrator - enables decoupled
// ABOUTME: communication between the orchestration loop and UI/consumers.
package orchestrator

import (
	"sync"

	"github.com/2389-research/mux/tool"
)

// EventType identifies the kind of orchestrator event.
type EventType string

const (
	// EventText is emitted for streamed text content.
	EventText EventType = "text"

	// EventToolCall is emitted when the LLM requests a tool.
	EventToolCall EventType = "tool_call"

	// EventToolResult is emitted after tool execution.
	EventToolResult EventType = "tool_result"

	// EventStateChange is emitted on state transitions.
	EventStateChange EventType = "state_change"

	// EventComplete is emitted when processing finishes successfully.
	EventComplete EventType = "complete"

	// EventError is emitted when an error occurs.
	EventError EventType = "error"
)

// Event represents an orchestrator lifecycle event.
type Event struct {
	Type EventType

	// For EventText
	Text string

	// For EventToolCall
	ToolID     string
	ToolName   string
	ToolParams map[string]any

	// For EventToolResult
	Result *tool.Result

	// For EventStateChange
	FromState State
	ToState   State

	// For EventError
	Error error

	// For EventComplete
	FinalText string
}

// NewTextEvent creates a text content event.
func NewTextEvent(text string) Event {
	return Event{Type: EventText, Text: text}
}

// NewToolCallEvent creates a tool call event.
func NewToolCallEvent(id, name string, params map[string]any) Event {
	return Event{
		Type:       EventToolCall,
		ToolID:     id,
		ToolName:   name,
		ToolParams: params,
	}
}

// NewToolResultEvent creates a tool result event.
func NewToolResultEvent(result *tool.Result) Event {
	return Event{Type: EventToolResult, Result: result}
}

// NewStateChangeEvent creates a state transition event.
func NewStateChangeEvent(from, to State) Event {
	return Event{Type: EventStateChange, FromState: from, ToState: to}
}

// NewCompleteEvent creates a completion event.
func NewCompleteEvent(finalText string) Event {
	return Event{Type: EventComplete, FinalText: finalText}
}

// NewErrorEvent creates an error event.
func NewErrorEvent(err error) Event {
	return Event{Type: EventError, Error: err}
}

// EventBus manages event distribution to subscribers.
type EventBus struct {
	mu          sync.RWMutex
	subscribers []chan Event
	closed      bool
}

// NewEventBus creates a new EventBus.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make([]chan Event, 0),
	}
}

// Subscribe returns a channel that receives events.
func (eb *EventBus) Subscribe() <-chan Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	ch := make(chan Event, 100) // Buffered to prevent blocking
	eb.subscribers = append(eb.subscribers, ch)
	return ch
}

// Publish sends an event to all subscribers.
func (eb *EventBus) Publish(event Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	if eb.closed {
		return
	}

	for _, ch := range eb.subscribers {
		select {
		case ch <- event:
		default:
			// Drop event if buffer full (non-blocking)
		}
	}
}

// Close shuts down the event bus and closes all subscriber channels.
func (eb *EventBus) Close() {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	if eb.closed {
		return
	}

	eb.closed = true
	for _, ch := range eb.subscribers {
		close(ch)
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./orchestrator/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add orchestrator/events.go orchestrator/orchestrator_test.go
git commit -m "feat(orchestrator): add event system with EventBus"
```

---

### Task 9: Implement Core Orchestrator

**Files:**
- Create: `orchestrator/orchestrator.go`
- Modify: `orchestrator/orchestrator_test.go`

**Step 1: Write the failing test**

```go
// Add to orchestrator/orchestrator_test.go
import (
	"context"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

// mockLLMClient for testing
type mockLLMClient struct {
	responses []*llm.Response
	callIndex int
}

func (m *mockLLMClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if m.callIndex >= len(m.responses) {
		return &llm.Response{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		}, nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return resp, nil
}

func (m *mockLLMClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		resp, _ := m.CreateMessage(ctx, req)
		for _, block := range resp.Content {
			if block.Type == llm.ContentTypeText {
				ch <- llm.StreamEvent{Type: llm.EventContentDelta, Text: block.Text}
			}
		}
		ch <- llm.StreamEvent{Type: llm.EventMessageStop, Response: resp}
		close(ch)
	}()
	return ch, nil
}

func TestOrchestratorSimpleResponse(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Hello!"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	ctx := context.Background()
	err := orch.Run(ctx, "Say hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Collect events
	var textEvents []string
	var completed bool
	for event := range events {
		switch event.Type {
		case orchestrator.EventText:
			textEvents = append(textEvents, event.Text)
		case orchestrator.EventComplete:
			completed = true
		}
	}

	if !completed {
		t.Error("expected completion event")
	}
	if len(textEvents) == 0 {
		t.Error("expected at least one text event")
	}
}

func TestOrchestratorWithToolUse(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{
			// First response: tool use
			{
				Content: []llm.ContentBlock{{
					Type:  llm.ContentTypeToolUse,
					ID:    "tool_1",
					Name:  "test_tool",
					Input: map[string]any{"arg": "value"},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			// Second response: final answer
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done!"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}

	registry := tool.NewRegistry()
	testTool := &mockTool{
		name: "test_tool",
		executeFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return tool.NewResult("test_tool", true, "tool output", ""), nil
		},
	}
	registry.Register(testTool)
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	ctx := context.Background()
	err := orch.Run(ctx, "Use the tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify tool was called
	var toolCalled bool
	var toolResult bool
	for event := range events {
		switch event.Type {
		case orchestrator.EventToolCall:
			toolCalled = true
			if event.ToolName != "test_tool" {
				t.Errorf("expected tool 'test_tool', got %q", event.ToolName)
			}
		case orchestrator.EventToolResult:
			toolResult = true
			if !event.Result.Success {
				t.Error("expected successful tool result")
			}
		}
	}

	if !toolCalled {
		t.Error("expected tool call event")
	}
	if !toolResult {
		t.Error("expected tool result event")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./orchestrator/... -v`
Expected: FAIL - orchestrator.New not defined

**Step 3: Write minimal implementation**

```go
// orchestrator/orchestrator.go
// ABOUTME: Implements the core Orchestrator - the agentic think-act loop that
// ABOUTME: coordinates LLM responses, tool execution, and event streaming.
package orchestrator

import (
	"context"
	"fmt"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/tool"
)

const (
	// DefaultMaxIterations limits the think-act loop to prevent runaway.
	DefaultMaxIterations = 50
)

// Config holds orchestrator configuration.
type Config struct {
	// MaxIterations limits the think-act loop iterations.
	MaxIterations int

	// SystemPrompt is prepended to all requests.
	SystemPrompt string

	// Model specifies which model to use.
	Model string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxIterations: DefaultMaxIterations,
	}
}

// Orchestrator manages the agentic think-act loop.
type Orchestrator struct {
	client   llm.Client
	executor *tool.Executor
	config   Config
	state    *StateMachine
	eventBus *EventBus
	messages []llm.Message
}

// New creates a new Orchestrator with default config.
func New(client llm.Client, executor *tool.Executor) *Orchestrator {
	return NewWithConfig(client, executor, DefaultConfig())
}

// NewWithConfig creates a new Orchestrator with custom config.
func NewWithConfig(client llm.Client, executor *tool.Executor, config Config) *Orchestrator {
	return &Orchestrator{
		client:   client,
		executor: executor,
		config:   config,
		state:    NewStateMachine(),
		eventBus: NewEventBus(),
		messages: make([]llm.Message, 0),
	}
}

// Subscribe returns a channel for receiving orchestrator events.
func (o *Orchestrator) Subscribe() <-chan Event {
	return o.eventBus.Subscribe()
}

// State returns the current orchestrator state.
func (o *Orchestrator) State() State {
	return o.state.Current()
}

// Run executes the think-act loop with the given prompt.
func (o *Orchestrator) Run(ctx context.Context, prompt string) error {
	// Reset state for new run
	o.state.Reset()
	o.messages = []llm.Message{llm.NewUserMessage(prompt)}

	defer o.eventBus.Close()

	for i := 0; i < o.config.MaxIterations; i++ {
		// Transition to streaming
		if err := o.transition(StateStreaming); err != nil {
			return o.handleError(err)
		}

		// Get LLM response
		resp, err := o.client.CreateMessage(ctx, o.buildRequest())
		if err != nil {
			return o.handleError(err)
		}

		// Process response content
		o.processResponse(resp)

		// Check if we need to execute tools
		if resp.HasToolUse() {
			if err := o.executeTools(ctx, resp.ToolUses()); err != nil {
				return o.handleError(err)
			}
			// Continue loop after tool execution
			continue
		}

		// No tool use - we're done
		if err := o.transition(StateComplete); err != nil {
			return o.handleError(err)
		}

		o.eventBus.Publish(NewCompleteEvent(resp.TextContent()))
		return nil
	}

	return o.handleError(fmt.Errorf("exceeded max iterations (%d)", o.config.MaxIterations))
}

// buildRequest constructs the LLM request from current state.
func (o *Orchestrator) buildRequest() *llm.Request {
	req := &llm.Request{
		Messages:  o.messages,
		System:    o.config.SystemPrompt,
		Model:     o.config.Model,
		MaxTokens: 4096,
	}

	// Add tool definitions from registry
	// (executor.registry is private, so we'd need to expose this)
	// For now, tools are passed via config or separate method

	return req
}

// processResponse handles content blocks from the LLM response.
func (o *Orchestrator) processResponse(resp *llm.Response) {
	for _, block := range resp.Content {
		switch block.Type {
		case llm.ContentTypeText:
			o.eventBus.Publish(NewTextEvent(block.Text))
		case llm.ContentTypeToolUse:
			o.eventBus.Publish(NewToolCallEvent(block.ID, block.Name, block.Input))
		}
	}

	// Add assistant response to history
	o.messages = append(o.messages, llm.Message{
		Role:   llm.RoleAssistant,
		Blocks: resp.Content,
	})
}

// executeTools runs requested tools and adds results to message history.
func (o *Orchestrator) executeTools(ctx context.Context, toolUses []llm.ContentBlock) error {
	if err := o.transition(StateExecutingTool); err != nil {
		return err
	}

	var resultBlocks []llm.ContentBlock

	for _, use := range toolUses {
		result, err := o.executor.Execute(ctx, use.Name, use.Input)
		if err != nil {
			// Tool execution failed - add error result
			resultBlocks = append(resultBlocks, llm.ContentBlock{
				Type:      llm.ContentTypeToolResult,
				ToolUseID: use.ID,
				Content:   fmt.Sprintf("Error: %v", err),
				IsError:   true,
			})
			o.eventBus.Publish(NewToolResultEvent(tool.NewErrorResult(use.Name, err.Error())))
			continue
		}

		// Add successful result
		resultBlocks = append(resultBlocks, llm.ContentBlock{
			Type:      llm.ContentTypeToolResult,
			ToolUseID: use.ID,
			Content:   result.Output,
			IsError:   !result.Success,
		})
		o.eventBus.Publish(NewToolResultEvent(result))
	}

	// Add tool results as user message
	o.messages = append(o.messages, llm.Message{
		Role:   llm.RoleUser,
		Blocks: resultBlocks,
	})

	return nil
}

// transition moves to a new state with event emission.
func (o *Orchestrator) transition(to State) error {
	from := o.state.Current()
	if err := o.state.Transition(to); err != nil {
		return err
	}
	o.eventBus.Publish(NewStateChangeEvent(from, to))
	return nil
}

// handleError transitions to error state and returns the error.
func (o *Orchestrator) handleError(err error) error {
	o.state.Transition(StateError) // Ignore transition error
	o.eventBus.Publish(NewErrorEvent(err))
	return err
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./orchestrator/... -v`
Expected: PASS (may need to add mockTool import or define locally)

**Step 5: Commit**

```bash
git add orchestrator/orchestrator.go orchestrator/orchestrator_test.go
git commit -m "feat(orchestrator): implement core think-act loop"
```

---

## Phase 4: MCP Integration

### Task 10: Define MCP Types

**Files:**
- Create: `mcp/types.go`
- Create: `mcp/mcp_test.go`

**Step 1: Write the failing test**

```go
// mcp/mcp_test.go
package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/2389-research/mux/mcp"
)

func TestJSONRPCRequest(t *testing.T) {
	req := mcp.NewRequest("tools/list", nil)
	if req.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc '2.0', got %q", req.JSONRPC)
	}
	if req.Method != "tools/list" {
		t.Errorf("expected method 'tools/list', got %q", req.Method)
	}
	if req.ID == 0 {
		t.Error("expected non-zero ID")
	}

	// Test JSON serialization
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty JSON")
	}
}

func TestJSONRPCResponse(t *testing.T) {
	// Successful response
	jsonData := `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`
	var resp mcp.Response
	if err := json.Unmarshal([]byte(jsonData), &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if resp.Error != nil {
		t.Error("expected no error")
	}

	// Error response
	errData := `{"jsonrpc":"2.0","id":2,"error":{"code":-32600,"message":"Invalid request"}}`
	var errResp mcp.Response
	if err := json.Unmarshal([]byte(errData), &errResp); err != nil {
		t.Fatalf("failed to unmarshal error: %v", err)
	}
	if errResp.Error == nil {
		t.Error("expected error to be present")
	}
	if errResp.Error.Code != -32600 {
		t.Errorf("expected code -32600, got %d", errResp.Error.Code)
	}
}

func TestMCPToolInfo(t *testing.T) {
	info := mcp.ToolInfo{
		Name:        "read_file",
		Description: "Read a file",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
		},
	}

	if info.Name != "read_file" {
		t.Errorf("expected name 'read_file', got %q", info.Name)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp/... -v`
Expected: FAIL - package not found

**Step 3: Write minimal implementation**

```go
// mcp/types.go
// ABOUTME: Defines MCP protocol types - JSON-RPC 2.0 messages, tool info,
// ABOUTME: and server configuration structures.
package mcp

import (
	"encoding/json"
	"sync/atomic"
)

var requestID uint64

// Request is a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// NewRequest creates a new JSON-RPC request with auto-incrementing ID.
func NewRequest(method string, params any) *Request {
	return &Request{
		JSONRPC: "2.0",
		ID:      atomic.AddUint64(&requestID, 1),
		Method:  method,
		Params:  params,
	}
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return e.Message
}

// ToolInfo describes an MCP tool.
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// ToolsListResult is the response from tools/list.
type ToolsListResult struct {
	Tools []ToolInfo `json:"tools"`
}

// ToolCallParams is the input for tools/call.
type ToolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// ToolCallResult is the response from tools/call.
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock is an MCP content block.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Data     string `json:"data,omitempty"`
}

// ServerConfig describes an MCP server configuration.
type ServerConfig struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"` // "stdio"
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// InitializeParams is sent during MCP handshake.
type InitializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	Capabilities    any        `json:"capabilities"`
	ClientInfo      ClientInfo `json:"clientInfo"`
}

// ClientInfo identifies the MCP client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./mcp/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add mcp/
git commit -m "feat(mcp): add MCP protocol types"
```

---

### Task 11: Implement MCP Client

**Files:**
- Create: `mcp/client.go`
- Modify: `mcp/mcp_test.go`

**Step 1: Write the failing test**

```go
// Add to mcp/mcp_test.go
func TestClientCreation(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "test-server",
		Transport: "stdio",
		Command:   "echo",
		Args:      []string{"hello"},
	}

	client := mcp.NewClient(config)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

// Integration test (skip if no server available)
func TestClientIntegration(t *testing.T) {
	t.Skip("requires MCP server - run manually")

	config := mcp.ServerConfig{
		Name:      "test-server",
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
	}

	client := mcp.NewClient(config)
	ctx := context.Background()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer client.Close()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("failed to list tools: %v", err)
	}

	if len(tools) == 0 {
		t.Error("expected at least one tool")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp/... -v`
Expected: FAIL - NewClient not defined

**Step 3: Write minimal implementation**

```go
// mcp/client.go
// ABOUTME: Implements the MCP client - manages JSON-RPC 2.0 communication
// ABOUTME: with MCP servers over stdio transport.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// Client communicates with an MCP server.
type Client struct {
	config  ServerConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	scanner *bufio.Scanner

	mu        sync.Mutex
	pending   map[uint64]chan *Response
	running   bool
	closeChan chan struct{}
}

// NewClient creates a new MCP client for the given server config.
func NewClient(config ServerConfig) *Client {
	return &Client{
		config:    config,
		pending:   make(map[uint64]chan *Response),
		closeChan: make(chan struct{}),
	}
}

// Start launches the MCP server process and initializes the connection.
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("client already running")
	}

	// Start server process
	c.cmd = exec.CommandContext(ctx, c.config.Command, c.config.Args...)

	// Set environment variables
	if len(c.config.Env) > 0 {
		for k, v := range c.config.Env {
			c.cmd.Env = append(c.cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	var err error
	c.stdin, err = c.cmd.StdinPipe()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	c.stdout, err = c.cmd.StdoutPipe()
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	if err := c.cmd.Start(); err != nil {
		c.mu.Unlock()
		return fmt.Errorf("failed to start server: %w", err)
	}

	c.scanner = bufio.NewScanner(c.stdout)
	c.running = true
	c.mu.Unlock()

	// Start response reader goroutine
	go c.readResponses()

	// Initialize MCP connection
	return c.initialize(ctx)
}

// initialize performs the MCP handshake.
func (c *Client) initialize(ctx context.Context) error {
	params := InitializeParams{
		ProtocolVersion: "2024-11-05",
		Capabilities:    map[string]any{},
		ClientInfo: ClientInfo{
			Name:    "mux",
			Version: "1.0.0",
		},
	}

	_, err := c.call(ctx, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}

	// Send initialized notification
	return c.notify("notifications/initialized", nil)
}

// ListTools retrieves available tools from the server.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools list: %w", err)
	}

	return result.Tools, nil
}

// CallTool executes a tool on the server.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error) {
	params := ToolCallParams{
		Name:      name,
		Arguments: args,
	}

	resp, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}

	var result ToolCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tool result: %w", err)
	}

	return &result, nil
}

// call sends a request and waits for the response.
func (c *Client) call(ctx context.Context, method string, params any) (*Response, error) {
	req := NewRequest(method, params)

	// Register pending request
	respChan := make(chan *Response, 1)
	c.mu.Lock()
	c.pending[req.ID] = respChan
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
	}()

	// Send request
	if err := c.send(req); err != nil {
		return nil, err
	}

	// Wait for response
	select {
	case resp := <-respChan:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closeChan:
		return nil, fmt.Errorf("client closed")
	}
}

// notify sends a notification (no response expected).
func (c *Client) notify(method string, params any) error {
	req := &Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return c.send(req)
}

// send writes a request to the server.
func (c *Client) send(req *Request) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	}

	return nil
}

// readResponses reads responses from the server in a loop.
func (c *Client) readResponses() {
	for c.scanner.Scan() {
		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			continue // Skip malformed responses
		}

		c.mu.Lock()
		if ch, ok := c.pending[resp.ID]; ok {
			ch <- &resp
		}
		c.mu.Unlock()
	}
}

// Close shuts down the client and server.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}

	close(c.closeChan)
	c.running = false

	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}

	return nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./mcp/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add mcp/client.go mcp/mcp_test.go
git commit -m "feat(mcp): implement MCP client with stdio transport"
```

---

### Task 12: Implement MCP Tool Adapter

**Files:**
- Create: `mcp/adapter.go`
- Modify: `mcp/mcp_test.go`

**Step 1: Write the failing test**

```go
// Add to mcp/mcp_test.go
func TestToolAdapter(t *testing.T) {
	info := mcp.ToolInfo{
		Name:        "read_file",
		Description: "Read file contents",
		InputSchema: map[string]any{"type": "object"},
	}

	// Create mock client
	mockClient := &mockMCPClient{
		callResult: &mcp.ToolCallResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "file contents"}},
		},
	}

	adapter := mcp.NewToolAdapter(info, mockClient)

	// Verify Tool interface
	var _ tool.Tool = adapter

	if adapter.Name() != "read_file" {
		t.Errorf("expected name 'read_file', got %q", adapter.Name())
	}

	// Test execution
	result, err := adapter.Execute(context.Background(), map[string]any{"path": "/tmp/test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}
	if result.Output != "file contents" {
		t.Errorf("expected 'file contents', got %q", result.Output)
	}
}

type mockMCPClient struct {
	callResult *mcp.ToolCallResult
	callError  error
}

func (m *mockMCPClient) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolCallResult, error) {
	return m.callResult, m.callError
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./mcp/... -v`
Expected: FAIL - NewToolAdapter not defined

**Step 3: Write minimal implementation**

```go
// mcp/adapter.go
// ABOUTME: Implements the Tool adapter - wraps MCP tools to implement
// ABOUTME: the mux Tool interface for seamless integration.
package mcp

import (
	"context"
	"strings"

	"github.com/2389-research/mux/tool"
)

// ToolCaller is the interface for calling MCP tools.
type ToolCaller interface {
	CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error)
}

// ToolAdapter wraps an MCP tool to implement the Tool interface.
type ToolAdapter struct {
	info   ToolInfo
	caller ToolCaller
}

// NewToolAdapter creates a new adapter for an MCP tool.
func NewToolAdapter(info ToolInfo, caller ToolCaller) *ToolAdapter {
	return &ToolAdapter{
		info:   info,
		caller: caller,
	}
}

// Name returns the tool name.
func (a *ToolAdapter) Name() string {
	return a.info.Name
}

// Description returns the tool description.
func (a *ToolAdapter) Description() string {
	return a.info.Description
}

// RequiresApproval returns true - MCP tools always require approval by default.
func (a *ToolAdapter) RequiresApproval(params map[string]any) bool {
	// MCP tools are external, so require approval by default
	// This can be customized via configuration
	return true
}

// Execute calls the MCP tool and converts the result.
func (a *ToolAdapter) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	mcpResult, err := a.caller.CallTool(ctx, a.info.Name, params)
	if err != nil {
		return tool.NewErrorResult(a.info.Name, err.Error()), nil
	}

	// Convert MCP result to tool.Result
	output := a.extractOutput(mcpResult)

	result := tool.NewResult(a.info.Name, !mcpResult.IsError, output, "")
	if mcpResult.IsError {
		result.Error = output
	}

	return result, nil
}

// extractOutput combines content blocks into a single output string.
func (a *ToolAdapter) extractOutput(mcpResult *ToolCallResult) string {
	var parts []string
	for _, block := range mcpResult.Content {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "image", "resource":
			// Handle binary content - include metadata
			parts = append(parts, "["+block.Type+": "+block.MimeType+"]")
		}
	}
	return strings.Join(parts, "\n")
}

// InputSchema returns the JSON schema for tool parameters.
func (a *ToolAdapter) InputSchema() map[string]any {
	return a.info.InputSchema
}

// ToolManager manages multiple MCP tool adapters.
type ToolManager struct {
	client *Client
	tools  map[string]*ToolAdapter
}

// NewToolManager creates a manager for MCP tools.
func NewToolManager(client *Client) *ToolManager {
	return &ToolManager{
		client: client,
		tools:  make(map[string]*ToolAdapter),
	}
}

// Refresh reloads tools from the MCP server.
func (m *ToolManager) Refresh(ctx context.Context) error {
	infos, err := m.client.ListTools(ctx)
	if err != nil {
		return err
	}

	m.tools = make(map[string]*ToolAdapter)
	for _, info := range infos {
		m.tools[info.Name] = NewToolAdapter(info, m.client)
	}

	return nil
}

// Tools returns all available tool adapters.
func (m *ToolManager) Tools() []*ToolAdapter {
	tools := make([]*ToolAdapter, 0, len(m.tools))
	for _, t := range m.tools {
		tools = append(tools, t)
	}
	return tools
}

// Get retrieves a specific tool adapter.
func (m *ToolManager) Get(name string) (*ToolAdapter, bool) {
	t, ok := m.tools[name]
	return t, ok
}

// RegisterAll adds all MCP tools to a tool registry.
func (m *ToolManager) RegisterAll(registry *tool.Registry) {
	for _, t := range m.tools {
		registry.Register(t)
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./mcp/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add mcp/adapter.go mcp/mcp_test.go
git commit -m "feat(mcp): add Tool adapter for MCP integration"
```

---

## Phase 5: Permission System

### Task 13: Implement Permission Checker

**Files:**
- Create: `permission/checker.go`
- Create: `permission/mode.go`
- Create: `permission/permission_test.go`

**Step 1: Write the failing test**

```go
// permission/permission_test.go
package permission_test

import (
	"context"
	"testing"

	"github.com/2389-research/mux/permission"
)

func TestPermissionModes(t *testing.T) {
	// Test Auto mode - always allows
	auto := permission.NewChecker(permission.ModeAuto)
	allowed, err := auto.Check(context.Background(), "any_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected Auto mode to allow")
	}

	// Test Deny mode - always denies
	deny := permission.NewChecker(permission.ModeDeny)
	allowed, err = deny.Check(context.Background(), "any_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected Deny mode to deny")
	}
}

func TestPermissionRules(t *testing.T) {
	checker := permission.NewChecker(permission.ModeAsk)

	// Add allowlist rule
	checker.AddRule(permission.AllowTool("safe_tool"))

	// Allowed tool should pass
	allowed, _ := checker.Check(context.Background(), "safe_tool", nil)
	if !allowed {
		t.Error("expected safe_tool to be allowed")
	}

	// Unknown tool should require asking (return false for non-interactive)
	allowed, _ = checker.Check(context.Background(), "unknown_tool", nil)
	if allowed {
		t.Error("expected unknown_tool to require asking")
	}

	// Add denylist rule
	checker.AddRule(permission.DenyTool("dangerous_tool"))
	allowed, _ = checker.Check(context.Background(), "dangerous_tool", nil)
	if allowed {
		t.Error("expected dangerous_tool to be denied")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./permission/... -v`
Expected: FAIL - package not found

**Step 3: Write minimal implementation**

```go
// permission/mode.go
// ABOUTME: Defines permission modes - Auto (allow all), Ask (prompt user),
// ABOUTME: and Deny (block all) for controlling tool execution.
package permission

// Mode defines how permissions are handled.
type Mode string

const (
	// ModeAuto automatically approves all tool executions.
	ModeAuto Mode = "auto"

	// ModeAsk prompts for approval on each tool execution.
	ModeAsk Mode = "ask"

	// ModeDeny automatically denies all tool executions.
	ModeDeny Mode = "deny"
)
```

```go
// permission/checker.go
// ABOUTME: Implements the permission checker - evaluates rules and modes
// ABOUTME: to determine if tool execution should be allowed.
package permission

import (
	"context"
)

// Rule defines a permission rule.
type Rule struct {
	ToolName string
	Allow    bool
}

// AllowTool creates a rule that allows a specific tool.
func AllowTool(name string) Rule {
	return Rule{ToolName: name, Allow: true}
}

// DenyTool creates a rule that denies a specific tool.
func DenyTool(name string) Rule {
	return Rule{ToolName: name, Allow: false}
}

// Checker evaluates permission rules for tool execution.
type Checker struct {
	mode  Mode
	rules []Rule
}

// NewChecker creates a new permission checker with the given mode.
func NewChecker(mode Mode) *Checker {
	return &Checker{
		mode:  mode,
		rules: make([]Rule, 0),
	}
}

// AddRule adds a permission rule.
func (c *Checker) AddRule(rule Rule) {
	c.rules = append(c.rules, rule)
}

// SetMode changes the permission mode.
func (c *Checker) SetMode(mode Mode) {
	c.mode = mode
}

// Mode returns the current permission mode.
func (c *Checker) Mode() Mode {
	return c.mode
}

// Check evaluates whether a tool execution should be allowed.
// Returns (allowed, error).
func (c *Checker) Check(ctx context.Context, toolName string, params map[string]any) (bool, error) {
	// Check mode first
	switch c.mode {
	case ModeAuto:
		return true, nil
	case ModeDeny:
		return false, nil
	}

	// Mode is Ask - check rules
	for _, rule := range c.rules {
		if rule.ToolName == toolName {
			return rule.Allow, nil
		}
	}

	// No matching rule - default to deny in Ask mode
	// The actual prompting is handled by the executor's approval function
	return false, nil
}

// CheckAll evaluates multiple tools at once.
func (c *Checker) CheckAll(ctx context.Context, toolNames []string, params []map[string]any) ([]bool, error) {
	results := make([]bool, len(toolNames))
	for i, name := range toolNames {
		var p map[string]any
		if i < len(params) {
			p = params[i]
		}
		allowed, err := c.Check(ctx, name, p)
		if err != nil {
			return nil, err
		}
		results[i] = allowed
	}
	return results, nil
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./permission/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add permission/
git commit -m "feat(permission): add permission checker with rules and modes"
```

---

## Phase 6: Examples and Integration

### Task 14: Create Simple Example

**Files:**
- Create: `examples/simple/main.go`

**Step 1: Write the example**

```go
// examples/simple/main.go
// ABOUTME: Demonstrates basic mux usage - creating a simple agent
// ABOUTME: with built-in tools and running a conversation.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/tool"
)

// EchoTool is a simple built-in tool for demonstration.
type EchoTool struct{}

func (t *EchoTool) Name() string        { return "echo" }
func (t *EchoTool) Description() string { return "Echoes the input message back" }
func (t *EchoTool) RequiresApproval(params map[string]any) bool { return false }
func (t *EchoTool) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	msg, _ := params["message"].(string)
	return tool.NewResult("echo", true, "Echo: "+msg, ""), nil
}

func main() {
	// Create tool registry and register tools
	registry := tool.NewRegistry()
	registry.Register(&EchoTool{})

	// Create executor
	executor := tool.NewExecutor(registry)

	// Create LLM client (you'd implement this for your provider)
	client := &DemoClient{} // Placeholder

	// Create orchestrator
	orch := orchestrator.New(client, executor)

	// Subscribe to events
	events := orch.Subscribe()

	// Handle events in goroutine
	go func() {
		for event := range events {
			switch event.Type {
			case orchestrator.EventText:
				fmt.Print(event.Text)
			case orchestrator.EventToolCall:
				fmt.Printf("\n[Calling tool: %s]\n", event.ToolName)
			case orchestrator.EventToolResult:
				fmt.Printf("[Tool result: %s]\n", event.Result.Output)
			case orchestrator.EventComplete:
				fmt.Println("\n[Complete]")
			case orchestrator.EventError:
				fmt.Fprintf(os.Stderr, "\nError: %v\n", event.Error)
			}
		}
	}()

	// Run the agent
	ctx := context.Background()
	if err := orch.Run(ctx, "Say hello and use the echo tool"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// DemoClient is a placeholder LLM client.
// In real usage, you'd implement this for Anthropic, OpenAI, etc.
type DemoClient struct{}

func (c *DemoClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	// Demo: return a simple response
	return &llm.Response{
		Content: []llm.ContentBlock{
			{Type: llm.ContentTypeText, Text: "Hello! This is a demo response."},
		},
		StopReason: llm.StopReasonEndTurn,
	}, nil
}

func (c *DemoClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		ch <- llm.StreamEvent{Type: llm.EventContentDelta, Text: "Hello! "}
		ch <- llm.StreamEvent{Type: llm.EventContentDelta, Text: "This is streaming."}
		ch <- llm.StreamEvent{Type: llm.EventMessageStop}
		close(ch)
	}()
	return ch, nil
}
```

**Step 2: Verify it compiles**

Run: `go build ./examples/simple/`
Expected: Build succeeds

**Step 3: Commit**

```bash
git add examples/
git commit -m "docs(examples): add simple usage example"
```

---

### Task 15: Final Integration Test

**Files:**
- Create: `integration_test.go`

**Step 1: Write comprehensive integration test**

```go
// integration_test.go
// ABOUTME: Integration tests verifying the full mux stack works
// ABOUTME: together - tools, orchestrator, MCP, and permissions.
//go:build integration

package midagent_test

import (
	"context"
	"testing"
	"time"

	"github.com/2389-research/mux/llm"
	"github.com/2389-research/mux/orchestrator"
	"github.com/2389-research/mux/permission"
	"github.com/2389-research/mux/tool"
)

// Full stack integration test
func TestFullStack(t *testing.T) {
	// Setup
	registry := tool.NewRegistry()

	// Register a test tool
	testTool := &testToolImpl{
		name:     "calculator",
		executed: false,
	}
	registry.Register(testTool)

	// Create executor with permission checker
	executor := tool.NewExecutor(registry)
	checker := permission.NewChecker(permission.ModeAuto)
	executor.SetApprovalFunc(func(ctx context.Context, t tool.Tool, params map[string]any) (bool, error) {
		return checker.Check(ctx, t.Name(), params)
	})

	// Create mock LLM that uses the tool
	mockLLM := &mockLLMWithToolUse{
		toolName: "calculator",
		toolArgs: map[string]any{"a": 1, "b": 2},
	}

	// Create orchestrator
	orch := orchestrator.New(mockLLM, executor)
	events := orch.Subscribe()

	// Collect events
	var toolCalled, completed bool
	go func() {
		for event := range events {
			switch event.Type {
			case orchestrator.EventToolCall:
				toolCalled = true
			case orchestrator.EventComplete:
				completed = true
			}
		}
	}()

	// Run
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := orch.Run(ctx, "Calculate 1 + 2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify
	time.Sleep(100 * time.Millisecond) // Allow events to process

	if !toolCalled {
		t.Error("expected tool to be called")
	}
	if !completed {
		t.Error("expected completion")
	}
	if !testTool.executed {
		t.Error("expected test tool to be executed")
	}
}

type testToolImpl struct {
	name     string
	executed bool
}

func (t *testToolImpl) Name() string                              { return t.name }
func (t *testToolImpl) Description() string                       { return "Test tool" }
func (t *testToolImpl) RequiresApproval(map[string]any) bool      { return false }
func (t *testToolImpl) Execute(ctx context.Context, params map[string]any) (*tool.Result, error) {
	t.executed = true
	return tool.NewResult(t.name, true, "result", ""), nil
}

type mockLLMWithToolUse struct {
	toolName string
	toolArgs map[string]any
	called   int
}

func (m *mockLLMWithToolUse) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	m.called++
	if m.called == 1 {
		// First call: request tool use
		return &llm.Response{
			Content: []llm.ContentBlock{{
				Type:  llm.ContentTypeToolUse,
				ID:    "tool_1",
				Name:  m.toolName,
				Input: m.toolArgs,
			}},
			StopReason: llm.StopReasonToolUse,
		}, nil
	}
	// Second call: final response
	return &llm.Response{
		Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "Done!"}},
		StopReason: llm.StopReasonEndTurn,
	}, nil
}

func (m *mockLLMWithToolUse) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		resp, _ := m.CreateMessage(ctx, req)
		ch <- llm.StreamEvent{Type: llm.EventMessageStop, Response: resp}
		close(ch)
	}()
	return ch, nil
}
```

**Step 2: Run integration test**

Run: `go test -tags=integration -v ./...`
Expected: PASS

**Step 3: Commit**

```bash
git add integration_test.go
git commit -m "test: add full stack integration test"
```

---

## Summary

This plan creates a **mux** library with:

1. **Tool System** (Phase 1): Interface, Registry, Executor with approval flow
2. **LLM Abstraction** (Phase 2): Provider-agnostic client interface
3. **Orchestrator** (Phase 3): State machine + event-driven think-act loop
4. **MCP Integration** (Phase 4): JSON-RPC client + Tool adapter
5. **Permission System** (Phase 5): Modes, rules, and checking
6. **Examples** (Phase 6): Usage demonstrations

**Key differentiators from Fantasy:**
- MCP-first design
- Permission-gated execution with approval flows
- Explicit state machine
- Event-driven architecture (not just callbacks)
- LLM-agnostic (interface-based)

**Migration path for jeff/hex:**
1. Add mux as dependency
2. Implement `llm.Client` for their provider
3. Replace `internal/tools` with `mux/tool`
4. Replace `internal/mcp` with `mux/mcp`
5. Replace agent loop with `mux/orchestrator`

---

**Plan complete and saved to `docs/plans/2025-12-13-mux-library.md`. Two execution options:**

**1. Subagent-Driven (this session)** - I dispatch fresh subagent per task, review between tasks, fast iteration

**2. Parallel Session (separate)** - Open new session with executing-plans, batch execution with checkpoints

**Which approach?**
