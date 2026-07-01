# Streaming Cleanup Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add explicit orchestrator streaming mode and make OpenAI streaming use the Responses API so streaming and non-streaming semantics match.

**Architecture:** Add a streaming transport flag to orchestrator and agent configs. Add a mux stream collector that converts provider stream events into a normal `llm.Response`, then reuse the existing orchestrator response processing path. Convert OpenAI streaming from Chat Completions chunks to Responses API semantic events while leaving OpenRouter and Ollama on Chat Completions.

**Tech Stack:** Go 1.24, `github.com/openai/openai-go/v3`, `net/http/httptest`, existing mux `llm`, `orchestrator`, and `agent` packages.

---

### Task 1: Add Orchestrator Streaming Config and Collector Tests

**Files:**
- Modify: `orchestrator/orchestrator.go`
- Modify: `orchestrator/orchestrator_test.go`

**Step 1: Write failing tests**

Add focused tests in `orchestrator/orchestrator_test.go`:

- `TestOrchestratorStreamingModeUsesCreateMessageStream`
- `TestOrchestratorStreamingModeProcessesToolUse`
- `TestOrchestratorStreamingModeReturnsStreamError`
- `TestOrchestratorStreamingModeKeepsUsage`

Use the existing test style in this file, but create a purpose-specific test client that records whether `CreateMessage` or `CreateMessageStream` was called and emits real `llm.StreamEvent` values through a channel. Do not add any production mock mode.

Expected behaviors:

- With `orchestrator.Config{Stream: true}`, `Run` must call `CreateMessageStream` and must not call `CreateMessage`.
- A streamed `EventMessageStop` response with `ContentTypeText` completes normally and publishes the same complete text as the non-streaming path.
- A streamed `EventMessageStop` response with `ContentTypeToolUse` executes the registered tool and continues to the next streamed response.
- A streamed `EventError` causes `Run` to return that error.
- Final response usage from the streamed `EventMessageStop` is added to `orch.Usage()`.

**Step 2: Run tests to verify failure**

Run:

```bash
go test ./orchestrator -run 'TestOrchestratorStreamingMode' -count=1
```

Expected: fail because `Config.Stream` and streaming collection do not exist.

**Step 3: Implement minimal orchestrator support**

In `orchestrator.Config`, add:

```go
Stream bool
```

In `runLoop`, replace the direct client call with a helper:

```go
resp, err := o.createMessage(ctx, o.buildRequest())
```

Add:

```go
func (o *Orchestrator) createMessage(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if !o.config.Stream {
		return o.client.CreateMessage(ctx, req)
	}
	return collectStream(ctx, o.client, req)
}
```

Add `collectStream` in `orchestrator/orchestrator.go` or a small new `orchestrator/stream.go` file with ABOUTME comments if extracted. It should:

- call `client.CreateMessageStream(ctx, req)`
- return immediately on setup error
- drain events until channel close
- return any `EventError`
- remember the latest `EventMessageStop.Response`
- return an error if the stream closes without a final response

**Step 4: Run tests to verify pass**

Run:

```bash
go test ./orchestrator -run 'TestOrchestratorStreamingMode' -count=1
```

Expected: pass.

---

### Task 2: Surface Streaming Through Agent Config

**Files:**
- Modify: `agent/config.go`
- Modify: `agent/agent.go`
- Modify: `agent/agent_test.go`

**Step 1: Write failing test**

Add a test in `agent/agent_test.go` showing that `agent.Config{Stream: true}` reaches the internal orchestrator by running an agent with a recording stream-capable client and asserting streaming was used.

**Step 2: Run test to verify failure**

Run:

```bash
go test ./agent -run 'TestAgentConfigStream' -count=1
```

Expected: fail because `agent.Config.Stream` does not exist.

**Step 3: Implement minimal agent config wiring**

In `agent.Config`, add:

```go
// Stream makes the orchestrator use the provider streaming API.
Stream bool
```

In `Agent.init`, after building `orchConfig`, set:

```go
orchConfig.Stream = a.config.Stream
```

Ensure `SpawnChild` inheritance keeps the default zero-value behavior. Do not add special inheritance logic unless a test shows it is required.

**Step 4: Run test to verify pass**

Run:

```bash
go test ./agent -run 'TestAgentConfigStream' -count=1
```

Expected: pass.

---

### Task 3: Convert OpenAI Streaming to Responses API

**Files:**
- Modify: `llm/openai.go`
- Modify: `llm/openai_test.go`

**Step 1: Write failing tests**

Update or add OpenAI streaming tests so the local `httptest.Server` expects `/responses` and emits Responses API stream events. Add tests for:

- text stream: `response.created`, `response.output_text.delta`, `response.completed`
- tool call stream: `response.output_item.added`, `response.function_call_arguments.delta`, `response.function_call_arguments.done`, `response.completed`
- error stream: `error` or `response.failed` emits `EventError`
- request path: fail the test if the client calls `/chat/completions`

Use `Content-Type: text/event-stream` and SSE `event:` / `data:` records that match the OpenAI Responses stream event JSON shape.

**Step 2: Run tests to verify failure**

Run:

```bash
go test ./llm -run 'TestOpenAIClient_Stream' -count=1
```

Expected: fail because `CreateMessageStream` still calls Chat Completions streaming.

**Step 3: Implement Responses API streaming**

In `OpenAIClient.CreateMessageStream`:

- build params with `convertOpenAIResponsesRequest(req)`
- call `o.client.Responses.NewStreaming(ctx, params)`
- handle `responses.ResponseStreamEventUnion` event types

Map events:

- `response.created` -> `EventMessageStart`
- `response.output_text.delta` -> `EventContentDelta` with `event.Delta`
- `response.function_call_arguments.done` -> `EventContentStop` with a `ContentTypeToolUse` block, using the event call ID/name/arguments fields or the completed output item when available
- `response.completed` -> `EventMessageStop` with `convertOpenAIResponsesResponse(&event.Response)`
- `error`, `response.failed`, `response.incomplete` -> `EventError`

Prefer the final `response.completed` response for the final mux response. Avoid manually reconstructing usage or stop reason from deltas unless the final response is unavailable.

Update the file ABOUTME comments if they still describe only chat completions.

**Step 4: Run tests to verify pass**

Run:

```bash
go test ./llm -run 'TestOpenAIClient_Stream' -count=1
```

Expected: pass.

---

### Task 4: Integration Verification and Cleanup

**Files:**
- Modify only files touched by Tasks 1-3 if tests expose issues.

**Step 1: Run package tests**

Run:

```bash
go test ./orchestrator ./agent ./llm -count=1
```

Expected: pass.

**Step 2: Run full test suite**

Run:

```bash
go test -race -short ./...
```

Expected: pass with pristine output.

**Step 3: Run build**

Run:

```bash
go build ./...
```

Expected: pass.

**Step 4: Run lint**

Run:

```bash
golangci-lint run --timeout=2m
```

Expected: pass. If `golangci-lint` is not installed, state that explicitly and do not claim lint passed.

**Step 5: Inspect git diff**

Run:

```bash
git diff --stat
git diff --check
```

Expected: only planned streaming files changed; no whitespace errors.
