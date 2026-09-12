# SIFT Codebase Audit — mux, pass 2

**Status:** COMPLETE

- **Repository:** `/Users/harper/Public/src/2389/mux` (`github.com/2389-research/mux`), branch `audit/deep-quality-2026-09-11`
- **Revision:** baseline `9a94996f093a364d7cb539ec130046e846182b26` (2026-09-11). HEAD advanced twice while the audit ran, to `0527430` and then `84c7e8b` (both documentation-only; `git diff --stat 6f7305e..HEAD -- '*.go'` is empty, so every `file:line` citation below still resolves).
- **Scope:** the whole application: Go packages `llm`, `orchestrator`, `session`, `agent`, `tool`, `permission`, `hooks`, `mcp`, `coordinator`, `skill`; `examples/`; the root integration test; test data; build and CI tooling; documentation and design records. Non-source categories are recorded as explicit skips in §5.
- **Audit mode:** read-only. No tests, builds, linters, formatters, generators or `go mod verify` were run. This file is the only repository write (§9).
- **Method:** SIFT structural audit as the spine (24 subsystem rows, bounded worker reviews, coordinator re-verification of every citation, five audit-of-audit passes), with five cross-cutting expert lenses (security, reliability and concurrency, public API design, performance, test quality and tooling) and a documentation-drift review folded into the same finding set. Every accepted finding was filed in the Kata tracker (project `mux`, label `audit-2026-09-11-pass2`); Appendix C maps finding IDs to Kata refs.
- **Relation to the 2026-09-11 first pass:** 51 issues were already open (38 `audit-2026-09-11`, 11 `relevance-2026-09-11`, 2 `evener-review-2026-09-11`). This pass is deduplicated against all 51; where a pass-2 finding is the root cause of an existing symptom issue it says so and the existing issue is kept as the acceptance test. Nineteen existing issues received a pass-2 comment instead of a duplicate issue.

**Pre-merge source corrections (2026-09-12):** corrected the executive summary's empty-candidate panic claim and clarified Anthropic empty-input versus JSON-null behavior. The audit findings remain proposals; current execution plans and further source corrections live in Kata.

## 1. Executive summary

The first pass found defects; this pass asked why the same kinds of defect keep appearing and found the representations that produce them. Four themes carry most of the value:

1. **Lifecycle state is spread across booleans and single-use channels.** The MCP stdio and HTTP clients each keep three or four uncoupled fields where one guarded state value belongs (`mcp/stdio.go:32-34`, `mcp/http.go:22-34`). Four of the first-pass P1/P2 issues (s53f, 64sw, xxrt, vfwt) are symptoms of that one representation, and a second restart panic in the stdio reader, independent of the one already filed, was found while tracing it (SIFT-SUB-16-01, P1). The orchestrator's `StateMachine` has the same shape in miniature (SIFT-SUB-06-02), and the async run handle's three launchers repeat one lifecycle block three times (SIFT-SUB-11-01).
2. **Shapes are mirrored by hand instead of shared.** `agent.TranscriptEntry` mirrors `llm.Message` with a same-named, differently-typed field and silently drops ordinary text turns, which is the root cause of the first-pass P1 issue bq4e (SIFT-SUB-12-02). The JSONL writer and reader redeclare the transcript shapes four times (SIFT-SUB-12-01); `CompactionResult` and `hooks.CompactionEvent` are copied field by field; the durable-session `Snapshot` has no version field, so the on-disk format cannot change safely (SIFT-SUB-07-01).
3. **Validation and conversion are duplicated per provider.** Media-source rules live in three validators plus a boolean transport flag (SIFT-SUB-01-01); stop-reason mapping is three unrelated casts (SIFT-SUB-01-02); OpenRouter and Ollama carry a 70-line identical streaming loop (SIFT-SUB-03-01); Gemini's non-streaming and streaming conversions diverge; empty candidates return an empty success, and blocked or abnormal finish reasons are not reported accurately (SIFT-SUB-04-01, P1).
4. **Interruption leaves the durable history inconsistent.** When the loop is cancelled between the assistant's tool-use turn and the tool results, the checkpoint persists a history that no provider will accept on resume, and no test covers a resume after an interrupted tool batch (SIFT-SUB-06-01, P1, the top-ranked recommendation, with a 35-45 line first slice).

Counts: 24 subsystem rows, 22 reviewed with findings or explicit skips and 2 added by the coverage pass; 79 findings filed (4 P1, 32 P2, 43 P3), 19 comments posted on existing issues, four candidates rejected or withdrawn and several more merged, narrowed or demoted (§7), one withdrawal being a coordinator error caught by the design-document review (§8). The repository integrity check is reported as **failed, external cause** (§9): two documentation commits and three journal files appeared during the audit; the audit itself changed nothing.

## 2. Coverage contract

Twenty-four rows. Rows SUB-23 and SUB-24 were added by the coverage audit-of-audit pass (§8) after it found two omissions; neither was hidden inside a previously completed row. Skip reasons are in §5 only.

| ID | Subsystem | Exact ownership boundary | Key files, interfaces, tests | Status |
|---|---|---|---|---|
| SUB-01 | LLM core types and validation | `llm/types.go`, `llm/client.go`, `llm/validate.go` | `llm.Client`, `Request`, `Response`, `ContentBlock`, `Source`, `Capabilities`, `StopReason`, `validateRequest`; `llm/types_test.go`, `llm/llm_test.go` | recommend (2) |
| SUB-02 | Anthropic client | `llm/anthropic.go` | `NewAnthropicClient`, request and response conversion, `anthropicStreamAccumulator`; `llm/anthropic_test.go`, `llm/testdata/` | recommend (2) |
| SUB-03 | OpenAI-compatible clients | `llm/openai.go`, `llm/openrouter.go`, `llm/ollama.go` | Responses API client, two Chat Completions clients, `convertOpenAIRequest`, `validateOpenAISources`; `llm/openai_test.go`, `llm/openrouter_test.go`, `llm/ollama_test.go` | recommend (1) |
| SUB-04 | Gemini client | `llm/gemini.go` | `convertGeminiResponse`, streaming conversion, `validateGeminiSources`; `llm/gemini_test.go` | recommend (2) |
| SUB-05 | Retry client | `llm/retry.go` | `RetryClient`, retry classification, both `CreateMessage` paths; `llm/retry_test.go` | recommend (2) |
| SUB-06 | Orchestrator loop and state machine | `orchestrator/orchestrator.go`, `orchestrator/state.go` | `Orchestrator.Run`, `Resume`, `executeTools`, `buildToolDefinitions`, `checkpoint`, `StateMachine`; `orchestrator/orchestrator_test.go`, `coverage_test.go`, `hooks_integration_test.go` | recommend (2) |
| SUB-07 | Durable sessions | `orchestrator/session.go`, `session/file_store.go` | `Snapshot`, `Status`, `Suspension`, `SessionStore`, `FileStore`; `orchestrator/session_internal_test.go`, `session/file_store_test.go` | recommend (1) |
| SUB-08 | Compaction and token accounting | `orchestrator/compact.go`, `orchestrator/tokens.go`, `orchestrator/usage.go` | `compact`, `CompactionResult`, `CompactUserMessageMaxTokens`, token estimation, `Usage`; `compact_test.go`, `compact_integration_test.go`, `tokens_test.go`, `usage_test.go` | recommend (1) |
| SUB-09 | Event bus | `orchestrator/events.go` | `EventBus.Subscribe`, `Close`, `Reset`; `orchestrator/orchestrator_test.go:1041-1726` | recommend, delivered as a comment on the existing issue mux#1wwm (no new issue; §7) |
| SUB-10 | Agent core and presets | `agent/agent.go`, `agent/config.go`, `agent/presets.go` | `agent.New`, `Config`, `SpawnChild`, `Preset.Apply`, `SpawnExplorer` and siblings; `agent/agent_test.go`, `presets_test.go`, `integration_test.go`, `skills_test.go` | recommend (2) |
| SUB-11 | Async runs | `agent/async.go` | `RunAsync`, `ContinueAsync`, `RunChildAsync`, `RunHandle`; `agent/async_test.go` | recommend (1) |
| SUB-12 | Transcript persistence | `agent/transcript.go` | `Transcript`, `TranscriptEntry`, `FromMessages`, JSON and JSONL save and load; `agent/transcript_test.go` | recommend (2) |
| SUB-13 | Tool registry and executor | `tool/*.go` | `Tool`, `SchemaProvider`, `Registry`, `Executor`, `Result`, `ApprovalFunc`; `tool/executor_test.go`, `filter_test.go`, `tool_test.go` | recommend (2) |
| SUB-14 | Permission checker | `permission/*.go` | `Checker`, `Mode`, rules; `permission/permission_test.go`, root `integration_test.go:84-458` | recommend (1) |
| SUB-15 | Hooks | `hooks/hooks.go` | `Manager`, seven `Fire*` methods, event structs, `SessionEndEvent.Reason`; `hooks/hooks_test.go` | recommend (2) |
| SUB-16 | MCP transports | `mcp/client.go`, `mcp/stdio.go`, `mcp/http.go`, `mcp/sse.go` | `Client`, `NewClient`, stdio and Streamable HTTP clients, SSE parser; `mcp/mcp_test.go`, `http_test.go`, `sse_test.go`, `mcp/testdata/` | recommend (2) |
| SUB-17 | MCP adapter and types | `mcp/adapter.go`, `mcp/types.go` | `ToolAdapter`, `ToolManager.RegisterAll`, `ServerConfig`, `Request`, `Notification`, sentinels; `mcp/mcp_test.go` | recommend (1) |
| SUB-18 | Coordinator | `coordinator/*.go` | `Coordinator`, `Cache`, `RateLimiter`; `coordinator/coordinator_test.go` | recommend (1) |
| SUB-19 | Skills | `skill/*.go` | `parseSkill`, `Registry`, `LoadDir`, the `load_skill` tool; `skill/skill_test.go`, `registry_test.go`, `tool_test.go`, `agent/skills_test.go` | skip (§5) |
| SUB-20 | Examples | `examples/full/`, `examples/minimal/`, `examples/simple/` | example tools, approval wiring, environment reads | recommend (1) |
| SUB-21 | Test harness and tooling | root `integration_test.go`, `llm/testdata/`, `mcp/testdata/`, `Makefile`, `.github/workflows/ci.yml`, `.golangci.yml`, `.pre-commit-config.yaml`, `.git_hooks/` | test gates, CI jobs, lint configuration | recommend (1) |
| SUB-22 | User-facing documentation and metadata | `README.md`, `CHANGELOG.md`, `DEPENDENTS.md`, `gotchas.md` (except the Evener section), `docs/audits/2026-09-11/README.md` and `relevance.md`, `architecture.dot`, `scenarios.jsonl` | every claim checked against code, tags and commits | recommend (2) |
| SUB-23 | Design documents (added by the coverage pass) | `docs/plans/**` (14 files), `docs/superpowers/specs/**` (5), `docs/superpowers/plans/**` (5), `docs/audits/2026-09-11/evener.md`, the Evener section of `gotchas.md`, the untracked `docs/plans/2026-06-26-provider-support-notes.md` | recorded decisions checked for drift against the code they describe | recommend (1) |
| SUB-24 | Dependency manifests (added by the coverage pass) | `go.mod`, `go.sum` | six direct requires, no `replace` or `exclude`, toolchain pin | skip (§5) |

## 3. Prioritized recommendations

Thirty-three accepted structural findings, at most two per subsystem. Thirty-two were filed as Kata issues; the SUB-09 recommendation was posted as a comment on the existing issue mux#1wwm because it is the fix for that issue rather than a new defect. The 47 remaining pass-2 issues came from the expert lenses and are listed with source attribution in Appendix A; one of them, mux#5ez6, is a P1 defect and is called out in §1 and §4. Ranks 1 to 17 follow the dependency-aware ranking pass (§8), which re-verified ranks 1 to 15 against the code; ranks 18 to 33 are the coordinator's order for the findings that pass placed in its second wave and backlog tiers.

