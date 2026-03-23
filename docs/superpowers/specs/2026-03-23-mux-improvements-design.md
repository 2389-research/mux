# Mux Improvements Design Spec

**Date**: 2026-03-23
**Source**: BBS mux topic — 4 threads from Terminal-Bench evaluation findings

## Overview

Four improvements to the mux LLM client framework:

1. Add `WithBaseURL` constructor for OpenRouter
2. Add retry with exponential backoff (decorator pattern)
3. Add thinking/reasoning support for Anthropic, OpenAI, and Gemini
4. Fix OpenAI tool_calls 400 error bug

## Task 1: OpenRouter WithBaseURL

**Status**: Anthropic, OpenAI, Gemini, and Ollama all support custom base URLs. Only OpenRouter is missing.

**Change**: Add `NewOpenRouterClientWithBaseURL(apiKey, model, baseURL string)` to `llm/openrouter.go`. When `baseURL` is non-empty, it **replaces** `OpenRouterBaseURL` entirely. When empty, falls back to `OpenRouterBaseURL`.

**Files**: `llm/openrouter.go`, `llm/openrouter_test.go`

## Task 2: Retry with Exponential Backoff

**Pattern**: Decorator wrapping `llm.Client`. Single implementation covers all providers.

**New file**: `llm/retry.go`

```go
type RetryConfig struct {
    MaxRetries    int           // default 3
    InitialDelay  time.Duration // default 1s
    MaxDelay      time.Duration // default 30s
    Multiplier    float64       // default 2.0
}

type RetryClient struct {
    inner  Client
    config RetryConfig
}

func NewRetryClient(inner Client, config *RetryConfig) *RetryClient
```

**Retryable conditions**:
- HTTP status codes: 429, 500, 502, 503, 504
- Detection via `errors.As`:
  - Anthropic SDK: `*anthropic.Error` has `StatusCode int` field
  - OpenAI SDK: `*openai.Error` has `StatusCode int` field
  - Gemini SDK: use `errors.As` with `*googleapi.Error` or fall back to error string inspection
- Both Anthropic and OpenAI error types have `Response *http.Response` for reading `Retry-After` header

**Backoff**: `min(initialDelay * multiplier^attempt, maxDelay)` with jitter. Respect `Retry-After` header via `err.Response.Header.Get("Retry-After")`.

**Both methods**: `CreateMessage` and `CreateMessageStream` get retry logic. For streaming, only retry when `CreateMessageStream` returns an `error` (connection failure). Mid-stream errors arrive via `EventError` on the channel and are not retried.

**Testing**: Use a test double (a struct implementing `llm.Client` that returns controlled errors) to verify retry count, backoff behavior, and non-retryable pass-through. This is standard decorator unit testing, not mock mode.

**Files**: `llm/retry.go`, `llm/retry_test.go`

## Task 3: Thinking/Reasoning Support

### Type changes in `llm/types.go`

Add to `Request`:
```go
type ThinkingConfig struct {
    Enabled bool
    Budget  int // token budget; interpretation varies by provider
}

// In Request struct:
Thinking *ThinkingConfig `json:"thinking,omitempty"`
```

Add thinking content type:
```go
ContentTypeThinking ContentType = "thinking"
```

Add to `ContentBlock`:
```go
Thinking string `json:"thinking,omitempty"` // thinking/reasoning text
```

Add to `Usage`:
```go
ThinkingTokens int `json:"thinking_tokens,omitempty"`
```

### Orchestrator fix: `orchestrator/orchestrator.go`

`buildRequest()` hardcodes `MaxTokens: 4096`. When thinking is enabled, Anthropic requires `MaxTokens >= budget_tokens`. Fix: when `Config` has thinking enabled and `MaxTokens` would be less than the thinking budget, auto-increase `MaxTokens` to at least `budget_tokens`. Also consider using `DefaultMaxTokens` (16384) instead of the hardcoded 4096.

### Anthropic (`llm/anthropic.go`)

In `convertRequest`:
- When `req.Thinking != nil && req.Thinking.Enabled`, set `params.Thinking` to `ThinkingConfigParamUnion{OfEnabled: &ThinkingConfigEnabledParam{BudgetTokens: int64(budget)}}`
- If `req.MaxTokens < req.Thinking.Budget`, bump `MaxTokens` to `Budget`

