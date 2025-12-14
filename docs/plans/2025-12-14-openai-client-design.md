# OpenAI Client Design

## Overview

Add OpenAI support to mux by implementing `llm.Client` interface with `OpenAIClient`.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Default model | `gpt-5.2` | GPT-5.2 Thinking variant, best for agentic work |
| SDK | `github.com/openai/openai-go` | Official SDK, matches Anthropic pattern |
| Tool format | Tools API | Modern approach, supports parallel tool calls |
| Architecture | Mirror AnthropicClient | Consistency, proven pattern |

## Implementation

### Structure

```go
type OpenAIClient struct {
    client *openai.Client
    model  string
}

func NewOpenAIClient(apiKey, model string) *OpenAIClient
```

### Key Mappings

**Messages:**
- System prompt → system message (first message)
- Tool results → user message with tool content

**Stop Reasons:**
- `stop` → `StopReasonEndTurn`
- `tool_calls` → `StopReasonToolUse`
- `length` → `StopReasonMaxTokens`

**Tool Calls:**
- OpenAI `ToolCall.ID` → mux `ContentBlock.ID`
- OpenAI `Function.Name` → mux `ContentBlock.Name`
- OpenAI `Function.Arguments` → mux `ContentBlock.Input`

### Files

- `llm/openai.go` - Client implementation (~200 lines)
- `llm/openai_test.go` - Tests (~300 lines)

## Usage

```go
// Anthropic
client := llm.NewAnthropicClient(apiKey, "claude-sonnet-4-20250514")

// OpenAI
client := llm.NewOpenAIClient(apiKey, "gpt-5.2")
```

Both implement `llm.Client` and work identically with mux agents.
