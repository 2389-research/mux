# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `tool.ExecuteOption` and `tool.WithApprovalDecision` give `tool.Executor.Execute` a per-call approval answer that is not executor-wide state. A resumed batch (`orchestrator.Resume`) now hands each call its own `Decision` answer this way instead of rebinding the executor's approval func around the call: identity travels with the call, so a tool that calls back into the same executor during its own `Execute` — a compose- or sub-agent-style tool running nested tool calls — resolves each nested call against the caller's approval func and the nested call's own name and params rather than inheriting the outer call's decision (mux#ts7d). The option is additive: `Execute`'s first three parameters and `tool.ApprovalFunc` are unchanged, so existing callers and `agent.Config.ApprovalFunc` keep compiling. A nested call with no override and no configured approval func is denied with `tool.ErrApprovalRequired`, an explicit signal, where before it could silently ride along on the outer call's approval.
- `llm.ProviderReplay` envelope (`ContentBlock.Replay`) preserving raw provider items — OpenAI reasoning items, message `phase`, and item IDs survive response → tool history → persistence → the next Responses request byte-for-byte; replayed items are emitted verbatim via raw-JSON overrides. A replay envelope belonging to a different provider/model is dropped with a warning on stderr instead of being sent. OpenAI Responses calls now request `reasoning.encrypted_content` on reasoning-capable models (the o1/o3/o4/gpt-5/codex families) so reasoning items remain replayable across turns.
- New `llm.StopReason` values `stop_sequence`, `refusal`, `content_filter`, `pause_turn`, and `other`, covering provider-native finish reasons outside the existing `end_turn`/`tool_use`/`max_tokens` set. `pause_turn` is surfaced as-is and does not trigger automatic continuation; callers decide how to handle it.
- `orchestrator.ToolCancelledText`, `orchestrator.ReasonCancelled`, and `orchestrator.CheckpointError` — the three names the cancelled-batch and failed-checkpoint paths now surface to callers (see `Breaking`, below, for the behavior each one describes). `ToolCancelledText` is the `tool_result` text the orchestrator synthesizes for a call a cancellation cut off, and is also how a resumed batch tells a call that still needs to run from one that already ran; a tool must not return it verbatim. `ReasonCancelled` (`execution.cancelled`) is the `Suspension.Reason` of the snapshot a cancelled batch writes. `CheckpointError` names every call whose result a failed post-batch checkpoint left in memory only: the durable snapshot predates the batch, so resuming from the store will dispatch those calls a second time, and the orchestrator reports them rather than choosing for the caller — it cannot tell a lost result from a call that never ran. Callers that cannot tolerate a repeated side effect should persist `Messages()` themselves, or check the effect, before resuming.
- New `recording` package: durable-recording value types (`Record`, `Snapshot`, `Recorder`, `Config`, `RecoveryPlan`, `CheckpointState` and its nested state types, `CommittedEnvelope`, `CommittedCheckpoint`, `RestoreInput`, `RestoredState`, 16 typed event payloads) and a canonical `mux-json/1` codec (`EncodePayload`, `EncodeRecord`, `DecodeRecord`, `RecordSHA256`, `ValidateRecord`, `ValidatePayload`): lexical map-key sorting that recurses into payload sub-documents, duplicate-key and unpaired-UTF-16-surrogate rejection, `encoding/json`-default HTML escaping, no trailing newline, and numeric-literal-preserving round-trips via `json.Number`/`UseNumber`. Implements Schema amendment 1's `retry_of_tool_call_id` (string, 1-512 — deliberately not the package's usual 1-160 ID bound), `evidence_ref`, and `result_event_id` fields. Golden byte and SHA-256 fixtures are persisted under `recording/testdata/golden/` so host recorder tests can check against the same bytes without reimplementing the encoder; they cover only the codec itself, encoding and decoding values already in memory as Go types or `json.RawMessage` — including a 17-digit integer beyond float64's exact-integer range round-tripping digit-for-digit. They do not cover the earlier boundary where tool arguments first reach `recording`: `llm.ContentBlock.Input map[string]any` was populated by `json.Unmarshal` call sites in `llm/anthropic.go` and `llm/openai.go` that did not use `UseNumber`, so a large integer could lose precision before it ever reached this codec. That earlier boundary is closed by kata s69y, in the `Fixed` entry below; this bullet records the codec's own guarantee, which was already correct. `RestoreInput`/`RestoredState`/`CommittedCheckpoint` declare the full input/output contract for a future `Restore(RestoreInput) (RestoredState, error)`, including `Clone()` methods that preserve the nil-versus-non-nil-empty distinction on every slice field (`HostKinds`, `Tail`, `RawTail`) — an empty `HostKinds` is a host-kind allowlist that accepts nothing, not "no allowlist". `Restore` itself is implemented separately (kata 9cg6); these are declarations only.
- Anthropic streaming now attaches typed block identity to `llm.StreamEvent`: `BlockID` is a stream-local `"anthropic:<index>"` key stable across one block's start/delta*/stop sequence, `DeltaKind` (`StreamDeltaText`, `StreamDeltaThinking`, `StreamDeltaToolInput`) says how to interpret a `content_block_delta`'s `Text`, and the terminal `message_stop` event carries `FinalBlocks []StreamBlockRef` mapping every `BlockID` to its index in the finished `Response.Content`. A stream that violates the Anthropic protocol — a block started twice, a delta or stop for a block never started or already stopped, an unrecognized delta variant, `message_stop` reached with a block still open or with no preceding `message_start`, or the connection ending before any `message_stop` — now ends with a typed `*llm.StreamProtocolError` (`errors.As`-detectable, `Reason` fixed and sanitized, never raw provider content) instead of misbehaving silently: a duplicate start used to overwrite the previous block outright, a delta for an absent block was dropped, and a stream cut short closed the event channel with no signal anything was wrong. The new fields are additive: a metadata-free legacy provider client leaves them at their zero value.