In `convertResponse`:
- Parse `type: "thinking"` content blocks (from `ContentBlockUnion.Thinking` field) into `ContentBlock{Type: ContentTypeThinking, Thinking: text}`
- For thinking token tracking: the current SDK (v1.19.0) may not expose a dedicated `thinking_tokens` field in Usage. Populate `ThinkingTokens` if the SDK provides it, otherwise leave as 0. Do not confuse with `CacheCreationInputTokens`.

### OpenAI (`llm/openai.go`)

In `convertOpenAIRequest`:
- When `req.Thinking != nil && req.Thinking.Enabled`, set `params.ReasoningEffort`:
  - Budget <= 4096: `openai.ReasoningEffortLow`
  - Budget <= 16384: `openai.ReasoningEffortMedium`
  - Budget > 16384: `openai.ReasoningEffortHigh`

In `convertOpenAIResponse`:
- Track reasoning tokens from `resp.Usage.CompletionTokensDetails.ReasoningTokens` into `Usage.ThinkingTokens`

### Gemini (`llm/gemini.go`)

In `convertGeminiRequest`:
- When `req.Thinking != nil && req.Thinking.Enabled`, add to config:
  ```go
  config.ThinkingConfig = &genai.ThinkingConfig{
      ThinkingBudget: ptr(int32(req.Thinking.Budget)),
      IncludeThoughts: true,  // required to receive thinking content
  }
  ```

In `convertGeminiResponse`:
- Check `part.Thought == true` to identify thinking parts, emit as `ContentBlock{Type: ContentTypeThinking, Thinking: part.Text}`
- Gemini SDK does not expose thinking-specific token counts; `ThinkingTokens` will be 0

**Files**: `llm/types.go`, `llm/anthropic.go`, `llm/openai.go`, `llm/gemini.go`, `orchestrator/orchestrator.go`, plus corresponding test files

## Task 4: OpenAI tool_calls Bug Fix

**Root cause**: `convertUserMessage` in `openai.go` returns after the first `ContentTypeToolResult` block. When the orchestrator packs multiple tool results into one user message (`orchestrator.go:411`), only the first tool response reaches OpenAI. The remaining `tool_call_id`s have no response, violating OpenAI's protocol.

**Fix**: Change `convertOpenAIRequest` to detect user messages containing tool result blocks and expand them into multiple `openai.ToolMessage` calls — one per tool result block.

In `convertOpenAIRequest`, replace:
```go
case RoleUser:
    messages = append(messages, convertUserMessage(msg))
```

With a helper `convertUserMessages(msg)` that returns `[]openai.ChatCompletionMessageParamUnion`:
- If the message has any `ContentTypeToolResult` blocks, emit one `openai.ToolMessage(block.Text, block.ToolUseID)` per tool result block
- If the message also has text blocks alongside tool results (defensive), emit the text as a separate `UserMessage` before the tool messages
- If no tool result blocks, return a single-element slice from `convertUserMessage` as before

This also fixes OpenRouter and Ollama since they use the same `convertOpenAIRequest` function.

**Files**: `llm/openai.go`, `llm/openai_test.go`

## Implementation Order

1. **Task 4** (bug fix) — highest impact, unblocks OpenAI provider entirely
2. **Task 1** (OpenRouter WithBaseURL) — trivial, quick win
3. **Task 3** (thinking support) — high value, multiple files
4. **Task 2** (retry) — new file, decorator pattern, independent of others

## Testing Strategy

- **Task 1**: Unit test verifying client creation with custom base URL
- **Task 2**: Unit tests with test double client returning controlled errors, verify retry count, backoff behavior, Retry-After parsing, non-retryable pass-through
- **Task 3**: Unit tests for request conversion (thinking params present in serialized request), response conversion (thinking blocks parsed correctly), budget-to-effort mapping for OpenAI
- **Task 4**: Unit test with multi-tool-call assistant message followed by multi-tool-result user message — verify all tool_call_ids get corresponding ToolMessages