| Priority | Finding | Subsystem | Impact | Confidence | Effort | Blast radius | Prerequisites |
|---|---|---|---|---|---|---|---|
| 1 | SIFT-SUB-06-01 Keep tool-batch history valid on cancellation; never re-run completed tools on Resume (mux#yhen, P1) | SUB-06 | high | high | medium | cross-subsystem | none; coordinate with mux#5ez6 (same lines) |
| 2 | SIFT-SUB-12-02 Store llm.Message in the transcript instead of a hand-mirrored entry (mux#2zdv, P2) | SUB-12 | high | high | small | subsystem, plus external transcript readers | SIFT-SUB-12-01 lands with or before |
| 3 | SIFT-SUB-04-01 Report Gemini blocked prompts and non-STOP finish reasons (mux#a2j0, P1) | SUB-04 | high | high | small | subsystem | SIFT-SUB-01-02 supplies the StopReason values |
| 4 | SIFT-SUB-08-01 Replace the dead recent-user-message budget with one block-level sanitizer (mux#0183, P2) | SUB-08 | medium | high | small | subsystem | none; supersedes a standalone mux#m886 patch |
| 5 | SIFT-SUB-01-02 Map every provider finish reason through one helper onto a complete StopReason set (mux#7fez, P2) | SUB-01 | medium | high | small | subsystem | none; authority for rank 3 |
| 6 | SIFT-SUB-16-01 Own transport lifecycle state in one place for stdio and HTTP clients (mux#nrhn, P1) | SUB-16 | high | high | large | subsystem | none; lands before SIFT-SUB-16-02 and mux#rqxt |
| 7 | SIFT-SUB-10-01 Give every agent.Config field one stated inheritance policy in SpawnChild (mux#qxfb, P2) | SUB-10 | medium | high | medium | subsystem | none; coordinate with rank 9 |
| 8 | SIFT-SUB-16-02 Build notifications, response matching and headers once for both transports (mux#6aqg, P2) | SUB-16 | medium | high | medium | subsystem | after rank 6 (same files) |
| 9 | SIFT-SUB-07-01 Validate the Snapshot invariant and version it at the Store boundary (mux#mmzv, P2) | SUB-07 | medium | high | small | subsystem | none; coordinate with rank 7 |
| 10 | SIFT-SUB-04-02 Map Gemini parts to blocks through one function on both paths (mux#d6rh, P2) | SUB-04 | medium | high | small | subsystem | none; mux#ac2b and mux#62ba land on it |
| 11 | SIFT-SUB-14-01 Make permission.Checker's Ask outcome reach a prompt and state rule precedence (mux#j6kd, P2) | SUB-14 | medium | high | medium | subsystem, breaking signature | none; coordinate with mux#datc |
| 12 | SIFT-SUB-12-01 Declare the transcript header and entry shapes once, with a format version (mux#yp6a, P2) | SUB-12 | medium | high | small | local | none; precedes rank 2 |
| 13 | SIFT-SUB-01-01 Consolidate media source validation into one pre-flight check with a transport axis (mux#ray8, P2) | SUB-01 | medium | high | medium | subsystem | none; fixes mux#drm8 |
| 14 | SIFT-SUB-17-01 Type the MCP transport name and validate ServerConfig in NewClient (mux#865b, P2) | SUB-17 | medium | high | small | subsystem, breaking for callers with bad configs | none |
| 15 | SIFT-SUB-11-01 Collapse the three async launchers into one helper (mux#qg5t, P2) | SUB-11 | medium | high | small | local | none; mux#y0j9 lands inside it |
| 16 | SIFT-SUB-03-01 Share one Chat Completions streaming loop between OpenRouter and Ollama (mux#qa9p, P2) | SUB-03 | medium | high | medium | subsystem | none; blocks mux#k5mx and mux#3zpg |
| 17 | SIFT-SUB-05-02 Drive CreateMessage and CreateMessageStream through one retry loop (mux#6vjj, P2) | SUB-05 | medium | high | small | local | none; precedes mux#vrjk and mux#r4z9 |
| 18 | SIFT-SUB-06-02 Route every state change through the validated StateMachine path (mux#kzxd, P2) | SUB-06 | medium | high | medium | subsystem | none; distinct from mux#t0av |
| 19 | SIFT-SUB-05-01 Classify retryable errors by SDK type instead of a substring scan (mux#ghk9, P2) | SUB-05 | medium | high | small | local | none |
| 20 | SIFT-SUB-15-01 Dispatch all seven hook event types through one helper (mux#7gb0, P3) | SUB-15 | low | high | small | local | none; mux#yr10 lands inside it |
| 21 | SIFT-SUB-15-02 Type SessionEndEvent.Reason and test the suspended value (mux#s0st, P3) | SUB-15 | low | high | small | local, exported field type | none |
| 22 | SIFT-SUB-20-01 Confine the full example's file tools or stop claiming they are confined (mux#3p4f, P2) | SUB-20 | medium | high | small | local | none |
| 23 | SIFT-SUB-21-01 One canonical check that every gate calls (mux#7gbz, P2) | SUB-21 | medium | high | small | application-wide (developer workflow) | none |
| 24 | SIFT-SUB-22-01 Fill the six missing CHANGELOG releases and mark the v0.8.0 break (mux#qs9q, P2) | SUB-22 | medium | high | small | local | none; vehicle for the breaking batch (§6) |
| 25 | SIFT-SUB-22-02 Make the README describe the library that shipped (mux#rqzk, P2) | SUB-22 | medium | high | small | local | none |
| 26 | SIFT-SUB-09-01 Make EventBus.Close clear its subscribers so Reset cannot close them twice (comment on mux#1wwm) | SUB-09 | low | high | small | local | none |
| 27 | SIFT-SUB-13-02 Treat a nil SchemaProvider result like a missing schema (mux#ghcv, P3) | SUB-13 | low | high | small | local | none |
| 28 | SIFT-SUB-13-01 Guarantee a non-nil Result from Executor.Execute (mux#9p0e, P3) | SUB-13 | low | high | small | local | none |
| 29 | SIFT-SUB-10-02 Make child agent IDs unique among siblings (mux#aezg, P3) | SUB-10 | low | high | small | local | none |
| 30 | SIFT-SUB-18-01 Bound the coordinator cache or make the host's cleanup duty explicit (mux#v028, P3) | SUB-18 | low | high | small | local | none |
| 31 | SIFT-SUB-02-01 Parse Anthropic tool input once for both paths (mux#rzr5, P3) | SUB-02 | low | high | small | local | none |
| 32 | SIFT-SUB-02-02 Emit a cloned Response on the message_delta stream event (mux#eb0c, P3) | SUB-02 | low | high | small | local | none |
| 33 | SIFT-SUB-23-01 Correct the six false design-document statements and the stale plan status lines (mux#s1xn, P3) | SUB-23 | low | high | small | local | none |

Field conventions for the entries below: every schema field is present; `not applicable` always carries a clause saying why. Line numbers cite HEAD 84c7e8b, which has the same Go files as the baseline.

### 3.1 LLM package (SUB-01 to SUB-05)

#### SIFT-SUB-01-01 · Consolidate media source validation into one pre-flight check with a transport axis
- **Authoritative subsystem:** SUB-01 LLM core types and validation. **Verdict:** recommend. **Kata:** mux#ray8 (P2). **Priority:** rank 13 of 33.
- **Primary evidence:** `llm/validate.go:5-9` documents that source-form constraints are checked inside each provider; `checkBlock` (`llm/validate.go:24-58`) checks media type and Source presence only, and its Bytes/File branch (50-53) never checks MediaType. `validateOpenAISources` (`llm/openai.go:394-421`, also called from `llm/openrouter.go:105,131`) and `validateGeminiSources` (`llm/gemini.go:377-401`) repeat the media switch; `ErrUnsupportedSource` is built only at `llm/openai.go:407,410,415` and `llm/gemini.go:399`. The transport axis is a bool: `allowURLPDF` is true at `llm/openai.go:650` and false at `:677` although `:681-682` also stream through the Responses API (mux#drm8). `validateMediaFamily` (`llm/types.go:396-408`) runs only inside the block constructors (`:245-260`). `convertOpenAIPDF` and `convertOpenAIAudio` (`llm/openai.go:339-368`) encode `Source.Bytes` without checking `Source.Kind`.
- **Interfaces and call sites:** the five `llm.Client` constructors; `validateRequest` (called at `llm/ollama.go:54,75` before conversion); `Capabilities()`.
- **Tests and intent evidence:** `llm/types_test.go:370-385` builds literal blocks that skip family validation; `llm/openai_test.go:1636` covers the helper and `:1667` pins the streaming rejection. The split is a recorded decision: `docs/plans/2026-04-21-llm-multimodal-input-design.md:145-146,151-152`; the untracked `docs/plans/2026-06-26-provider-support-notes.md:19-20` asks that `Capabilities` stay media-only.
- **Current representation:** four validators plus a bool parameter, one per provider file, each a partial copy of the same switch.
- **Current complexity or invalid states:** the same URL-form PDF is accepted by OpenAI `CreateMessage`, rejected by OpenAI `CreateMessageStream`, rejected by a second validator on Gemini, rejected earlier by capability on Ollama, and never checked on Anthropic.
- **Why it is material:** one parity bug is already filed (mux#drm8) and the bool made it easy to write; every new provider or transport adds another switch.
- **Proposed representation:** `validateRequest` is the only decision point; a descriptor beside `Capabilities` names the supported source kinds per media type and per transport; `validateGeminiSources` and the `allowURLPDF` parameter are deleted; `validateMediaFamily` runs for literal blocks too.
- **Why it is simpler:** one table replaces four switches and a bool, and the provider files stop carrying validation they cannot see the whole of.
- **Implementation scope:** 20-30 lines in `llm/validate.go`, 10-15 in `llm/types.go`, about 25 lines deleted in `llm/gemini.go`, the bool removed in `llm/openai.go`.
- **Smallest credible slice:** flip the bool at `llm/openai.go:677` (mux#drm8's own fix) and add the table-driven test over the five clients; the descriptor follows in a second slice.
- **Regression risks:** the Responses path must keep allowing URL PDFs while Chat Completions (OpenRouter, Ollama) must keep rejecting them, so the descriptor needs a transport dimension.
- **Migration concerns:** no persisted data; some inputs change from `ErrUnsupportedSource` to the pre-flight error, which needs a CHANGELOG line.
- **Existing validation:** `llm/openai_test.go:1636,1667`; `llm/types_test.go`; per-provider conversion tests.
- **Additional validation required:** one table-driven test across all five clients covering URL-form PDF, URL-form audio, unknown audio format and empty bytes.
- **Impact:** medium (provider parity, recurring class of bug). **Confidence:** high (traced at every site; drm8 reproduces it). **Implementation effort:** medium. **Blast radius:** subsystem (llm). **Prerequisites:** none.

#### SIFT-SUB-01-02 · Map every provider finish reason through one helper onto a complete StopReason set
- **Authoritative subsystem:** SUB-01. **Verdict:** recommend. **Kata:** mux#7fez (P2). **Priority:** rank 5 of 33.
- **Primary evidence:** `llm/types.go:36-41` defines end_turn, tool_use and max_tokens. `llm/anthropic.go:153` and `:410` cast the SDK string directly. `llm/openai.go:552-563` sends content_filter and function_call to `StopReasonEndTurn`. `llm/gemini.go:196-208` sends safety, recitation, blocklist and other to `StopReasonEndTurn`; `:191-193` returns the zero value `""` when `Candidates` is empty.
- **Interfaces and call sites:** `Response.StopReason` (exported); the orchestrator decides the next step with `HasToolUse` (`orchestrator/orchestrator.go:350-352`), not `StopReason`.
- **Tests and intent evidence:** provider tests cover only end_turn and tool_use; `llm/validate.go` never checks `StopReason` against its constants.
- **Current representation:** an open string type with three constants and three ad hoc mappings.
- **Current complexity or invalid states:** values outside the declared set (refusal, pause_turn) and the empty string are reachable; a filtered answer is indistinguishable from a complete one.
- **Why it is material:** external consumers branching on `StopReason` get silent-wrong answers on every provider; rank 3 and mux#5n3p both need the missing values.
- **Proposed representation:** one mapping helper per provider family and at least one abnormal-termination constant, documented on the type; the Gemini empty-candidate case can no longer yield `""`.
- **Why it is simpler:** one table per provider replaces raw casts and default branches, and the constants become the contract.
- **Implementation scope:** 10-15 lines of helper per provider, 3-5 constants, table tests.
- **Smallest credible slice:** add the constants and the Gemini and OpenAI helpers with their table tests (about 35-45 lines); Anthropic's raw casts follow.
- **Regression risks:** consumers that treated every non-tool_use as success now see a new value.
- **Migration concerns:** additive constants only; a CHANGELOG note that "not tool_use" no longer implies a successful end_turn.
- **Existing validation:** `llm/anthropic_test.go`, `llm/openai_test.go`, `llm/gemini_test.go` conversion tests.
- **Additional validation required:** table tests enumerating each provider's native finish reasons and the mapped value.
- **Impact:** medium (silent-wrong for external consumers; nil in-tree). **Confidence:** high. **Implementation effort:** small. **Blast radius:** subsystem. **Prerequisites:** none; authority for SIFT-SUB-04-01.

#### SIFT-SUB-02-01 · Parse Anthropic tool input once for both paths
- **Authoritative subsystem:** SUB-02 Anthropic client. **Verdict:** recommend. **Kata:** mux#rzr5 (P3). **Priority:** rank 31 of 33.
- **Primary evidence:** `convertResponse` (`llm/anthropic.go:172-186`) leaves `Input` nil for null or empty input; `stopBlock` in the stream accumulator (`llm/anthropic.go:260-275`) creates an empty map for an empty input buffer, but unmarshalling explicit JSON `null` leaves the map nil on both paths.
- **Interfaces and call sites:** `tool.Executor.Execute` hands `Input` straight to `Tool.Execute` (`tool/executor.go:141`).
- **Tests and intent evidence:** `TestConvertResponse_ToolUseWithNullInput` (`llm/anthropic_test.go:625-655`) only logs the outcome. Both design documents specify an empty non-nil map (`docs/plans/2026-06-26-streaming-cleanup-design.md`, `docs/plans/2026-06-26-anthropic-stream-normalization-design.md`).
- **Current representation:** two parsers of the same raw JSON with different zero-value behaviour.
- **Current complexity or invalid states:** empty input yields nil on the non-streaming path and an empty map on the streaming path; explicit JSON `null` yields nil on both. A tool that writes into a nil params map can panic.
- **Why it is material:** empty-input divergence between streaming and non-streaming, plus null handling that violates the documented non-nil-map contract on both paths.
- **Proposed representation:** one `parseToolInput(raw json.RawMessage) (map[string]any, error)` used by both sites; null and empty return an empty non-nil map.
- **Why it is simpler:** one parser, one documented zero value.
- **Implementation scope:** about 15 lines plus test edits.
- **Smallest credible slice:** the helper plus the assertion fix in the existing test.
- **Regression risks:** choosing one canonical value changes what tools see on the other path.
- **Migration concerns:** not applicable; nothing persisted changes shape.
- **Existing validation:** the logging-only test above.
- **Additional validation required:** one asserting test per path pinning the empty non-nil map.
- **Impact:** low. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none.

#### SIFT-SUB-02-02 · Emit a cloned Response on the message_delta stream event
- **Authoritative subsystem:** SUB-02. **Verdict:** recommend. **Kata:** mux#eb0c (P3). **Priority:** rank 32 of 33.
- **Primary evidence:** `mergeDelta` (`llm/anthropic.go:277-288`) returns a fresh `&Response{StopReason, Usage}`; `cloneResponse` (`:307-325`) already exists and is used for message_start at `:364`; the delta event is emitted at `:410`; `start()` records ID and Model at `:228-232`.
- **Interfaces and call sites:** `EventMessageDelta.Response`; in-tree, `collectStreamResponse` (`orchestrator/orchestrator.go:403-434`) reads only `EventError` and `EventMessageStop`.
- **Tests and intent evidence:** `llm/anthropic_test.go:927-940` checks only `StopReason` and `OutputTokens`.
- **Current representation:** two ways to build the event's Response, one a snapshot and one a partial literal.
- **Current complexity or invalid states:** the delta event carries no ID, Model, Content or InputTokens although the accumulator has them.
- **Why it is material:** external stream consumers get an inconsistent Response shape; the fix is one line.
- **Proposed representation:** `mergeDelta` returns `cloneResponse(a.response)`, or the delta event stops carrying a Response and the doc says so.
- **Why it is simpler:** every emitted Response is the same kind of snapshot.
- **Implementation scope:** 1-3 lines plus a test.
- **Smallest credible slice:** the one-line clone plus the assertion.
- **Regression risks:** the snapshot must be a copy or later deltas mutate a value a consumer holds.
- **Migration concerns:** not applicable; the event shape is in memory only.
- **Existing validation:** the test above.
- **Additional validation required:** assert ID, Model and InputTokens on the message_delta event.
- **Impact:** low. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none.

#### SIFT-SUB-03-01 · Share one Chat Completions streaming loop between OpenRouter and Ollama
- **Authoritative subsystem:** SUB-03 OpenAI-compatible clients. **Verdict:** recommend. **Kata:** mux#qa9p (P2). **Priority:** rank 16 of 33.
- **Primary evidence:** `llm/openrouter.go:138-206` and `llm/ollama.go:82-150` are the same 65-line loop; the only difference is the provider label (`:143` and `:87`). `convertOpenAIRequest` (`llm/openai.go:53-118`) is shared by all three providers with no seam.
- **Interfaces and call sites:** both `CreateMessageStream` methods; mux#k5mx (`llm/openai.go:58-60`, max_tokens field) and mux#3zpg (stream_options.include_usage) both need a provider branch inside the shared builder or a third copy.
- **Tests and intent evidence:** the streaming tests are duplicated too: `llm/ollama_test.go:414,557` mirror `llm/openrouter_test.go:554,646`.
- **Current representation:** two hand-copied loops over one shared request builder.
- **Current complexity or invalid states:** every provider-specific fix must be written twice and tested twice.
- **Why it is material:** two open issues are waiting on a seam that does not exist.
- **Proposed representation:** one unexported `runChatCompletionStream(ctx, client, label, params)` called by both, and `convertOpenAIRequest` taking a small per-provider options value.
- **Why it is simpler:** one loop, one table test, a provider quirk becomes an options field.
- **Implementation scope:** about 70 lines deleted, 30-40 added, tests consolidated.
- **Smallest credible slice:** extract the loop with the label as its only parameter and keep both test pairs until the helper has its own table test.
- **Regression risks:** the provider label in errors and events must survive the merge.
- **Migration concerns:** not applicable; no exported signature or persisted shape changes.
- **Existing validation:** the four duplicated streaming tests.
- **Additional validation required:** one table test for the shared helper; one test each for mux#k5mx and mux#3zpg on top of it.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** medium. **Blast radius:** subsystem. **Prerequisites:** none; blocks mux#k5mx and mux#3zpg.

#### SIFT-SUB-04-01 · Report Gemini blocked prompts and non-STOP finish reasons instead of returning an empty success
- **Authoritative subsystem:** SUB-04 Gemini client. **Verdict:** recommend. **Kata:** mux#a2j0 (P1). **Priority:** rank 3 of 33.
- **Primary evidence:** `llm/gemini.go:191-193` returns the partial Response as soon as `len(resp.Candidates) == 0`, with no content, a zero-value `StopReason` and no error; `PromptFeedback` and `BlockReason` are read nowhere in `llm/gemini.go` (grep: zero hits); `:196-208` maps only Stop and MaxTokens, so SAFETY, RECITATION, BLOCKLIST, PROHIBITED_CONTENT, SPII, MALFORMED_FUNCTION_CALL and OTHER become `StopReasonEndTurn`.
- **Interfaces and call sites:** the orchestrator (`orchestrator/orchestrator.go:350-352`) sees no tool use, records the empty assistant message and completes the run; the streaming path (`llm/gemini.go:312-338`) never inspects finish reasons either.
- **Tests and intent evidence:** no test covers a blocked prompt or a non-STOP finish; the materiality pass promoted this to P1 after confirming the zero-hit grep.
- **Current representation:** an early return on an empty candidate list and a two-case switch.
- **Current complexity or invalid states:** a blocked prompt is an empty successful turn; the transcript persists an empty assistant message and the user sees nothing.
- **Why it is material:** silent total failure on one provider, with the block reason discarded.
- **Proposed representation:** empty candidates with a block reason return a typed, `errors.As`-checkable error (or a dedicated `StopReason`); non-STOP finish reasons map to a distinguishable value from SIFT-SUB-01-02.
- **Why it is simpler:** the zero value can no longer escape, and the reason text reaches the caller through the existing error path instead of a second channel.
- **Implementation scope:** 15-25 lines in `llm/gemini.go` plus tests.
- **Smallest credible slice:** the empty-candidates branch and the finish-reason switch in `convertGeminiResponse` only, consuming one new constant; the streaming loop is a second slice.
- **Regression risks:** callers that received an empty success for a blocked prompt now receive an error; `RetryClient` must classify it as non-retryable or it re-sends blocked prompts.
- **Migration concerns:** none persisted; a documented behaviour change, not a compile break.
- **Existing validation:** `llm/gemini_test.go` conversion tests (STOP and tool-call cases only).
- **Additional validation required:** tests for blocked prompt, safety finish with no parts, and MAX_TOKENS with partial text.
- **Impact:** high. **Confidence:** high. **Implementation effort:** small. **Blast radius:** subsystem. **Prerequisites:** SIFT-SUB-01-02 (constants).

#### SIFT-SUB-04-02 · Map Gemini parts to blocks through one function on both paths
- **Authoritative subsystem:** SUB-04. **Verdict:** recommend. **Kata:** mux#d6rh (P2). **Priority:** rank 10 of 33.
- **Primary evidence:** non-streaming `convertGeminiResponse` (`llm/gemini.go:212-233`) checks `part.Thought` and emits `ContentTypeThinking` (212-221) then maps FunctionCall parts (226-233); the streaming loop (`:316-337`) emits `part.Text` as `EventContentDelta` regardless of `part.Thought` and repeats the FunctionCall mapping at 325-334. The client sets `IncludeThoughts: true` at `:86`.
- **Interfaces and call sites:** both `CreateMessage` paths; mux#ac2b and mux#62ba land in the same duplicated region.
- **Tests and intent evidence:** no streaming test with a thought part exists.
- **Current representation:** two hand-written part-to-block mappings that already disagree.
- **Current complexity or invalid states:** reasoning text streams to the consumer as answer text; every fix in this region is written twice.
- **Why it is material:** user-visible wrong output under thinking, and two open issues waiting on the same seam.
- **Proposed representation:** one unexported `geminiPartToBlock(part) (ContentBlock, bool)` used by both paths; the streaming loop emits thinking deltas (or a thinking block on the final response) for thought parts.
- **Why it is simpler:** one mapping, and mux#ac2b's accumulator is written once.
- **Implementation scope:** 20-30 lines net removal plus tests.
- **Smallest credible slice:** the shared mapper plus the streaming thought test; land with mux#ac2b.
- **Regression risks:** consumers that concatenated all streamed text now see thinking separated out.
- **Migration concerns:** not applicable; in-memory shapes only.
- **Existing validation:** `llm/gemini_test.go` non-streaming conversion tests.
- **Additional validation required:** a streaming test asserting no `EventContentDelta` carries thought text.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** subsystem. **Prerequisites:** none; mux#ac2b and mux#62ba are implemented on top of it.

#### SIFT-SUB-05-01 · Classify retryable errors by SDK type instead of a substring scan
- **Authoritative subsystem:** SUB-05 Retry client. **Verdict:** recommend. **Kata:** mux#ghk9 (P2). **Priority:** rank 19 of 33.
- **Primary evidence:** `llm/retry.go:118-141` uses `errors.As` for the Anthropic and OpenAI error types then falls back to `strings.Contains(err.Error(), "<code>")` over `retryableStatusCodes` (`:114-116`); there is no branch for the genai module's `APIError` although go.mod requires genai v1.54.0 (`go.mod:12`). Anthropic's 529 is pinned non-retryable by `llm/retry_test.go:119-126` without a documented reason.
- **Interfaces and call sites:** `RetryClient` on every provider; mux#a825 uses the same classifier on the streaming path.
- **Tests and intent evidence:** `docs/superpowers/specs/2026-03-23-mux-improvements-design.md:45-53` specifies `errors.As` for Gemini "or fall back to error string inspection"; the typed half never shipped, and the type the spec names lives in a module go.mod does not require.
- **Current representation:** two typed checks and a text scan for everything else.
- **Current complexity or invalid states:** "image exceeds 5000 px" is retried; a Gemini 429 whose message omits the number is not.
- **Why it is material:** wrong retries on one provider and false retries on all of them, decided by message text.
- **Proposed representation:** an `errors.As` branch for the genai `APIError` (by value, matching its receiver); the substring fallback removed or narrowed to a documented list of transport errors; the 529 decision made explicit.
- **Why it is simpler:** classification depends on types the SDKs already export, not on their prose.
- **Implementation scope:** 15-25 lines plus tests.
- **Smallest credible slice:** add the genai branch and the table test; narrow the fallback in a second commit.
- **Regression risks:** removing the fallback can stop retries for wrapped errors that carried the status only in text.
- **Migration concerns:** not applicable; behaviour change only, noted in the CHANGELOG.
- **Existing validation:** `llm/retry_test.go`.
- **Additional validation required:** a table over each SDK error type plus one message that merely contains a retryable number.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none.

#### SIFT-SUB-05-02 · Drive CreateMessage and CreateMessageStream through one retry loop
- **Authoritative subsystem:** SUB-05. **Verdict:** recommend. **Kata:** mux#6vjj (P2). **Priority:** rank 17 of 33.
- **Primary evidence:** `RetryClient.CreateMessage` (`llm/retry.go:53-75`) and `CreateMessageStream` (`:77-100`) are the same loop with a different inner call.
- **Interfaces and call sites:** both exported methods keep their signatures.
- **Tests and intent evidence:** `llm/retry_test.go` has two streaming tests against many non-streaming ones; mux#a825 shows the stream copy already drifted.
- **Current representation:** two copies of the retry policy.
- **Current complexity or invalid states:** every policy change (Retry-After, classification, attempt accounting) is made twice or drifts.
- **Why it is material:** mux#vrjk and mux#r4z9 both need a single loop to land once; the ranking pass promoted this to P2 for that reason.
- **Proposed representation:** one generic helper, for example `retry[T any](ctx, cfg, func(ctx) (T, error)) (T, error)`, driving both methods.
- **Why it is simpler:** one loop, one test table.
- **Implementation scope:** about 25 lines removed, 15 added.
- **Smallest credible slice:** the helper with behaviour unchanged (no stream retry after the channel is returned).
- **Regression risks:** the stream path's current behaviour must be preserved until mux#a825 decides otherwise.
- **Migration concerns:** not applicable; no signature change.
- **Existing validation:** `llm/retry_test.go`.
- **Additional validation required:** the streaming tests mirror the non-streaming table.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none; precedes mux#vrjk and mux#r4z9.

### 3.2 Orchestrator, sessions, compaction and events (SUB-06 to SUB-09)

#### SIFT-SUB-06-01 · Keep tool-batch history valid on cancellation and never re-run completed tools on Resume retry
- **Authoritative subsystem:** SUB-06 Orchestrator loop and state machine. **Verdict:** recommend. **Kata:** mux#yhen (P1). **Priority:** rank 1 of 33.
- **Primary evidence:** `processResponse` appends the assistant tool_use message before any tool runs (`orchestrator/orchestrator.go:545`); `executeTools` (`:548-591`) checks ctx per tool (556-563) and on cancellation returns `ctx.Err()`, discarding every collected result; results are appended only at `:591`. `runIterations` calls `executeTools` then `checkpoint(ctx, StatusRunning)` (358-361); `handleError` (604-608) only transitions to `StateError`. `resumeCore` (695-716) has the same order (706, 711); `lastAssistantToolUses` (745-759) always returns the whole batch.
- **Interfaces and call sites:** `Run`, `Continue` (whose doc advertises multi-turn reuse), `Resume`; every provider rejects an assistant tool_use without a tool_result.
- **Tests and intent evidence:** no test cancels mid-batch or fails the StatusRunning checkpoint; the comment at `:553-556` explains the discard as avoiding partial-output confusion and does not address the orphan. `docs/audits/2026-09-11/evener.md` section 4 records the sibling project's history repair by call ID as prior art.
- **Current representation:** batch completion is implicit in the order of two appends; per-call completion is not represented anywhere.
- **Current complexity or invalid states:** after a cancelled batch the history ends on an unpaired tool_use, so the next `Continue` is malformed until the host repairs it through `SetMessages`; a retried `Resume` re-executes tools that already ran and had side effects; a failed StatusRunning checkpoint after `:591` leaves a snapshot that predates the batch.
- **Why it is material:** correctness and data loss in the core loop for every tool-using session.
- **Proposed representation:** on cancellation, append the partial results plus synthesized cancelled tool_result blocks for the remaining calls (or drop the just-appended assistant message); `Resume` tracks per-call completion persisted in `Suspension` or the snapshot.
- **Why it is simpler:** history is valid by construction after every exit path instead of by the host's repair; Resume dispatches from an explicit set instead of recomputing the whole batch.
- **Implementation scope:** 40-70 lines in `orchestrator/orchestrator.go` plus `Snapshot`/`Suspension` fields in `orchestrator/session.go` and tests.
- **Smallest credible slice:** `executeTools` only: on mid-batch cancellation synthesize an `IsError` tool_result for every not-yet-run tool_use, append it with the real results, then return the error. No exported signature or persisted format changes. About 35-45 lines plus one test. Resume retry and the checkpoint race are the second slice.
- **Regression risks:** history after a cancelled batch changes shape; durable-session tests that assume full re-execution must change deliberately; the approval binding in mux#t0qf interacts with any per-call completion record.
- **Migration concerns:** the second slice adds fields to `Suspension` or `Snapshot`; zero-value compatible, but pair it with SIFT-SUB-07-01's version field.
- **Existing validation:** `orchestrator/orchestrator_test.go` and `session_internal_test.go` cover suspend and resume on the happy path.
- **Additional validation required:** cancel after tool 1 of 2 and assert a valid turn; suspend with two pending approvals, cancel during the second, retry Resume and assert the first tool ran once; a failing StatusRunning checkpoint.
- **Impact:** high. **Confidence:** high. **Implementation effort:** medium. **Blast radius:** cross-subsystem (orchestrator, session, hooks consumers). **Prerequisites:** none; coordinate with mux#5ez6, whose fix lands at `orchestrator.go:581-587` inside the same function (the ranking pass flagged the shared span 565-591 for one PR or a joint review).

#### SIFT-SUB-06-02 · Route every state change through the validated StateMachine path
- **Authoritative subsystem:** SUB-06. **Verdict:** recommend. **Kata:** mux#kzxd (P2). **Priority:** rank 18 of 33.
- **Primary evidence:** `validTransitions` (`orchestrator/state.go:22-29`) lets `StateComplete` reach only `StateIdle`; `Transition` (50-61) validates, `Reset` (81-86) validates and publishes nothing; `Orchestrator.transition()` (`orchestrator/orchestrator.go:595-602`) is the only validating and publishing path; `handleError` (604-608) calls `Transition(StateError)` directly and discards the error. Four raw `Reset()` sites (`:214, 235, 377, 685`), one from `StateStreaming` (369-381). `ForceState` (`state.go:64-71`) is exported and test-only.
- **Interfaces and call sites:** `Orchestrator.State()`, `StateChangeEvent`; no production consumer of `State()` in `agent/` or `examples/`.
- **Tests and intent evidence:** `orchestrator/orchestrator_test.go:70-86` and `coverage_test.go:286-315` enumerate Error-reachable states without Complete; `docs/superpowers/specs/2026-06-18-mux-durable-sessions-design.md` cites `state.go:24-25` as the safety argument for AwaitingApproval.
- **Current representation:** one validated write path, three unvalidated ones (`handleError`, `Reset`, `ForceState`).
- **Current complexity or invalid states:** `transition(StateComplete)` at `:383` succeeds, the checkpoint at `:387` fails, `handleError` attempts Complete to Error, the table forbids it, and `State()` says Complete while `Run` returns an error; the Streaming to Idle hop at `:377` is not in the table.
- **Why it is material:** the table is treated as authoritative by the design record but does not govern every write; the contradiction is latent only because nobody reads `State()` yet.
- **Proposed representation:** `StateMachine` owns an onChange hook invoked by `Transition` and `Reset`; `Orchestrator.transition()` is deleted; a recorded policy for post-Complete checkpoint failure; `ForceState` moved behind an `export_test.go` helper.
- **Why it is simpler:** one write path, one table, one event source.
- **Implementation scope:** 40-60 lines across `orchestrator/state.go` and `orchestrator/orchestrator.go`.
- **Smallest credible slice:** the onChange hook plus deleting `Orchestrator.transition()`; the Complete/Error policy and the `ForceState` move follow.
- **Regression risks:** event ordering relative to checkpoints changes for observers that relied on publish-before-checkpoint (mux#t0av); the two `ForceState` test call sites break.
- **Migration concerns:** not applicable; no persisted shape changes unless `StatusError` is added (SIFT-SUB-07-01 decision).
- **Existing validation:** the two transition-table tests above.
- **Additional validation required:** a test with a failing StatusComplete checkpoint asserting `State()` and the returned error agree.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** medium. **Blast radius:** subsystem. **Prerequisites:** none; distinct from mux#t0av (reordering publish and checkpoint does not remove the forbidden hop).

#### SIFT-SUB-07-01 · Validate the Snapshot invariant and version it at the Store boundary
- **Authoritative subsystem:** SUB-07 Durable sessions. **Verdict:** recommend. **Kata:** mux#mmzv (P2). **Priority:** rank 9 of 33.
- **Primary evidence:** `orchestrator/session.go:49-58` declares `Status` and `Suspension` as independent fields with no `Validate` and no constructor; `Suspension.Pending` is `omitempty` (`:46`); only `Resume` checks part of the rule (`orchestrator/orchestrator.go:673-675`); `FileStore.Load` (`session/file_store.go:65-82`) decodes anything; `Snapshot` carries no schema version.
- **Interfaces and call sites:** `SessionStore.Save`/`Load`, `orchestrator.snapshot()` (the only producer today), the planned HTTP resume API.
- **Tests and intent evidence:** the design record states the invariant verbatim (`docs/superpowers/specs/2026-06-18-mux-durable-sessions-design.md:114`); its fourth status `StatusError` (`:88-91`) never shipped (`session.go:17-21` has three); fixtures at `orchestrator/orchestrator_test.go:2449-2465` and `session/file_store_test.go:17-32` already satisfy the rule.
- **Current representation:** two independent fields whose product includes forbidden combinations, and a file format with no version.
- **Current complexity or invalid states:** `{"status":"suspended","suspension":{"reason":"authorization.required"}}` loads cleanly and passes Resume's guard with nothing pending; Running or Complete with a populated Suspension is never rejected; an older or newer file is indistinguishable.
- **Why it is material:** the Store boundary is where every other producer (migration, hand edit, remote API) will arrive, and today it checks nothing.
- **Proposed representation:** `func (s *Snapshot) Validate() error` encoding Status == Suspended iff Suspension != nil and Suspended implies pending calls; `FileStore.Save` calls it before marshaling (`file_store.go:41`) and `Load` after unmarshaling (77-81); a `Version` field written by Save and checked at the same boundary.
- **Why it is simpler:** one rule in one place replaces a partial check in one consumer.
- **Implementation scope:** 20-30 lines in `orchestrator/session.go`, about 6 in `session/file_store.go`.
- **Smallest credible slice:** `Validate` plus its call in `Load`; the version field and the `StatusError` decision follow.
- **Regression risks:** existing snapshot files that violate the invariant become unloadable; decide whether Load rejects or repairs them.
- **Migration concerns:** the version field is additive and zero-value compatible; the decision on `StatusError` changes the persisted enum.
- **Existing validation:** `orchestrator/session_internal_test.go`, `session/file_store_test.go`.
- **Additional validation required:** one test per forbidden combination and one for a mismatched version.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** subsystem. **Prerequisites:** none; coordinate with SIFT-SUB-10-01 (same ApprovalSuspend/no-store gap from the other side).

#### SIFT-SUB-08-01 · Replace the dead recent-user-message budget with one block-level sanitizer
- **Authoritative subsystem:** SUB-08 Compaction and token accounting. **Verdict:** recommend. **Kata:** mux#0183 (P2). **Priority:** rank 4 of 33.
- **Primary evidence:** `compact()` (`orchestrator/compact.go:72-77`) calls `collectRecentUserMessages(CompactUserMessageMaxTokens)`; the collector (131-167) builds a token-bounded reversed list; `buildCompactedHistory` (172-187) uses only `recentUserMsgs[len-1]` at `:183`. `CompactUserMessageMaxTokens` (`orchestrator/orchestrator.go:61-62`) is exported and has no effect for any positive value. `hasUserContent` (`compact.go:119-129`) is a whole-message predicate. `CompactionResult` (`compact.go:29-35`) and `hooks.CompactionEvent` (`hooks/hooks.go:82-89`) are copied field by field at `orchestrator.go:313-319`.
- **Interfaces and call sites:** the compaction step of `Run`; the exported constant.
- **Tests and intent evidence:** `TestCollectRecentUserMessages` (`orchestrator/compact_test.go:155-185`) proves the collector gathers messages the call site discards; commit 1de0dfa patched the pure-carrier case and mux#m886 is the mixed case, both at the wrong granularity; `compact_test.go:408` and `:441` pin the orphan guarantees.
- **Current representation:** about 35 lines and an exported knob to pick the most recent user message, plus a message-level content predicate.
- **Current complexity or invalid states:** a retained user message can still carry a tool_result whose tool_use was compacted away (mux#m886); the knob promises a budget it never applies.
- **Why it is material:** every long session runs this path; the open bug is a symptom of the granularity, so a third patch on `hasUserContent` would not close the class.
- **Proposed representation:** one function that returns the most recent user message with tool_result blocks stripped into a fresh slice; the collector, the predicate and the budget bookkeeping deleted; the knob removed or repurposed; one conversion function (or a shared type in `hooks`) for the compaction event.
- **Why it is simpler:** net removal of about 30 lines and one rule at the granularity the invariant actually has.
- **Implementation scope:** net removal of about 30 lines in `orchestrator/compact.go` plus test rewrites.
- **Smallest credible slice:** the sanitizer replacing the three functions, existing orphan tests ported, the mixed-block case from mux#m886 added.
- **Regression risks:** compaction output changes for mixed user messages (tool_result dropped, text kept); never mutate the shared backing array.
- **Migration concerns:** removing an exported constant is a breaking-by-removal change with low real risk since it never worked; CHANGELOG line.
- **Existing validation:** `compact_test.go:155-185, 408, 441`; `compact_integration_test.go`.
- **Additional validation required:** the mixed-block case; a test that the knob's removal changes nothing.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** subsystem. **Prerequisites:** none; supersedes a standalone mux#m886 patch (comment posted there).

#### SIFT-SUB-09-01 · Make EventBus.Close clear its subscribers so Reset cannot close them twice
- **Authoritative subsystem:** SUB-09 Event bus. **Verdict:** recommend, delivered as a comment on mux#1wwm (no new issue: it is that issue's fix). **Priority:** rank 26 of 33.
- **Primary evidence:** `Close` (`orchestrator/events.go:137-148`) sets `closed = true` but leaves the now-closed channels in `eb.subscribers`; `Reset` (151-162) closes every retained channel again before clearing the slice.
- **Interfaces and call sites:** `Reset` is called only by the orchestrator (`orchestrator/orchestrator.go:218, 239, 686`), so the panic is reachable only for a host holding the bus directly; `Subscribe` after `Close` returns a pre-closed channel (`orchestrator_test.go:1077-1081, 1547-1552`).
- **Tests and intent evidence:** `orchestrator/orchestrator_test.go:1041` (TestEventBusCleanup) through `:1726` (TestEventBusRaceConditions).
- **Current representation:** a `closed` flag beside a slice that can still hold closed channels.
- **Current complexity or invalid states:** `closed == true` with a non-empty subscriber slice is representable and nothing names it; the second close panics.
- **Why it is material:** it is the root of an open issue and the fix removes the state instead of guarding it.
- **Proposed representation:** `Close` clears `eb.subscribers` after closing them; `Reset` reuses the same close-and-clear step.
- **Why it is simpler:** the invalid state cannot be built; no new guard.
- **Implementation scope:** under 10 lines.
- **Smallest credible slice:** the whole change.
- **Regression risks:** none identified; `Subscribe` does not change.
- **Migration concerns:** not applicable; in-memory state only.
- **Existing validation:** the EventBus tests above.
- **Additional validation required:** one test for Close, then Reset, then Subscribe.
- **Impact:** low. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none.

### 3.3 Agent facade, async handles and transcripts (SUB-10 to SUB-12)

#### SIFT-SUB-10-01 · Give every agent.Config field one stated inheritance policy in SpawnChild
- **Authoritative subsystem:** SUB-10 Agent facade and child spawning. **Verdict:** recommend. **Kata:** mux#qxfb (P2). **Priority:** rank 7 of 33.
- **Primary evidence:** `agent/config.go:14-60` defines 15 fields. `SpawnChild` (`agent/agent.go:224-293`) inherits only Registry (228-230), LLMClient (233-235), HookManager (238-240) and AllowedTools (243-254), and unions DeniedTools (257); SystemPrompt, ApprovalFunc, MaxIterations, Stream, ThinkingSettings, SessionStore, ApprovalMode and Skills reset to zero values. A child with `ApprovalMode: ApprovalSuspend` and no SessionStore panics inside `child.init()` (`agent/agent.go:273`) via `orchestrator.NewWithConfig` (`orchestrator/orchestrator.go:117-119`).
- **Interfaces and call sites:** the `SpawnChild` doc (`agent/agent.go:220-223`) promises "inherited configuration"; `examples/full/main.go:263-267` and `agent/integration_test.go:86-95` spawn children without restating approval settings; the inherited approval-gated tool then fails with `ErrApprovalRequired` (`tool/executor.go:16,115-117`).
- **Tests and intent evidence:** no test sets ApprovalFunc, ApprovalMode or SessionStore on a `SpawnChild` call; the only ApprovalMode/SessionStore reference in agent tests (`agent/agent_test.go:1361-1362`) uses `agent.New`. Only Skills' non-propagation is documented, in `docs/superpowers/specs/2026-06-19-mux-skills-design.md:188-192`.
- **Current representation:** a field-by-field copy with no stated policy, so eight fields are dropped silently.
- **Current complexity or invalid states:** a child that inherits a gated tool without the func cannot call it; ApprovalSuspend without a store is a panic rather than an error although `ErrInvalidChildTools` shows the error convention already exists.
- **Why it is material:** the documented contract and the behaviour disagree on eight of fifteen fields, and adding a field forces no decision anywhere.
- **Proposed representation:** one function that lists every field's policy (inherit-if-zero, union, caller must restate); the SessionStore/ApprovalMode mismatch returns an error from `SpawnChild`.
- **Why it is simpler:** one site decides, the doc can be generated from it, and the panic path joins the existing error path.
- **Implementation scope:** 30-50 lines in `agent/agent.go` plus tests.
- **Smallest credible slice:** record the current per-field behaviour in a test, add the policy function with behaviour unchanged, then return the mismatch error.
- **Regression risks:** fields that were silently inherited or dropped change behaviour for existing child agents; callers that set ApprovalSuspend without a store and today silently cannot suspend start getting an error.
- **Migration concerns:** not applicable; configuration is in memory only.
- **Existing validation:** `agent/agent_test.go` and `agent/integration_test.go` spawn paths.
- **Additional validation required:** a child that inherits an approval-gated tool without restating ApprovalFunc; the mismatch error.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** medium. **Blast radius:** subsystem. **Prerequisites:** none; coordinate with SIFT-SUB-07-01 (the same gap seen from the store side) and mux#t0qf.

#### SIFT-SUB-10-02 · Make child agent IDs unique among siblings
- **Authoritative subsystem:** SUB-10. **Verdict:** recommend. **Kata:** mux#aezg (P3). **Priority:** rank 29 of 33.
- **Primary evidence:** `childID := a.id + "." + cfg.Name` (`agent/agent.go:260`) with no sibling check; `Preset.Apply` (`agent/presets.go:123-147`) fills Name with the preset name and `SpawnExplorer` and friends (`agent/presets.go:180-202`) pass it straight to `SpawnChild`, so two `SpawnExplorer(Config{})` calls yield the same ID.
- **Interfaces and call sites:** `Agent.ID()` is the correlation key for `SubagentStartEvent.ChildID` (`agent/agent.go:284`) and `SubagentStopEvent.ChildID` (`agent/agent.go:400`; `hooks/hooks.go:68-80`); `Children()` and `RemoveChild` key on pointer identity, so the collision is invisible inside the package.
- **Tests and intent evidence:** `TestConcurrentChildSpawning` (`agent/agent_test.go:874`) spawns 100 siblings named "child" and asserts only the count; the subtest at `agent/agent_test.go:836` named "empty name is allowed (gets parent prefix)" sets `Name: "valid"`, so the empty-name case (ID "parent.") is untested.
- **Current representation:** an ID derived from a user-supplied name with no uniqueness rule.
- **Current complexity or invalid states:** two live agents with one ID; hook consumers keyed on ChildID conflate them.
- **Why it is material:** the default preset path produces the collision, and the field exists only to correlate.
- **Proposed representation:** disambiguate at the append site (`agent/agent.go:276-278`, numeric suffix on collision) or reject duplicate sibling names with an error.
- **Why it is simpler:** the ID means one agent again; no consumer needs a workaround.
- **Implementation scope:** 10-15 lines.
- **Smallest credible slice:** the suffix on collision plus the two assertions.
- **Regression risks:** an ID format change affects transcripts and hosts keyed on child IDs; keep the parent-prefix scheme and add a suffix only on collision.
- **Migration concerns:** not applicable; IDs are not persisted by this repository.
- **Existing validation:** `TestConcurrentChildSpawning`.
- **Additional validation required:** it asserts 100 distinct IDs; one test covers the empty name.
- **Impact:** low. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none.

#### SIFT-SUB-11-01 · Collapse the three async launchers into one helper
- **Authoritative subsystem:** SUB-11 Async run handles. **Verdict:** recommend. **Kata:** mux#qg5t (P2). **Priority:** rank 15 of 33.
- **Primary evidence:** `RunAsync` (`agent/async.go:149-175`), `ContinueAsync` (177-200) and `RunChildAsync` (202-225) are the same block of about 20 lines, differing only in the handle's agent field and the wrapped call; only the first copy carries the CAS explanation (163-165).
- **Interfaces and call sites:** the three exported launchers keep their signatures; `Resume` (`agent/agent.go:160-164`) has no async counterpart.
- **Tests and intent evidence:** two earlier fixes were applied three times each: 5bae4e2 added WithCancel and deferred cancel to all three, 1e3d169 added the CAS short-circuit to all three. `agent/async_test.go:329-331` pins that `RunChildAsync` stores the child.
- **Current representation:** three copies of the launch lifecycle.
- **Current complexity or invalid states:** every lifecycle fix is a three-site change and two already were.
- **Why it is material:** mux#y0j9 (the RunHandle.Cancel fix) lands in this lifecycle and would otherwise be a fourth triple edit.
- **Proposed representation:** an unexported `newRunHandle(ctx, agent, run func(context.Context) error) *RunHandle` that owns context derivation, the Pending to Running CAS, deferred cancel and setComplete; the wrappers pass the right agent; the closure uses the derived ctx.
- **Why it is simpler:** about 75 lines collapse to about 30, and `ResumeAsync` becomes a three-line wrapper if a host needs it.
- **Implementation scope:** about 75 lines collapse to about 30 in `agent/async.go`.
- **Smallest credible slice:** the helper with the three wrappers; `agent/async_test.go` passes unchanged under -race.
- **Regression risks:** the launchers differ in small steps (child registration, status transitions) and the helper must reproduce each.
- **Migration concerns:** not applicable; no exported signature changes.
- **Existing validation:** `agent/async_test.go`.
- **Additional validation required:** one test per launcher for its differing step.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none; mux#y0j9 lands inside it.

#### SIFT-SUB-12-01 · Declare the transcript header and entry shapes once, with a format version
- **Authoritative subsystem:** SUB-12 Transcripts. **Verdict:** recommend. **Kata:** mux#yp6a (P2). **Priority:** rank 12 of 33.
- **Primary evidence:** `Transcript` (`agent/transcript.go:24-29`) and `TranscriptEntry` (17-21) are redeclared as anonymous structs in `SaveJSONL` (105-115 header, 122-132 entry) and again in `LoadJSONL` (167-174, 180-187): four hand-maintained copies of two shapes. `SaveJSON` and `LoadJSON` (83-97) encode the real struct. Neither form carries a format version.
- **Interfaces and call sites:** the four exported Save/Load functions; DEPENDENTS.md lists transcript persistence as a public feature.
- **Tests and intent evidence:** round-trip tests at `agent/transcript_test.go:123-152, 186-211, 255-272, 274-288, 290-338`.
- **Current representation:** four anonymous copies of two shapes plus the real types.
- **Current complexity or invalid states:** a field added to `TranscriptEntry` (as the mux#bq4e fix will add) never reaches JSONL; a loader cannot tell which shape it is reading.
- **Why it is material:** the next required change to this file will silently miss one format.
- **Proposed representation:** package-level record types that embed `TranscriptEntry` and the transcript metadata, used by both JSONL functions; the header carries a format version.
- **Why it is simpler:** one declaration per shape; output stays byte-identical.
- **Implementation scope:** about 20 lines removed.
- **Smallest credible slice:** the record types with byte-identical output verified by the existing round-trip tests; the version field in the same change so one bump covers SIFT-SUB-12-02.
- **Regression risks:** a format version changes the on-disk header; `LoadJSONL` must accept files without it or the change is not additive.
- **Migration concerns:** the version field is the migration hook for both transcript findings.
- **Existing validation:** the round-trip tests above.
- **Additional validation required:** a load of a pre-version file; a load of an unknown version.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none; precedes SIFT-SUB-12-02 and lands before or with mux#bq4e.

#### SIFT-SUB-12-02 · Store llm.Message in the transcript instead of a hand-mirrored entry
- **Authoritative subsystem:** SUB-12. **Verdict:** recommend. **Kata:** mux#2zdv (P2; the ranking pass called it "arguably P1"). **Priority:** rank 2 of 33.
- **Primary evidence:** `TranscriptEntry{Timestamp, Role string, Content []llm.ContentBlock}` (`agent/transcript.go:17-21`) mirrors `llm.Message{Role, Content string, Blocks}` (`llm/types.go:69-74`) with a same-named, differently typed Content field; `FromMessages` (43-53) and `Append` (68-75) copy only Blocks, while `NewUserMessage` and `NewAssistantMessage` (`llm/types.go:77-84`) populate Content and leave Blocks nil, so ordinary turns serialize as `"content":null` (mux#bq4e's root cause).
- **Interfaces and call sites:** `orchestrator.Snapshot.Messages` already persists the real `llm.Message` (`orchestrator/session.go:53`, marshaled as-is by `session/file_store.go:49`); DEPENDENTS.md lists transcript persistence (v0.6.0) as a public feature with external consumers this repository cannot verify.
- **Tests and intent evidence:** fixtures at `agent/transcript_test.go:57-60, 256-260, 275-278, 292-308`; `docs/superpowers/specs/2026-06-18-mux-durable-sessions-design.md:134-136` claims the two persisted forms share "a single on-disk message shape", which is false today.
- **Current representation:** a partial mirror of `llm.Message` that drops the field ordinary turns use.
- **Current complexity or invalid states:** every text-only turn is stored with null content; any future `llm.Message` field is dropped by default.
- **Why it is material:** the persisted record of a session is wrong for the common case, and the design record claims the opposite.
- **Proposed representation:** `TranscriptEntry{Timestamp; Message llm.Message}` (embedded) with the format version from SIFT-SUB-12-01, or the mirror kept with a widened field and a comment that the wire format is frozen.
- **Why it is simpler:** one message shape on disk, as the design record already says.
- **Implementation scope:** 20-40 lines plus fixtures.
- **Smallest credible slice:** replace Content with the embedded message, bump the header version together with SIFT-SUB-12-01, no auto-upgrade of old files; about 25-35 lines plus fixtures and the round-trip test asserting `Message.Content` survives.
- **Regression risks:** external readers of the transcript format break on the shape change.
- **Migration concerns:** on-disk format change; version bump and a CHANGELOG entry in the breaking batch (§6); decide whether old files load or are rejected.
- **Existing validation:** the round-trip tests above.
- **Additional validation required:** the `Message.Content` round trip (mux#bq4e's acceptance); a load of a pre-version file.
- **Impact:** high. **Confidence:** high. **Implementation effort:** small. **Blast radius:** subsystem, plus external transcript readers. **Prerequisites:** SIFT-SUB-12-01 lands with or before it.

### 3.4 Tool execution, permissions and hooks (SUB-13 to SUB-15)

#### SIFT-SUB-13-01 · Guarantee a non-nil Result from Executor.Execute
- **Authoritative subsystem:** SUB-13 Tool registry and executor. **Verdict:** recommend. **Kata:** mux#9p0e (P3). **Priority:** rank 28 of 33.
- **Primary evidence:** `tool/tool.go:23-24` documents `Execute` without saying whether a nil Result is legal; `tool/executor.go:141` stores whatever the tool returned and `:155` passes it through; the after-hooks at `tool/executor.go:143-153` already receive the possibly nil pointer. The only guard is one consumer, `orchestrator/orchestrator.go:577-580`, which substitutes a struct literal whose Metadata is nil, unlike results built by `tool.NewResult` or `tool.NewErrorResult` (`tool/result.go:15-33`).
- **Interfaces and call sites:** `Executor.Execute`; `examples/full/main.go:333-338` dereferences `ev.Result` without a nil check; `tool/result.go:6-12` carries no field docs.
- **Tests and intent evidence:** no test in orchestrator or tool exercises a (nil, nil) tool.
- **Current representation:** the guarantee lives in one consumer and produces a differently built Result.
- **Current complexity or invalid states:** a (nil, nil) tool panics an after-hook (recovered and printed to stderr at `tool/executor.go:148`) and then reaches the model as an empty success.
- **Why it is material:** every tool call passes through `Execute`; the invariant belongs there, not in one of its callers.
- **Proposed representation:** `Executor.Execute` substitutes `tool.NewResult(toolName, true, "", "")` before the after-hooks run and documents the guarantee; the orchestrator guard becomes a comment.
- **Why it is simpler:** one guarantee at the one path, one way to build a Result.
- **Implementation scope:** 10-15 lines plus one test.
- **Smallest credible slice:** the substitution and the doc line; remove the consumer guard after the guarantee exists.
- **Regression risks:** hooks and events now see a synthesized Result for (nil, nil) tools.
- **Migration concerns:** not applicable; in-memory results only.
- **Existing validation:** `tool/executor_test.go` paths for error and success results.
- **Additional validation required:** one Executor test for the (nil, nil) case with an after-hook.
- **Impact:** low. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none.

#### SIFT-SUB-13-02 · Treat a nil SchemaProvider result like a missing schema
- **Authoritative subsystem:** SUB-13 (merged with SUB-17 recommendation 1; the adapter is the concrete producer, the fix site is in SUB-06's boundary). **Verdict:** recommend. **Kata:** mux#ghcv (P3). **Priority:** rank 27 of 33.
- **Primary evidence:** `buildToolDefinitions` (`orchestrator/orchestrator.go:505-533`) builds the default schema at 514-518 then overwrites it with `sp.InputSchema()` unconditionally at 520-521; the warning at 522-527 fires only for tools that do not implement the interface. `mcp/adapter.go:82` returns `a.info.InputSchema` verbatim, nil whenever the server's tools/list entry carried no inputSchema. `llm/gemini.go:105-106` omits the function's parameters entirely when the schema is nil.
- **Interfaces and call sites:** `tool.SchemaProvider` (`tool/tool.go:27-30`, no nil rule); every MCP-adapted tool.
- **Tests and intent evidence:** the only test of this path, `orchestrator/coverage_test.go:141-153`, always returns a non-nil schema.
- **Current representation:** "has a SchemaProvider" and "has a schema" are treated as the same state.
- **Current complexity or invalid states:** a schema-less MCP tool is sent to Gemini with no parameters and no warning.
- **Why it is material:** the model is told nothing about the tool's arguments and nothing explains why.
- **Proposed representation:** nil from `InputSchema()` means "no schema": keep the default and emit the same warning; the doc says so.
- **Why it is simpler:** one rule for the missing case instead of two states that differ only by interface presence.
- **Implementation scope:** 5-10 lines plus one test.
- **Smallest credible slice:** the whole change.
- **Regression risks:** tools returning nil today are sent with the default object schema instead of nothing; check each `SchemaProvider` implementation.
- **Migration concerns:** not applicable; request shape only.
- **Existing validation:** `orchestrator/coverage_test.go:141-153`.
- **Additional validation required:** one test with a nil-returning provider.
- **Impact:** low. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none; related to mux#2jw4.

#### SIFT-SUB-14-01 · Make permission.Checker's Ask outcome reach a prompt and state rule precedence
- **Authoritative subsystem:** SUB-14 Permission checker. **Verdict:** recommend. **Kata:** mux#j6kd (P2). **Priority:** rank 11 of 33.
- **Primary evidence:** `permission/checker.go:49-66` returns only (true, nil) or (false, nil); in ModeAsk an unmatched tool is denied at line 65 under the comment "The actual prompting is handled by the executor's approval function", but every wiring in the repository (root `integration_test.go:84, 304, 433, 458`) installs `Check` as the whole ApprovalFunc, so nothing is left to prompt. The first matching rule wins (`permission/checker.go:57-61`), so Allow then Deny for one tool allows it. The package has no production importer.
- **Interfaces and call sites:** `Checker.Check` (exported, nine test call sites: five in `permission/permission_test.go`, four in the root `integration_test.go`); `tool.ApprovalFunc`.
- **Tests and intent evidence:** no test in `permission/permission_test.go` has two rules for one tool. `permission/mode.go:1-2` says "Ask (prompt user)", `README.md:15` advertises built-in approval flows, `CHANGELOG.md:147` calls the checker an interface (it is a struct, `permission/checker.go:27`); the original design (`docs/plans/2025-12-13-mux-library.md:2805-2826`) carries the same code and the same "prompting happens elsewhere" comment.
- **Current representation:** a two-valued answer for a three-valued question, and an undocumented precedence.
- **Current complexity or invalid states:** "ask" is indistinguishable from "deny"; a rule set's outcome depends on insertion order nobody documented.
- **Why it is material:** the documented composition cannot be built, and the README sells it.
- **Proposed representation:** a three-valued outcome (allow, deny, ask) or an (allowed, matched) pair; one helper or documented pattern composes a Checker with an interactive ApprovalFunc; precedence is last-wins or documented first-wins with a test.
- **Why it is simpler:** the type says what the caller must do next; no comment points at code that does not exist.
- **Implementation scope:** 30-50 lines plus tests and doc lines.
- **Smallest credible slice:** the outcome type and the composition helper with the nine call sites updated; the precedence decision in the same change.
- **Regression risks:** a public signature change on `Check`; changing precedence alters outcomes for existing rule sets.
- **Migration concerns:** breaking API change for external callers of `Check`; belongs in the breaking batch (§6).
- **Existing validation:** `permission/permission_test.go`; root `integration_test.go`.
- **Additional validation required:** two-rules-one-tool precedence test; an end-to-end test where ModeAsk reaches a prompt.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** medium. **Blast radius:** subsystem, breaking signature. **Prerequisites:** none; coordinate with mux#datc and mux#t0qf; mux#3ctg owns the README claim.

#### SIFT-SUB-15-01 · Dispatch all seven hook event types through one helper
- **Authoritative subsystem:** SUB-15 Hooks manager. **Verdict:** recommend. **Kata:** mux#7gb0 (P3). **Priority:** rank 20 of 33.
- **Primary evidence:** `hooks/hooks.go` carries seven copies of one method body: `FireSessionStart` 189-204, `FireSessionEnd` 207-222, `FireStop` 227-245, `FireIteration` 248-263, `FireSubagentStart` 266-281, `FireSubagentStop` 284-299, `FireCompaction` 302-317; each repeats RLock, copy, RUnlock, iterate, type-assert, return on first error. `FireStop` adds the Continue latch.
- **Interfaces and call sites:** `orchestrator/orchestrator.go:258,281,321,330,371` and `agent/agent.go:289,405`; no exported signature changes.
- **Tests and intent evidence:** `hooks/hooks_test.go` covers copy-before-unlock, first-error return and the sticky Continue; the refactor was scoped as behaviour-preserving in `docs/superpowers/specs/2026-06-05-mux-robustness-design.md:89` and never done.
- **Current representation:** seven hand-kept copies of the dispatch contract.
- **Current complexity or invalid states:** nothing enforces that the seven agree; the coordinator verified they currently do.
- **Why it is material:** the two open hook-lifecycle changes (mux#yr10 and the FireStop latch issue) each touch the contract once per copy.
- **Proposed representation:** one unexported generic `fireHooks[H Hook](m *Manager, t EventType, apply func(H) error) error`; each `Fire*` becomes a one-to-three line wrapper; `FireStop` keeps its latch in its wrapper.
- **Why it is simpler:** about 130 lines removed, 30 added, one place for the lock and short-circuit rules.
- **Implementation scope:** about 30 lines added, about 130 removed, one file.
- **Smallest credible slice:** the whole change; existing tests pass unchanged under -race.
- **Regression risks:** the latch and the short-circuit differ per event and the helper must keep each documented semantic.
- **Migration concerns:** not applicable; no exported change.
- **Existing validation:** `hooks/hooks_test.go`.
- **Additional validation required:** none beyond the existing suite; each `Fire*` keeps its own test.
- **Impact:** low. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none; mux#yr10 lands inside it.

#### SIFT-SUB-15-02 · Type SessionEndEvent.Reason and test the suspended value
- **Authoritative subsystem:** SUB-15. **Verdict:** recommend. **Kata:** mux#s0st (P3). **Priority:** rank 21 of 33.
- **Primary evidence:** `hooks/hooks.go:42-47` declares Reason as a bare string documenting three values; the only producer, `orchestrator/orchestrator.go:265-278`, emits a fourth, "suspended", when `errors.As` finds an `orchestrator.Suspended` error.
- **Interfaces and call sites:** `SessionEndEvent.Reason` (exported); hooks cannot import orchestrator (cycle), so the type lives in hooks.
- **Tests and intent evidence:** no test asserts `Reason == "suspended"` (git grep over `*_test.go` finds none); `orchestrator/hooks_integration_test.go` has complete, error and cancelled cases only. `orchestrator/session.go:13-30` already uses the named-string-plus-constants convention.
- **Current representation:** an open string with a doc comment that is one value short.
- **Current complexity or invalid states:** a consumer written against the doc treats the suspended end as unknown.
- **Why it is material:** the durable-sessions feature added a value the type does not name and no test pins.
- **Proposed representation:** `type SessionEndReason string` with four exported constants in hooks, used by the field and the producer.
- **Why it is simpler:** the set is closed and named in one place, following house style.
- **Implementation scope:** 10-20 lines plus one test.
- **Smallest credible slice:** the whole change.
- **Regression risks:** the exported field type changes; hosts comparing against string literals keep compiling (untyped constants) but should switch.
- **Migration concerns:** not applicable; not persisted.
- **Existing validation:** `orchestrator/hooks_integration_test.go`.
- **Additional validation required:** one test asserting the suspended reason via ApprovalSuspend.
- **Impact:** low. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local, exported field type. **Prerequisites:** none; related to mux#jc2v and mux#t0qf.

### 3.5 MCP transports, adapter and types (SUB-16 to SUB-17)

#### SIFT-SUB-16-01 · Own transport lifecycle state in one place for stdio and HTTP clients
- **Authoritative subsystem:** SUB-16 MCP transports. **Verdict:** recommend. **Kata:** mux#nrhn (P1). **Priority:** rank 6 of 33.
- **Primary evidence (stdio):** state is spread across `running` (`mcp/stdio.go:32`) and two single-use channels, `closeChan` and `done` (33-34), allocated once in `newStdioClient` (42) and never recreated. `Start` (53-107) guards only `if c.running` (55-58), so a second Start after Close reuses the closed `closeChan`: its initialize call selects that channel in `call` (175-176), fails with `errClientClosed`, and Start calls Close again (103), which re-closes `closeChan` at line 234 (the panic in mux#s53f). `readResponses` (199-224) closes `done` on exit but never flips `running` or fails `c.pending`, and `call`'s select (167-177) does not watch `done`, so a call under `context.Background()` hangs after the child exits (mux#64sw). Every Start launches a new reader (99) whose deferred `close(c.done)` (200) targets the channel allocated once at line 43, so a restart panics on the second reader's exit with no second Close needed.
- **Primary evidence (HTTP):** four uncoupled fields, `running`, `sessionID`, `closeOnce`, `notifyClosed` (`mcp/http.go:22-34`). `Start` (46-80) sets `running = true` at line 71 before the initialized notification at line 75, so a failed notification leaves a half-initialized client that `ListTools` and `CallTool` accept (222-227, 246-251). `Close` (274-286) runs under `closeOnce`, so after Start, Close, Start the second Close is a no-op and never cancels in-flight requests (mux#xxrt).
- **Interfaces and call sites:** `mcp/client.go` (no exported signature changes); the exported `ErrTransportClosed` (`mcp/types.go:15`) has no production return site: stdio uses the unexported `errClientClosed` (`mcp/stdio.go:20`) and HTTP an ad hoc `fmt.Errorf` (`mcp/http.go:50`).
- **Tests and intent evidence:** `mcp/mcp_test.go:204-231` asserts the literal "client already running"; `:1880-1905` and `:1909` exercise init-failure teardown; `mcp/http_test.go:385` and `:416-478` cover double Close and Close-during-notification. Commits d527669, 40defbf, c0efd4b, 13899dc, 1129286 and c4f7453 each hardened one corner; none introduced an owned state value.
- **Current representation:** booleans, single-use channels and a `sync.Once` standing in for a lifecycle.
- **Current complexity or invalid states:** closed with `running` still set; started but not initialized (HTTP); reader gone with calls still pending; two independent restart panics.
- **Why it is material:** four filed defects (mux#s53f, mux#64sw, mux#xxrt, mux#vfwt) share this root; each earlier patch fixed one corner and left the others.
- **Proposed representation:** an unexported enum (idle, running, closed) per client, changed only inside one guarded method; `Start` returns distinct errors for running and closed; stdio creates its channels when entering running (or rejects restart outright, a product decision mux#s53f also leaves open); the reader's exit transitions to closed and drains pending calls with a transport-closed error; HTTP keeps a `CancelFunc` that Close invokes; both transports return `ErrTransportClosed`.
- **Why it is simpler:** one state value replaces three flags, two channels and a once; the four symptom fixes become one change.
- **Implementation scope:** 100-150 lines across `mcp/stdio.go` and `mcp/http.go`.
- **Smallest credible slice:** the stdio enum with the restart and reader-exit transitions, which covers mux#s53f and mux#64sw; HTTP in a second slice.
- **Regression risks:** error timing changes for concurrent Close and Call; the literal-message assertion must be updated deliberately.
- **Migration concerns:** not applicable; in-process state only, no wire or file format.
- **Existing validation:** the lifecycle tests above.
- **Additional validation required:** the three filed reproductions (s53f, 64sw, xxrt) as tests against the new state model, under -race.
- **Impact:** high. **Confidence:** high. **Implementation effort:** large. **Blast radius:** subsystem. **Prerequisites:** none; lands before SIFT-SUB-16-02 and mux#rqxt, which rewrite the same files (re-cite line numbers after each lands).

#### SIFT-SUB-16-02 · Build notifications, response matching and headers once for both transports
- **Authoritative subsystem:** SUB-16. **Verdict:** recommend. **Kata:** mux#6aqg (P2). **Priority:** rank 8 of 33.
- **Primary evidence:** `mcp/stdio.go:180-183` and `mcp/http.go:181-182` each build `&Request{JSONRPC: "2.0", Method: method, Params: params}`; `Request.ID` (`mcp/types.go:23`) is `uint64` without `omitempty`, so every notification goes out with `"id":0`, which JSON-RPC forbids (mux#xat1). The correct wire shape already exists as `Notification` (`mcp/types.go:114-117`) but is used only for receiving (`mcp/http.go:147`). Inside `post`, the SSE branch checks `candidate.ID == req.ID` (`mcp/http.go:165`) while the plain-JSON branch decodes straight into the response with no check (171-175; mux#ns1f). `post` sets Accept at 96-98; `notify` (181-218) sets Content-Type and MCP-Protocol-Version at 194-195 but not Accept (the 406 half of mux#xat1). Line 145 accepts only `event.Event == "message"` and `mcp/sse.go:12-15` never applies the SSE default event name (the default-event half of mux#whef).
- **Interfaces and call sites:** both transports' `notify`; both `post` branches; no change to the `Client` interface. `mcp/types.go` is SUB-17's file, so the helper's home is a shared decision.
- **Tests and intent evidence:** `mcp/mcp_test.go:2033-2044` (TestNotificationType) already decodes into `Notification`; `mcp/http_test.go:202` and `:307` cover the SSE and JSON branches. The design record agrees with this finding: `docs/plans/2025-12-29-streamable-http-transport.md:151` routes id-less messages to notifications, `:142-145` declares `Notification` without an ID field, `:97` lists Accept among the required headers.
- **Current representation:** each transport hand-builds the same three wire details, and the two copies already disagree.
- **Current complexity or invalid states:** a notification with an id; a response accepted for the wrong request; a request without the header the spec requires.
- **Why it is material:** three open issues (mux#xat1, mux#ns1f, mux#whef) are each a symptom of one copy drifting.
- **Proposed representation:** a `NewNotification(method string, params any) *Notification` beside `NewRequest` (`mcp/types.go:29-36`) used by both transports; one response-ID match used by both `post` branches; one request builder for headers shared by `post` and `notify`.
- **Why it is simpler:** the wire rules exist once and the existing `Notification` type gets its second half.
- **Implementation scope:** 40-60 lines across `mcp/types.go`, `mcp/stdio.go`, `mcp/http.go`.
- **Smallest credible slice:** the notification constructor with the wire-level test from mux#xat1; the ID match and header builder follow.
- **Regression risks:** the two transports differ in small ways today (default event name, header set); verify the shared helper against both testdata servers.
- **Migration concerns:** not applicable; wire shape moves toward the spec, no persisted data.
- **Existing validation:** the tests above.
- **Additional validation required:** the wire-level tests specified in mux#xat1 and mux#ns1f; grep finds one notification constructor and one response match.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** medium. **Blast radius:** subsystem. **Prerequisites:** after SIFT-SUB-16-01 (same files); mux#6n9g (string JSON-RPC ids) touches the same types.

#### SIFT-SUB-17-01 · Type the MCP transport name and validate ServerConfig in NewClient
- **Authoritative subsystem:** SUB-17 MCP adapter and types. **Verdict:** recommend. **Kata:** mux#865b (P2). **Priority:** rank 14 of 33.
- **Primary evidence:** `mcp/types.go:87-105` is one flat exported struct holding stdio fields (Command, Args, Env), HTTP fields (URL, Headers, commented "HTTP transport fields" at line 95) and `MaxResponseBytes` (documented at 99-103 as stdio-only), discriminated by a bare string `Transport` (line 90). `mcp/client.go:33-42` switches on the literals "stdio", "", "http", "streamable-http" and checks no field. An empty Command reaches `exec.CommandContext` at `mcp/stdio.go:61`; an empty URL reaches `http.NewRequestWithContext` at `mcp/http.go:91` and `:189`.
- **Interfaces and call sites:** `mcp.NewClient`, `mcp.ServerConfig` (exported, built with string literals by consumers).
- **Tests and intent evidence:** `mcp/mcp_test.go:568-612` (TestClientServerConfigValidation, "EmptyCommand") asserts that an empty Command produces a client and no error, so the test's name promises what its assertions disprove; `:2098-2107, 2109-2123, 2125-2159` must still pass. Every other named-string config in the codebase uses a type plus constants (`permission.Mode`, `orchestrator.Status`).
- **Current representation:** a product of both transports' fields discriminated by an unchecked string.
- **Current complexity or invalid states:** `ServerConfig{Transport: "http", Command: "node"}` constructs; Start fails later with a URL parse error that never mentions the missing URL.
- **Why it is material:** every bad config fails late with the wrong message, and the one test of validation asserts the absence of validation.
- **Proposed representation:** `type Transport string` with exported constants; a validate method switching on Transport that requires Command for stdio and URL for HTTP, called from `NewClient`.
- **Why it is simpler:** the constraint is stated once at construction instead of discovered per transport at Start.
- **Implementation scope:** 30-40 lines plus test updates.
- **Smallest credible slice:** the validate method and the inverted EmptyCommand subtest plus an empty-URL case; the typed constants in the same change.
- **Regression risks:** construction becomes stricter for external callers; typing the field changes an exported type.
- **Migration concerns:** keep string constants of the new type so untyped literals still compile; CHANGELOG line in the breaking batch (§6).
- **Existing validation:** `mcp/mcp_test.go:568-612` and the three ranges above.
- **Additional validation required:** the inverted subtest and the empty-URL case.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** subsystem, breaking for callers with bad configs. **Prerequisites:** none; related to mux#kzkx.

### 3.6 Coordinator, examples, developer tooling and documentation (SUB-18 to SUB-23)

#### SIFT-SUB-18-01 · Bound the coordinator cache or make the host's cleanup duty explicit
- **Authoritative subsystem:** SUB-18 Coordinator. **Verdict:** recommend. **Kata:** mux#v028 (P3). **Priority:** rank 30 of 33.
- **Primary evidence:** `coordinator/cache.go:11-12` is an unbounded map; `Get` (31-57) deletes an expired entry only when that key is read again; `Cleanup` (86-87) is documented "Call periodically" and is called only from tests.
- **Interfaces and call sites:** `Cache.Set`, `Get`, `Cleanup`; the lock map in `coordinator/coordinator.go:17` has the same shape but entries leave on Release.
- **Tests and intent evidence:** `coordinator/coordinator_test.go:470` and `:726` are the only Cleanup callers.
- **Current representation:** a TTL that expires reads but not memory.
- **Current complexity or invalid states:** a key written once and never read again stays forever, TTL or not.
- **Why it is material:** the type's own doc promises expiry that the host must implement.
- **Proposed representation:** one of: a janitor owned by the Cache with a Stop method; a maximum entry count with eviction; or a doc on Cache saying the host owns Cleanup, with an example.
- **Why it is simpler:** the expiry rule has one owner instead of a comment and a test.
- **Implementation scope:** 15-30 lines plus one test.
- **Smallest credible slice:** the doc option plus the example if the janitor is not wanted.
- **Regression risks:** a janitor adds a goroutine and a Stop requirement; evicting on Set changes Set's cost.
- **Migration concerns:** not applicable; in-memory cache.
- **Existing validation:** `coordinator/coordinator_test.go` cache tests.
- **Additional validation required:** one test for the chosen behaviour.
- **Impact:** low. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none.

#### SIFT-SUB-20-01 · Confine the full example's file tools or stop claiming they are confined
- **Authoritative subsystem:** SUB-20 Examples. **Verdict:** recommend. **Kata:** mux#3p4f (P2). **Priority:** rank 22 of 33.
- **Primary evidence:** `examples/full/main.go:49-50` says "Clean the path to prevent directory traversal" and calls `filepath.Clean`, which normalizes but does not confine; `ReadTool` then reads any path the process can (line 52) and returns false from `RequiresApproval` (line 42). `WriteTool` repeats the comment and Clean at 94-95 and creates directories and files anywhere (105-114); only its `RequiresApproval` is true (line 83). `SearchTool` in the same file uses `os.OpenRoot` (line 167).
- **Interfaces and call sites:** the interactive approval at `examples/full/main.go:254-258` gates writes and never reads.
- **Tests and intent evidence:** the examples have no tests; the security lens raised it as C3 and the API lens as finding 12 ("examples are the API contract consumers copy").
- **Current representation:** a comment claiming a property the code does not have, three functions away from the code that has it.
- **Current complexity or invalid states:** the documented example reads /etc/passwd without a prompt.
- **Why it is material:** consumers copy the full example; the confined pattern already exists in the file.
- **Proposed representation:** `ReadTool` and `WriteTool` operate through an `os.Root` opened on a configured working directory, or the comment goes and the README says the example is unconfined; reads require approval or the README says why not.
- **Why it is simpler:** one confinement mechanism in the file instead of one real and two claimed.
- **Implementation scope:** 15-25 lines.
- **Smallest credible slice:** the whole change.
- **Regression risks:** example only; error messages change for paths outside the root.
- **Migration concerns:** not applicable; example code.
- **Existing validation:** none.
- **Additional validation required:** a run reading a path outside the root is refused.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none; related to mux#x39z.

#### SIFT-SUB-21-01 · One canonical check that every gate calls
- **Authoritative subsystem:** SUB-21 Build and developer tooling. **Verdict:** recommend. **Kata:** mux#7gbz (P2). **Priority:** rank 23 of 33.
- **Primary evidence:** four invocations disagree. `.git_hooks/pre-commit-tests` runs `go test ./... -short` (no -race) and is wired to nothing: `core.hooksPath` is unset in the repository config, the global config on this machine points at a user-level directory, and `.git/hooks` has no active hook. `.pre-commit-config.yaml:25-28` runs `-race -short -timeout=120s` with an unpinned golangci-lint; its header (line 1) names another project and three excludes (54, 59, 69) name a directory that does not exist. `Makefile:27-28` runs `-race -short` and has no check target. `.github/workflows/ci.yml:35-36` runs `-race -timeout=300s` without `-short` and pins golangci-lint v2.12.2 (line 58).
- **Interfaces and call sites:** every contributor's commit path and CI; `ci.yml:32-33` builds examples via `go build ./...` while the Makefile's examples target (`Makefile:15-24`) never runs in CI.
- **Tests and intent evidence:** the test-quality lens (findings 9, 16, 18, 19) reached the same four-way split independently.
- **Current representation:** four hand-written command lines, one of them dead.
- **Current complexity or invalid states:** a test that fails only without `-short` passes every local gate and fails CI.
- **Why it is material:** the gates disagree on what "the tests" means, and the one in the repository never runs.
- **Proposed representation:** a `make check` target running vet, the pinned golangci-lint and the same `go test` flags CI uses; pre-commit, the Makefile and CI all call it; the dead hook script and dead excludes go; the Go matrix decision is recorded.
- **Why it is simpler:** one command, three callers.
- **Implementation scope:** 20-30 lines across the four files.
- **Smallest credible slice:** the target plus CI calling it; the hook cleanup second.
- **Regression risks:** `-race` on the commit gate slows commits; CI and make must agree on flags or drift returns.
- **Migration concerns:** not applicable; tooling only.
- **Existing validation:** CI green on main.
- **Additional validation required:** the target runs identically locally and in CI.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** application-wide (developer workflow). **Prerequisites:** none; related to mux#sask.

#### SIFT-SUB-22-01 · Fill the six missing CHANGELOG releases and mark the v0.8.0 break
- **Authoritative subsystem:** SUB-22 User-facing documentation. **Verdict:** recommend. **Kata:** mux#qs9q (P2). **Priority:** rank 24 of 33.
- **Primary evidence:** the repository has 16 tags; `CHANGELOG.md` has 11 sections and jumps from 0.9.0 (line 8) to 0.6.0 (line 20). Missing: v0.6.1, v0.6.2, v0.7.0, v0.7.1, v0.8.0 (which added `Capabilities()` to the `llm.Client` interface in 5a7c300, breaking every external implementation) and v0.8.1. No Unreleased section covers v0.9.0..HEAD (4c21f4c, 2c5cdf5, 6f7305e). Four dates disagree with the tags (lines 63, 107, 117, 123); line 133 lists 0.1.0, which has no tag; line 147 calls the permission checker an interface.
- **Interfaces and call sites:** consumers pinning a version (DEPENDENTS.md; mux#zee0).
- **Tests and intent evidence:** the documentation audit compared `git for-each-ref` with the file (sections 2.2 and 4.1).
- **Current representation:** a changelog that skips six releases and hides the one breaking change.
- **Current complexity or invalid states:** a reader cannot learn what changed between the version they pin and v0.9.0.
- **Why it is material:** it is the vehicle every breaking change in §6 needs, and it is already wrong about the last one.
- **Proposed representation:** six sections from the commit history, an Unreleased section, a BREAKING marker under 0.8.0, dates from the tags, 0.1.0 marked untagged or tagged, and a release step that requires an entry per tag.
- **Why it is simpler:** one record of releases that matches the tags.
- **Implementation scope:** 60-90 lines of CHANGELOG text.
- **Smallest credible slice:** the six sections plus the BREAKING marker; the release step second.
- **Regression risks:** documentation only; the dates must come from git tags, not memory.
- **Migration concerns:** not applicable; documentation.
- **Existing validation:** none.
- **Additional validation required:** each new section's content traced to its tag's commits.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none; vehicle for the breaking batch (§6).

#### SIFT-SUB-22-02 · Make the README describe the library that shipped
- **Authoritative subsystem:** SUB-22. **Verdict:** recommend. **Kata:** mux#rqzk (P2). **Priority:** rank 25 of 33.
- **Primary evidence:** `README.md` (26 lines) names or implies four of ten packages; agent (the documented entry point), coordinator, hooks, llm, session and skill are absent. No provider, transport or feature list; no go 1.24 floor (`go.mod:3`); no mention of the two API-key variables only the examples read (`examples/full/main.go:228`, `examples/minimal/main.go:51,54`). Line 14 claims a full MCP server and client (client only, mux#3ctg); line 18 claims a "plugin architecture" (the extension point is `tool.Tool`, `tool/tool.go:12-25`; there is no loader or discovery).
- **Interfaces and call sites:** every prospective consumer.
- **Tests and intent evidence:** documentation audit sections 2.1 and 4.3.
- **Current representation:** a README that describes a different, smaller library with two features it does not have.
- **Current complexity or invalid states:** the entry point package is unnamed; two claims are false.
- **Why it is material:** it is the first document a consumer reads and it overclaims in both directions.
- **Proposed representation:** a package map, a provider and transport list, a one-line feature list, toolchain and environment notes, the two overclaims reworded; kept short.
- **Why it is simpler:** the README says what exists, no more.
- **Implementation scope:** 40-60 lines of README.
- **Smallest credible slice:** the package map and the two rewordings.
- **Regression risks:** documentation only; check every claim against the code when writing.
- **Migration concerns:** not applicable; documentation.
- **Existing validation:** none.
- **Additional validation required:** a claim-by-claim check against the code.
- **Impact:** medium. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none; mux#3ctg owns the MCP-server sentence.

#### SIFT-SUB-23-01 · Correct the six false design-document statements and the stale plan status lines
- **Authoritative subsystem:** SUB-23 Design documents and plans. **Verdict:** recommend. **Kata:** mux#s1xn (P3). **Priority:** rank 33 of 33.
- **Primary evidence:** six statements are false against the code: `docs/plans/2025-01-13-agent-tool-registration-design.md:3` says "Implemented" while lines 30, 93, 168, 183, 233 name `agent.AgentConfig`, `agent.NewAgent` and `SpawnChild(cfg AgentConfig)` (shipped: `agent.Config`, `agent.New`, `SpawnChild(cfg Config)`); `docs/plans/2026-04-21-llm-multimodal-input-design.md:115` says OpenAI accepts audio (`llm/openai.go:812` returns false, reason at 807-810); the same document at 215-221 says Gemini, Ollama and OpenRouter declare empty capabilities (they declare real ones at `llm/gemini.go:361`, `llm/ollama.go:158`, `llm/openrouter.go:217`); `docs/superpowers/specs/2026-06-18-mux-durable-sessions-design.md:134-136` claims one on-disk message shape (there are two) and `:88-91` lists a fourth status that never shipped; `docs/superpowers/specs/2026-03-23-mux-improvements-design.md:50` names an error type from a module go.mod does not require.
- **Interfaces and call sites:** every reader using a plan as the API reference; `docs/audits/2026-09-11/evener.md:11-12` promises an `evener/` path prefix no path in the file carries.
- **Tests and intent evidence:** the five plans under `docs/superpowers/plans` have 243 unticked boxes (54, 29, 82, 55, 23) although their code shipped; three specs still say awaiting or pending review; the streamable-HTTP plan says "Approved" for a transport that shipped.
- **Current representation:** status lines and checklists that were never updated after landing.
- **Current complexity or invalid states:** a document marked Implemented names an API that does not exist.
- **Why it is material:** one of the six (the single on-disk shape) is the sentence SIFT-SUB-12-02 has to correct, and the first-pass audit record (evener.md) cites a line range that this repository's `llm/types.go` does not have.
- **Proposed representation:** the six statements corrected in place (about twelve lines) or the phase-1 note given a superseded-by pointer; each plan and spec carries a status line naming the shipping commit or abandonment; the checklists ticked or replaced by that line; evener.md prefixes its paths or drops the sentence.
- **Why it is simpler:** one status convention across the plans instead of 243 boxes nobody ticks.
- **Implementation scope:** 30-40 lines of markdown across nine files; no Go changes.
- **Smallest credible slice:** the six corrections.
- **Regression risks:** none to code.
- **Migration concerns:** not applicable; documentation.
- **Existing validation:** none.
- **Additional validation required:** open each cited line beside the code line named next to it.
- **Impact:** low. **Confidence:** high. **Implementation effort:** small. **Blast radius:** local. **Prerequisites:** none; the single-shape sentence is the evidence behind SIFT-SUB-12-02.

## 4. Smallest credible implementation slices

The first five are the ranking pass's top five, re-checked by the coordinator; the last two complete the P1 set. Nothing here is implemented. Line estimates assume a frontier-LLM implementer.

1. **SIFT-SUB-06-01 (mux#yhen, P1), about 35-45 lines.** Boundary: `executeTools` only (`orchestrator/orchestrator.go:548-591`). Change: on context cancellation mid-batch, synthesize an `IsError` tool_result for every tool_use not yet run, append it with the real results already collected, then return the error, so the history never ends on an unpaired tool_use. Interfaces or schema: none; no exported signature or persisted format changes. Migration: none. Tests: cancel between the assistant's tool-use turn and the tool results, then Resume; assert the next provider request is well formed and that no completed tool runs again. Deferred: the retried-Resume path in `resumeCore`, which re-executes tools that already ran, and the call-ID set on Resume. Coordinate with mux#5ez6, which edits the same function.
2. **SIFT-SUB-12-02 (mux#2zdv, P2), about 25-35 lines plus fixtures.** Boundary: `TranscriptEntry` plus `FromMessages`, `Append` and `ToMessages` in `agent/transcript.go`. Change: replace `Content []llm.ContentBlock` with an embedded `llm.Message`. Schema: the on-disk JSON and JSONL shape changes (the persisted-format break in §6). Migration: a header format version, bumped together with SIFT-SUB-12-01; old files are not upgraded in this slice; refusing them on the version check is the minimum. Tests: the round-trip test asserting `Message.Content` survives; fixtures for both shapes; the reproduction in mux#bq4e as the acceptance test. Deferred: upgrading old files, and mux#jmff's question of whether transcript persistence stays a second history authority.
3. **SIFT-SUB-04-01 (mux#a2j0, P1), about 15-25 lines.** Boundary: the empty-candidates branch and the finish-reason switch of `convertGeminiResponse`. Change: read `PromptFeedback.BlockReason` when `Candidates` is empty and return a wrapped, `errors.As`-checkable error instead of an empty success; map every non-STOP finish reason onto the `StopReason` set. Schema: consumes one new `StopReason` constant from SIFT-SUB-01-02 and mints none. Migration: none. Tests: a blocked-prompt fixture, an empty candidate list, one case per finish reason. Deferred: the streaming loop (`llm/gemini.go:312-338`), which SIFT-SUB-04-02 rewrites together with mux#ac2b. Prerequisite: land the constant first, or in this slice.
4. **SIFT-SUB-08-01 (mux#0183, P2), net removal of about 30 lines.** Boundary: `collectRecentUserMessages`, `hasUserContent` and `buildCompactedHistory` in `orchestrator/compact.go`. Change: delete the three and replace them with one function that walks back to the last user message, strips tool_result blocks at block level and returns it. Schema: the exported constant `CompactUserMessageMaxTokens` (`orchestrator/orchestrator.go:61-62`) goes, with a CHANGELOG line. Migration: none, the history is in memory. Tests: port the orphan guarantees pinned at `orchestrator/compact_test.go:408` and `:441`; add the mixed-block case from mux#m886. Deferred: nothing; mux#m886 closes through this slice.
5. **SIFT-SUB-01-02 (mux#7fez, P2), about 35-45 lines for the first slice.** Boundary: one unexported mapping function per provider file plus two or three constants in `llm/types.go`. Change: the Gemini and OpenAI-compatible helpers with their table tests first; Anthropic's raw casts follow. Schema: additive constants only, existing comparisons keep compiling. Migration: a CHANGELOG note that a stop reason other than tool_use no longer implies a successful end_turn. Tests: one table per provider from native finish reason to `StopReason`. Deferred: the Anthropic casts.
6. **SIFT-SUB-16-01 (mux#nrhn, P1), stdio half about 60-80 of the 100-150 lines.** Boundary: `mcp/stdio.go` only. Change: an unexported state value (idle, running, closed) changed under one lock; `closeChan` and `done` created on entering running; the reader's exit moves the client to closed and drains pending calls with `ErrTransportClosed`. Interfaces: none exported; the literal message asserted at `mcp/mcp_test.go:204-231` changes deliberately. Migration: none. Tests: the reproductions in mux#s53f and mux#64sw as tests, under -race. Deferred: the HTTP client (mux#xxrt) as the second slice, then SIFT-SUB-16-02 and mux#rqxt in the same files.
7. **mux#5ez6 (P1, Appendix A), about 10-20 lines plus one test.** Boundary: `orchestrator/orchestrator.go:581-587`, or one accessor on `tool.Result`. Change: one rule for the text of a failed tool_result (Output when non-empty, otherwise Error), and docs on `NewErrorResult` and `NewResult` saying which field the model sees. Schema: none. Tests: a `NewErrorResult` reaches the provider request with its text. Coordinate with slice 1.

## 5. Explicit skips

A skip is a completed review, not missing work. Two subsystem rows and the non-source categories below were inspected and passed nothing through the materiality gate.

- **SUB-19 Skills (`skill/*.go`).** Inspected: `parseSkill` (`skill/skill.go:27`), `LoadDir` and `Register` (`skill/registry.go:30-57`, `:61-69`), the prompt section builder (`:125-126`), the `load_skill` tool (`skill/tool.go`) and the four test files. The package is small and its representations are direct: one parser, one map guarded by one mutex, one tool. No structural change would remove complexity rather than move it. Cross-subsystem notes, both filed: the `load_skill` failure text never reaches the model because `tool.NewErrorResult` leaves Output empty and the orchestrator renders Output only (owner: SUB-13 and SUB-06; filed as mux#5ez6, P1, after the materiality pass promoted it); the loader follows a symlinked `SKILL.md` but skips a symlinked directory (`skill/registry.go:37-39` against `:41`; owner: SUB-19; filed as mux#77s2, P3, merged with the security lens's symlink note). The two Register contracts (`tool/registry.go:23-27` overwrites silently, `skill/registry.go:61-69` rejects duplicates) are a documentation item, mux#dmtf.
- **SUB-24 Dependency manifests (`go.mod`, `go.sum`).** Inspected: the 40-line `go.mod` (module path, `go 1.24`, `toolchain go1.24.11`, six direct requires, nineteen indirect, no `replace` or `exclude` directives) and the 148-line `go.sum`. Nothing hand-maintained lives here beyond the pins, so there is no representation to simplify. Cross-subsystem notes: dependency vulnerability scanning is already mux#frc3 (P1, first pass); the toolchain pin and the missing Go version matrix are part of mux#ae24. `go mod verify` was not run because it can populate the module cache.
- **Non-source categories.** `bin/` (ignored binaries), `.scratch/` (ignored orphan scenario tests with their own `go.mod`; the SUB-18 worker noted `.scratch/scenario_test.go` drives the coordinator, and nothing there is compiled by the module), `output.txt` and `coverage.out` (tool output), `architecture.png` and `architecture.svg` (generated; their source `architecture.dot` is mux#rkk5), `.env` (ignored secrets; only the variable names were listed, see Appendix D), `.superpowers/` and `.private-journal/` (not read), `posts/` (an untracked blog draft; its technical claims were checked by the documentation audit and filed as mux#vzvk at P3), `.gitignore` (the uncommitted change adds `.kata.local.toml`), `.kata.toml` (the untracked Kata project binding) and `LICENSE`. None contains reviewable behaviour owned by the module.

## 6. Cross-cutting patterns

Each pattern below was seen by at least two independently reviewed subsystem rows and classified by what it implies.

1. **Lifecycle state kept as flags, single-use channels and once-guards (SUB-16, SUB-06, SUB-11, SUB-09).** The MCP stdio and HTTP clients (SIFT-SUB-16-01), the orchestrator's `StateMachine` and its parallel `transition()` (SIFT-SUB-06-02), the three run-handle launchers (SIFT-SUB-11-01) and the event bus's `Close` that leaves a closed channel in place (the comment on mux#1wwm) all have the same shape. The implication is **not a shared abstraction**: each owner should hold one guarded state value locally. A shared lifecycle type would relocate the complexity and couple four packages that share nothing else.
2. **Shapes mirrored by hand (SUB-12, SUB-08, SUB-07).** `TranscriptEntry` mirrors `llm.Message` and disagrees with it (SIFT-SUB-12-02); the JSONL header and entry structs are declared four times (SIFT-SUB-12-01); `CompactionResult` is copied field by field into `hooks.CompactionEvent`; `Snapshot` has no version field (SIFT-SUB-07-01). These are **shared source-of-truth problems**, one per pair, and they carry a **sequencing dependency**: SIFT-SUB-12-01 lands first and its format version covers SIFT-SUB-12-02, so one bump serves both.
3. **Validation and conversion duplicated per provider (SUB-01, SUB-03, SUB-04).** Media-source rules live in three validators plus a boolean (SIFT-SUB-01-01); stop reasons are cast three different ways (SIFT-SUB-01-02); OpenRouter and Ollama carry an identical streaming loop (SIFT-SUB-03-01); Gemini's two conversion paths diverge (SIFT-SUB-04-01, SIFT-SUB-04-02). The rules (what a provider accepts, what a finish reason means) are **one source-of-truth problem** and belong in `llm/types.go` and `llm/validate.go`. The provider request builders are **repeated but appropriately local** code and stay per file, except the byte-identical OpenRouter/Ollama loop.
4. **Interruption leaves durable history inconsistent (SUB-06, SUB-07, SUB-13).** Cancellation mid-batch persists an unpaired tool_use (SIFT-SUB-06-01); `Load` accepts a snapshot no `Resume` can use (SIFT-SUB-07-01); failed tool text is dropped before it reaches the model (mux#5ez6); the mid-loop checkpoint runs after side effects (the comment on mux#t0av). These share an invariant, "the persisted history is one a provider will accept", stated in the durable-sessions design at line 114 and enforced nowhere. The implication is a **sequencing dependency**: `Validate` in `Load` (SIFT-SUB-07-01) is the check, SIFT-SUB-06-01 is the first producer fix.
5. **Documentation describes a library that did not ship (SUB-22, SUB-23, SUB-10, SUB-14, SUB-15).** The README names four of ten packages and overclaims twice (SIFT-SUB-22-02); the CHANGELOG skips six releases and the one breaking change (SIFT-SUB-22-01); six design-document statements are false (SIFT-SUB-23-01); file headers and package docs describe absent code (mux#han8); the exported seams state no concurrency, cancellation or error contract (mux#ekms). **No shared abstraction** applies; the shared cause is that nothing in the release path requires a document to be updated when code lands, which SIFT-SUB-22-01's release step addresses.

**Breaking-change batch.** Four findings change an exported signature or a persisted format and should ship in one release with one CHANGELOG entry: mux#865b (`NewClient` rejects configs it used to accept), mux#j6kd (`Checker.Check` contract), mux#2zdv (on-disk transcript shape) and, conditionally, mux#aag7 (only if `Registry.Register` gains an error return rather than the prefix option). SIFT-SUB-22-01 is the vehicle, and it also has to record the break that already shipped: `Capabilities()` was added to `llm.Client` in 5a7c300, first tagged v0.8.0, with no CHANGELOG section at all. The version fields in SIFT-SUB-07-01 and SIFT-SUB-12-01 are additive and do not belong in the batch.

**Sequencing edges adopted from the ranking pass (all acyclic):** SIFT-SUB-01-02 before SIFT-SUB-04-01; SIFT-SUB-12-01 before SIFT-SUB-12-02; SIFT-SUB-11-01 before mux#y0j9; SIFT-SUB-05-02 before mux#vrjk and mux#r4z9; SIFT-SUB-03-01 before mux#k5mx and mux#3zpg; SIFT-SUB-16-01 before mux#s53f, mux#64sw, mux#xxrt and mux#vfwt; SIFT-SUB-16-02 before mux#xat1 and mux#ns1f; SIFT-SUB-04-02 before mux#ac2b and mux#62ba; SIFT-SUB-08-01 before mux#m886; SIFT-SUB-15-01 before mux#yr10. **Same-file order** in `mcp/stdio.go` and `mcp/http.go`: SIFT-SUB-16-01, then SIFT-SUB-16-02, then mux#rqxt, re-citing line numbers after each. **Coordinate, do not order:** SIFT-SUB-06-01 with mux#5ez6 (same function), SIFT-SUB-10-01 with SIFT-SUB-07-01 (the same suspend-without-a-store gap seen from two sides), mux#datc with SIFT-SUB-14-01 (both are approval integrity, and mux#t0qf touches both).

## 7. Rejected, merged, and superseded candidates

| Candidate | Disposition | Reason | Authoritative finding/subsystem, if any |
|---|---|---|---|
| L16 export default model constants | rejected | Materiality pass: naming taste, no traced bug. Accepted. | SUB-01, SUB-03 |
| E03 examples for undemonstrated features | rejected | Additive content, not simplification. The observation that `RetryClient` has no in-tree caller survives in mux#mj3p. | SUB-20 |
| C03 unexport coordinator record types, add lock error sentinels | rejected | Dead-surface cleanup. Recorded, not filed: `ResourceLock.AcquiredAt` (`coordinator/coordinator.go:25`, `:55`) is written and never read. | SUB-18 |
| L07 validate media sources on the Ollama path | withdrawn | Coordinator error found by the design-document review: `llm/ollama.go:54` and `:75` call `validateRequest` before conversion and `Capabilities` is image-only (`:158`), so PDF and audio already return `ErrUnsupportedMedia`. The residue (converters encode `Source.Bytes` without checking `Kind`) is one clause in SIFT-SUB-01-01. The same false sentence was removed from L01. | SUB-01 |
| D02 blog draft claims | demoted P2 to P3 (reviewer said reject) | Reviewer: untracked file, no reader today. Coordinator: the front matter says `draft: false` (`posts/2026-02-13-agentic-infrastructure-compiled-languages.md:13`), so the next publish ships fifteen false technical claims; a P3 issue is the cheapest place to keep the correction list. | SUB-22, mux#vzvk |
| SUB-06 second recommendation (positional approval queue) | superseded | Already tracked by mux#t0qf; the structural note was posted as a comment there. | SUB-06 |
| SUB-09 event bus Close/Reset representation | superseded | Already tracked by mux#1wwm; root-cause comment posted, no new issue. | SUB-09 |
| SUB-08 patch to `hasUserContent` (the route mux#m886 proposes) | superseded | SIFT-SUB-08-01 deletes the function; comment on mux#m886 routes the fix through the sanitizer. | SUB-08 |
| Test-lens claim that `mockDynamicLLMClient` is dead | rejected | Used at `orchestrator/compact_integration_test.go:409` and `orchestrator/hooks_integration_test.go:454`. | SUB-21 |
| SUB-17 first recommendation (nil `InputSchema` handling per provider) | merged | All three providers tolerate a nil schema; impact low; folded into mux#ghcv. | SUB-17 |
| SUB-18 cache growth (security note M1, performance note I1, SUB-18 recommendation) | merged | Three lens notes, one finding. | SIFT-SUB-18-01 |
| A04/A05 merge (duplication pass) | not applied | A05 is a closable defect with its own loop test; A04 is the consolidation vehicle; A05's Related line states both landing orders. | SUB-11 |
| A06/A07 merge (duplication pass) | not applied | A06 holds under either A07 outcome; both Related lines now say one format-version bump covers both. | SUB-12 |
| M13 into M01 (duplication pass) | narrowed | The dead `ErrTransportClosed` item moved into M01's acceptance; M13 keeps the doc bullets and the stdio Close `errors.Join` item and was retitled. | SIFT-SUB-16-01, mux#4bcm |
| S07 trim (duplication pass) | narrowed, wider than proposed | Dropped the bullets duplicating mux#619z, SIFT-SUB-13-01, mux#ghcv and SIFT-SUB-15-02 (the pass missed the SIFT-SUB-13-01 one); S07 keeps FileStore traversal, corrupt JSON and media compaction. | SUB-21, mux#g4v9 |
| Security lens C1, before-hooks mutate params after approval | demoted from critical to P2 | "Coordinator re-verified the code path and set P2 because the repository registers no before-hooks outside tool/tool_test.go." | SUB-13, mux#datc |
| Security lens, duplicate MCP tool names shadow built-ins | filed at P2, not higher | "The silent name collision is verified in code; whether a model acts on instructions embedded in a server-supplied description is a model property this audit did not test." | SUB-13, SUB-17, mux#aag7 |
| Security lens I4, stdio servers inherit the full environment | reframed as an opt-out | `CHANGELOG.md:111` records the inheritance as a deliberate v0.2.2 change, so it is a product option, not a defect. | SUB-16, mux#abry |
| A07 "arguably P1" (ranking pass) | kept P2 | mux#bq4e already carries the P1 symptom; §6 says fix bq4e through A07. | SUB-12 |
| L12 and A05 at P3 (ranking pass) | promoted to P2 | A prerequisite cannot rank below its dependent (L12 before L13 and L14); A05 is an unambiguous defect in an exported method. | SUB-05, SUB-11 |
| "LLM deep-dive" lane | merged | Its transcript was an interim copy of the API review, not a separate report; its items are covered by SUB-01 to SUB-05 and the final API review. | EXP-API |

## 8. Audit-of-audit results

Five fresh passes ran after the subsystem reviews, each in a clean context with the bodies, the plan and the ledger as input. Their verdicts and the coordinator's dispositions:

1. **Coverage.** Verdict "Incomplete, narrowly": every Go file (75) and nearly every tracked non-Go file mapped to one ledger row or the skip list, but SUB-22's `docs/**` boundary had never been claimed for `docs/plans/**` and `docs/superpowers/**` (23 files). Resolved by adding SUB-23 (design documents, worker-reviewed and coordinator-verified line by line) and SUB-24 (dependency manifests, coordinator-reviewed, skip). Two files the pass found unopened were read: the untracked provider-support note (a paused design, its "current repo shape" bullet checked true against `llm/types.go:189-194`) and the first-pass `evener.md` (its two issues, mux#r6tc and mux#d1dj, were already in the dedup set; its history-repair section became a reference line on SIFT-SUB-06-01). `.gitignore`, `.kata.toml` and `LICENSE` were added to the explicit skip list. Two bookkeeping errors were fixed: a stale "pending grep" on SUB-16 and an unclosed "LLM deep-dive" lane (§7).
2. **Duplication and authority.** Ran on the 82-body set that predates the materiality dispositions, the L07 withdrawal and D09. Four merge verdicts: two not applied, one applied as a narrowing, one applied wider than proposed (§7). One cross-link (A08 to A06 and A07) applied. Zero duplicates of the 51 existing issues, confirmed by the coordinator's own search. One missing Related line (L06) added. Three wrong-owner findings (T02, M11, D08) fixed by adding the second owner's label and crediting the SIFT row in each Source line. Zero duplicate abstractions; the same-file risk among M01, M02 and M04 became a Sequencing line in all three bodies. Its L01/L07 "keep separate" row is superseded by the withdrawal.
3. **Materiality.** 82 reviewed, 20 individually re-verified against source by the reviewer: 4 reject, 4 promote, 0 demote, 0 weak evidence. Three rejects accepted (L16, E03, C03); one overridden to a demotion (D02, reason in §7). All four promotions accepted after the coordinator re-traced each path: L03 (blocked prompts are an empty success; `PromptFeedback` is read nowhere), M01 (the second restart panic through `mcp/stdio.go:99` and the deferred close at `:200`), T01b (`orchestrator/orchestrator.go:585` renders Output only) and O09 (no timeout or deadline in the orchestrator or hooks packages).
4. **Schema completeness.** The pass found the SIFT schema fields absent from every Kata body: verdict, a finding-specific confidence, impact rationale, blast radius, migration concerns, the smallest slice as distinct from the full fix, and the two "why" statements. That is by design: bodies carry Trigger, Expected, Actual, Reproduction, Acceptance, Estimate, Risk and Related in the tracker's style, and §3 of this report carries the full schema for every accepted finding. Applied from the pass: S05 now cites all seventeen `llm.Client` fakes in nine files (the draft undercounted one); T05 names the root `integration_test.go` (an `agent/integration_test.go` also exists); L11 marks its `api_client.go` citations as genai-module source; call-site citations the pass located were added to O03, M10, L18, L08, D03, A10 and A05 after re-verification; S02's hook-wiring sentence was corrected. Every body now carries a Risk line (82 of 82) and 66 carry a Reproduction section. Of the fourteen filed bodies without one, eleven are documentation or tooling items (C06, D01 to D08, M06, S07, S09); M01, M02 and T06 state Actual against cited code, and M01 and M02 also lean on the first-pass reproductions in the issues they name.
5. **Dependency-aware ranking.** 82 keys reconciled; ranks 1-15 re-verified live against HEAD by the reviewer, 16-25 not. Its two priority disagreements (L12, A05) were accepted; its soft note on A07 was not (§7). Its sequencing edges, breaking-change batch and top-5 slices were adopted into §4 and §6, with the coordinator's P1 set added. Its L01 row predates the L07 correction and its count predates the withdrawal.

Omissions found and resolved: the design-document category (SUB-23), the dependency manifests (SUB-24), two unread documentation files, and one coordinator error (L07) that the SUB-23 review caught. No omission was concealed by widening an existing row.

## 9. Repository integrity

- **Baseline status** (at `9a94996f093a364d7cb539ec130046e846182b26`, 2026-09-11):

```
 M .gitignore
?? .kata.toml
?? .private-journal/2026-09-11/21-04-19-000000-c902efcc.md
?? docs/plans/2026-06-26-provider-support-notes.md
?? posts/2026-02-13-agentic-infrastructure-compiled-languages.md
```

- **Final status** (captured at `84c7e8b2be955e7f42509bd6c0f9c0f30d152b9e`, 2026-09-12 11:13 CDT, immediately before this file was written):

```
 M .gitignore
?? .kata.toml
?? .private-journal/2026-09-11/21-04-19-000000-c902efcc.md
?? .private-journal/2026-09-11/22-18-47-000000-0f9378c5.md
?? .private-journal/2026-09-11/22-44-32-000000-e90f91c9.md
?? docs/plans/2026-06-26-provider-support-notes.md
?? posts/2026-02-13-agentic-infrastructure-compiled-languages.md
```

- **Comparison:** changed. Lines present only in the final status:

```
?? .private-journal/2026-09-11/22-18-47-000000-0f9378c5.md
?? .private-journal/2026-09-11/22-44-32-000000-e90f91c9.md
```

- The two added lines are private journal entries created at 22:18:47 and 22:44:32 on 2026-09-11 (file metadata; the files were not read). Each sits within a minute of one of the two documentation commits made in another session (`0527430` at 22:18:34, `84c7e8b` at 22:44:57), and this session's transcript contains no journal-writing call, so they belong to that session, not to the audit. HEAD also moved from the baseline commit to `84c7e8b` through two documentation-only commits made outside the audit (`0527430`, `84c7e8b`; `git diff --stat 6f7305e..HEAD -- '*.go'` is empty, so every code citation still resolves).
- **Integrity verdict:** fail, external cause. The audit itself edited, created, staged, committed or deleted nothing before this file; it ran no test, build, formatter, linter, generator or `go mod verify`.
- **Post-audit writes:** `docs/audits/2026-09-11/sift-pass2.md` (this file), created after the final status above was captured. No other write by the audit. Session housekeeping after this file may touch `gotchas.md` and `.private-journal/` (house conventions); neither is part of the audit.

## Appendix A. Expert-lens findings filed outside §3

The 47 pass-2 issues that did not come from a SIFT subsystem recommendation. They were raised by the cross-cutting lenses (security, reliability and concurrency, public API design, performance, test quality and tooling) or by the documentation-drift audit, and each was re-verified by the coordinator against the code before filing. Priority is as filed. Source names the lens or the SIFT row that raised the item, so credibility can be weighed per reviewer.

### P1

| Kata | Key | Title | Source |
|---|---|---|---|
| mux#5ez6 | T01b | Render failed tool Results so NewErrorResult text reaches the model | row SUB-13 result-contract trace, verified by the coordinator against every in-tree producer |

### P2

| Kata | Key | Title | Source |
|---|---|---|---|
| mux#vrjk | L13 | Honor Retry-After in RetryClient backoff | row SUB-05 (RetryClient) defect note; coordinator-verified against the design spec |
| mux#mj3p | L15 | Decide which layer retries: SDK default retries stack under RetryClient | row SUB-05 (RetryClient) cross-check against SDK defaults; coordinator-verified in the module cache |
| mux#vz82 | O07 | Skip repeated summarization calls when compaction cannot reduce history | row SUB-08 defect note 1; coordinator-verified |
| mux#yr10 | O09 | Document that hooks run synchronously under the loop lock and bound hook execution | reliability lens finding 2; coordinator-verified |
| mux#y0j9 | A05 | Return the CAS outcome from RunHandle.Cancel so completed runs are not reported as cancelled | row SUB-11 defect note 1; reliability lens finding 3 |
| mux#0xem | A10 | Write transcript files with owner-only permissions through an atomic rename | row SUB-12 defect note 2; security lens I5 |
| mux#datc | T04 | Stop before-hooks from mutating params after approval | security lens finding C1 (rated critical by that reviewer); coordinator re-verified the code path and set P2 because the repository registers no before-hooks outside tool/tool_test.go, so th |
| mux#rqxt | M04 | Bound the HTTP MCP transport: timeout, redirect policy, and one response ceiling | security lens finding I3; row SUB-16 defect note 3 and its cluster note on response-size ceilings |
| mux#aag7 | M11 | Refuse or namespace duplicate tool names from MCP servers | security lens finding C2; rows SUB-13 (tool/registry.go:24-28, Register overwrites silently) and SUB-17 (mcp/adapter.go:132-138, RegisterAll); coordinator verified the registration path. Fil |
| mux#2sv7 | C02 | Validate RateLimiter parameters and reject impossible Take requests | reliability lens finding 5 and performance lens note M1; coordinator verified against the existing tests |
| mux#t0mr | S01 | Make TestCompactionHookError and TestMultipleAgentsDeadlockScenario assert their outcomes | test-quality lens findings 1-2; row SUB-20 (test harness) |
| mux#c3ps | S05 | Share one scripted llm.Client fake across the test suites | test-quality lens finding 3; row SUB-20 |

### P3

| Kata | Key | Title | Source |
|---|---|---|---|
| mux#59bt | L08 | Let NewOllamaClient take an API key or document it as local-only | row SUB-03 (OpenAI-compatible clients), API review lens |
| mux#e729 | L10 | Reject out-of-range Gemini MaxTokens and thinking budgets instead of dropping them silently | row SUB-04 (Gemini client) defect note; coordinator-verified |
| mux#r4z9 | L14 | Validate RetryConfig in NewRetryClient | row SUB-05 (RetryClient) defect note; merges API review finding 14 |
| mux#e7fn | O03 | Persist adaptive-thinking counters in Snapshot so Resume does not reset them | row SUB-06 defect note 1; coordinator-verified |
| mux#84mr | O05 | Clean up orphaned FileStore temp files and wrap OS errors returned through Store | row SUB-07 defect notes; coordinator-verified |
| mux#tm9p | O08 | Guard NewToolResultEvent against nil results | row SUB-09 defect note; API review finding 40 |
| mux#6d5h | A03 | Copy ThinkingSettings in Agent.Config so callers cannot mutate the running orchestrator | row SUB-10 defect note 2; API review finding 33 |
| mux#jmff | A08 | Document or deprecate transcript persistence as a second history authority | row SUB-12 cross-note 1 |
| mux#8zz1 | A09 | Stop fabricating per-entry timestamps in Transcript.FromMessages | row SUB-12 defect note 1 |
| mux#j6sw | A11 | Document that same-agent RunAsync calls serialize on the orchestrator lock | row SUB-11 cross-note 1; API review finding 35 |
| mux#kq37 | T03 | Reject typed-nil sources, copy filter slices, and fix Executor.Registry reach-through | row SUB-13 defect notes 1-3; coordinator trace of agent.Agent.init |
| mux#atdv | T08 | Document the three observation mechanisms and the Fire* short-circuit | API review finding 7; rows SUB-13, SUB-14 and SUB-15 cross-notes |
| mux#619z | M03 | Delete the test-only SSE parser and test the end-of-stream and reader-error paths | row SUB-16 defect notes 1-2; test-quality lens |
| mux#6f1j | M05 | Document and count dropped MCP notifications | row SUB-16 defect note 4; API review finding 16 |
| mux#kmgk | M06 | Reconcile the Streamable HTTP design doc with the shipped transport | row SUB-16 defect note 5 |
| mux#6n9g | M09 | Accept string JSON-RPC ids in MCP Request and Response | row SUB-17 defect note 1 |
| mux#7hrd | M10 | Add a default branch for unknown MCP content block types | row SUB-17 defect note 2 |
| mux#abry | M12 | Offer an opt-out from full environment inheritance for stdio MCP servers | security lens finding I4. Framed as an opt-out because CHANGELOG.md:111 records the inheritance as a deliberate v0.2.2 change |
| mux#4bcm | M13 | Document the MCP error contract and make stdio Close report failures | API review findings 11 and 29; reliability lens finding 4 |
| mux#77s2 | C05 | State and test the skill loader's symlink policy | row SUB-19 (skills) defect note |
| mux#dmtf | C06 | Document the two Register contracts and the skill loader's extension points | API review findings 26-28; row SUB-19 |
| mux#mfae | C08 | Decide the checkpoint cadence: every iteration serializes the whole history | performance lens finding C1. Filed at P3: the mechanism is verified in code; no measurement was taken and per-iteration durability is the feature's purpose, so the cadence is a design decisi |
| mux#09q9 | C09 | Document that history is unbounded by default and fix the orphan DefaultContextBudget comment | security lens finding I2; row SUB-08 |
| mux#g5wt | L17 | Base64-encode media once instead of on every request | performance lens finding C2. Filed at P3: verified mechanism, unmeasured magnitude |
| mux#k1jr | L18 | Accumulate Anthropic streaming deltas with a builder instead of string concatenation | performance lens finding C3 |
| mux#e9eg | S03 | Replace wall-clock sleeps in coordinator tests with an injected clock | test-quality lens finding 5; row SUB-20 |
| mux#18sc | S04 | Skip node-dependent MCP tests when node is absent and document the requirement | test-quality lens finding 10; documentation audit section 4.3 |
| mux#g4v9 | S07 | Add tests for the untested guards: FileStore path traversal, corrupt JSON, media compaction | test-quality lens findings 4 and 8; rows SUB-10, SUB-13, SUB-15, SUB-16 |
| mux#ae24 | S09 | Scope the gosec exclusions, pin CI actions, and decide the Go matrix | security lens findings M5 and M6; test-quality lens findings 16, 18, 19; row SUB-21 |
| mux#vzvk | D02 | Fix the blog draft's 15 claims that contradict the code before publishing | documentation audit section 2.5. The file is untracked in this working tree, so this issue records where the draft must be fixed before it is published anywhere |
| mux#rkk5 | D03 | Regenerate, label or delete the stale architecture.dot | documentation audit section 2.6 |
| mux#han8 | D06 | Fix ABOUTME headers and package docs that describe code that is not there | documentation audit section 2.6; rows SUB-10, SUB-14, SUB-15; API review findings 21, 22, 39 |
| mux#ekms | D07 | State the concurrency, cancellation and error contracts on the exported seams | API review findings 4, 31, 34, 35, 37, 38; row SUB-15 (Fire* short-circuit) |
| mux#82tn | D08 | Route the 21 stderr diagnostics through one injectable logger | API review finding 6; rows SUB-01 through SUB-05, SUB-13, SUB-16 |


## Appendix B. Documentation drift

The documentation review (a fork of the expert panel, owner of SUB-22) and the SUB-23 design-document review produced nine filed items. Each claim below was re-opened by the coordinator at the cited line.

| Kata | Key | P | Drift |
|---|---|---|---|
| mux#qs9q | D01 | P2 | 16 tags, 11 CHANGELOG sections; v0.6.1, v0.6.2, v0.7.0, v0.7.1, v0.8.0 and v0.8.1 missing; no Unreleased section; four dates disagree with the tags; the v0.8.0 `Capabilities()` break unrecorded. |
| mux#vzvk | D02 | P3 | The untracked blog draft has `draft: false` and fifteen claims that contradict the code. |
| mux#rkk5 | D03 | P3 | `architecture.dot` (249 lines, last changed 2025-12-29) names identifiers that exist nowhere in the Go sources: `Tool.Execute` returning `(string, error)`, `llm.Client` with `Chat` and `SupportsStreaming`, a `StateType` enum, `mcp.ClientConfig`; the rendered PNG and SVG follow it. |
| mux#rqzk | D04 | P2 | README names four of ten packages, no providers, transports, toolchain floor or environment variables; claims an MCP server and a plugin architecture. |
| mux#han8 | D06 | P3 | Eight header and package-doc errors: `agent/presets.go:2` lists three of five presets; `permission/mode.go:1` promises a prompt nothing issues; `hooks/hooks.go:2,5` claim tool lifecycle events the package lacks; `agent/transcript.go:2` says JSONL only; `examples/simple/main.go:1-2` says built-in tools; `session/file_store.go` has no package doc; a typo at `orchestrator/session.go:29`; the agent package doc is split across files. |
| mux#ekms | D07 | P3 | Exported seams state no concurrency, cancellation or error contract (six API-review findings plus the `Fire*` short-circuit). |
| mux#82tn | D08 | P3 | 21 direct `fmt.Fprintf(os.Stderr, ...)` sites in production code across the llm, mcp, orchestrator and tool packages; none can be redirected or silenced by a consumer. |
| mux#s1xn | D09 | P3 | Six false design-document statements and 243 unticked plan checkboxes for shipped work. |
| mux#zee0 (comment) | — | — | DEPENDENTS.md is stale in every dimension; the full table promised in that comment is B.2 below. |

**The six false design-document statements (SIFT-SUB-23-01):**

1. `docs/plans/2025-01-13-agent-tool-registration-design.md:3` says "Status: Implemented" while lines 30, 93, 168, 183 and 233 name `agent.AgentConfig`, `agent.NewAgent` and `SpawnChild(cfg AgentConfig)`; the shipped names are `agent.Config` (`agent/config.go:14`), `agent.New` (`agent/agent.go:40`) and `SpawnChild(cfg Config)` (`agent/agent.go:224`).
2. `docs/plans/2026-04-21-llm-multimodal-input-design.md:115` says the OpenAI client accepts audio; `llm/openai.go:812` returns `Audio: false`, with the reason at 807-810 and the change in commit 8224cec.
3. The same document at 215-221 says Gemini, Ollama and OpenRouter declare empty capabilities; they declare real ones at `llm/gemini.go:361`, `llm/ollama.go:158` and `llm/openrouter.go:217`. The phase-2 design (`docs/plans/2026-04-23-llm-multimodal-phase2-design.md`) supersedes it without a pointer.
4. `docs/superpowers/specs/2026-06-18-mux-durable-sessions-design.md:134-136` claims one on-disk message shape; `orchestrator/session.go:53` and `agent/transcript.go:17-21` are two.
5. The same specification at 88-91 lists four `Status` values including `StatusError`; `orchestrator/session.go:17-21` has three.
6. `docs/superpowers/specs/2026-03-23-mux-improvements-design.md:50` names `*googleapi.Error`, from a module `go.mod` does not require; the genai module exports `APIError` (its `go.mod:12`).

Status bookkeeping in the same set: 243 unticked boxes across five plans (54, 29, 82, 55, 23) whose code shipped; the robustness spec still says "Awaiting review" (line 5); the durable-sessions and skills specs say "Approved design; pending spec review -> implementation plan" (line 7 of each); the streamable-HTTP plan says "Approved" (line 4) for a transport that shipped. In the first-pass audit record, `docs/audits/2026-09-11/evener.md:11-12` announces an `evener/` path prefix that no path in the file carries, and `:160-161` cites `llm/types.go:435-476` while this repository's file has 434 lines.

### B.2 `DEPENDENTS.md` inventory (the table promised in the comment on mux#zee0)

`DEPENDENTS.md` (45 lines, tracked) makes 18 checkable claims; 11 hold and 7 do not. The seven, each re-opened at the cited line:

| Line | Claim | Reality | Fix |
|---|---|---|---|
| 13 | jeff, "Path: `../jeff`" | `/Users/harper/Public/src/2389/jeff` is a folder of sub-projects (`jeff-android`, `jeff-bot`, `jeff-cli`, `jeff-coven`, `jeff-evals`, `jeff-ios`, `jeff-kit`, `jeff-soma`, `jeff-soma-ffi`) with no `go.mod` to depth 4 and no Go import of mux. | Remove the Go entry. |
| 14 | jeff, "Backend wrapper around mux agent for event streaming" | Jeff consumes the Rust port: `jeff/jeff-soma/Cargo.toml:13` and `jeff/jeff-soma/jeff-ffi/Cargo.toml:12-13` pull `mux` and `mux-ffi` from `https://github.com/2389-research/mux-rs.git`. | Describe it as a mux-rs consumer, or move it to a mux-rs list. |
| 15 | jeff key files `internal/mux/backend.go`, `internal/adapter/tool.go` | Neither path exists under `../jeff`. | Remove. |
| 18 | mouse, "Path: `../mouse`" | Neither `/Users/harper/Public/src/2389/mouse` nor `/Users/harper/Public/src/mouse` exists. | Remove or point at a real location. |
| 22 | sysop, "Path: `../sysop`" | Neither `/Users/harper/Public/src/2389/sysop` nor `/Users/harper/Public/src/sysop` exists. | Remove or point at a real location. |
| 27 | "Current Version v0.6.0 (2026-01-01)" | Latest tag is v0.9.0 (2026-06-26). | Update, or replace the literal with "see `git describe --tags`". |
| 44 | `go get github.com/2389-research/mux@v0.6.0` | Pins a version eight releases old. | Use `@latest` or v0.9.0. |

The eleven true claims: the line-3 description; the hex entry (`hex/internal/providers/mux_adapter.go:15-16` embeds `llm.Client`, `hex/cmd/hex/mux_runner.go` imports mux, hex pins v0.7.0); the five-provider list (`llm/client.go:4-6`); lines 32-34; and the "(v0.6.0)" attributions at 35-39, which match the CHANGELOG 0.6.0 entry.

Local consumers, from every `go.mod` under `/Users/harper/Public/src/2389` (depth 4) that requires `github.com/2389-research/mux`, cross-checked against `docs/audits/2026-09-11/relevance.md:44-52`:

| Consumer (under `/Users/harper/Public/src/2389/`) | Pin | Go files importing mux | In `DEPENDENTS.md`? |
|---|---|---|---|
| `hex` | v0.7.0 | 2 (`cmd/hex/mux_runner.go`, `internal/providers/mux_adapter.go`) | yes, correct |
| `mux-mush` | v0.9.0 | 24 | no |
| `elves` | v0.6.0 with `replace => ../mux` (`go.mod:38`) | 20 | no |
| `glassspider` | v0.9.0 | 16 | no |
| `vertex/vertex-bridge` | v0.6.2, marked `// indirect` at `go.mod:14` despite direct imports | 14 | no |
| `hawk` | v0.8.1 | 6 | no |
| `aed` | v0.7.0 | 2 | no |
| `gateway/scenario/agents/mux-go` | v0.8.0 | 2 | no |
| `mux-evals/runners/go` | v0.6.0 with `replace => /Users/harper/Public/src/2389/mux` (`go.mod:34`) | 1 | no |
| `jeff` | Rust port over git; no Go module | 0 | listed, wrong |
| `mouse`, `sysop` | paths do not exist | – | listed, wrong |

Not consumers: `agent-class/agents/mux` is a copy of the mux module itself (same `module` line), and the ignored `.scratch/go.mod` inside this repository is a scratch module. Eight consumers are unlisted; pins span four minor versions. Suggested fix, as in the comment: regenerate the file from a `go.mod` scan instead of hand-editing it.

Correction to the comment on mux#zee0: it says one of the eight unlisted consumers is `agent-class/agents/mux`. The count of eight is right, but `agent-class/agents/mux` is not among them; it is a copy of the module, not a consumer. The table above is the authority.

## Appendix C. Kata filing index

### C.1 Issues filed (79)

All carry the label `audit-2026-09-11-pass2` and an idempotency key `pass2-<Key>`. Where names the report entry that owns the item.

| Kata | Key | P | Title | Where |
|---|---|---|---|---|
| mux#ray8 | L01 | P2 | Consolidate media source validation into one pre-flight check with a transport axis | SIFT-SUB-01-01 |
| mux#7fez | L02 | P2 | Map every provider finish reason through one StopReason helper and name abnormal terminations | SIFT-SUB-01-02 |
| mux#a2j0 | L03 | P1 | Report Gemini blocked prompts and non-STOP finish reasons instead of returning an empty success | SIFT-SUB-04-01 |
| mux#rzr5 | L04 | P3 | Parse Anthropic tool input through one helper so null input yields the same map on both paths | SIFT-SUB-02-01 |
| mux#eb0c | L05 | P3 | Return a full accumulated snapshot from the Anthropic message_delta event | SIFT-SUB-02-02 |
| mux#qa9p | L06 | P2 | Share the Chat Completions streaming loop between OpenRouter and Ollama behind a per-provider seam | SIFT-SUB-03-01 |
| mux#59bt | L08 | P3 | Let NewOllamaClient take an API key or document it as local-only | Appendix A |
| mux#d6rh | L09 | P2 | Share the Gemini part-to-block mapping so streaming handles thought parts | SIFT-SUB-04-02 |
| mux#e729 | L10 | P3 | Reject out-of-range Gemini MaxTokens and thinking budgets instead of dropping them silently | Appendix A |
| mux#ghk9 | L11 | P2 | Classify Gemini errors by type in isRetryable and drop the substring status scan | SIFT-SUB-05-01 |
| mux#6vjj | L12 | P2 | Merge the duplicated CreateMessage and CreateMessageStream retry loops | SIFT-SUB-05-02 |
| mux#vrjk | L13 | P2 | Honor Retry-After in RetryClient backoff | Appendix A |
| mux#r4z9 | L14 | P3 | Validate RetryConfig in NewRetryClient | Appendix A |
| mux#mj3p | L15 | P2 | Decide which layer retries: SDK default retries stack under RetryClient | Appendix A |
| mux#yhen | O01 | P1 | Keep tool-batch history valid on cancellation and never re-run completed tools on Resume retry | SIFT-SUB-06-01 |
| mux#kzxd | O02 | P2 | Route every orchestrator state change through one validated, observable StateMachine path | SIFT-SUB-06-02 |
| mux#e7fn | O03 | P3 | Persist adaptive-thinking counters in Snapshot so Resume does not reset them | Appendix A |
| mux#mmzv | O04 | P2 | Enforce the Snapshot Status/Suspension invariant at the Store boundary | SIFT-SUB-07-01 |
| mux#84mr | O05 | P3 | Clean up orphaned FileStore temp files and wrap OS errors returned through Store | Appendix A |
| mux#0183 | O06 | P2 | Replace compaction's dead multi-message collector with a block-level sanitize step | SIFT-SUB-08-01 |
| mux#vz82 | O07 | P2 | Skip repeated summarization calls when compaction cannot reduce history | Appendix A |
| mux#tm9p | O08 | P3 | Guard NewToolResultEvent against nil results | Appendix A |
| mux#yr10 | O09 | P2 | Document that hooks run synchronously under the loop lock and bound hook execution | Appendix A |
| mux#qxfb | A01 | P2 | Make SpawnChild inheritance explicit for every Config field and return an error for ApprovalSuspend without SessionStore | SIFT-SUB-10-01 |
| mux#aezg | A02 | P3 | Guarantee unique IDs for same-named sibling child agents | SIFT-SUB-10-02 |
| mux#6d5h | A03 | P3 | Copy ThinkingSettings in Agent.Config so callers cannot mutate the running orchestrator | Appendix A |
| mux#qg5t | A04 | P2 | Unify RunAsync, ContinueAsync and RunChildAsync behind one launch helper | SIFT-SUB-11-01 |
| mux#y0j9 | A05 | P2 | Return the CAS outcome from RunHandle.Cancel so completed runs are not reported as cancelled | Appendix A |
| mux#yp6a | A06 | P2 | Declare the JSONL transcript header and entry shapes once | SIFT-SUB-12-01 |
| mux#2zdv | A07 | P2 | Decide the transcript entry payload shape instead of mirroring llm.Message by hand | SIFT-SUB-12-02 |
| mux#jmff | A08 | P3 | Document or deprecate transcript persistence as a second history authority | Appendix A |
| mux#8zz1 | A09 | P3 | Stop fabricating per-entry timestamps in Transcript.FromMessages | Appendix A |
| mux#0xem | A10 | P2 | Write transcript files with owner-only permissions through an atomic rename | Appendix A |
| mux#j6sw | A11 | P3 | Document that same-agent RunAsync calls serialize on the orchestrator lock | Appendix A |
| mux#9p0e | T01 | P3 | Guarantee a non-nil Result from Executor.Execute instead of guarding in one consumer | SIFT-SUB-13-01 |
| mux#5ez6 | T01b | P1 | Render failed tool Results so NewErrorResult text reaches the model | Appendix A |
| mux#ghcv | T02 | P3 | Treat a nil InputSchema from SchemaProvider as no schema | SIFT-SUB-13-02 |
| mux#kq37 | T03 | P3 | Reject typed-nil sources, copy filter slices, and fix Executor.Registry reach-through | Appendix A |
| mux#datc | T04 | P2 | Stop before-hooks from mutating params after approval | Appendix A |
| mux#j6kd | T05 | P2 | Let permission.Checker express "ask" and define rule precedence | SIFT-SUB-14-01 |
| mux#7gb0 | T06 | P3 | Collapse the seven hand-written Fire* dispatch loops into one generic helper | SIFT-SUB-15-01 |
| mux#s0st | T07 | P3 | Type SessionEndEvent.Reason and test the suspended reason | SIFT-SUB-15-02 |
| mux#atdv | T08 | P3 | Document the three observation mechanisms and the Fire* short-circuit | Appendix A |
| mux#nrhn | M01 | P1 | Own transport lifecycle state in one place for stdio and HTTP clients | SIFT-SUB-16-01 |
| mux#6aqg | M02 | P2 | Share notification building, response matching and request headers across MCP transports | SIFT-SUB-16-02 |
| mux#619z | M03 | P3 | Delete the test-only SSE parser and test the end-of-stream and reader-error paths | Appendix A |
| mux#rqxt | M04 | P2 | Bound the HTTP MCP transport: timeout, redirect policy, and one response ceiling | Appendix A |
| mux#6f1j | M05 | P3 | Document and count dropped MCP notifications | Appendix A |
| mux#kmgk | M06 | P3 | Reconcile the Streamable HTTP design doc with the shipped transport | Appendix A |
| mux#865b | M08 | P2 | Validate ServerConfig per transport and type the Transport field | SIFT-SUB-17-01 |
| mux#6n9g | M09 | P3 | Accept string JSON-RPC ids in MCP Request and Response | Appendix A |
| mux#7hrd | M10 | P3 | Add a default branch for unknown MCP content block types | Appendix A |
| mux#aag7 | M11 | P2 | Refuse or namespace duplicate tool names from MCP servers | Appendix A |
| mux#abry | M12 | P3 | Offer an opt-out from full environment inheritance for stdio MCP servers | Appendix A |
| mux#4bcm | M13 | P3 | Document the MCP error contract and make stdio Close report failures | Appendix A |
| mux#v028 | C01 | P3 | Decide who evicts expired Coordinator cache entries | SIFT-SUB-18-01 |
| mux#2sv7 | C02 | P2 | Validate RateLimiter parameters and reject impossible Take requests | Appendix A |
| mux#77s2 | C05 | P3 | State and test the skill loader's symlink policy | Appendix A |
| mux#dmtf | C06 | P3 | Document the two Register contracts and the skill loader's extension points | Appendix A |
| mux#mfae | C08 | P3 | Decide the checkpoint cadence: every iteration serializes the whole history | Appendix A |
| mux#09q9 | C09 | P3 | Document that history is unbounded by default and fix the orphan DefaultContextBudget comment | Appendix A |
| mux#g5wt | L17 | P3 | Base64-encode media once instead of on every request | Appendix A |
| mux#k1jr | L18 | P3 | Accumulate Anthropic streaming deltas with a builder instead of string concatenation | Appendix A |
| mux#3p4f | E01 | P2 | Confine or stop claiming to confine file access in examples/full | SIFT-SUB-20-01 |
| mux#t0mr | S01 | P2 | Make TestCompactionHookError and TestMultipleAgentsDeadlockScenario assert their outcomes | Appendix A |
| mux#7gbz | S02 | P2 | Make the four test gates run the same command | SIFT-SUB-21-01 |
| mux#e9eg | S03 | P3 | Replace wall-clock sleeps in coordinator tests with an injected clock | Appendix A |
| mux#18sc | S04 | P3 | Skip node-dependent MCP tests when node is absent and document the requirement | Appendix A |
| mux#c3ps | S05 | P2 | Share one scripted llm.Client fake across the test suites | Appendix A |
| mux#g4v9 | S07 | P3 | Add tests for the untested guards: FileStore path traversal, corrupt JSON, media compaction | Appendix A |
| mux#ae24 | S09 | P3 | Scope the gosec exclusions, pin CI actions, and decide the Go matrix | Appendix A |
| mux#qs9q | D01 | P2 | Add the six missing CHANGELOG sections, an Unreleased section, and the BREAKING marker on Capabilities() | SIFT-SUB-22-01 |
| mux#vzvk | D02 | P3 | Fix the blog draft's 15 claims that contradict the code before publishing | Appendix A |
| mux#rkk5 | D03 | P3 | Regenerate, label or delete the stale architecture.dot | Appendix A |
| mux#rqzk | D04 | P2 | Expand README.md to name the packages, providers, transports, features and toolchain | SIFT-SUB-22-02 |
| mux#han8 | D06 | P3 | Fix ABOUTME headers and package docs that describe code that is not there | Appendix A |
| mux#ekms | D07 | P3 | State the concurrency, cancellation and error contracts on the exported seams | Appendix A |
| mux#82tn | D08 | P3 | Route the 21 stderr diagnostics through one injectable logger | Appendix A |
| mux#s1xn | D09 | P3 | Correct six false design-document claims and the stale plan status markers | SIFT-SUB-23-01 |

### C.2 Comments posted on existing issues (19)

Each comment opens with Pass-2 note or Pass-2 structural note and names the SIFT row or lens that produced it; the first line of each is quoted.

| Issue | First line of the comment |
|---|---|
| mux#1wwm | Pass-2 structural note (SIFT row SUB-09, 2026-09-11). The double close this issue reproduces is the visible half of a representation problem. Close (orchestrator/events.go:137-148) sets closed=true but leaves the now-closed channe |
| mux#64sw | Pass-2 note (SIFT row SUB-16, 2026-09-11). This issue, mux#s53f, mux#xxrt and mux#vfwt share one root: transport lifecycle is spread across a running bool, a closeChan and a done channel allocated once in newStdioClient (mcp/stdio |
| mux#93yp | Pass-2 note (SIFT row SUB-07 and security lens, 2026-09-11). Two adjacent points for whoever takes this: |
| mux#a825 | Pass-2 note (SIFT row SUB-05, 2026-09-11). Supporting evidence, not a re-file: the initial streaming failure this issue describes is structural. Client.CreateMessageStream (llm/client.go:13) returns (<-chan StreamEvent, error) and |
| mux#ac2b | Pass-2 note (SIFT row SUB-04, 2026-09-11). Root cause, for the record: the streaming loop keeps only lastResp (llm/gemini.go:302, 314) and converts that single chunk at the stop event (340-350). The same loop is a second hand-writ |
| mux#bq4e | Pass-2 note (SIFT row SUB-12, 2026-09-11). Before fixing this, decide the entry shape once. TranscriptEntry (agent/transcript.go) mirrors llm.Message by hand and disagrees with it (both types have a field named Content with differ |
| mux#drm8 | Pass-2 note (SIFT rows SUB-01 and SUB-03, 2026-09-11). Root cause located: llm/openai.go:677 calls validateOpenAISources("openai", false, req) under a comment (673-676) that says CreateMessageStream uses Chat Completions, but the  |
| mux#jh1c | Pass-2 note (SIFT row SUB-12 and security lens, 2026-09-11). The fix pattern is already in the tree: session/file_store.go:49-57 writes to a temp file and renames. Two additions when porting it to SaveToFile (agent/transcript.go:2 |
| mux#m886 | Pass-2 note (SIFT row SUB-08, 2026-09-11). Route this fix through the block-level sanitize step proposed in mux#0183 rather than patching hasUserContent (orchestrator/compact.go:114-129). collectRecentUserMessages builds a token-b |
| mux#ns1f | Pass-2 note (SIFT row SUB-17, 2026-09-11). Adjacent: Request.ID and Response.ID are uint64 (mcp/types.go:23, 41), so a server that answers with a string id, which JSON-RPC 2.0 permits, fails to unmarshal before the id check this i |
| mux#p52w | Pass-2 note (SIFT row SUB-18, 2026-09-11). Distinct from the cancellation check here but in the same function: NewRateLimiter (coordinator/ratelimiter.go:20-30) accepts capacity 0 and negative values, tryTake divides by refillRate |
| mux#s53f | Pass-2 note (SIFT row SUB-16, 2026-09-11). This issue, mux#64sw, mux#xxrt and mux#vfwt share one root: transport lifecycle is spread across a running bool, a closeChan and a done channel allocated once in newStdioClient (mcp/stdio |
| mux#t0av | Pass-2 note (SIFT rows SUB-06 and SUB-07, 2026-09-11). Adjacent and distinct: the mid-loop StatusRunning checkpoint at orchestrator/orchestrator.go:361 runs after executeTools has already appended tool results and applied their si |
| mux#t0qf | Pass-2 note (SIFT row SUB-06 and reliability lens, 2026-09-11). Confirmed against current code: installDecisionApproval (orchestrator/orchestrator.go:723-740) maps decisions to tool calls by position in a queue built from toolUses |
| mux#vfwt | Pass-2 note (SIFT row SUB-16, 2026-09-11). This issue, mux#s53f, mux#64sw and mux#xxrt share one root: transport lifecycle is spread across a running bool, a closeChan and a done channel allocated once in newStdioClient (mcp/stdio |
| mux#whef | Pass-2 note (SIFT row SUB-16 and security lens, 2026-09-11). Two adjacent structural items. The response-size ceilings this issue meets are defined in three places that disagree: the stdio scanner buffer (mcp/stdio.go:86-95, the o |
| mux#wr5j | Pass-2 note (SIFT row SUB-16 and security lens, 2026-09-11). Sizing the initial buffer from MaxResponseBytes stays the first slice. Adjacent: the same ceiling is defined in three places that disagree: this stdio scanner buffer (mc |
| mux#x39z | Pass-2 note (SIFT row SUB-20, 2026-09-11). The minimal example has the same class of race: examples/minimal/main.go:92-93 subscribes and starts an unjoined goroutine on every turn, and nothing waits for it before the loop prints t |
| mux#zee0 | Pass-2 note (documentation audit, 2026-09-11), on the inventory this issue says it includes. DEPENDENTS.md is stale in every dimension: "Current Version v0.6.0" (DEPENDENTS.md:27; latest tag v0.9.0), the go get line pins v0.6.0 (l |

### C.3 Candidates not filed (4)

| Key | Disposition | Reason |
|---|---|---|
| L16 | rejected | Export default model constants: naming taste, no traced bug (materiality pass). |
| C03 | rejected | Unexport coordinator record types and add lock sentinels: dead-surface cleanup (materiality pass). |
| E03 | rejected | Examples for undemonstrated features: additive content, not simplification (materiality pass). |
| L07 | withdrawn | Ollama media validation: the reproduction is unreachable; coordinator error caught by the SUB-23 review. |


## Appendix D. Method and disclosures

**Method.** Baseline captured at 9a94996 (revision, branch, full porcelain status). Twenty-two subsystem rows were inventoried, two more added by the coverage pass. Rows SUB-01 to SUB-19 were reviewed by fresh read-only workers, one row each, from the SIFT worker brief verbatim; SUB-20, SUB-22 (with the documentation fork) and SUB-24 by the coordinator; SUB-21 by the test-quality lens; SUB-23 by a fresh worker after the coverage pass. Five expert lenses (security, reliability and Go concurrency, public API design, performance, test quality and tooling) ran in parallel with the same no-code guard, and the API lens forked into three sub-reviews. Every candidate was re-opened by the coordinator at the cited lines before acceptance, then deduplicated against the 51 open first-pass issues by search and by reading each. Five audit-of-audit passes ran in clean contexts (§8). Findings were filed in Kata with idempotency keys, one label per category, and a Related line naming the first-pass issue each supersedes or supports; 19 existing issues received a comment instead of a duplicate. The report entries in §3 were drafted from the bodies and every `file:line` in them was re-checked by a script against the body it came from; one wrong range was found and fixed before this report was written.

**Disclosures, in the order they occurred.**

1. The expert-panel skill asks the user to customize the panel before dispatch. The user was absent, so the coordinator chose an API/library panel and ran it. Deviation from the letter, by intent.
2. Worker output was harvested by a script that extracts only the final assistant text block of each transcript with jq; no transcript was read into the coordinator's context. The mapping from transcript to subsystem tag was built by hand. Internal agent identifiers do not appear in this report.
3. One activity-log (chronicle) call returned an error earlier in the session.
4. The documentation review and the SUB-23 review each delivered their report in the conversation rather than as a file (the harness refused SUB-23's scratchpad write); both were copied verbatim to the scratchpad before verification.
5. HEAD moved twice during the audit, to 0527430 and 84c7e8b, both documentation-only, and two journal files appeared under `.private-journal/` beside one already present at baseline. §9 shows from file metadata and commit times that both belong to the session that made those commits; this session wrote no journal entry during the audit. The integrity verdict in §9 is therefore "fail, external cause".
6. `.env` is ignored and holds secrets. It was not read; one command listed its four variable names, no values. `.superpowers/`, `.private-journal/` and `bin/` were not read. `.scratch/` was listed and its test-function count taken; the test lens read its file headers and `go.mod`, and the SUB-18 worker listed it and noted that one file drives the coordinator. Nothing else from it was used.
7. The SUB-14 worker cited one claim against the wrong file; the coordinator re-verified it against the right one before accepting.
8. The API-review scratch file was overwritten once by an interim copy while the review was still running; the final 43-finding version is the one in the ledger. A lane labelled "LLM deep-dive" turned out to be another interim copy of the same review and was closed as merged (§7).
9. Two security-lens items were filed below the lens's severity, with the reasons quoted in §7 (T04, M11). The materiality reviewer's rejection of D02 was overridden to a P3 demotion (§7).
10. The Kata bodies do not carry the full SIFT finding schema; this report does (§8, pass 4).
11. L07 was a coordinator error, caught by the SUB-23 review and withdrawn before filing; the same false sentence was removed from L01 (§7).
12. M02's design-record citations were corrected once (from lines 170-171, 161-166 and 117 to 151, 142-145 and 97) before filing. D09's Related line was corrected twice.
13. The duplication pass ran on the 82-body set before three rejections and one withdrawal; its verdicts were applied to the surviving 79 (§8).
14. A comment on the Kata plan said "82 findings" when 79 were filed; it was corrected. The ledger's priority histogram had a typo, also corrected.
15. A check that the 19 comments had landed first reported zero, because Kata was run from outside the repository, where the project binding is not visible. Re-run from the repository, every target shows exactly one pass-2 comment. A false alarm, not a failure.
16. The footer of every pass-2 Kata body says reproduction sources and scope notes are preserved in this file. This report is that record: pass 2 was read-only and wrote no separate evidence or reproduction files; the first pass's `evidence/` and `repros/` directories are untouched. An earlier session note claiming those directories did not exist was wrong; they are tracked since 9a94996.
17. The §3 entries for SUB-01 to SUB-09 were drafted after a context compaction and then checked citation by citation against the bodies; one range was wrong and was fixed (§3.2 prerequisites line for SIFT-SUB-06-01).
18. No tests, builds, linters, formatters or generators were run; `go mod verify` was not run. Kata received 79 creates and 19 comments and no delete or purge. Nothing was committed.
19. The comment posted on mux#zee0 miscounts `agent-class/agents/mux` as one of the eight unlisted consumers; it is a copy of the module. B.2 carries the corrected inventory; the comment was left as posted rather than adding a second comment.