### Breaking

- OpenAI Responses status policy, batched with 865b, j6kd, 2zdv, and aag7 into one breaking entry per gotchas.md:
  - A Responses result whose status is `failed` — or any status mux does not recognize, such as `queued` — is now a typed `*llm.ErrProviderResponse` error carrying no `Response`, on both the non-streaming and streaming paths. Previously it converted to a successful `Response` with empty content and `StopReason` `other`.
  - Token-limit truncation (`incomplete`, including `max_output_tokens`) and content filtering (`incomplete` with `content_filter`) are now successful `Response`s carrying `StopReason` `max_tokens` / `content_filter` with the partial output preserved in `Content`, on both call shapes (the streaming path delivers them as the final stream response instead of an error). They are never an error path. They briefly returned an error in the unreleased PR #30 change; that was corrected before release, and the `Fixed` entry describing it has been replaced by this one.
  - Callers should branch on `Response.StopReason` to handle truncation and filtering gracefully (e.g. show the partial answer, offer to continue), and use `errors.As` with `*llm.ErrProviderResponse` to detect genuine provider failures. The partial output of a `failed` result remains discarded with the error.
- MCP transport clients (`mcp.Client`, both stdio and HTTP) are single use, and `mcp.ErrTransportClosed` is now returned from production paths for the first time:
  - `Start` on a closed client, or on one whose handshake failed, returns `ErrTransportClosed` instead of launching a second server process or session. Previously a restart reused already-closed lifecycle channels and panicked with "close of closed channel" (stdio) or silently opened a second session whose `Close` was a no-op (HTTP). Reconnecting requires a new `mcp.NewClient`.
  - A failed handshake is terminal: an HTTP client whose `notifications/initialized` is rejected no longer accepts `ListTools`/`CallTool`.
  - `Close` now fails every call in flight with `ErrTransportClosed` and cancels active HTTP requests and SSE reads, instead of leaving callers blocked until their own deadline — a `context.Background()` call used to hang forever.
  - Cancelling a call's context ends that call only. A stdio frame already handed to the transport is still written in full by the transport's writer, so a per-call timeout leaves the connection correctly framed and usable for the next call rather than killing the MCP server for the session.
