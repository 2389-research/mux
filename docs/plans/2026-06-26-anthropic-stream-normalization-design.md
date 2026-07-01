# Anthropic Stream Normalization Design

Status: approved by Doctor Biz on 2026-06-26.

## Goal

Normalize provider streaming so Anthropic, like OpenAI, emits `EventMessageStop` with a complete `llm.Response`. This lets `orchestrator.Config.Stream` work end-to-end for Anthropic long-running calls.

## Scope

This change is limited to Anthropic streaming reconstruction and tests. It does not change the orchestrator collector contract and does not alter OpenAI, OpenRouter, Ollama, or Gemini streaming behavior.

## Design

`AnthropicClient.CreateMessageStream` should keep forwarding live stream events, but also accumulate provider events into a final mux response:

- `message_start`: initialize response ID, model, input usage, and any already-present content.
- `content_block_start`: create a response block by index for text, thinking, or tool use.
- `content_block_delta`: append text, thinking, or tool JSON fragments to the indexed block.
- `content_block_stop`: parse accumulated tool JSON into `ContentBlock.Input`.
- `message_delta`: merge stop reason and output usage.
- `message_stop`: emit `EventMessageStop` with the reconstructed response.

If tool JSON cannot be parsed, keep the existing stderr warning pattern and use an empty input map rather than dropping the tool call.

## Testing

Tests should prove:

- streamed Anthropic text yields a final response with text and usage
- streamed Anthropic tool use yields a final tool-use block with parsed input
- streamed Anthropic thinking yields a final thinking block without corrupting text/tool content
- orchestrator streaming works end-to-end against an Anthropic-style delta-only stream
