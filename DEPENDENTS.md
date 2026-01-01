# Projects Using mux Library

The `github.com/2389-research/mux` library provides agent orchestration, LLM clients, and tool execution.

## Dependents

### hex
- **Path:** `../hex`
- **Usage:** MuxAdapter wraps mux llm.Client for hex's Provider interface
- **Key files:** `internal/providers/mux_adapter.go`, `cmd/hex/mux_runner.go`

### jeff
- **Path:** `../jeff`
- **Usage:** Backend wrapper around mux agent for event streaming
- **Key files:** `internal/mux/backend.go`, `internal/adapter/tool.go`

### mouse
- **Path:** `../mouse`
- **Usage:** TBD

### sysop
- **Path:** `../sysop`
- **Usage:** TBD

## Current Version

**v0.6.0** (2026-01-01)

## Key Features Available

- Multi-provider LLM support (Anthropic, OpenAI, Gemini, OpenRouter, Ollama)
- Agent orchestration with think-act loop
- Tool registry and execution
- MCP client for external tool servers
- Lifecycle hooks (v0.6.0)
- Async execution (v0.6.0)
- Transcript persistence (v0.6.0)
- Token usage tracking (v0.6.0)
- Preset agent configurations (v0.6.0)

## Updating Dependents

```bash
go get github.com/2389-research/mux@v0.6.0
```
