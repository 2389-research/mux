# What mux can take from Evener

Evener contains concrete implementations for several gaps identified in the
mux audits. The best next step is to adapt its provider and persistence
contracts in small pieces, proving each against mux's callers. Replacing mux's
LLM or agent layer wholesale would import a different configuration model,
runtime and public API.

Compared Evener `96973838a51caba9e37b9aa4d6b7bcac1d2fe45b` with mux
`05274308bd7e459b091e606ed21a6c10db293010` on 2026-09-11. Mux production code
is still the `6f7305e` baseline from the earlier audits. Paths prefixed with
`evener/` refer to the sibling checkout. This is implementation guidance,
not approval of a new architecture or a claim of live API compatibility.

**Kata:** added references and adoption caveats to 12 existing issues; filed
two P2 proposals under `evener-review-2026-09-11`:

- `mux#r6tc`: bounded model-facing tool output with recoverable full results.
- `mux#d1dj`: invariant-based fuzz corpus checks for replay and streaming.

## Highest-value transfers

| Order | Transfer | Evener source | Mux work |
| --- | --- | --- | --- |
| 1 | Separate display content from provider replay state | `llm/types.go:124,162,209`; `llm/providers/responses/response.go:17`; `input.go:116,246` | `e451`, alongside existing Anthropic/Gemini signature repairs |
| 2 | Resolve provider identity/capabilities once, then shape requests consistently | `llm/registry/types.go:80,98,185`; `llm/registry_shape.go:75`; `llm/client.go:293` | `zee0`, `1g9g` |
| 3 | Settle streams into one canonical final response, with explicit cancellation ownership | `llm/stream_accumulator.go:124`; `llm/chan_stream.go:17`; `llm/providers/responses/stream.go:353` | `38f5`, `mgpj`; informs `a825` |
| 4 | Make persistence success and validated resume explicit boundaries | `agent/transcript/transcript.go:410-473,590-671`; `agent/session.go:1581` | `jh1c`, `bq4e`; informs `t0av`, `tp9k`, `93yp` |
| 5 | Bound the model view of a tool result while preserving its recoverable source | `agent/internal/tool/registry.go:226,791`; `agent/session_tool_artifacts.go:26` | New `r6tc` |
| 6 | Test invariants across transformations and operation sequences | `llm/providers/responses/stream_fuzz_test.go`; `agent/transcript/writer_roundtrip_fuzz_test.go` | New `d1dj`; existing `sask`, `m886` |

### 1. Replay metadata is part of the conversation contract

Evener models assistant phase, provider item ID, tool-call ID, encrypted
reasoning, signatures and visible reasoning separately. This gives mux a
practical starting point for data that its normalized blocks currently lose.
`TestSession_RetainsEncryptedReasoningAcrossToolRound` exercises a real session
with a scripted provider boundary and checks the next request.

Port the representation and next-request invariant first. Add the stronger mux
test: provider response → history → save/load → tool result → next wire request.
Preserve item order and provider ownership deliberately. Evener's encoder groups
some text before other items; it is not a generic lossless ordering template.
The inspected tests do not establish a complete phase-plus-persistence wire
roundtrip. Mux needs that proof rather than assuming the reference has it.

### 2. Provider instance, protocol and capability are different facts

Evener separates a named backend from its wire protocol and transport. Its
resolved record includes capability values, provenance and warnings, and can
distinguish an absent setting from an explicit false. Both Complete and Stream
use the same request shaping; continuation planning sees the shaped request too.
That is a direct answer to the duplicated factories found in Hex, Hawk and
Glassspider during the [relevance audit](relevance.md).

The small mux version is a caller-supplied profile and one resolver/validator
feeding the current SDK adapters. Prove the value by deleting factory and
reasoning-threshold duplication in two consumers. Useful tests to adapt:
`TestResolve_LayerOrder`, `TestResolve_CrossProtocolInstances`,
`TestClientEmbeddedRegistryIsHermetic` and `TestShapeRequest_DoesNotMutateInput`.