- Anthropic and Gemini requests now honor a replay envelope instead of ignoring it, and an envelope belonging to a different provider or model is dropped with a warning on stderr naming both identities rather than failing the request (9jn5). Only signature-bearing blocks are stamped — Anthropic `thinking` and `redacted_thinking`, Gemini parts with a `thoughtSignature` — so a history with no thinking in it still switches models freely, and caller edits to assistant text or tool input still reach the wire. Dropping an envelope costs that block its signature: a block with a normalized form, such as a tool call, still reaches the wire without it, while a thinking or provider-item-only block contributes nothing to the request at all. Editing tool input is not free either: Anthropic invalidates the signature on every thinking block after a changed `tool_use`, so an edit mid-conversation means dropping the thinking blocks that follow it. Structural problems in a payload — empty data, invalid JSON, no type discriminator — have no safe normalized fallback and do still fail preflight with zero HTTP requests.
- `agent.Transcript.SaveToFile` and `SaveToFileJSONL` now write at mode `0600` (owner read/write only) instead of following the process umask (typically `0644`), and replace the destination atomically instead of truncating it in place. The mode is a side effect of fixing kata jh1c — a serialization or write failure no longer destroys the previous transcript — via `os.CreateTemp`, whose temp files are created at `0600`; that mode was kept rather than widened back to the prior umask-derived value. Kata 0xem holds the formal owner-only-permissions approval gate for this file family and remains open; this entry records the change so it stays visible and reversible pending that decision.
- `mcp.ToolAdapter.Execute`'s `Output` field on a failed call is now empty instead of duplicating the error text (5ez6):
  - A failed MCP tool call previously put the server's error text in both `Result.Output` and `Result.Error`. `Output` is now empty on failure; the text lives only in `Error`.
  - Callers that read `Output` directly off a failed `mcp.ToolAdapter.Execute` result will see `""` where they used to see the server's error text. Read `Result.ModelText()` instead — it returns `Output` when set, otherwise `Error` on failure, so it renders the same text regardless of which field carries it.
  - No in-tree consumer reads `Output` on a failed MCP result, so this does not break anything in this repo, but mux has external Go consumers outside this tree that may.
- Minimum supported Go version: 1.24 → 1.25.0 (frc3). The `grpc`, `x/text`, and `x/net` versions patched under `Security`, below, each declare `go 1.25.0` in their own go.mod; building or consuming this module now requires Go ≥ 1.25.0.
- A tool batch cut short by context cancellation now leaves valid history and a resumable session, and a resumed batch never re-runs a call that already ran (yhen):
  - `orchestrator.executeTools` used to return `ctx.Err()` as soon as it noticed cancellation, discarding every result it had already collected. The assistant `tool_use` message was already in history, so the turn was left holding tool calls with no `tool_result` for any of them — a shape every provider rejects as malformed, and one callers had to repair by hand through `SetMessages` before the session could continue. The orchestrator now appends exactly one `tool_result` per call on every exit path: the real results of the calls that ran, plus a synthesized `IsError` result carrying `orchestrator.ToolCancelledText` for each call the cancellation cut off. The model therefore sees a cancelled batch where it previously saw an abandoned turn, so callers whose prompts or evals assume a cancelled turn leaves history untouched will see that extra user message. `Run`, `Continue`, and `Resume` still return the cancellation and `errors.Is(err, context.Canceled)` still holds.
  - With a `SessionStore` configured, a cancelled batch now also writes a `StatusSuspended` snapshot whose `Suspension.Reason` is `orchestrator.ReasonCancelled` and whose `Pending` lists only the calls that never ran. The store previously kept the pre-batch snapshot, so a session reloaded after a cancellation could only restart the entire batch. The write deliberately runs on a `context.WithoutCancel` copy of the cancelled context: the cancellation is exactly what makes the record worth keeping.
  - `orchestrator.Resume` no longer re-dispatches a call whose `tool_result` is already in the snapshot. It carries the recorded result into the replayed turn and runs only the calls still owed, rewriting the batch's single results message rather than appending a second set. Retrying a `Resume` that had been cancelled part-way through previously executed every tool in the batch again, side effects included. Completion is read out of the messages themselves — there is no parallel list that can disagree with history — so a call counts as still owed exactly when its result is absent or carries `ToolCancelledText`. A tool whose own output is that string verbatim would be run a second time; tools must not return it.
  - The `StatusRunning` checkpoint taken after a tool batch, on both the `Run`/`Continue` and `Resume` paths, now fails with a typed `*orchestrator.CheckpointError` instead of `fmt.Errorf("checkpoint failed: %w", err)`. Callers matching on the old message text must switch to `errors.As`; `errors.Is` against the store's own error still works through `Unwrap`. The orchestrator does not retry or re-run those calls — it names them, because a lost result and a call that never ran are indistinguishable from here.
