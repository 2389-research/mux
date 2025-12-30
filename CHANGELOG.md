# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