Choose a stricter policy for explicit intent: Evener silently clamps effort and
drops unsupported sampling/reasoning fields. Mux should return diagnostics or
errors when it cannot honor a caller's requested control. A catalog refresh
service, credential UI and Evener's application-specific defaults are unnecessary
for the first slice.

### 3. Consumers should not each reconstruct streamed responses

Evener gives final typed content precedence over deltas, while preserving
accumulated content when the finish contains only metadata. Its tests cover
final tool-call identity and lazy partial responses. That rule helps remove
Hawk's stream-to-synchronous wrapper without duplicating final text.

Its channel wrapper also makes ownership explicit: the consumer cancels;
the sole producer closes the event channel. Blocked sends observe the stop
signal. `TestChanStream_Close_UnblocksBlockedSend` and
`TestChanStream_NoRaceProducerVsConsumerClose` are useful fixtures for `mgpj`.
The wrapper still waits for producer completion, so uncancellable provider I/O
can block Close. Copying the wrapper alone would not solve mux's transport bugs.

Evener distinguishes idle timeout, broken read and truncated streams, and tracks
whether a retry would repeat already displayed output (`llm/stream_retry.go`).
Take those distinctions. Treat its empty-unrecognized-stream capability fallback
as a policy choice, not proof that an endpoint is permanently unsupported.
Its generic accumulator also does not preserve every fine-grained phase/item
detail without a substantive final response; mux's replay contract must cover
that case.

### 4. Retain complete history and make durability observable

Evener stores semantic turns separately from the model's compacted projection.
`agent/transcript_read.go:143-173` rebuilds model context from a checkpoint or
summary plus later records. This is useful for mux without requiring an
event-sourced engine or replacing the Store interface.

For durable appends, Evener marshals first, records the old file offset, rolls
back failed writes/fsyncs and advances sequence state only after success. Resume
validates complete records and the expected session before removing an incomplete
tail. The most useful material to port is the failure matrix:

- `TestAppendDurable_WriteFailsRollback` and
  `TestAppendDurable_SyncFailsRollback`.
- `TestRestoreSessionRejectsCorruptTranscriptWithoutMutation` and
  `TestOpenWriterFS_ValidatesHeaderBeforeTruncatingPartialTail`.

Use equivalent cases for mux's atomic snapshots and transcript writer. Ordinary
Evener Append has weaker guarantees than AppendDurable; some recording paths
warn and continue, and nil/closed writers can return success. It is not a
universal durability model to copy. Neither implementation makes external tool
effects exactly-once.

Evener's `agent/history_repair.go` tracks missing results by call ID and reports
unknown execution rather than rerunning a tool. Its compaction cutoff preserves
complete tool exchanges (`agent/internal/contextmgr/context_manager.go:1428`).
Adapt interrupted-batch and repeated-compaction fixtures. Mux's deliberately
suspended approval calls must remain pending, and its existing `m886` fix still
needs to remove orphan results from retained mixed messages. Evener's repair
routine is not a general authorization or duplicate-ID validator.

### 5. A bounded preview needs a truthful recovery path

Evener separates model-visible Output, display FullOutput, exact RecoverableOutput
and Truncated. The distinction matters: a display override can differ from the
text that the model actually lost. It retains full data before advertising an
artifact handle and reports retention failure explicitly.

For mux, start with an optional per-tool policy and host-owned retention hook.
Keep one canonical complete result and derive the bounded view. Preserve error
status, call identity and typed content. Cover Unicode, storage failure, full
retrieval, JSON/media and resume in acceptance tests. Do not truncate serialized
JSON into invalid text while claiming the structured result survived.

Evener's implementation is not a hard memory or total-wire-size bound: full
results initially exist in memory and markers/references add bytes after the
text cap. Its artifact store is temporary and removed on Close
(`agent/internal/artifactstore/store.go:84-93`). Mux must define handle lifetime
across save/load and restart; it must not persist promises to retrieve expired
artifacts.

### 6. Adopt the useful properties, not the whole fuzzing system