- Anthropic streaming ends with a `*llm.StreamProtocolError` instead of completing normally when a `tool_use` block's arguments cannot be recovered as one JSON object: truncated mid-argument by `max_tokens`, malformed, `null`, a JSON array, or a block whose `content_block_start` declared no `input` and which then received no `input_json_delta` at all. Previously the block was silently dropped from the finished `Response` (truncated or malformed JSON, logged only to stderr) or given an empty `map[string]any` (the no-information case), and the stream still completed successfully with `EventMessageStop` either way. Callers that treated a completed stream as proof the answer was whole, or a missing tool block as simply "no call this turn," now need to also handle the error event; use `errors.As` with `*llm.StreamProtocolError` to detect it. A `tool_use` block whose `content_block_start` declares an explicit empty `input: {}` and receives no deltas is unaffected — that is a genuine zero-argument call and still finishes with `Input` set to an empty map.
- Anthropic streaming no longer forwards a thinking block's `signature_delta` fragments as a public `EventContentDelta`. Previously every delta type reached the channel unconditionally; a `signature_delta` event's `Text` was always empty, since a signature is not display content, but the event itself still arrived and still carried `Index`. The fragments are still accumulated into the block's replay envelope, so the signature itself is unaffected. No in-tree consumer reads `EventContentDelta` for this case — the orchestrator's stream collector only inspects `EventError` and `EventMessageStop` — but mux has external Go consumers outside this tree that may count or log every delta event during an extended-thinking response and will now see one fewer per accumulated signature fragment.
- Numbers in `llm.ContentBlock.Input` now decode as `json.Number` instead of `float64` on every provider response path and on every history round-trip (s69y). The field type is unchanged (`map[string]any`), so no signature breaks, but the dynamic type of a numeric input value is now `json.Number`. A tool that reads its arguments with `p["a"].(float64)` — as `examples/minimal` did — will get the zero value and a failed type assertion instead; switch to `json.Number` and call `Float64()`, or accept both. Decoding with `json.Number` is what makes an integer past 2^53 survive: previously a wire input of `10000000000000001` came back as `10000000000000000` on the next round-trip, silently invalidating the signature on every Anthropic thinking block after a changed `tool_use` input. Gemini is the one provider still unaffected either way: the genai SDK decodes its function-call args to `float64` before mux sees them, so a large integer in a Gemini call can still round.

