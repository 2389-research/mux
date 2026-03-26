# Adaptive Thinking Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-call thinking control to the orchestrator so it enables/disables thinking based on configurable strategy and iteration context.

**Architecture:** New `ThinkingSettings` config + `shouldEnableThinking()` decision function in the orchestrator. The orchestrator tracks `consecutiveToolIterations` and `justCompacted` state to make adaptive decisions. Token usage gains `ThinkingTokens` tracking, and thinking content blocks get published as events.

**Tech Stack:** Go, existing mux orchestrator/llm/agent packages

---

### Task 1: Add ThinkingTokens to TokenUsage

**Files:**
- Modify: `orchestrator/usage.go:12-25` (TokenUsage struct)
- Modify: `orchestrator/usage.go:33-40` (Add method)
- Modify: `orchestrator/usage.go:42-52` (AddWithCache method)
- Modify: `orchestrator/usage.go:62-72` (Snapshot method)
- Modify: `orchestrator/usage.go:75-83` (Reset method)
- Test: `orchestrator/usage_test.go`

- [ ] **Step 1: Write the failing test for ThinkingTokens tracking**

Add to `orchestrator/usage_test.go`:

```go
func TestTokenUsageAddThinkingTokens(t *testing.T) {
	u := NewTokenUsage()

	u.Add(llm.Usage{InputTokens: 100, OutputTokens: 50, ThinkingTokens: 200})

	snapshot := u.Snapshot()
	if snapshot.ThinkingTokens != 200 {
		t.Errorf("ThinkingTokens = %d, want 200", snapshot.ThinkingTokens)
	}
}

func TestTokenUsageAddMultipleWithThinking(t *testing.T) {
	u := NewTokenUsage()

	u.Add(llm.Usage{InputTokens: 100, OutputTokens: 50, ThinkingTokens: 200})
	u.Add(llm.Usage{InputTokens: 100, OutputTokens: 50, ThinkingTokens: 300})

	snapshot := u.Snapshot()
	if snapshot.ThinkingTokens != 500 {
		t.Errorf("ThinkingTokens = %d, want 500", snapshot.ThinkingTokens)
	}
}

func TestTokenUsageResetClearsThinking(t *testing.T) {
	u := NewTokenUsage()
	u.Add(llm.Usage{InputTokens: 100, OutputTokens: 50, ThinkingTokens: 200})
	u.Reset()

	snapshot := u.Snapshot()
	if snapshot.ThinkingTokens != 0 {
		t.Errorf("ThinkingTokens after reset = %d, want 0", snapshot.ThinkingTokens)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./orchestrator/ -run TestTokenUsageAddThinkingTokens -v`
Expected: FAIL — `ThinkingTokens` field does not exist on `TokenUsage`

- [ ] **Step 3: Add ThinkingTokens field and update all methods**

In `orchestrator/usage.go`, add `ThinkingTokens` to the struct:

```go
type TokenUsage struct {
	mu sync.RWMutex

	// Core token counts
	InputTokens    int64 `json:"input_tokens"`
	OutputTokens   int64 `json:"output_tokens"`
	ThinkingTokens int64 `json:"thinking_tokens,omitempty"`

	// Cache tokens (if supported by provider)
	CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`

	// Request count
	RequestCount int64 `json:"request_count"`
}
```

Update `Add()` to include thinking tokens:

```go
func (u *TokenUsage) Add(usage llm.Usage) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.InputTokens += int64(usage.InputTokens)
	u.OutputTokens += int64(usage.OutputTokens)
	u.ThinkingTokens += int64(usage.ThinkingTokens)
	u.RequestCount++
}
```

Update `AddWithCache()`:

```go
func (u *TokenUsage) AddWithCache(usage llm.Usage, cacheRead, cacheWrite int) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.InputTokens += int64(usage.InputTokens)
	u.OutputTokens += int64(usage.OutputTokens)
	u.ThinkingTokens += int64(usage.ThinkingTokens)
	u.CacheReadTokens += int64(cacheRead)
	u.CacheWriteTokens += int64(cacheWrite)
	u.RequestCount++
}
```

Update `Snapshot()`:

```go
func (u *TokenUsage) Snapshot() TokenUsage {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return TokenUsage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		ThinkingTokens:   u.ThinkingTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
		RequestCount:     u.RequestCount,
	}
}
```

Update `Reset()`:

```go
func (u *TokenUsage) Reset() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.InputTokens = 0
	u.OutputTokens = 0
	u.ThinkingTokens = 0
	u.CacheReadTokens = 0
	u.CacheWriteTokens = 0
	u.RequestCount = 0
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./orchestrator/ -run TestTokenUsage -v`
Expected: All TokenUsage tests PASS

- [ ] **Step 5: Commit**

```bash
git add orchestrator/usage.go orchestrator/usage_test.go
git commit -m "feat: add ThinkingTokens tracking to TokenUsage"
```

---

### Task 2: Add EventThinking to the Event System

**Files:**
- Modify: `orchestrator/events.go:14-21` (EventType constants)
- Modify: `orchestrator/events.go:24-47` (Event struct)
- Modify: `orchestrator/orchestrator.go:356-366` (processResponse)
- Test: `orchestrator/orchestrator_test.go`

- [ ] **Step 1: Write the failing test for thinking events**

Add to `orchestrator/orchestrator_test.go`:

```go
func TestOrchestratorThinkingEvent(t *testing.T) {
	client := &mockLLMClient{
		responses: []*llm.Response{{
			Content: []llm.ContentBlock{
				{Type: llm.ContentTypeThinking, Thinking: "Let me reason about this..."},
				{Type: llm.ContentTypeText, Text: "Hello!"},
			},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	orch := orchestrator.New(client, executor)
	events := orch.Subscribe()

	ctx := context.Background()
	err := orch.Run(ctx, "Think about something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hasThinking bool
	var thinkingText string
	for event := range events {
		if event.Type == orchestrator.EventThinking {
			hasThinking = true
			thinkingText = event.Thinking
		}
	}

	if !hasThinking {
		t.Error("expected thinking event")
	}
	if thinkingText != "Let me reason about this..." {
		t.Errorf("thinking text = %q, want 'Let me reason about this...'", thinkingText)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./orchestrator/ -run TestOrchestratorThinkingEvent -v`
Expected: FAIL — `EventThinking` not defined, `Thinking` field not on Event

- [ ] **Step 3: Add EventThinking constant, Thinking field on Event, constructor, and processResponse handling**

In `orchestrator/events.go`, add to the constants block:

```go
EventThinking   EventType = "thinking"
```

Add to the `Event` struct:

```go
// For EventThinking
Thinking string
```

Add constructor:

```go
// NewThinkingEvent creates a thinking content event.
func NewThinkingEvent(thinking string) Event {
	return Event{Type: EventThinking, Thinking: thinking}
}
```

In `orchestrator/orchestrator.go`, update `processResponse()` to handle thinking blocks:

```go
func (o *Orchestrator) processResponse(resp *llm.Response) {
	for _, block := range resp.Content {
		switch block.Type {
		case llm.ContentTypeText:
			o.eventBus.Publish(NewTextEvent(block.Text))
		case llm.ContentTypeToolUse:
			o.eventBus.Publish(NewToolCallEvent(block.ID, block.Name, block.Input))
		case llm.ContentTypeThinking:
			o.eventBus.Publish(NewThinkingEvent(block.Thinking))
		}
	}
	o.messages = append(o.messages, llm.Message{Role: llm.RoleAssistant, Blocks: resp.Content})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./orchestrator/ -run TestOrchestratorThinkingEvent -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add orchestrator/events.go orchestrator/orchestrator.go orchestrator/orchestrator_test.go
git commit -m "feat: add EventThinking to orchestrator event system"
```

---

### Task 3: Add ThinkingSettings Config and shouldEnableThinking

**Files:**
- Modify: `orchestrator/orchestrator.go:38-46` (Config struct)
- Modify: `orchestrator/orchestrator.go:54-65` (Orchestrator struct)
- Test: `orchestrator/orchestrator_test.go`

- [ ] **Step 1: Write failing tests for shouldEnableThinking**

The `shouldEnableThinking` method and the state fields are internal to the orchestrator. Since the test file is `package orchestrator_test` (external), we need to test through the public API. We'll use a request-capturing mock to verify thinking was set on the request.

Add to `orchestrator/orchestrator_test.go`:

```go
// capturingLLMClient records the requests it receives for assertion.
type capturingLLMClient struct {
	requests []*llm.Request
	responses []*llm.Response
	callIndex int
	mu        sync.Mutex
}

func (c *capturingLLMClient) CreateMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	if c.callIndex >= len(c.responses) {
		return &llm.Response{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		}, nil
	}
	resp := c.responses[c.callIndex]
	c.callIndex++
	return resp, nil
}

func (c *capturingLLMClient) CreateMessageStream(ctx context.Context, req *llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	go func() {
		resp, _ := c.CreateMessage(ctx, req)
		ch <- llm.StreamEvent{Type: llm.EventMessageStop, Response: resp}
		close(ch)
	}()
	return ch, nil
}

func TestThinkingStrategyOff(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy: orchestrator.ThinkingOff,
		Budget:   8192,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()

	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(client.requests))
	}
	if client.requests[0].Thinking != nil {
		t.Error("ThinkingOff: expected Thinking to be nil")
	}
}

func TestThinkingStrategyAlways(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type: llm.ContentTypeToolUse, ID: "t1", Name: "test_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "test_tool"})
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy: orchestrator.ThinkingAlways,
		Budget:   8192,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()

	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(client.requests))
	}
	for i, req := range client.requests {
		if req.Thinking == nil {
			t.Errorf("request %d: ThinkingAlways: expected Thinking to be set", i)
		} else if req.Thinking.Budget != 8192 {
			t.Errorf("request %d: Budget = %d, want 8192", i, req.Thinking.Budget)
		}
	}
}

func TestThinkingStrategyFirstOnly(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type: llm.ContentTypeToolUse, ID: "t1", Name: "test_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "test_tool"})
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy: orchestrator.ThinkingFirstOnly,
		Budget:   8192,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()

	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(client.requests))
	}
	// First request should have thinking
	if client.requests[0].Thinking == nil {
		t.Error("request 0: ThinkingFirstOnly: expected Thinking to be set")
	}
	// Second request should NOT have thinking
	if client.requests[1].Thinking != nil {
		t.Error("request 1: ThinkingFirstOnly: expected Thinking to be nil")
	}
}

