# LLM Providers Design: Gemini, OpenRouter, Ollama

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add support for Gemini, OpenRouter, and Ollama LLM providers to mux.

**Architecture:** Each provider gets its own client implementation following the existing `Client` interface pattern. OpenRouter and Ollama use OpenAI-compatible APIs with custom base URLs. Gemini uses the official Google genai SDK.

**Tech Stack:** Go, google.golang.org/genai SDK, openai-go SDK

---

## New Files

| File | Purpose |
|------|---------|
| `llm/gemini.go` | Gemini client using google.golang.org/genai |
| `llm/gemini_test.go` | Tests for Gemini client |
| `llm/openrouter.go` | OpenRouter client (OpenAI-compatible) |
| `llm/openrouter_test.go` | Tests for OpenRouter client |
| `llm/ollama.go` | Ollama client (OpenAI-compatible) |
| `llm/ollama_test.go` | Tests for Ollama client |

## Dependencies

```go
// New dependency
google.golang.org/genai

// Existing (reused)
github.com/openai/openai-go
```

## API Design

### Gemini

```go
type GeminiClient struct {
    client *genai.Client
    model  string
}

// Returns error because genai.NewClient can fail
func NewGeminiClient(apiKey, model string) (*GeminiClient, error)

func (g *GeminiClient) CreateMessage(ctx context.Context, req *Request) (*Response, error)
func (g *GeminiClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error)
```

Default model: `gemini-2.0-flash`

### OpenRouter

```go
type OpenRouterClient struct {
    client *openai.Client
    model  string
}

func NewOpenRouterClient(apiKey, model string) *OpenRouterClient

func (o *OpenRouterClient) CreateMessage(ctx context.Context, req *Request) (*Response, error)
func (o *OpenRouterClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error)
```

Base URL: `https://openrouter.ai/api/v1`
Default model: `anthropic/claude-3.5-sonnet`

### Ollama

```go
type OllamaClient struct {
    client *openai.Client
    model  string
}

func NewOllamaClient(baseURL, model string) *OllamaClient

func (o *OllamaClient) CreateMessage(ctx context.Context, req *Request) (*Response, error)
func (o *OllamaClient) CreateMessageStream(ctx context.Context, req *Request) (<-chan StreamEvent, error)
```

Default base URL: `http://localhost:11434/v1`
Default model: `llama3.2`

## Gemini Translation Details

Gemini uses different terminology and structure:

| mux | Gemini |
|-----|--------|
| `RoleAssistant` | `"model"` |
| `RoleUser` | `"user"` |
| `ContentBlock` | `Part` |
| `ToolDefinition` | `FunctionDeclaration` |
| `tool_use` | `FunctionCall` |
| `tool_result` | `FunctionResponse` |

## OpenRouter/Ollama Notes

Both are OpenAI-compatible, so they:
- Reuse `convertOpenAIRequest` and `convertOpenAIResponse` logic
- Use `option.WithBaseURL()` to override endpoint
- OpenRouter requires API key; Ollama uses placeholder

## Testing Strategy

1. Unit tests with `httptest.Server` mocking HTTP responses
2. Conversion tests for type mapping
3. Streaming tests for event channel behavior
4. No real API calls in unit tests

---

## Implementation Plan

### Task 1: Add Gemini dependency

**Files:**
- Modify: `go.mod`

```bash
go get google.golang.org/genai
```

### Task 2: Implement Gemini client

**Files:**
- Create: `llm/gemini.go`
- Create: `llm/gemini_test.go`

Implement:
- `GeminiClient` struct
- `NewGeminiClient(apiKey, model) (*GeminiClient, error)`
- `CreateMessage` with type conversion
- `CreateMessageStream` with event translation
- Tool calling support

### Task 3: Implement OpenRouter client

**Files:**
- Create: `llm/openrouter.go`
- Create: `llm/openrouter_test.go`

Implement:
- `OpenRouterClient` struct
- `NewOpenRouterClient(apiKey, model) *OpenRouterClient`
- Reuse OpenAI conversion logic
- Base URL: `https://openrouter.ai/api/v1`

### Task 4: Implement Ollama client

**Files:**
- Create: `llm/ollama.go`
- Create: `llm/ollama_test.go`

Implement:
- `OllamaClient` struct
- `NewOllamaClient(baseURL, model) *OllamaClient`
- Reuse OpenAI conversion logic
- Default base URL: `http://localhost:11434/v1`

### Task 5: Run full test suite

```bash
go test ./... -v
golangci-lint run ./...
```

### Task 6: Update CHANGELOG and tag release

Add v0.5.0 entry with new providers.

---

## Success Criteria

- [ ] All three clients implement `llm.Client` interface
- [ ] Unit tests pass for all clients
- [ ] Tool calling works with all providers
- [ ] Streaming works with all providers
- [ ] No lint errors
- [ ] Documentation in CHANGELOG