`FuzzResponsesStreamMetamorphic` runs the actual HTTP decoder against local
servers. Bytewise rechunking and added SSE comments must leave the response
unchanged. `FuzzLmCloneProviderOptions` checks that mutating a clone cannot alter
its source. Transcript roundtrip tests check reopening and subsequent writes.
These directly target classes of bugs found in mux's audit.

Start with native `testing.F` targets for stream equivalence, replay preservation
and transcript roundtrip. Keep minimized failures in `testdata/fuzz`, replay the
corpus in ordinary checks, and make random searches separate and bounded.
No promoter service, automated PR fleet or extra assertion framework is needed.
Inspect the oracle: Evener's `FuzzRetryStreamCore` ignores its fuzz input, so its
name is not evidence of meaningful exploration. Default scripted tests should
exercise real mux plumbing; separately gated real-provider checks remain needed
for provider behavior. A credential alone must not trigger live calls.

## Additional references and limits

- **Cache usage (`kjsz`):** optional counters and `Usage.Add` in
  `llm/types.go:435-476` are useful. Decide mux's input-token semantics first.
  Evener subtracts cached input in Responses but copies Google's prompt count
  unchanged; Google's reported zero also becomes absent. The universal accounting
  contract is not fully consistent in this reference.
- **Structured outputs (`rjqv`):** `llm/generate_object.go` separates parsing and
  schema validation from the provider request. But Anthropic's implementation
  adds prompt instructions (`llm/providers/anthropic/request.go:152`), Responses
  can downgrade schemas to plain JSON (`llm/providers/responses/input.go:20`), and
  refusal content is not retained by its Responses converter. Port the typed
  request/validation boundary, not those weaker guarantees.
- **Continuation (`eq0g` design input):**
  `llm/responses_continuation_plan.go` derives compatibility from the same wire
  builder used for requests and separates storage/auth scope. Its v2 fingerprint
  excludes input; history-prefix/anchor validation is a separate responsibility.
  The older June plan is stale. This is OpenAI guidance, not a Gemini Interactions
  implementation. Keep hosted storage opt-in and revisit only for a named caller.

## Adaptation versus direct dependency

Evener's modules require Go 1.27; mux currently declares 1.24. The LLM module
also brings sibling auth, identifier and invariant modules, its registry/config
model and different request/event types. A direct dependency deserves a separate
decision if maintaining provider wires becomes the dominant cost. It is not a
prerequisite for the fixes above.

Do not assume the documented module boundary is enforced. Evener's architecture
doc says agent libraries never import app code, but current
`agent/session_client_mutation.go` imports `appwire` and
`agent/skill/builtin_skills.go` imports `internal/bundled`; `agent/go.mod` requires
the root module. This is a reason to check actual dependencies before treating
the agent module as a small standalone replacement.

Evener credits Kilroy and includes `LICENSE-kilroy`. If source or substantial
fixtures are copied, preserve applicable notices and inspect the copied files'
provenance. This comparison copied no production code. The hub UI, scheduler,
job runtime, plugin marketplace and temporary artifact-store ownership remain
outside the proposed mux scope.

## Verification and next implementation slice

The parent review ran selected tests against the actual Evener checkout with
Go 1.27.0: **75 test/subtest passes across llm, registry and Responses**, then
**38 deterministic fuzz-seed passes across llm and Responses**. No failures,
skips or warning/error output appeared. These were seed replays, not random
fuzz campaigns. Additional reviewer-selected race checks passed for llm,
transcript, tool, context manager and agent packages; provider checks also
covered Anthropic. The selected Google package run had no matching tests and
does not count as Google behavior coverage. Full Evener gates and live provider
calls did not run. Evener's checkout remained unchanged.

The next slice should be `e451`: port the replay-state distinctions, then prove
full persistence-to-wire preservation in mux. Follow with `mgpj`/`38f5` stream
ownership and settlement. Provider profiles can then use those reliable contracts
to remove code in two consumers. Introduce the bounded-output policy only after
its retention lifetime and typed-result behavior are agreed. Every proposed
transfer still needs mux regression tests and the normal release checks.
