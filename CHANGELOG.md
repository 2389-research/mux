# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `llm.ProviderReplay` envelope (`ContentBlock.Replay`) preserving raw provider items — OpenAI reasoning items, message `phase`, and item IDs survive response → tool history → persistence → the next Responses request byte-for-byte; replayed items are emitted verbatim via raw-JSON overrides. Requests carrying a replay envelope for a different provider/model are rejected pre-flight with `*llm.ErrReplayMismatch`. OpenAI Responses calls now request `reasoning.encrypted_content` on reasoning-capable models (the o1/o3/o4/gpt-5/codex families) so reasoning items remain replayable across turns.
- New `llm.StopReason` values `stop_sequence`, `refusal`, `content_filter`, `pause_turn`, and `other`, covering provider-native finish reasons outside the existing `end_turn`/`tool_use`/`max_tokens` set. `pause_turn` is surfaced as-is and does not trigger automatic continuation; callers decide how to handle it.

### Breaking

- OpenAI Responses status policy, batched into this release's single breaking entry with 865b, j6kd, 2zdv, and aag7 per gotchas.md:
  - A Responses result whose status is `failed` — or any status mux does not recognize, such as `queued` — is now a typed `*llm.ErrProviderResponse` error carrying no `Response`, on both the non-streaming and streaming paths. Previously it converted to a successful `Response` with empty content and `StopReason` `other`.
  - Token-limit truncation (`incomplete`, including `max_output_tokens`) and content filtering (`incomplete` with `content_filter`) are now successful `Response`s carrying `StopReason` `max_tokens` / `content_filter` with the partial output preserved in `Content`, on both call shapes (the streaming path delivers them as the final stream response instead of an error). They are never an error path. They briefly returned an error in the unreleased PR #30 change; that was corrected before release, and the `Fixed` entry describing it has been replaced by this one.
  - Callers should branch on `Response.StopReason` to handle truncation and filtering gracefully (e.g. show the partial answer, offer to continue), and use `errors.As` with `*llm.ErrProviderResponse` to detect genuine provider failures. The partial output of a `failed` result remains discarded with the error.
- MCP transport clients (`mcp.Client`, both stdio and HTTP) are single use, and `mcp.ErrTransportClosed` is now returned from production paths for the first time:
  - `Start` on a closed client, or on one whose handshake failed, returns `ErrTransportClosed` instead of launching a second server process or session. Previously a restart reused already-closed lifecycle channels and panicked with "close of closed channel" (stdio) or silently opened a second session whose `Close` was a no-op (HTTP). Reconnecting requires a new `mcp.NewClient`.
  - A failed handshake is terminal: an HTTP client whose `notifications/initialized` is rejected no longer accepts `ListTools`/`CallTool`.
  - `Close` now fails every call in flight with `ErrTransportClosed` and cancels active HTTP requests and SSE reads, instead of leaving callers blocked until their own deadline — a `context.Background()` call used to hang forever.
  - Cancelling a call's context ends that call only. A stdio frame already handed to the transport is still written in full by the transport's writer, so a per-call timeout leaves the connection correctly framed and usable for the next call rather than killing the MCP server for the session.
- Anthropic and Gemini requests now reject a replay envelope belonging to a different provider or model, failing preflight with `*llm.ErrReplayMismatch` and zero HTTP requests. Both adapters previously ignored envelopes entirely, so a history carrying one was silently accepted and sent without it. Only signature-bearing blocks are stamped — Anthropic `thinking` and `redacted_thinking`, Gemini parts with a `thoughtSignature` — so a history with no thinking in it still switches models freely, and caller edits to assistant text or tool input still reach the wire. Editing tool input is not free, though: Anthropic invalidates the signature on every thinking block after a changed `tool_use`, so an edit mid-conversation means dropping the thinking blocks that follow it. Callers that switch models mid-conversation should likewise drop thinking blocks from the history, or use `errors.As` with `*llm.ErrReplayMismatch` to detect the rejection.

### Fixed
- Anthropic extended thinking survives tool turns: `thinking` blocks keep their `signature` and `redacted_thinking` blocks keep their encrypted `data` through response conversion, history JSON, and the next request, on both the non-streaming and streaming paths. Streaming accumulates `signature_delta` fragments without routing them into displayed text. Both ride the existing `ProviderReplay` envelope, so replayed blocks reach the API byte-for-byte. A stream cut off before its signature arrives leaves the block unstamped rather than sending an invented signature.
- Gemini thought signatures survive replayed function calls: a part carrying a `thoughtSignature` is preserved whole in a `ProviderReplay` envelope and sent back unmodified, instead of being rebuilt as a fresh function-call part with the signature dropped (which Gemini answers with HTTP 400). Parallel calls keep their per-part signatures, and parts without one stay on the normalized path.
- Gemini replay payloads are validated against the SDK's own `genai.Part` struct, so a payload carrying a top-level field no part declares is rejected pre-flight instead of decoding into an empty part and shipping as `{}`. The check is top-level only: a part-shaped payload holding nothing but zero values (`{"text":""}`) still passes and still ships as `{}`, and a bogus key nested inside a field with its own unmarshaler is not caught. Neither is reachable from mux's own output.
- OpenAI non-streaming responses now derive `StopReason` from the Responses API `status` and `incomplete_details.reason` (previously every response defaulted to `end_turn`); refusal output content overrides a completed response to `refusal`.
- Chat Completions and Gemini responses with an empty choice/candidate list now report `StopReason` `other` instead of an empty string.
- Inline PDF content blocks now serialize `file_data` as a `data:application/pdf;base64,...` URL on both the OpenAI Responses and Chat Completions paths; previously they sent a bare base64 string, which matches neither the OpenAI file-inputs contract nor OpenRouter's PDF contract. `input_audio.data` is unaffected and remains raw base64.

