# mux

Agentic infrastructure for Go. Tool execution, MCP integration, permission-gated approval flows, and orchestration.

## Installation

```bash
go get github.com/2389-research/mux
```

## Features

- **Tool Execution**: Define and execute tools with structured input/output handling
- **MCP Integration**: Full Model Context Protocol server and client implementations
- **Permission-Gated Approvals**: Built-in approval flows for sensitive operations
- **Orchestration**: Coordinate multi-step agentic workflows
- **Type Safety**: Leverages Go's type system for reliable tool definitions
- **Extensible**: Plugin architecture for custom tool implementations

## Status

🚧 Under active development. API subject to change.

## Testing

The canonical suite runs the race detector and the integration build tag:

```bash
make test          # go test -race -tags=integration -timeout=300s ./...
```

The integration tag matters: root-level assembled cases (an agent driving a real
MCP server subprocess) live behind `//go:build integration`, so a plain
`go test ./...` skips them. CI runs the same flags.

### Live provider checks (opt-in)

The `live` suite calls a real LLM provider and costs money, so it never runs in
`make test` or CI. Configure a provider and run the preflight, which fails
loudly when an input is missing instead of letting the tests skip silently:

```bash
MUX_LIVE_PROVIDER=openrouter MUX_LIVE_MODEL=openai/gpt-4o-mini make check-live
```

| Variable | Meaning |
| --- | --- |
| `MUX_LIVE_PROVIDER` | `anthropic`, `openai`, `gemini` or `openrouter` |
| `MUX_LIVE_MODEL` | Model id to test |
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `GEMINI_API_KEY` / `OPENROUTER_API_KEY` | Key for the selected provider |

By default `go test -tags=live ./...` skips each unconfigured check with the
missing variable names (never values). A skipped live check is not evidence of
provider compatibility.

## License

MIT
