# Adaptive Thinking: Per-Call Budget Control in the Orchestrator

## Problem

The mux orchestrator has no built-in thinking support. External wrappers (like hex's ThinkingClient) inject a fixed thinking budget on every API call. This hurts performance because most calls in an agentic loop — file reads, tool execution, simple follow-ups — don't benefit from deep reasoning. The latency cost of entering thinking mode on every call compounds across 30+ iterations, causing timeouts on long-running tasks.

Evidence from Terminal-Bench 2.0:
- With fixed 10K thinking on every call: 54.5% (12 timeouts)
- Without thinking: 60.2% (8 timeouts)
- Thinking caused 4 extra timeouts while only saving 2 wrong answers

## Solution

Add per-call thinking control to the orchestrator. The orchestrator decides whether to enable thinking on each API call based on a configurable strategy and iteration context.

## Design

### Configuration Types

New types in the orchestrator package:

```go
type ThinkingStrategy int

const (
    ThinkingOff       ThinkingStrategy = iota // No thinking on any call
    ThinkingAlways                            // Thinking on every call
    ThinkingFirstOnly                         // Thinking on iteration 0 only
    ThinkingAdaptive                          // First call + re-enable on errors/stuck/compaction
)

type ThinkingSettings struct {
    Strategy                 ThinkingStrategy
    Budget                   int // Token budget when thinking is enabled
    ConsecutiveToolThreshold int // Re-enable after N consecutive tool iterations (Adaptive only)
}
```

Add `ThinkingSettings *ThinkingSettings` to the orchestrator `Config` struct.

**Defaults:** `ThinkingAdaptive`, 8192 budget, threshold of 5.

### Orchestrator State

New fields on the `Orchestrator` struct for tracking iteration context:

```go
consecutiveToolIterations int  // Incremented on tool use response, reset on text-only response
justCompacted             bool // Set after compaction, cleared after next API call
```

### Decision Logic

```go
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
```

#### Adaptive Re-enable Triggers

1. **Iteration 0** — the model needs to understand the task and plan its approach.
2. **Tool execution errors** — a tool returned `is_error: true`. The model may need to reason about what went wrong.
3. **Consecutive tool loops** — after N iterations of pure tool use without text output (default 5). The model may be stuck in a loop.
4. **Post-compaction** — after context compaction, the model lost context and needs to re-orient.

### Request Building

`buildRequest()` calls `shouldEnableThinking()` and sets `req.Thinking` when enabled:

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

### Loop Bookkeeping

In the main orchestrator loop, after processing each response:

- Response has tool use: `o.consecutiveToolIterations++`
- Response has no tool use (text-only): `o.consecutiveToolIterations = 0`
- After each API call: `o.justCompacted = false`
- In compaction path: `o.justCompacted = true`

### Token Usage Tracking

Add `ThinkingTokens` to `TokenUsage` and update `Add()`:

```go
type TokenUsage struct {
    InputTokens      int64
    OutputTokens     int64
    ThinkingTokens   int64 // new
    CacheReadTokens  int64
    CacheWriteTokens int64
    RequestCount     int64
}

func (u *TokenUsage) Add(usage llm.Usage) {
    u.mu.Lock()
    defer u.mu.Unlock()
    u.InputTokens += int64(usage.InputTokens)
    u.OutputTokens += int64(usage.OutputTokens)
    u.ThinkingTokens += int64(usage.ThinkingTokens)
    u.RequestCount++
}
```

### Thinking Events

Add `EventThinking` to the event system:

```go
const EventThinking EventType = "thinking"
```

Published in `processResponse()` when a `ContentTypeThinking` block is encountered, following the same pattern as text events.

### Agent Config Passthrough

Add `ThinkingSettings *orchestrator.ThinkingSettings` to the agent `Config` struct. The agent passes this through to the orchestrator when constructing it. No logic in the agent layer.

## Files Changed

| File | Change |
|------|--------|
| `orchestrator/orchestrator.go` | Add ThinkingSettings to Config, state fields, shouldEnableThinking(), update buildRequest(), loop bookkeeping |
| `orchestrator/usage.go` | Add ThinkingTokens field and tracking |
| `orchestrator/events.go` | Add EventThinking type and publishing |
| `agent/config.go` | Add ThinkingSettings passthrough |
| `agent/agent.go` | Wire ThinkingSettings to orchestrator Config |
| `orchestrator/orchestrator_test.go` | Tests for all four strategies, adaptive triggers, state tracking |
| `orchestrator/usage_test.go` | Test ThinkingTokens tracking |
| `orchestrator/events_test.go` | Test thinking event publishing |

## Testing Strategy

- **Unit tests** for `shouldEnableThinking()` covering all four strategies and all adaptive triggers
- **Unit tests** for state bookkeeping (consecutiveToolIterations increment/reset, justCompacted set/clear)
- **Unit tests** for ThinkingTokens in usage tracking
- **Integration tests** using mock LLM client to verify thinking is enabled/disabled at correct iterations through a multi-step tool use loop
- **Event tests** to verify thinking content blocks produce EventThinking events

## Non-Goals

- No ML-based or heuristic complexity detection. Simple iteration-based heuristics only.
- No per-provider thinking strategy differences. The orchestrator decides; providers translate.
- No runtime strategy switching. Strategy is set at config time.