## [0.9.0] - 2026-06-26

### Added
- `Stream` option on `orchestrator.Config` and `agent.Config` to route LLM calls through provider streaming APIs while preserving the normal final response flow.
- Orchestrator stream collector that drains `llm.StreamEvent` values into a final `llm.Response` for existing tool execution, hooks, usage accounting, and completion handling.

### Changed
- OpenAI streaming now uses the Responses API, matching the non-streaming OpenAI transport and preserving Responses API semantics for text, function calls, usage, and typed stream errors.

### Fixed
- Long-running Anthropic workflows can opt into streaming transport instead of forcing lower reasoning budgets to avoid provider non-streaming duration limits.

## [0.6.0] - 2026-01-01

### Added
- Lifecycle hooks system (`hooks` package) for observability and control
  - `SessionStart`/`SessionEnd` hooks for session lifecycle
  - `Stop` hook with ability to continue execution (`event.Continue = true`)
  - `SubagentStart`/`SubagentStop` hooks for child agent lifecycle
- `HookManager` field in `orchestrator.Config` and `agent.Config`
- `SessionID()` and `Hooks()` methods on orchestrator
- `Hooks()` and `RunChild()` methods on agent
- Hook manager inheritance for child agents
- Background agent execution (`RunAsync`, `ContinueAsync`, `RunChildAsync`)
  - `RunHandle` for status tracking, polling, and waiting
  - `WaitWithTimeout` for bounded waits
  - `Cancel` for cooperative cancellation
- Transcript persistence for agent resume
  - `Transcript` type for serializable conversation history
  - JSON and JSONL format support
  - `SaveTranscript`/`RestoreTranscript` on agent
  - File-based save/load utilities
- Token usage tracking
  - `TokenUsage` type tracks input/output/cache tokens
  - `Usage()` and `ResetUsage()` on orchestrator and agent
  - Thread-safe accumulation across requests
  - Snapshot for point-in-time usage data
- Preset agent configurations for common patterns
  - `ExplorerPreset` - codebase exploration with read-only tools
  - `PlannerPreset` - architecture and planning
  - `ResearcherPreset` - multi-source research with web access
  - `WriterPreset` - code implementation
  - `ReviewerPreset` - code review
  - `SpawnExplorer`, `SpawnPlanner`, etc. convenience methods
  - Fluent API for customizing presets (`WithName`, `WithMaxIterations`, etc.)

## [0.5.1] - 2026-01-01

### Fixed
- Gemini tool calling now works correctly - tool results include required `Name` field
- Defensive nil check for tool results prevents panic if tool returns `(nil, nil)`

### Added
- Six new scenario tests for LLM provider tool calling and subagent hierarchies

## [0.5.0] - 2025-12-30

### Added
- Gemini LLM client using official `google.golang.org/genai` SDK
- OpenRouter client with OpenAI-compatible API (`llm.NewOpenRouterClient`)
- Ollama client for local LLM inference (`llm.NewOllamaClient`)

### Changed
- All three new clients implement the existing `llm.Client` interface
- Tool calling and streaming supported on all new providers

## [0.4.0] - 2025-12-30

### Added
- Streamable HTTP transport for MCP client (2025-06-18 spec)
- `Client` interface for transport abstraction
- SSE (Server-Sent Events) response parsing
- `Notification` type for server-initiated messages
- `Notifications()` method returns channel for async server messages
- Session management via `Mcp-Session-Id` header
- Sentinel errors: `ErrSessionExpired`, `ErrNotConnected`, `ErrTransportClosed`
- HTTP config fields: `URL` and `Headers` in `ServerConfig`

### Changed
- **BREAKING**: `NewClient` now returns `(Client, error)` instead of `*Client`
- Refactored stdio transport to implement new `Client` interface

## [0.3.0] - 2025-12-29

### Added
- `Continue()` method for opt-in multi-turn conversations that preserve history
- `Messages()` to get current conversation history
- `SetMessages()` to restore conversation state from persistence
- `ClearMessages()` to reset conversation history
- Shared `runLoop()` internal method to reduce code duplication

### Unchanged
- `Run()` behavior remains fresh-start (backwards compatible with v0.2.x)

## [0.2.3] - 2025-12-29

### Fixed
- Orchestrator reuse for multiple `Run()` calls - `EventBus.Reset()` now properly reopens the bus instead of permanently closing it

## [0.2.2] - 2025-12-29

### Fixed
- OpenAI client now uses `max_completion_tokens` instead of `max_tokens` for GPT-5.x models
- MCP client now inherits environment variables from parent process before applying custom env
- Added warning when tools lack `InputSchema` (LLM may not call them correctly)

### Changed
- Example tools in `examples/full` now perform real file I/O instead of simulations

## [0.2.1] - 2025-12-27

### Fixed
- Race condition in orchestrator with concurrent `Run()` calls (added mutex)
- Various lint and test coverage improvements

## [0.2.0] - 2025-12-26

### Added
- OpenAI client implementation (`llm.NewOpenAIClient`)
- Makefile for building examples
- Scenario test specifications

### Changed
- Improved test coverage across all packages

## [0.1.0] - 2025-12-13

### Added
- Initial release
- Tool execution framework with `tool.Tool` interface
- `tool.Registry` for tool management
- `tool.FilteredRegistry` for access control
- MCP client for Model Context Protocol servers
- `mcp.ToolAdapter` to bridge MCP tools to native interface
- Anthropic LLM client
- Orchestrator with think-act loop state machine
- Event bus for streaming responses
- Agent wrapper for simplified usage
- Coordinator for multi-agent workflows
- Permission checker interface
