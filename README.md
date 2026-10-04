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
npm ci --ignore-scripts # pinned reference MCP servers; Node >=24.8
make test          # go test -race -tags=integration -timeout=300s ./...
```

The integration tag matters: root-level assembled cases (an agent driving a real
MCP server subprocess) live behind `//go:build integration`, so a plain
`go test ./...` skips them. CI runs the same flags.

The tools-only [tester-army/e2e](https://github.com/tester-army/e2e) suite
reports real MCP subprocess and filesystem contracts without AI credentials:

```bash
npm ci --ignore-scripts # Node >=24.8; pinned test dependencies only
make test-e2e     # typecheck, then seven local contract flows
```

Go must be on PATH. On this Mac, use `env -u GOROOT mise exec -- make test-e2e`
to select the installed toolchain. See [flow coverage](docs/testing.md) for the
boundary between local contracts, scripted agent integration, and live checks.

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

For bounded tools-only live tests, use an existing key and an explicit model:

```bash
MUX_LIVE_PROVIDER=openrouter MUX_LIVE_MODEL=openai/gpt-4o-mini make test-e2e-live
MUX_LIVE_PROVIDER=gemini MUX_LIVE_MODEL=gemini-2.5-flash make test-e2e-live
```

OpenRouter runs streaming, token-limit and compaction tests (up to seven
requests, at most 5,640 output tokens). Gemini runs two streaming requests,
each capped at 2,048 output tokens including a 1,024-token thinking budget.
Only synthetic markers go to the provider. Retries are disabled in e2e;
provider SDK transport retries retain their own policy. The Gemini stream
check exposed an intermittent missing finish reason; see the coverage notes.

By default `go test -tags=live ./...` skips each unconfigured check with the
missing variable names (never values). A skipped live check is not evidence of
provider compatibility.

## License

MIT
