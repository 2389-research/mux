# Streaming Cleanup Design

Status: approved by Doctor Biz on 2026-06-26.

## Goal

Make mux streaming effective enough for long-running provider calls while keeping provider semantics consistent between streaming and non-streaming paths.

## Scope

This work has two pieces:

- Add an explicit streaming transport option through `orchestrator.Config` and `agent.Config`.
- Move `OpenAIClient.CreateMessageStream` from Chat Completions streaming to Responses API streaming.

OpenRouter and Ollama stay on Chat Completions streaming for this pass because they are OpenAI-compatible providers, not guaranteed Responses API providers.

## Architecture

The orchestrator will keep its existing response processing flow. When streaming is enabled, it will call a mux-owned stream collector instead of `CreateMessage`. The collector drains `llm.StreamEvent` values and returns a normal `llm.Response`. After that, the existing `processResponse`, tool execution, hooks, usage accounting, and completion handling continue unchanged.

OpenAI streaming should use `client.Responses.NewStreaming(ctx, convertOpenAIResponsesRequest(req))`, matching `CreateMessage`. It should map Responses API semantic events into mux stream events and prefer the final `response.completed` payload for the final reconstructed response.

## Error Handling

The stream collector should fail on any `EventError` and on streams that close without a final `EventMessageStop` response. Provider stream implementations should emit `EventError` for typed API failures such as `error`, `response.failed`, and `response.incomplete`.

## Testing

Tests should cover:

- Orchestrator streaming mode calls `CreateMessageStream` instead of `CreateMessage`.
- Streamed text is processed through the normal orchestrator completion flow.
- Streamed tool-use responses execute tools and continue the loop.
- Streamed errors fail the run.
- Usage and stop reason survive stream aggregation.
- OpenAI streaming posts to `/responses`, not `/chat/completions`.
- OpenAI text, tool-call, completed, and error Responses stream events map into mux stream events.