func TestThinkingStrategyAdaptiveFirstCall(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type: llm.ContentTypeToolUse, ID: "t1", Name: "test_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "test_tool"})
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy:                 orchestrator.ThinkingAdaptive,
		Budget:                   8192,
		ConsecutiveToolThreshold: 5,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()

	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// First call gets thinking, second doesn't (only 1 consecutive tool iteration, threshold is 5)
	if client.requests[0].Thinking == nil {
		t.Error("request 0: Adaptive: expected Thinking on first call")
	}
	if client.requests[1].Thinking != nil {
		t.Error("request 1: Adaptive: expected no Thinking on second call")
	}
}

func TestThinkingStrategyAdaptiveToolError(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{
			{
				Content: []llm.ContentBlock{{
					Type: llm.ContentTypeToolUse, ID: "t1", Name: "error_tool",
					Input: map[string]any{},
				}},
				StopReason: llm.StopReasonToolUse,
			},
			{
				Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
				StopReason: llm.StopReasonEndTurn,
			},
		},
	}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{
		name: "error_tool",
		execFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return nil, fmt.Errorf("something went wrong")
		},
	})
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy:                 orchestrator.ThinkingAdaptive,
		Budget:                   8192,
		ConsecutiveToolThreshold: 5,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()

	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(client.requests))
	}
	// First call: thinking (iteration 0)
	if client.requests[0].Thinking == nil {
		t.Error("request 0: expected Thinking on first call")
	}
	// Second call: thinking re-enabled because tool returned error
	if client.requests[1].Thinking == nil {
		t.Error("request 1: expected Thinking re-enabled after tool error")
	}
}

