# Mux quality audit — 2026-09-11

Audited commit: `6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0`. This report records findings, not fixes. Kata is the source of truth for current issue status; all 38 issues were open when this report was written. Filter with `kata list --label audit-2026-09-11 --agent`.

**Result: 13 P1, 23 P2, 2 P3 findings. No P0 incident was established.** P1 means fix before relying on the affected feature in production; P2 is an important correctness defect or verification gap; P3 is documentation or diagnostic cleanup. Conditional filesystem and dependency findings state their preconditions rather than claiming a demonstrated remote exploit.

Fix the approval bypass (mux#t0qf) and transcript loss (mux#bq4e, mux#jh1c) first. Next address stream/transport cancellation and lifecycle, provider history and response integrity, and the dependency findings. Land reproducing tests with each fix, then add stricter protocol and assembled-stack checks so the same classes of defects cannot pass unnoticed. I would not ship the affected approval, persistence, or streaming paths with these P1s open.

## Verification

| Check | Observed result |
| --- | --- |
| Go 1.26.6: make build | Passed |
| Go 1.26.6: go vet ./... | Passed |
| make lint; golangci-lint 2.12.2 with fresh cache | Passed, 0 lint issues |
| go mod tidy -diff | Passed, no dependency-file diff |
| go test -race -timeout=300s -json -coverprofile=... ./... | 718 passing test/subtest records, no failed/skipped test records |
| Coverage | 80.5% statements, including examples; not evidence that omitted scenarios work |
| go test -race -tags=integration -timeout=300s -json ./... | 724 passing records; TestMCPAgentIntegration unconditionally skipped |
| Declared toolchain Go 1.24.11: race suite with integration tag | Passed; same unconditional integration skip remains |
| govulncheck v1.8.0, Go 1.26.6 | Four symbol-level advisory matches in three dependencies; review exploitability |
| Fresh simple-example binary, 20 invocations | 20/20 returned no stdout; missing-schema warning on stderr |
| Adversarial offline reproductions | Exposed reported behavior, including a race in SessionID and execution of an explicitly denied tool |

Commands ran under `env -u GOROOT mise exec --` to avoid this machine's stale GOROOT. Full baseline JSON logs remain in `/tmp/mux-audit-20260911`; compact summaries and defect evidence are archived below. The baseline passes with pre-existing schema warnings, recovered-panic stack traces, and a shutdown read-error log in verbose/JSON output; mux#6ybc tracks capturing and asserting those diagnostics. These were not ignored or represented as pristine output.

## Scope and limits

Three independent reviewers covered providers; agent/orchestrator/coordinator/hooks; and MCP/session/tool/permission/skills. The lead reviewed CI, examples, dependencies and issue quality, and reran representative high-impact reproductions independently. All production packages, tests, relevant approved designs and canonical verification commands were examined. No production code or pre-existing local edits changed.

Reproductions use real conversion/execution code with local HTTP servers, subprocess fixtures or small in-memory test collaborators. They verify mux behavior and protocol envelopes. They are not live provider end-to-end tests. No credentialed model calls were made. Current official provider/MCP documentation and pinned SDK source were used where correctness depends on a wire contract. Linux/Windows runtime behavior and deployed exploitability were not tested; macOS arm64 was used with both Go versions above.

Two reproduced behaviors are excluded as outside approved contracts: concurrent writers to one session ID, and different skill catalogs sharing one tool.Registry. The API also documents whole-loop history locking and lossy event delivery; this audit does not relabel those choices as new bugs. They remain constraints callers must understand. Nil-request misuse alone was excluded. See [boundary caveats](evidence/boundary-caveats.md).

The dependency scanner reports static reachable-symbol matches, not proof of an exploit. Official advisories: [grpc](https://pkg.go.dev/vuln/GO-2026-6061), [x/text](https://pkg.go.dev/vuln/GO-2026-5970), [x/net IDNA](https://pkg.go.dev/vuln/GO-2026-5026), [x/net HTTP/2](https://pkg.go.dev/vuln/GO-2026-4918). The archived scan records versions and suggested patched versions; an upgrade must preserve or explicitly revise the supported Go floor.

## Filed issues

| Kata | Priority | Finding | Reviewer |
| --- | --- | --- | --- |
| mux#frc3 | P1 | [Update dependencies flagged by govulncheck and add a vulnerability gate](#frc3) | Lead |
| mux#t0qf | P1 | [Bind resumed approval decisions to tool call IDs instead of callback order](#t0qf) | Orchestration |
| mux#bq4e | P1 | [Preserve Message.Content when creating and appending transcripts](#bq4e) | Orchestration |
| mux#jh1c | P1 | [Preserve the previous transcript when file serialization or writes fail](#jh1c) | Orchestration |
| mux#ac2b | P1 | [Accumulate every Gemini chunk into the final streamed response](#ac2b) | Providers |
| mux#w9xj | P1 | [Preserve Anthropic thinking blocks and signatures through tool turns](#w9xj) | Providers |
| mux#62ba | P1 | [Retain Gemini thought signatures on replayed function calls](#62ba) | Providers |
| mux#vfwt | P1 | [Make stdio request writes cancellable without blocking Close](#vfwt) | Boundaries |
| mux#5n3p | P1 | [Preserve non-streaming OpenAI incomplete and failed response status](#5n3p) | Providers |
| mux#64sw | P1 | [Fail pending stdio calls when the response reader exits](#64sw) | Boundaries |
| mux#s53f | P1 | [Reject or correctly reset stdio clients before restarting a closed transport](#s53f) | Boundaries |
| mux#mgpj | P1 | [Make provider event sends cancellable when consumers stop reading](#mgpj) | Providers |
| mux#b2cn | P1 | [Encode inline PDF file_data as a MIME-qualified data URL](#b2cn) | Providers |
| mux#sask | P2 | [Run the integration-tagged suite in CI and replace the skipped MCP agent test](#sask) | Lead |
| mux#x39z | P2 | [Wait for event output before the simple example exits](#x39z) | Lead |
| mux#tp9k | P2 | [Allow JSONL loader to read large records emitted by its own writer](#tp9k) | Orchestration |
| mux#t0av | P2 | [Publish completion only after the final session checkpoint succeeds](#t0av) | Orchestration |
| mux#1wwm | P2 | [Make EventBus.Reset safe after EventBus.Close](#1wwm) | Orchestration |
| mux#jc2v | P2 | [Synchronize SessionID reads with Resume state restoration](#jc2v) | Orchestration |
| mux#m886 | P2 | [Strip tool-result blocks from mixed user turns retained by compaction](#m886) | Orchestration |
| mux#p52w | P2 | [Check cancellation before consuming immediately available rate-limit tokens](#p52w) | Orchestration |
| mux#2jw4 | P2 | [Return Gemini function results with the matching call ID](#2jw4) | Providers |
| mux#dz9z | P2 | [Keep Anthropic max_tokens strictly above the thinking budget](#dz9z) | Providers |
| mux#29jh | P2 | [Forward Request.Temperature to Anthropic message parameters](#29jh) | Providers |
| mux#drm8 | P2 | [Allow URL PDFs on the OpenAI Responses streaming path](#drm8) | Providers |
| mux#a825 | P2 | [Make initial provider streaming failures reach RetryClient retry logic](#a825) | Providers |
| mux#xat1 | P2 | [Send protocol-valid initialized notifications on both MCP transports](#xat1) | Boundaries |
| mux#xxrt | P2 | [Keep HTTP client state and active requests consistent across failure and Close](#xxrt) | Boundaries |
| mux#k0s2 | P2 | [Preserve all assistant text blocks in Chat Completions history](#k0s2) | Providers |
| mux#whef | P2 | [Accept default SSE message events and tool responses larger than 64 KiB](#whef) | Boundaries |
| mux#wr5j | P2 | [Honor MaxResponseBytes when it is below the initial scanner buffer](#wr5j) | Boundaries |
| mux#k5mx | P2 | [Send Ollama's supported max_tokens field](#k5mx) | Providers |
| mux#xe1p | P2 | [Follow tools/list pagination instead of silently dropping later tools](#xe1p) | Boundaries |
| mux#3zpg | P2 | [Request the usage trailer when streaming from Ollama](#3zpg) | Providers |
| mux#ns1f | P2 | [Validate JSON HTTP response IDs before accepting tool results](#ns1f) | Boundaries |
| mux#93yp | P2 | [Create session snapshot temporary files exclusively instead of following a predictable symlink](#93yp) | Boundaries |
| mux#6ybc | P3 | [Capture and assert expected warnings and recovered panic logs in tests](#6ybc) | Lead |
| mux#3ctg | P3 | [Correct the README claim that mux implements an MCP server](#3ctg) | Lead |

## Reproduction bundle

The files under `repros/` are archived source text, deliberately excluded from the normal passing suite. Assertions in the defect tests expect correct behavior and fail against the audited commit. Copy them to a disposable directory with their `.go` names to rerun; use the same module as the audit baseline. These are audit probes, not production tests or mock application modes.

- `provider_audit_test.go.txt`: place in `llm/` through a Go `-overlay` mapping from an added test filename to its scratch copy. Run `go test -overlay=OVERLAY.json ./llm -run TestAudit -count=1`. The nil-request probe was removed from this archive because it was excluded from findings.
- `findings_test.go.txt`, `persistence_test.go.txt`, `approval_test.go.txt`: copy together and run `go test -race -count=1` with the three explicit scratch filenames from the mux workspace.
- `boundary-repro.go.txt`: copy to a scratch `.go` file and `go run` it. It launches only local fixtures and temporary files. Its concurrent-writer demonstration is explicitly excluded from findings.
- `boundary-lifecycle.go.txt`: copy to `.go`; it expects the preceding helper built at `/tmp/mux-boundary-audit/helper`. Run normally for HTTP cases, or with `restart` to demonstrate the stdio restart panic. The restart case intentionally panics; contain it in its own process.

Evidence files preserve the actual outputs, including intentional failing assertions. Temporary absolute paths in issue descriptions identify the original run; the source text is preserved here for later use.

## Finding details

<a id="frc3"></a>

### mux#frc3 — Update dependencies flagged by govulncheck and add a vulnerability gate (P1)

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0, 2026-09-11.

Locations: go.mod:29-35; .github/workflows/ci.yml.

Verification: env -u GOROOT mise exec -- go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./... on Go 1.26.6 reports four symbol-level findings in three dependencies: grpc v1.66.2 (GO-2026-6061; fixed v1.82.1), x/text v0.27.0 (GO-2026-5970; fixed v0.39.0), x/net v0.41.0 (GO-2026-5026; fixed v0.55.0, and GO-2026-4918; fixed v0.53.0). Scanner exits 3 (go run wrapper exits 1). This is static reachability evidence, not a demonstrated exploit; especially review the conservative grpc interface-dispatch traces before assigning deployment exposure.

Official advisories: https://pkg.go.dev/vuln/GO-2026-6061 https://pkg.go.dev/vuln/GO-2026-5970 https://pkg.go.dev/vuln/GO-2026-5026 https://pkg.go.dev/vuln/GO-2026-4918 . Full scan log: /tmp/mux-audit-20260911/vuln.log.

Fix: update the SDK/transitive dependency graph to patched versions compatible with the supported Go version, or document any required minimum-Go change. Add a maintained govulncheck gate. Do not blindly raise every dependency or treat the 30 additional non-called/module-only advisories as proven reachable.

Acceptance: govulncheck has no unresolved reachable findings; build, vet, lint, race tests including integration tags pass; supported minimum Go version is tested.

<a id="t0qf"></a>

### mux#t0qf — Bind resumed approval decisions to tool call IDs instead of callback order (P1)

orchestrator/orchestrator.go:723-738 constructs a queue of approval-required IDs and consumes one ID per executor approval callback. Tool registry changes during a resumed batch can remove an earlier approved tool, so Execute returns ErrToolNotFound without consuming its queued approval. The next explicitly denied tool then consumes the approved ID and executes. This violates Decision.Approvals' documented per-tool-call-ID contract. A synchronous first tool unregistering a later tool reproduces the issue with no race or unsupported concurrent execution.

Reproduction: TestResumeApprovalRemainsBoundToToolCallID in /tmp/mux-orchestration-audit/approval_test.go. Batch refresh (no approval), approved-id (true), denied-id (false); refresh unregisters approved. Output: explicitly denied tool executed after an earlier approved tool disappeared from registry.

Acceptance: Apply each decision to the exact ContentBlock.ID being executed. Regression must keep denied-id blocked when an earlier tool disappears or changes approval requirements during a batch, while preserving same-name per-ID approvals.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="bq4e"></a>

### mux#bq4e — Preserve Message.Content when creating and appending transcripts (P1)

agent/transcript.go:43-50 and 68-73 record only msg.Blocks. llm.NewUserMessage and NewAssistantMessage store text in msg.Content (llm/types.go:77-82), and ordinary Run/Continue prompts use NewUserMessage. SaveTranscript→SaveJSON/JSONL→restore silently replaces those turns with empty messages, losing user instructions and history. This affects the default path, not only custom messages.

Reproduction: TestTranscriptPreservesTextConstructors in /tmp/mux-orchestration-audit/findings_test.go. FromMessages(NewUserMessage("critical user instruction")), JSON roundtrip, ToMessages yields Role:user Content:"" Blocks:nil.

Acceptance: Round-trip text constructors and mixed Content/Blocks through FromMessages, Append, JSON, JSONL, and actual Agent SaveTranscript/RestoreTranscript without losing or duplicating text.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="jh1c"></a>

### mux#jh1c — Preserve the previous transcript when file serialization or writes fail (P1)

agent/transcript.go:208-221 and 236-249 call os.Create on the destination before JSON serialization. An unsupported ContentBlock.Input value causes a normal encode error after truncating the prior valid file. JSON output becomes zero bytes; JSONL can leave only its header. Disk-write failures and interruption likewise expose partially rewritten history. The error is returned but the only prior durable transcript is already destroyed.

Reproduction: TestTranscriptSaveFailurePreservesExistingFile in /tmp/mux-orchestration-audit/persistence_test.go saves valid history, adds an unsupported channel value to a ContentBlock.Input, and saves to the same path. Actual JSON: 349→0 bytes; JSONL: 278→140 bytes, only header.

Acceptance: Encode/write to a temporary file in the destination directory and atomically replace only after successful completion. On serialization/write failure the previous file must remain byte-for-byte intact and loadable, for both formats.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="ac2b"></a>

### mux#ac2b — Accumulate every Gemini chunk into the final streamed response (P1)

At llm/gemini.go:314 each chunk replaces lastResp, and line 344 converts only that last chunk into EventMessageStop.Response. Gemini chunks contain incremental content. An offline httptest SSE response containing text chunks 'Hello ' and 'world' produces live deltas 'Hello world' but final Response.TextContent() == 'world'. A final metadata-only chunk can erase all text and previously emitted tool calls. The orchestrator trusts this final response, so streaming changes execution/history, not merely presentation. Reproduction: TestAuditGeminiStreamAccumulates in /tmp/mux-provider-audit/provider_audit_test.go; failure recorded in results.log. Acceptance: accumulate text, thinking and function-call parts across chunks, preserving final usage/finish reason; add multi-chunk text, early tool call plus final usage-only chunk, and thinking/text separation regressions. Existing gemini_test.go has no streaming integration coverage.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="w9xj"></a>

### mux#w9xj — Preserve Anthropic thinking blocks and signatures through tool turns (P1)

llm/anthropic.go:167-171 drops the signature when converting a thinking block; llm/types.go:108-109 cannot retain it. llm/anthropic.go:77-90 has no thinking serialization case, so the next request omits the whole block. The stream delta switch also ignores signature_delta. Trigger: an extended-thinking response contains thinking plus tool_use, then the caller returns the tool result while continuing that turn. Anthropic requires the complete unmodified thinking block alongside the tool call; mux cannot supply it, breaking the next request. Offline TestAuditAnthropicThinkingRoundTrip converts a signed thinking+tool response through mux history; serialized history contains only tool_use. Acceptance: preserve signed and redacted thinking data in normal and streamed responses and replay them unmodified; exercise two requests through httptest and assert the second retains the original thinking/signature. Official contract: https://platform.claude.com/docs/en/about-claude/models/extended-thinking-models (Thinking with tool use / Preserving thinking blocks).

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="62ba"></a>

### mux#62ba — Retain Gemini thought signatures on replayed function calls (P1)

llm/gemini.go:226-232 copies function name, ID and arguments but drops genai.Part.ThoughtSignature; llm/gemini.go:147 constructs a fresh function-call part with no signature. Streaming has the same omission at lines 328-333. Trigger: Gemini 3 returns a signed function call and the next request includes its tool result. The official GenerateContent contract requires the signature on the first functionCall of each step of the current turn, and omission produces HTTP 400. This fails even when the caller correctly appends mux Response.Content to history. Offline TestAuditGeminiToolMetadata proves an opaque signature is lost after response/request conversion. Acceptance: retain opaque part signatures in shared content/history and replay them in non-streaming and streamed multi-step tool calls; test signed parallel calls and exact byte preservation. Official current documentation: https://ai.google.dev/gemini-api/docs/generate-content/thought-signatures .

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="vfwt"></a>

### mux#vfwt — Make stdio request writes cancellable without blocking Close (P1)

Source: mcp/stdio.go:190-195 holds c.mu while writing an entire JSON request to the child stdin. Close requires the same mutex at line 228, and call does not select on ctx.Done until send has returned (lines 163-175).
Trigger: child completes initialization then stops reading stdin; issue CallTool with a 4 MiB argument and a 50 ms request timeout.
Expected: request timeout returns promptly; Close remains able to terminate/reap the child and release the write.
Actual reproduced: the call remains blocked after its deadline, and concurrent Close also blocks. Only canceling the separate context originally passed to Start, thereby killing the subprocess, released it. Existing Close timeouts cannot help because Close never gets past c.mu.Lock.
Reproduction: env -u GOROOT mise exec -- go run /tmp/mux-boundary-audit/repro.go; output includes 'stdio write ignores expired CallTool context' and 'stdio Close blocked behind stdin write'. Helper subprocess is local, no real MCP provider.
Acceptance: integration test with a real helper process that stops reading after handshake; large request with short context must terminate and Close must finish without canceling the Start context. Separate write serialization from lifecycle lock and ensure blocked writes can be interrupted.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="5n3p"></a>

### mux#5n3p — Preserve non-streaming OpenAI incomplete and failed response status (P1)

llm/openai.go:601 defaults every Responses result to end_turn and the conversion never examines resp.Status, resp.IncompleteDetails or resp.Error. CreateMessage returns that conversion without status checks at line 660. Trigger: HTTP 200 response with status=incomplete, incomplete_details.reason=max_output_tokens and partial output. mux reports end_turn, so the orchestrator treats truncation as successful completion; failed responses similarly become empty success. TestAuditOpenAIIncomplete unmarshals this valid SDK response and observes end_turn instead of max_tokens. Streaming already emits errors for response.failed/incomplete. Acceptance: handle failed status as an error and max-output truncation as MaxTokens or the same documented error policy used by streaming; add HTTP-level non-streaming status regressions and verify no partial tool call is executed as complete.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="64sw"></a>

### mux#64sw — Fail pending stdio calls when the response reader exits (P1)

Source: mcp/stdio.go:199-207 only closes c.done when Scan reaches EOF or errors; call selects on respChan, ctx.Done and closeChan at lines 167-176, never done. running also remains true.
Trigger: initialized subprocess exits after receiving tools/call, or exceeds the configured scanner limit while caller uses context.Background.
Expected: all outstanding requests receive a transport EOF/read error immediately and the client transitions out of running.
Actual reproduced: subprocess EOF produces context deadline exceeded only when the caller's 200 ms timeout fires. With Background the call has no event that can complete it until an explicit Close. Scanner errors similarly log stderr and strand callers.
Reproduction: repro.go prints 'stdio EOF CallTool: context deadline exceeded'. Existing TestStdioCustomMaxResponseBytes (mcp/mcp_test.go:2010) only asserts any error after a 10-second context and therefore passes on this wrong timeout behavior.
Acceptance: real subprocess exits or emits an oversized line during requests; outstanding calls promptly receive a transport error distinct from caller deadline and later calls fail as disconnected. Assert cleanup and error propagation for both EOF and scanner failure.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="s53f"></a>

### mux#s53f — Reject or correctly reset stdio clients before restarting a closed transport (P1)

Source: mcp/stdio.go:54-58 only guards running; Start does not recreate closeChan/done from newStdioClient. Close sets running=false and closes closeChan at lines 233-234. readResponses also closes the reused done channel at line 200.
Trigger: Start -> Close -> Start, including retry after a handshake failure which itself calls Close.
Expected: a stable closed-client error, or a clean new lifecycle if restart is supported.
Actual reproduced: second Start enters, starts another child, its initialize call observes the already-closed closeChan, then cleanup panics 'close of closed channel' at stdio.go:234. The reader can independently double-close done.
Reproduction: build local helper with env -u GOROOT mise exec -- go build -o /tmp/mux-boundary-audit/helper /tmp/mux-boundary-audit/repro.go; run env -u GOROOT mise exec -- go run /tmp/mux-boundary-audit/lifecycle.go restart. Observed panic stack points to stdio.go:234 -> Start:103.
Acceptance: tests for Start/Close/Start and retry after initialization failure return safely and leave no subprocess. If restart is unsupported, explicitly reject it before launching the child; test concurrent Close/Start lifecycle too.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="mgpj"></a>

### mux#mgpj — Make provider event sends cancellable when consumers stop reading (P1)

llm/openai.go:713-716 sends directly to the 100-slot event channel without selecting ctx.Done. The same root cause exists at llm/anthropic.go:362-365, llm/gemini.go:320-323, llm/ollama.go:109-112 and llm/openrouter.go:165-168, including terminal/error sends. Trigger: a caller stops draining a long stream and cancels its context after the buffer fills. The producer remains blocked on channel send forever and retains its accumulator even though the HTTP request is canceled. TestAuditCancellationFullStream serves 250 OpenAI text deltas, waits for len(ch)==100, cancels, and finds CreateMessageStream.func1 still on the goroutine stack; draining afterward cleans it up. Acceptance: all sends, including recovery/error paths, must select cancellation and producers must release SDK streams; table-test all providers with full buffers and cancellation, confirming termination without requiring consumers to drain.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="b2cn"></a>

### mux#b2cn — Encode inline PDF file_data as a MIME-qualified data URL (P1)

llm/openai.go:282 and :349 assign raw base64 to FileData. OpenAI Responses and OpenRouter Chat PDF contracts use a data:application/pdf;base64,... URL; unlike input_audio.data, file_data is not the raw base64 string. Both NewPDFFromBytes and NewPDFFromFile therefore produce the wrong documented wire format. TestAuditPDFWireEncoding marshals both paths and observes file_data='JVBERi0xLjQ=' rather than the PDF data URL. No live provider call was made; contract checked against official documentation. Acceptance: add the PDF MIME prefix in both translators and assert the complete wire value (not merely nonempty file_data) for bytes/file inputs; retain filename behavior. Sources: https://developers.openai.com/api/docs/guides/file-inputs (Go Responses and Chat examples explicitly construct the data URL) and https://openrouter.ai/docs/guides/overview/multimodal/pdfs (requires base64-encoded data URLs).

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="sask"></a>

### mux#sask — Run the integration-tagged suite in CI and replace the skipped MCP agent test (P2)

Audit baseline: 6f7305e, 2026-09-11.

Locations: integration_test.go:1 and :234-280; .github/workflows/ci.yml:35-36; Makefile:26-27.

The root full-stack tests require the integration build tag, but neither CI nor make test supplies it. Running the baseline command passed 718 test/subtest records; adding -tags=integration passed 724, with TestMCPAgentIntegration unconditionally skipped. The skipped test contains only commented-out implementation. Package-local transport and orchestration tests do exist; the missing coverage is the assembled agent-to-MCP path and execution of the root suite in CI.

Reproduce: go test -race -tags=integration -timeout=300s ./... and compare with the CI command.

Fix: add integration-tagged verification to CI/canonical commands and implement an agent-to-real-MCP-server scenario with actual tool execution. The current root integration tests use a mock LLM, so they do not establish live provider end-to-end compatibility. Provide a separately gated real-provider smoke path if credentials are needed.

Acceptance: CI demonstrably runs root integration cases; MCP-agent scenario executes rather than skips; end-to-end checks use a real server/provider where advertised, with explicit reporting when credentials are unavailable.

<a id="x39z"></a>

### mux#x39z — Wait for event output before the simple example exits (P2)

Audit baseline: 6f7305e, 2026-09-11.

Location: examples/simple/main.go:54-73; similar unsynchronized output in examples/minimal/main.go:90-113.

The simple example launches a goroutine to print events, then returns from main as soon as Run returns. Run closes the event channel but does not wait for this consumer to drain it. All 20 local runs of a freshly built example exited successfully with zero stdout, losing both Hello from mux! and [Complete]. A missing InputSchema warning still appears on stderr. The minimal REPL can likewise advance or exit before its output drains; examples/full already demonstrates the done-channel pattern.

Reproduce: go build -o /tmp/mux-simple ./examples/simple; run /tmp/mux-simple with captured stdout repeatedly.

Fix: wait for each event consumer before exit or advancing the REPL; define EchoTool.InputSchema so the introductory example runs without a schema warning.

Acceptance: a process-level test captures the complete expected stdout on every run and no schema warning; the minimal example flushes each response before printing the next prompt or exiting.

<a id="tp9k"></a>

### mux#tp9k — Allow JSONL loader to read large records emitted by its own writer (P2)

agent/transcript.go:147 creates a default bufio.Scanner with its approximately 64 KiB token limit, while SaveJSONL imposes no record-size limit. A 70,000-byte text/tool output (or ordinary base64 image/PDF payload) saves successfully but cannot be loaded, preventing resume of otherwise valid transcripts.

Reproduction: TestTranscriptLargeJSONLRoundTrip in /tmp/mux-orchestration-audit/findings_test.go saves a 70,000-byte text block and LoadJSONL fails: read lines: bufio.Scanner: token too long.

Acceptance: Large tool-output and multimodal records accepted by the writer must round-trip through LoadJSONL and file wrappers, with an explicit safe limit/error if a limit is intended.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="t0av"></a>

### mux#t0av — Publish completion only after the final session checkpoint succeeds (P2)

orchestrator/orchestrator.go:383-388 transitions to StateComplete and publishes EventComplete before saving the complete snapshot. If Store.Save fails, Run returns a checkpoint error and emits EventError, but handleError cannot transition StateComplete→StateError (state.go:35 only permits Idle), leaving State() equal to complete. Event subscribers can already have treated the run as durable success.

Reproduction: TestCheckpointFailureDoesNotAnnounceSuccess in /tmp/mux-orchestration-audit/findings_test.go uses an in-memory final-response client and Store.Save returning disk full. Run returns error; State()==complete; subscribed stream contains EventComplete.

Acceptance: A failed final checkpoint must return the persistence error, expose StateError, and publish no successful completion event. Successful persistence must still publish exactly one completion.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="1wwm"></a>

### mux#1wwm — Make EventBus.Reset safe after EventBus.Close (P2)

orchestrator/events.go:143-146 closes subscriber channels but retains them. Reset at lines 151-156 closes every retained channel again without checking closed, panicking. The exported Reset contract promises to prepare the bus for reuse; neither method documents a forbidden Close→Reset sequence. Concurrent Close and Reset can reach the same state despite their mutex.

Reproduction: TestEventBusCloseReset in /tmp/mux-orchestration-audit/findings_test.go calls NewEventBus, Subscribe, Close, Reset and catches close of closed channel.

Acceptance: Close→Reset→Subscribe must allow reuse without panic; repeated and concurrently ordered Close/Reset calls must never close any channel twice.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="jc2v"></a>

### mux#jc2v — Synchronize SessionID reads with Resume state restoration (P2)

orchestrator/orchestrator.go:145 reads sessionID without synchronization while Resume writes it at line 678 under o.mu. An observer querying the public SessionID accessor while one normal Resume runs has a data race. The whole-loop lock protects messages but not this accessor. Adding that same lock directly would also need to preserve SessionID calls from session hooks without introducing reentrant deadlock.

Reproduction: TestSessionIDConcurrentWithResume in /tmp/mux-orchestration-audit/persistence_test.go. go test -race reports Read at SessionID line 145 versus Write at Resume line 678. Test uses one resume goroutine and one observing goroutine, never concurrent Run/Resume calls.

Acceptance: Concurrent SessionID observation during Resume must pass -race; session hooks must remain able to read the current session ID without deadlock.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="m886"></a>

### mux#m886 — Strip tool-result blocks from mixed user turns retained by compaction (P2)

orchestrator/compact.go:119-128 treats a user message containing any text/image/non-tool-result content as retainable, then collectRecentUserMessages at 137-140 copies the whole message. buildCompactedHistory removes the original assistant tool_use turn but retains its tool_result within the mixed user turn. Restoring valid mixed history and calling Continue(ctx, "") with compaction enabled therefore sends an orphan result to the provider. The current pure-tool-result regression does not cover mixed blocks.

Reproduction: TestMixedUserToolResultCompaction in /tmp/mux-orchestration-audit/findings_test.go restores long prior context plus a matching tool-use/mixed user-result pair, calls Continue with empty text, observes the next request, and reports orphan result retained after compaction: call1. Also verified direct helper test through Go overlay in internal_test.go.

Acceptance: Compaction of mixed text/image plus tool-result turns must retain genuine user content while removing results whose matching tool-use was summarized away. Validate the actual next request contains no orphan result IDs.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="p52w"></a>

### mux#p52w — Check cancellation before consuming immediately available rate-limit tokens (P2)

coordinator/ratelimiter.go:33-37 invokes tryTake before checking ctx.Done. If tokens are available, Take returns nil and consumes capacity even when the context was already canceled. Cancellation is only honored on the waiting branch, contrary to Take's context-cancellation contract; cancellation-aware callers can proceed and spend a token after their work has been canceled.

Reproduction: TestRateLimiterHonorsAlreadyCancelledContext in /tmp/mux-orchestration-audit/findings_test.go cancels a context before NewRateLimiter(10,1).Take(ctx,1). Actual error is nil.

Acceptance: Pre-canceled and pre-expired contexts must return their context error without consuming tokens; waiting cancellation and normal token acquisition must continue working.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="2jw4"></a>

### mux#2jw4 — Return Gemini function results with the matching call ID (P2)

llm/gemini.go:147 ignores ContentBlock.ID when constructing FunctionCall, and line 154 ignores ContentBlock.ToolUseID when constructing FunctionResponse. A response call ID survives convertGeminiResponse but disappears from both the replayed call and its result. For ID-bearing calls, especially parallel invocations of the same function, this removes the required correlation. The pinned google.golang.org/genai v1.54.0 types.go:1176-1180 says populated function-call IDs must be matched on responses; FunctionResponse.ID is explicitly provided for this. TestAuditGeminiToolMetadata supplies call_1 and observes both IDs empty. Acceptance: assign call ID and response ID on the corresponding SDK fields; regression with two same-name calls and distinct IDs/results. This is separate from opaque thought-signature loss.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="dz9z"></a>

### mux#dz9z — Keep Anthropic max_tokens strictly above the thinking budget (P2)

llm/anthropic.go:60-61 raises MaxTokens to exactly Thinking.Budget when the budget is greater. Equality is also left unchanged. Trigger: MaxTokens=4096 and enabled thinking Budget=8192; serialized max_tokens and budget_tokens both become 8192, which Anthropic rejects. The pinned SDK ThinkingConfigEnabledParam explicitly requires budget_tokens >=1024 and less than max_tokens, as does the current official guide; this client does not enable the interleaved-thinking exception. Offline TestAuditAnthropicBudget reproduces equality. Acceptance: ensure a valid budget leaves output headroom (or return a clear local validation error), and cover budget below, equal to and above max_tokens. Existing TestConvertRequest_WithThinkingBumpsMaxTokens incorrectly blesses equality. Contract: https://platform.claude.com/docs/en/build-with-claude/extended-thinking .

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="29jh"></a>

### mux#29jh — Forward Request.Temperature to Anthropic message parameters (P2)

convertRequest at llm/anthropic.go:52-68 initializes model, max tokens and thinking, but never assigns req.Temperature anywhere in the function. An ordinary text request with Temperature=&0.2 silently uses Anthropic's default on both transports. The pinned anthropic.MessageNewParams has a Temperature field; the other providers already honor the shared Request field. TestAuditAnthropicTemperature marshals a request with 0.2 and finds temperature absent. Acceptance: forward a nonnil temperature (including explicit zero), keep nil omitted, and add wire assertions for both cases; handle incompatible thinking combinations explicitly rather than silently discarding the field.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="drm8"></a>

### mux#drm8 — Allow URL PDFs on the OpenAI Responses streaming path (P2)

llm/openai.go:675-677 still applies Chat Completions source validation (allowURLPDF=false), although lines 681-682 actually use the Responses API. A NewPDFFromURL request succeeds through non-streaming conversion but CreateMessageStream returns ErrUnsupportedSource before reaching HTTP. This violates the approved streaming-cleanup goal of matching request semantics and the capability declaration. TestAuditOpenAIStreamingURLPDF reproduces the local rejection using an offline Responses SSE server. Acceptance: apply Responses-compatible source validation in both transports; replace the stale TestOpenAICreateMessageStream_PDFFromURL_ErrUnsupportedSource with a wire test that asserts /responses and input_file.file_url.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="a825"></a>

### mux#a825 — Make initial provider streaming failures reach RetryClient retry logic (P2)

llm/retry.go:90-92 returns immediately whenever the inner streaming call returns a channel and nil error. All concrete provider stream methods return that shape even when the initial HTTP request fails; the SDK failure appears later as EventError. Consequently RetryConfig.MaxRetries does not retry actual initial 429/503 stream failures, although its comment and fake-client tests claim that behavior. SDK-internal retries do not make the wrapper's configured retry budget work. Offline TestAuditRetryInitialHTTPFailure disables only SDK retries in an internal client, serves initial HTTP 503, sets RetryConfig.MaxRetries=1, and records exactly one HTTP request and a nil synchronous error instead of two attempts. Acceptance: surface initialization failures synchronously or retry before any generated content has escaped; test real adapter+httptest initial 503 then success, plus no replay after mid-stream content.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="xat1"></a>

### mux#xat1 — Send protocol-valid initialized notifications on both MCP transports (P2)

Source: mcp/types.go:23 declares ID with json:"id" (not optional), and both mcp/stdio.go:180-182 and mcp/http.go:180-195 serialize a Request with zero ID as a notification. HTTP notify also omits the Accept header supplied by post.
Trigger: connect to an MCP server that validates notification envelopes and HTTP content negotiation.
Expected: notifications/initialized JSON has no id field; each HTTP POST advertises both application/json and text/event-stream.
Actual reproduced: a server rejecting an id in initialized yields Start error 400; independently a server requiring the specified Accept header yields Start error 406. Both are ordinary handshake interoperability failures; the stdio wire notification also contains id:0.
Reproduction: repro.go prints 'HTTP strict-id Start: ... 400 Bad Request' and 'HTTP strict-accept Start: ... 406 Not Acceptable'.
Acceptance: wire-level tests inspect actual outgoing initialized JSON on stdio and HTTP, require id absence, require the HTTP Accept values, and complete a handshake against strict validation. Use a distinct notification envelope or optional request IDs so request IDs remain intact.
Sources: https://modelcontextprotocol.io/specification/2025-06-18/basic/lifecycle (initialized envelope); https://modelcontextprotocol.io/specification/2025-06-18/basic/transports (POST Accept requirements).

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="xxrt"></a>

### mux#xxrt — Keep HTTP client state and active requests consistent across failure and Close (P2)

Source: mcp/http.go:69-76 sets running=true before initialized notification succeeds. Close at lines 275-284 only flips a flag/closes notifications via sync.Once; it never cancels active HTTP requests, and Start at lines 46-52 does not check a terminal closed state.
Triggers: (1) server returns 503 for initialized, (2) server holds a tools/call response open while caller calls Close, (3) Start/Close/Start/Close.
Expected: failed Start leaves no usable half-initialized client; Close unblocks current operations; a closed client either rejects Start or starts a completely reset lifecycle that subsequent Close can close.
Actual reproduced: (1) Start returns an error but ListTools succeeds and retry Start returns 'client already running'; (2) Close returns while a Background-context request stays blocked; (3) second Start succeeds, but second Close is a no-op and ListTools still succeeds, while notifications is permanently closed.
Reproduction: repro.go ('HTTP Close returned but pending request still blocked'); lifecycle.go ('failed-start', 'restart-http' cases). No remote servers used.
Acceptance: local HTTP lifecycle tests cover failed initialized notification, Close during blocked POST/stream, Start after Close, and Close racing Start. Enforce explicit lifecycle state and cancellation tied to client lifetime; publish running only after successful handshake.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="k0s2"></a>

### mux#k0s2 — Preserve all assistant text blocks in Chat Completions history (P2)

llm/openai.go:504 assigns textContent=block.Text for each text block. convertAssistantMessage therefore silently retains only the last block (and overwrites Message.Content) for Ollama and OpenRouter requests. Trigger: replay a valid assistant Message with several text ContentBlocks, for example history imported from a Responses/Anthropic result. TestAuditChatAssistantText serializes Content='one' plus blocks 'two' and 'three' and sees only content='three'. Acceptance: retain text in source order when converting assistant history, with and without tool calls; test multiple blocks and simultaneous Content/Blocks. Other provider converters already preserve all these fields.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="whef"></a>

### mux#whef — Accept default SSE message events and tool responses larger than 64 KiB (P2)

Source: mcp/http.go:145 processes only event.Event == "message", while mcp/sse.go:33 initializes the event name to empty and never applies the SSE default. newSSEReader at sse.go:25-27 also leaves bufio.Scanner's 64 KiB token maximum unchanged.
Triggers: server returns a valid data-only SSE event (omitting optional event: message), or a single JSON result line with 100,000 text bytes, typical of file/search tool output.
Expected: both responses decode as successful MCP responses, subject to an intentional documented size policy.
Actual reproduced: data-only event is silently ignored and CallTool ends 'response not found in SSE stream'; explicitly named large event fails 'read SSE: bufio.Scanner: token too long'. Equivalent large JSON HTTP responses are accepted, and stdio already raises its scanner limit.
Reproduction: repro.go 'HTTP sse-default CallTool' and 'HTTP sse-large CallTool' cases.
Acceptance: parser/HTTP integration tests for data-only default events, explicit message events, multiline data, and 100 KB payloads; exercise an explicit maximum and proper transport error for truly oversized data. Apply default event semantics and an intentional scanner capacity.
Source: https://modelcontextprotocol.io/specification/2025-06-18/basic/transports defines SSE response support and links its SSE standard.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="wr5j"></a>

### mux#wr5j — Honor MaxResponseBytes when it is below the initial scanner buffer (P2)

Source: mcp/stdio.go:95 passes make([]byte,0,64*1024) to Scanner.Buffer regardless of the configured max. Scanner can use the supplied buffer without growing it, so a lower max does not enforce that lower ceiling. This contradicts ServerConfig.MaxResponseBytes docs at mcp/types.go:99-103.
Trigger: configure MaxResponseBytes=4096 and receive an 8192-byte text result in one JSON line.
Expected: reject the line for exceeding 4096 bytes and surface a transport size error.
Actual reproduced: CallTool succeeds and returns all 8192 text bytes. Limits below 64 KiB effectively inherit the 64 KiB initial buffer.
Reproduction: repro.go prints 'stdio quota4096 accepted length=8192 error=<nil>'. Existing quota test emits >100 KiB, so does not catch this gap.
Acceptance: tests with payloads between a small configured ceiling and 64 KiB, plus boundary/default values; size the initial buffer consistently with configured maximum or enforce the per-line bound directly.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="k5mx"></a>

### mux#k5mx — Send Ollama's supported max_tokens field (P2)

Ollama calls the shared convertOpenAIRequest at llm/ollama.go:58 and :79; llm/openai.go:58-60 sets only max_completion_tokens. Ollama's ChatCompletionRequest accepts max_tokens, and its FromChatRequest maps that field to num_predict. It does not define max_completion_tokens, so mux's configured limit is silently ignored on both transports. Offline TestAuditOllamaMaxTokens sends MaxTokens=17 to an Ollama-shaped HTTP decoder and finds max_tokens absent/zero. Acceptance: serialize the supported Ollama field and verify a request MaxTokens=17 reaches max_tokens=17 on streaming and non-streaming paths. Current primary docs/source checked: https://docs.ollama.com/api/openai-compatibility and https://github.com/ollama/ollama/blob/main/openai/openai.go (ChatCompletionRequest and FromChatRequest).

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="xe1p"></a>

### mux#xe1p — Follow tools/list pagination instead of silently dropping later tools (P2)

Source: mcp/types.go:63-65 ToolsListResult has only Tools; stdio.go:123-132 and http.go:221-241 make one tools/list call with no cursor and return immediately.
Trigger: an MCP server returns first-page tools plus nextCursor. This is valid protocol behavior; later tools require tools/list with that cursor.
Expected: ListTools returns the complete tool list (or exposes pagination explicitly), allowing ToolManager.Refresh to discover every tool.
Actual reproduced: ListTools returns only the first tool and issues exactly one request; nextCursor is discarded by unmarshalling and the public API gives callers no way to fetch the rest. ToolManager consequently never registers later tools.
Reproduction: lifecycle.go 'pagination' local server returns tools:[first],nextCursor:'second-page'; output len=1/request_count=1 confirms the cursor is ignored.
Acceptance: local HTTP and stdio server tests with two pages assert second request includes returned cursor and both tools are returned; propagate page errors and guard malformed repeated cursors.
Source: https://modelcontextprotocol.io/specification/2025-06-18/server/tools, Listing Tools request/response pagination.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="3zpg"></a>

### mux#3zpg — Request the usage trailer when streaming from Ollama (P2)

llm/ollama.go:79-80 sends convertOpenAIRequest directly without stream_options.include_usage. Ollama emits its token-usage trailer only when that flag is true; otherwise the accumulator's usage remains zero and the final mux Response reports no tokens for a completed generation. Offline TestAuditOllamaStreamRequestsUsage confirms stream_options is absent. The live-server implication is confirmed in official Ollama middleware ChatWriter.writeResponse: the only trailer carrying usage is guarded by streamOptions != nil && IncludeUsage. Acceptance: request usage on the streaming path and add an httptest response that emits usage only when requested; assert final Response usage and orchestrator accounting. Primary source: https://github.com/ollama/ollama/blob/main/middleware/openai.go (ChatWriter.writeResponse). Do not automatically group OpenRouter here; its default usage semantics differ.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="ns1f"></a>

### mux#ns1f — Validate JSON HTTP response IDs before accepting tool results (P2)

Source: mcp/http.go:171-177 decodes a JSON response and returns it without checking ID against req.ID. The neighboring SSE branch checks IDs at lines 165-168, so this trust check changes with response format.
Trigger: server or intermediary returns a valid JSON-RPC result with an unrelated ID, including during initialize or tools/list/call.
Expected: reject mismatched/missing IDs as a protocol error; never associate another request's result with the current operation.
Actual reproduced: server hardcodes id:999999 for every JSON response, but Start and ListTools both succeed. This silently attributes an unrelated result to the caller's requested tool/action.
Reproduction: lifecycle.go 'mismatched-id' case prints successful Start and ListTools despite every response ID differing from its request.
Acceptance: JSON HTTP tests for matching, mismatched, absent and null IDs; accept only the request-correlated result, preserving parity with SSE handling.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="93yp"></a>

### mux#93yp — Create session snapshot temporary files exclusively instead of following a predictable symlink (P2)

Source: session/file_store.go:53-57 uses target+'.tmp' and os.WriteFile, which follows an existing symlink; Rename then moves that symlink to the final snapshot filename. Lexical session-ID validation at lines 32-36 does not cover this path.
Trigger/precondition: an existing <session>.json.tmp symlink in the configured store directory points to a writable file outside it. This requires a seeded/compromised/shared store directory; it is not a remote path-traversal exploit from session ID alone.
Expected: Save creates its own regular temporary file inside the store and never follows an existing temp entry or overwrites files outside the root.
Actual reproduced: Save returns nil, overwrites the external victim file with session JSON, then leaves <session>.json as a symlink to that victim.
Reproduction: repro.go creates both store and victim under a disposable temp directory, seeds s.json.tmp -> victim, and prints 'error=<nil> victim_overwritten=true'.
Acceptance: regression uses a temp-directory victim and pre-existing temp symlink, verifies victim stays unchanged and final snapshot is a regular file. Use an exclusively created unique temp file (e.g. os.CreateTemp), ensure close/error cleanup, then atomic rename. Do not claim protection for arbitrary attacker mutation of the whole store; this fixes Save's own arbitrary-file-write behavior.

Audit baseline: 6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0 (2026-09-11). Reproduction sources and scope notes are preserved in docs/audits/2026-09-11/; scratch paths in the evidence refer to that audit run.

<a id="6ybc"></a>

### mux#6ybc — Capture and assert expected warnings and recovered panic logs in tests (P3)

Audit baseline: 6f7305e, 2026-09-11.

Locations: tool/tool_test.go TestHookErrorHandling; tool/executor_test.go TestExecutorAfterHookPanicDoesNotCrash; agent/agent_test.go TestAgentCleanupWhenParentDestroyed and TestAgent_SuspendAndResume; orchestrator hook/compaction tests; mcp/mcp_test.go TestClientStartAlreadyRunning.

The passing full race suite emits unasserted missing-InputSchema warnings, recovered-panic stack traces, and mcp: stdio read error: read |0: file already closed during normal shutdown. Evidence is present in go test -json output (/tmp/mux-audit-20260911/tests.jsonl). This contradicts the repository quality requirement to capture and assert expected errors and makes unexpected runtime errors hard to distinguish.

Fix: give fixtures schemas unless missing-schema behavior is under test; capture/assert intentional panic/error logs; suppress or correctly classify expected shutdown read errors at their source if confirmed benign. Do not globally discard stderr or hide unexpected errors.

Acceptance: the full race and integration-tagged runs produce no unasserted warning/error output while tests still verify the intended diagnostics.

<a id="3ctg"></a>

### mux#3ctg — Correct the README claim that mux implements an MCP server (P3)

Audit baseline: 6f7305e, 2026-09-11.

Location: README.md:14.

README advertises full Model Context Protocol server and client implementations. The mcp package implements clients/transports, tool adapters, and tool management; its ServerConfig configures a remote/subprocess server to connect to, and no MCP server implementation or serving API exists in this repository. This claim misleads consumers choosing mux to expose tools as an MCP server.

Verification: reviewed all production files under mcp/ and the exported APIs.

Fix: describe the supported MCP client transports and adapter functionality accurately and add a minimal usage link. Do not implement an unsolicited server solely to match the claim.

Acceptance: README feature claims map to existing exported APIs and runnable examples.