### Fixed
- Nested tool calls no longer inherit the outer call's approval decision during a resumed batch (mux#ts7d). `orchestrator.executeTools` bound a resumed `Decision` to each call by assigning `tool.Executor.SetApprovalFunc` immediately before running it, and the approval func was executor-wide state: a tool whose own `Execute` called back into the same executor saw the outer call's answer, so an approved outer call approved every nested call it made regardless of the nested call's own identity. The orchestrator now passes the answer as a per-call `tool.WithApprovalDecision` option, and a nested `Execute` falls through to the caller's approval func with its own tool and params. This is the residual case that mux#t0qf deliberately left alone — t0qf removed the positional queue but kept the binding ambient — and it is fixed without the `tool.ApprovalFunc` signature widening that would have needed separate approval.
- Anthropic extended thinking survives tool turns: `thinking` blocks keep their `signature` and `redacted_thinking` blocks keep their encrypted `data` through response conversion, history JSON, and the next request, on both the non-streaming and streaming paths. Streaming accumulates `signature_delta` fragments without routing them into displayed text. Both ride the existing `ProviderReplay` envelope, so replayed blocks reach the API byte-for-byte. A stream cut off before its signature arrives leaves the block unstamped rather than sending an invented signature.
- Gemini thought signatures survive replayed function calls: a part carrying a `thoughtSignature` is preserved whole in a `ProviderReplay` envelope and sent back unmodified, instead of being rebuilt as a fresh function-call part with the signature dropped (which Gemini answers with HTTP 400). Parallel calls keep their per-part signatures, and parts without one stay on the normalized path.
- Gemini replay payloads are validated against the SDK's own `genai.Part` struct, so a payload carrying a top-level field no part declares is rejected pre-flight instead of decoding into an empty part and shipping as `{}`. The check is top-level only: a part-shaped payload holding nothing but zero values (`{"text":""}`) still passes and still ships as `{}`, and a bogus key nested inside a field with its own unmarshaler is not caught. Neither is reachable from mux's own output.
- OpenAI non-streaming responses now derive `StopReason` from the Responses API `status` and `incomplete_details.reason` (previously every response defaulted to `end_turn`); refusal output content overrides a completed response to `refusal`.
- Chat Completions and Gemini responses with an empty choice/candidate list now report `StopReason` `other` instead of an empty string.
- Gemini prompts blocked before generation — `PromptFeedback.BlockReason` set for safety, blocklist, or prohibited content — now return a typed `*llm.ErrProviderResponse` (`Reason` is `"blocked: "` plus Gemini's raw reason) instead of an empty successful `Response`, on both `CreateMessage` and `CreateMessageStream` (mux#a2j0). `convertGeminiResponse` previously returned an empty `Response` with `StopReason` `other` as soon as `Candidates` was empty and never read `PromptFeedback` at all, so a caller could not tell a model with nothing left to say from a provider that refused the prompt, and `RetryClient` had nothing to key on. Streaming checks every chunk for a block reason as it arrives rather than only the final one, so an ordinary trailing usage-only chunk is never mistaken for a block. `RetryClient` already treats the new error as non-retryable through its existing default fallback; a regression test pins that rather than adding retry logic. `mapGeminiStopReason` also gains the three image-generation finish reasons genai v1.54.0 defines but the existing `content_filter` case omitted — `IMAGE_SAFETY`, `IMAGE_PROHIBITED_CONTENT`, `IMAGE_RECITATION` — which previously fell through to `other` with every other unmapped reason. A response with zero candidates and no `PromptFeedback` still reports `StopReason` `other` unchanged, per the entry above, and `MAX_TOKENS` truncation already preserved partial text before this change; both are now pinned by regression tests.
- Inline PDF content blocks now serialize `file_data` as a `data:application/pdf;base64,...` URL on both the OpenAI Responses and Chat Completions paths; previously they sent a bare base64 string, which matches neither the OpenAI file-inputs contract nor OpenRouter's PDF contract. `input_audio.data` is unaffected and remains raw base64.
- JSONL transcripts holding an entry larger than 64 KiB now load back. `agent.LoadJSONL` used `bufio.Scanner`'s default line limit, so a transcript carrying a large replay envelope or extended-thinking block saved without error and then failed to load with `bufio.Scanner: token too long` — an agent that could not be resumed from an intact file. The ceiling is now `agent.MaxTranscriptLineBytes` (16 MiB), matching what the MCP stdio transport already used for the same reason.
- Tool results that fail with a nil Go error (`Result{Success: false, Error: ..., Output: ""}`, as `tool.NewErrorResult` produces) now surface their `Error` text in the `tool_result` block sent to the model, instead of an empty string. `orchestrator.go`'s `executeTools` now reads tool output through the new `tool.Result.ModelText()` accessor (`Output` when set, else `Error` on failure) rather than `Output` alone. The MCP tool adapter (`mcp.ToolAdapter.Execute`) no longer duplicates a failing call's text into both `Output` and `Error` as a workaround for this bug — `Output` is now empty on failure and the text lives only in `Error`, matching the `ModelText` contract; code reading `Output` directly from a failed MCP `Result` will observe this change. See mux#jstq for related work preserving structured/multimodal MCP results.
- Approval decisions handed to `orchestrator.Resume` now bind to the tool call ID they name rather than to callback order. `Decision.Approvals` is documented as keyed by `PendingToolCall.ID`, but the replayed batch ran under a single approval func that answered each callback from the next slot in a queue of approval-required IDs. A call that failed before reaching its approval check never consumed its slot — a tool unregistered between suspend and resume is enough, since `tool.Executor.Execute` rejects it with `tool.ErrToolNotFound` before any approval runs — so every later decision shifted by one and an explicitly denied call could execute on an approved call's decision. The orchestrator now rebinds the executor's approval func to the exact `llm.ContentBlock.ID` of each call immediately before that call runs, and hands the caller's own approval func back once the batch ends, so a decision can only ever answer the call it was made for; two calls to the same tool name in one batch still resolve against their own IDs (`tool.WithApprovalDecision`, in the `Added` entry above, is that mechanism as of mux#ts7d). `tool.ApprovalFunc` keeps its signature, and callers that key `Approvals` by the IDs in `Suspension.Pending` need change nothing.
- Provider stream producers (OpenAI, Anthropic, Gemini, Ollama, OpenRouter) no longer leak a goroutine, HTTP connection, and SDK stream when a caller cancels `CreateMessageStream`'s context and stops reading its channel (mgpj). Every event send now goes through one cancellable helper, `sendStreamEvent`, which abandons a send blocked on the channel's full 100-event buffer as soon as the context is done. Previously, cancellation only stopped a producer still decoding from the SDK stream; once a send blocked because the caller had stopped draining the channel, nothing unblocked it, and the goroutine ran forever holding its HTTP response body and SDK stream open. `defer stream.Close()` was added to the OpenAI, Anthropic, Ollama, and OpenRouter producers so the SDK stream handle is always released on exit; Gemini's range-over-func iterator already closes its response body on an early return, so it needed no separate close call. A caller that cancels a stream and stops reading is no longer guaranteed a terminal or error event — the stream just ends — but a caller that drains the channel to completion sees no change in behavior.
- Transcript entries now preserve `Message.Content` as well as `Message.Blocks` (mux#bq4e). `agent.TranscriptEntry` gained a `Text` field (JSON key `text`, `omitempty`), wired through `FromMessages`, `Append`, `ToMessages`, and both the JSON and JSONL codecs' entry shapes. Previously only `Blocks` was captured, so any message built with `llm.NewUserMessage` or `NewAssistantMessage` — the constructors behind every ordinary `Agent.Run`/`Continue` prompt — silently lost its text across `SaveTranscript` → serialize → `RestoreTranscript`: the message stayed in the restored history, just empty, which a length-only check on the restored slice could not catch. A message that sets both `Content` and `Blocks` round-trips each independently; neither is folded into the other. Transcripts saved before this change carry no `text` key and still load, with `Content` on those entries restoring empty exactly as it did before — no migration needed, no format-version bump.
- Anthropic requests now forward `Request.Temperature` to the API (mux#29jh): `convertRequest` built `anthropic.MessageNewParams` without ever reading `Temperature`, so an explicit sampling temperature — including an explicit `0` — was silently discarded and every Anthropic call ran at the provider's own default regardless of what the caller asked for. `Temperature` is now set via `param.NewOpt` whenever it is non-nil, so `nil` still omits the field from the wire request and an explicit `0` is sent as `"temperature":0` rather than being mistaken for unset (the other two providers that already honor this field, OpenAI and Gemini, were unaffected). An explicit non-`1` `Temperature` combined with enabled manual `Thinking` now fails preflight with a typed `*llm.ErrIncompatibleThinking` and zero HTTP requests, rather than silently discarding the field or reaching the API and failing there — Anthropic documents temperature as incompatible with extended thinking (https://platform.claude.com/docs/en/build-with-claude/thinking#limits-and-feature-compatibility). `Temperature` left `nil`, or set to `1` (the API default), is unaffected and still combines with `Thinking` normally. The guard covers only `Temperature`, the one sampling parameter `Request` exposes, and only the thinking-combination case; `top_p` and `top_k` carry their own, partly different restrictions under the same doc section and are out of scope here, and so is Anthropic's separate, broader rule for current-generation models, where any non-default temperature, top_p, or top_k errors on every request regardless of whether thinking is used at all — an explicit non-default `Temperature` on an ordinary, non-thinking request against one of those models still reaches the wire today and depends on the live API to reject it. Callers using `errors.As` to detect provider-side rejections should add `*llm.ErrIncompatibleThinking` alongside the existing thinking-related error types.
- Gemini streaming responses now carry every chunk instead of only the last one (ac2b). `CreateMessageStream` converted the final chunk alone into the `EventMessageStop` response, so a two-chunk answer arrived as its second half, and a function call followed by Gemini's usage-only trailer vanished outright: that trailer carries no candidate content, so the final response held no blocks at all. The orchestrator persists that response as history, so the same turn that worked unstreamed recorded an empty assistant message and never ran the call. Chunks are now folded in as they arrive — blocks keep arrival order and are never merged, usage replaces prior usage only when a chunk carries it, and a finish reason is recorded only when a chunk states one, since the zero value means "not stopped yet". Streamed and whole responses share one part-to-block mapping, so thought signatures, thinking blocks, and parallel tool calls mean the same thing on both paths.
- Integers in a tool-call input no longer round on a history round-trip (s69y). `llm.ContentBlock.Input` is `map[string]any`, so `encoding/json`'s default decode turned every JSON number into a `float64`; a wire input of `10000000000000001` rebuilt as `10000000000000000`. That is not replay-specific and not Anthropic-specific — it affected every provider on every round-trip through mux. Every decode path mux owns that builds a `ContentBlock.Input` now decodes with `json.Decoder.UseNumber`, so the number keeps its literal text as a `json.Number` and reaches the next request digit-for-digit: the Anthropic non-streaming conversion and streamed `input_json_delta` accumulation; the OpenAI Responses and Chat Completions responses (the latter shared with Ollama and OpenRouter) and their streaming tool-call paths; the Ollama and OpenRouter stream accumulators; and the two persistence boundaries that reload history, `session.FileStore.Load` and the `agent` transcript JSON/JSONL codecs (`llm.ContentBlock` gained an `UnmarshalJSON` that decodes `Input` with `UseNumber`, keeping every other field's behavior). The outgoing direction needed a matching fix: the Anthropic SDK shims `encoding/json` and renders a stdlib `json.Number` as a quoted string, so a tool input is now handed to it as a `json.RawMessage`, which every SDK passes through verbatim. The `Fixed` entry that named this gap was `recording`'s; that codec already used `UseNumber` and was never the losing boundary.

### Security
- Updated `google.golang.org/grpc` v1.66.2 → v1.83.1, `golang.org/x/text` v0.27.0 → v0.39.0, and `golang.org/x/net` v0.41.0 → v0.55.0, resolving govulncheck's reachable findings GO-2026-6348 and GO-2026-6061 (grpc), GO-2026-5970 (x/text), and GO-2026-5026 and GO-2026-4918 (x/net). Reachability ran through mux's own MCP client code (`mcp/stdio.go`, `mcp/http.go`) calling into the dependency graph; these are static reachability findings, not confirmed exploits. The patched versions each declare `go 1.25.0` in their own go.mod, forcing this module's minimum Go version up to match — see `Breaking`, above, for what that means for callers. GO-2026-5026 and GO-2026-4918 also have separate fixes in the Go standard library itself (`net/http/internal/http2`): GO-2026-5026 is fixed at 1.25.13 and 1.26.6, GO-2026-4918 at 1.25.10 and 1.26.3. `go.mod`'s `toolchain` directive is bumped from `go1.24.11` to `go1.26.6` — the higher of the two 1.26-line thresholds, so it clears both — so this repository's default builds and the toolchain-pinned CI jobs run a standard library past both. The `test-floor` job is the deliberate exception: it pins `GOTOOLCHAIN=local` at `go1.25.0` to prove the declared floor still builds, and `go1.25.0` is below both 1.25-line thresholds, so that job compiles against a standard library vulnerable to GO-2026-5026 and GO-2026-4918. No gate here can scan it either way: govulncheck v1.8.0 itself requires Go >= 1.26 to run, so `make vulncheck` always executes under the pinned 1.26.6 and never sees the floor's standard library. The floor tracks what the patched dependencies themselves declare (`go 1.25.0`); raising it past 1.25.13 to clear both stdlib advisories would raise mux's minimum for every consumer. That pin is a property of this main module only: it is not inherited by anything that requires mux as a dependency (verified empirically — a consumer module has no way to see it), which stays on whatever toolchain it itself builds with and remains independently exposed to both stdlib CVEs until it upgrades past those thresholds on its own. Build, vet, lint, and the race and integration test suites pass under the pinned toolchain (1.26.6), and `test-floor` builds, vets, and race-tests the module at the floor on every push and pull request. govulncheck is clean under the pinned toolchain.
- Added a pinned `govulncheck` gate (`make vulncheck`, scanner version `v1.8.0` tracked in one Makefile variable) as its own CI job, run on every push and pull request. The one remaining imported-but-unreachable finding, GO-2026-6443 in `grpc`, is left unpatched: `grpc` v1.83.2 fixes it but forces `x/net`, `x/sync`, `x/sys`, `x/crypto`, and `x/text` forward with it, and mux's code does not reach the vulnerable symbol.

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