func TestThinkingStrategyAdaptiveConsecutiveTools(t *testing.T) {
	// Build a chain: 6 tool calls in a row, then done
	// Threshold is 3 so thinking should re-enable on call 4 (index 3)
	responses := make([]*llm.Response, 0, 7)
	for i := 0; i < 6; i++ {
		responses = append(responses, &llm.Response{
			Content: []llm.ContentBlock{{
				Type: llm.ContentTypeToolUse, ID: fmt.Sprintf("t%d", i), Name: "test_tool",
				Input: map[string]any{},
			}},
			StopReason: llm.StopReasonToolUse,
		})
	}
	responses = append(responses, &llm.Response{
		Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
		StopReason: llm.StopReasonEndTurn,
	})

	client := &capturingLLMClient{responses: responses}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "test_tool"})
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy:                 orchestrator.ThinkingAdaptive,
		Budget:                   8192,
		ConsecutiveToolThreshold: 3,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()

	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 7 requests total (6 tool use + 1 text)
	if len(client.requests) != 7 {
		t.Fatalf("expected 7 requests, got %d", len(client.requests))
	}

	// Request 0: thinking (first call)
	// Request 1: no thinking (1 consecutive tool)
	// Request 2: no thinking (2 consecutive tools)
	// Request 3: thinking (3 consecutive tools >= threshold)
	// Request 4: thinking (4 consecutive tools >= threshold)
	// Request 5: thinking (5 consecutive tools >= threshold)
	// Request 6: thinking (6 consecutive tools >= threshold)
	expected := []bool{true, false, false, true, true, true, true}
	for i, req := range client.requests {
		hasThinking := req.Thinking != nil
		if hasThinking != expected[i] {
			t.Errorf("request %d: thinking=%v, want %v", i, hasThinking, expected[i])
		}
	}
}

func TestThinkingNilSettings(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()
	executor := tool.NewExecutor(registry)

	// No ThinkingSettings at all
	orch := orchestrator.New(client, executor)
	_ = orch.Subscribe()

	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if client.requests[0].Thinking != nil {
		t.Error("nil ThinkingSettings: expected Thinking to be nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./orchestrator/ -run "TestThinking" -v`
Expected: FAIL — `ThinkingSettings`, `ThinkingStrategy`, `ThinkingOff`, etc. are not defined

- [ ] **Step 3: Add ThinkingStrategy type, ThinkingSettings struct, Config field, Orchestrator state fields, and shouldEnableThinking**

In `orchestrator/orchestrator.go`, add after the `DefaultContextBudget` constant:

```go
// ThinkingStrategy controls when the orchestrator enables thinking on API calls.
type ThinkingStrategy int

const (
	ThinkingOff       ThinkingStrategy = iota // No thinking on any call
	ThinkingAlways                            // Thinking on every call
	ThinkingFirstOnly                         // Thinking on iteration 0 only
	ThinkingAdaptive                          // First call + re-enable on errors/stuck/compaction
)

// DefaultThinkingBudget is the default thinking token budget when thinking is enabled.
const DefaultThinkingBudget = 8192

// DefaultConsecutiveToolThreshold is the default number of consecutive tool iterations
// before adaptive thinking re-enables thinking.
const DefaultConsecutiveToolThreshold = 5

// ThinkingSettings configures per-call thinking behavior in the orchestrator.
type ThinkingSettings struct {
	Strategy                 ThinkingStrategy
	Budget                   int // Token budget when thinking is enabled
	ConsecutiveToolThreshold int // Re-enable after N consecutive tool iterations (Adaptive only)
}
```

Add `ThinkingSettings` to the `Config` struct:

```go
type Config struct {
	MaxIterations    int
	SystemPrompt     string
	Model            string
	HookManager      *hooks.Manager
	ContextBudget    int
	CompactionModel  string
	ThinkingSettings *ThinkingSettings // Per-call thinking control (nil = no thinking)
}
```

Add state fields to the `Orchestrator` struct:

```go
type Orchestrator struct {
	client      llm.Client
	executor    *tool.Executor
	config      Config
	state       *StateMachine
	eventBus    *EventBus
	hookManager *hooks.Manager
	sessionID   string
	usage       *TokenUsage
	mu          sync.Mutex
	messages    []llm.Message

	// Thinking state tracking
	iteration                 int
	consecutiveToolIterations int
	justCompacted             bool
}
```

Add the `shouldEnableThinking` method:

```go
// shouldEnableThinking decides whether to enable thinking for the current API call.
func (o *Orchestrator) shouldEnableThinking() bool {
	if o.config.ThinkingSettings == nil {
		return false
	}
	switch o.config.ThinkingSettings.Strategy {
	case ThinkingOff:
		return false
	case ThinkingAlways:
		return true
	case ThinkingFirstOnly:
		return o.iteration == 0
	case ThinkingAdaptive:
		if o.iteration == 0 {
			return true
		}
		if o.justCompacted {
			return true
		}
		if o.consecutiveToolIterations >= o.config.ThinkingSettings.ConsecutiveToolThreshold {
			return true
		}
		if o.lastToolResultHadError() {
			return true
		}
		return false
	}
	return false
}

// lastToolResultHadError checks if any tool result in the last user message had an error.
func (o *Orchestrator) lastToolResultHadError() bool {
	if len(o.messages) == 0 {
		return false
	}
	last := o.messages[len(o.messages)-1]
	if last.Role != llm.RoleUser {
		return false
	}
	for _, block := range last.Blocks {
		if block.Type == llm.ContentTypeToolResult && block.IsError {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Update buildRequest to call shouldEnableThinking**

Replace `buildRequest()`:

```go
func (o *Orchestrator) buildRequest() *llm.Request {
	tools := o.buildToolDefinitions()
	req := &llm.Request{
		Messages:  o.messages,
		System:    o.config.SystemPrompt,
		Model:     o.config.Model,
		MaxTokens: llm.DefaultMaxTokens,
		Tools:     tools,
	}
	if o.shouldEnableThinking() {
		req.Thinking = &llm.ThinkingConfig{
			Enabled: true,
			Budget:  o.config.ThinkingSettings.Budget,
		}
	}
	return req
}
```

- [ ] **Step 5: Update runLoop with iteration tracking and bookkeeping**

Replace the `runLoop` method. Key changes:
- Reset `o.iteration`, `o.consecutiveToolIterations` at start
- Increment `o.iteration` each loop
- Set `o.justCompacted = true` after compaction
- Clear `o.justCompacted = false` after each API call
- Track `o.consecutiveToolIterations`: increment on tool use, reset on no tool use

```go
func (o *Orchestrator) runLoop(ctx context.Context, prompt string) error {
	// Reset thinking state for this run
	o.iteration = 0
	o.consecutiveToolIterations = 0
	o.justCompacted = false

	for i := 0; i < o.config.MaxIterations; i++ {
		o.iteration = i

		// Check context at start of each iteration
		select {
		case <-ctx.Done():
			return o.handleError(ctx.Err())
		default:
		}

		// Check for compaction before LLM call
		if result, err := o.compact(ctx); err != nil {
			return o.handleError(fmt.Errorf("compaction failed: %w", err))
		} else if result != nil {
			o.justCompacted = true
			// Fire compaction hook
			if o.hookManager != nil {
				event := &hooks.CompactionEvent{
					SessionID:       o.sessionID,
					OriginalTokens:  result.OriginalTokens,
					CompactedTokens: result.CompactedTokens,
					MessagesRemoved: result.MessagesRemoved,
					Summary:         result.Summary,
				}
				if err := o.hookManager.FireCompaction(ctx, event); err != nil {
					return o.handleError(err)
				}
			}
		}

		// Fire Iteration hook at start of each loop iteration
		if o.hookManager != nil {
			event := &hooks.IterationEvent{
				SessionID: o.sessionID,
				Iteration: i,
			}
			if err := o.hookManager.FireIteration(ctx, event); err != nil {
				return o.handleError(err)
			}
		}

		if err := o.transition(StateStreaming); err != nil {
			return o.handleError(err)
		}

		resp, err := o.client.CreateMessage(ctx, o.buildRequest())
		if err != nil {
			return o.handleError(err)
		}

		// Clear justCompacted after each API call
		o.justCompacted = false

		// Track token usage
		o.usage.Add(resp.Usage)

		o.processResponse(resp)

		if resp.HasToolUse() {
			o.consecutiveToolIterations++
			if err := o.executeTools(ctx, resp.ToolUses()); err != nil {
				return o.handleError(err)
			}
			continue
		}

		// No tool use — reset consecutive counter
		o.consecutiveToolIterations = 0

		// Fire Stop hook - allows hooks to prevent stopping
		if o.hookManager != nil {
			stopEvent := &hooks.StopEvent{
				SessionID: o.sessionID,
				FinalText: resp.TextContent(),
			}
			continueLoop, err := o.hookManager.FireStop(ctx, stopEvent)
			if err != nil {
				return o.handleError(err)
			}
			if continueLoop {
				// Hook requested continuation - reset state and add user message for next iteration
				o.state.Reset()
				o.messages = append(o.messages, llm.NewUserMessage("continue"))
				continue
			}
		}

		if err := o.transition(StateComplete); err != nil {
			return o.handleError(err)
		}
		o.eventBus.Publish(NewCompleteEvent(resp.TextContent()))
		return nil
	}

	return o.handleError(fmt.Errorf("exceeded max iterations (%d) while processing: %s", o.config.MaxIterations, prompt))
}
```

- [ ] **Step 6: Run all thinking tests**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./orchestrator/ -run "TestThinking" -v`
Expected: All PASS

- [ ] **Step 7: Run full orchestrator test suite**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./orchestrator/ -v`
Expected: All PASS (no regressions)

- [ ] **Step 8: Commit**

```bash
git add orchestrator/orchestrator.go orchestrator/orchestrator_test.go
git commit -m "feat: add ThinkingSettings and per-call adaptive thinking control"
```

---

### Task 4: Add ThinkingSettings to Agent Config

**Files:**
- Modify: `agent/config.go:12-42` (Config struct)
- Modify: `agent/agent.go:62-88` (init method)
- Test: `agent/agent_test.go`

- [ ] **Step 1: Write failing test for agent thinking passthrough**

Add to `agent/agent_test.go` (check what test patterns exist first — the test may need a capturing mock):

```go
func TestAgentThinkingSettingsPassthrough(t *testing.T) {
	client := &capturingLLMClient{
		responses: []*llm.Response{{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		}},
	}
	registry := tool.NewRegistry()

	a := agent.New(agent.Config{
		Name:      "test",
		Registry:  registry,
		LLMClient: client,
		ThinkingSettings: &orchestrator.ThinkingSettings{
			Strategy: orchestrator.ThinkingAlways,
			Budget:   4096,
		},
	})

	_ = a.Subscribe()
	err := a.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.requests) == 0 {
		t.Fatal("expected at least 1 request")
	}
	if client.requests[0].Thinking == nil {
		t.Error("expected Thinking to be set via agent passthrough")
	}
	if client.requests[0].Thinking.Budget != 4096 {
		t.Errorf("Budget = %d, want 4096", client.requests[0].Thinking.Budget)
	}
}
```

Note: The `capturingLLMClient` mock needs to exist in the agent test file too (or use the same pattern from the agent's existing tests). Check existing test patterns in `agent/agent_test.go` and replicate. The key assertion is that `req.Thinking` is set when the agent is configured with `ThinkingSettings`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./agent/ -run TestAgentThinkingSettingsPassthrough -v`
Expected: FAIL — `ThinkingSettings` field does not exist on `agent.Config`

- [ ] **Step 3: Add ThinkingSettings to agent Config and wire in init()**

In `agent/config.go`, add to the Config struct:

```go
import "github.com/2389-research/mux/orchestrator"
```

```go
// ThinkingSettings configures per-call thinking behavior (nil = no thinking).
ThinkingSettings *orchestrator.ThinkingSettings
```

In `agent/agent.go`, update `init()` to pass ThinkingSettings to the orchestrator config:

```go
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
	orchConfig.HookManager = a.hookManager
	orchConfig.ThinkingSettings = a.config.ThinkingSettings

	// Create orchestrator
	a.orch = orchestrator.NewWithConfig(a.config.LLMClient, a.executor, orchConfig)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./agent/ -run TestAgentThinkingSettingsPassthrough -v`
Expected: PASS

- [ ] **Step 5: Run full agent test suite**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./agent/ -v`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add agent/config.go agent/agent.go agent/agent_test.go
git commit -m "feat: add ThinkingSettings passthrough from Agent to Orchestrator"
```

---

### Task 5: Integration Test — Adaptive Thinking Through Multi-Step Loop

**Files:**
- Test: `orchestrator/orchestrator_test.go`

- [ ] **Step 1: Write integration test for adaptive thinking through a full multi-step loop**

This test exercises the full adaptive cycle: first call gets thinking, middle calls don't, error re-enables, consecutive tools re-enable.

Add to `orchestrator/orchestrator_test.go`:

```go
func TestThinkingAdaptiveFullCycle(t *testing.T) {
	// Scenario:
	// Call 0: first call → thinking ON
	// Call 1: tool success → thinking OFF (1 consecutive)
	// Call 2: tool error → thinking ON next call
	// Call 3: thinking ON (re-enabled by error)
	// Call 4: done

	responses := []*llm.Response{
		// Call 0 → tool use (success)
		{
			Content: []llm.ContentBlock{{
				Type: llm.ContentTypeToolUse, ID: "t0", Name: "good_tool",
				Input: map[string]any{},
			}},
			StopReason: llm.StopReasonToolUse,
		},
		// Call 1 → tool use (will error)
		{
			Content: []llm.ContentBlock{{
				Type: llm.ContentTypeToolUse, ID: "t1", Name: "bad_tool",
				Input: map[string]any{},
			}},
			StopReason: llm.StopReasonToolUse,
		},
		// Call 2 → tool use (success, after error re-enable)
		{
			Content: []llm.ContentBlock{{
				Type: llm.ContentTypeToolUse, ID: "t2", Name: "good_tool",
				Input: map[string]any{},
			}},
			StopReason: llm.StopReasonToolUse,
		},
		// Call 3 → done
		{
			Content:    []llm.ContentBlock{{Type: llm.ContentTypeText, Text: "done"}},
			StopReason: llm.StopReasonEndTurn,
		},
	}

	client := &capturingLLMClient{responses: responses}
	registry := tool.NewRegistry()
	registry.Register(&mockTool{name: "good_tool"})
	registry.Register(&mockTool{
		name: "bad_tool",
		execFunc: func(ctx context.Context, params map[string]any) (*tool.Result, error) {
			return nil, fmt.Errorf("tool failed")
		},
	})
	executor := tool.NewExecutor(registry)

	config := orchestrator.DefaultConfig()
	config.ThinkingSettings = &orchestrator.ThinkingSettings{
		Strategy:                 orchestrator.ThinkingAdaptive,
		Budget:                   8192,
		ConsecutiveToolThreshold: 5,
	}
	orch := orchestrator.NewWithConfig(client, executor, config)
	_ = orch.Subscribe()

	err := orch.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.requests) != 4 {
		t.Fatalf("expected 4 requests, got %d", len(client.requests))
	}

	// Call 0: thinking ON (first call)
	// Call 1: thinking OFF (1 consecutive tool, no error)
	// Call 2: thinking ON (previous tool had error)
	// Call 3: thinking OFF (no error, 1 consecutive tool)
	expected := []bool{true, false, true, false}
	for i, req := range client.requests {
		hasThinking := req.Thinking != nil
		if hasThinking != expected[i] {
			t.Errorf("request %d: thinking=%v, want %v", i, hasThinking, expected[i])
		}
	}
}
```

- [ ] **Step 2: Run test**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./orchestrator/ -run TestThinkingAdaptiveFullCycle -v`
Expected: PASS

- [ ] **Step 3: Run full test suite to confirm no regressions**

Run: `cd /Users/harper/Public/src/2389/mux && go test ./... -v`
Expected: All PASS

- [ ] **Step 4: Commit**

```bash
git add orchestrator/orchestrator_test.go
git commit -m "test: add integration test for adaptive thinking full cycle"
```

---

### Task 6: Update BBS Thread

- [ ] **Step 1: Post resolution to the BBS thread**

Post a message to BBS thread `bd7fa482-05fd-49b4-94ca-0c7bc7aaf830` noting the feature is resolved, listing the commits, and summarizing the approach (hybrid of Options A and C — Adaptive strategy with configurable fallbacks).
