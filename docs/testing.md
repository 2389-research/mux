# Flow coverage

Mux is an embeddable Go library, with interactive examples that require a live
LLM. It has no browser UI or standalone application API. The e2e 0.17.0 runner
uses a tools-only target and executes Go public-contract tests with the race
detector. The local suite needs no browser engine, model or provider credential.
The separate live suite requires an existing key and an explicit model.
This keeps native Go assertions as the source of truth.

Install with `npm ci --ignore-scripts` (Node >=24.8) and run `make test-e2e`. Keep `make test`
as the canonical full unit/integration suite; `make test-e2e` is a focused
addition. The runner disables telemetry and uses one worker, no retries, and
uncached Go tests. Results live in `.e2e/report.json`; raw Go JSON output lives
in `.e2e/logs/`. The runner checks that every named Go test actually passes and
that none skip. An empty `go test -run` selection cannot appear green.

## Verified local flows, 2026-10-04

| Public flow | Test or command | Observed result / scope |
| --- | --- | --- |
| Discover MCP tools; transmit Unicode, escapes, and a 128 KiB frame; terminal close/restart | `TestMCPProcessContract/round_trip` / e2e | Pass; real stdio child, byte-exact result and child log |
| Reject unknown tool, empty input, wrong input type; reuse connection afterward | `TestMCPProcessContract/error_recovery` / e2e | Pass; real child rejects calls, then echoes one nonce |
| Missing/denied approval, approved execution, no later approval leak | `TestMCPProcessContract/approval` / e2e | Pass; real executor/adapter, child log proves only approved call arrived |
| Independent MCP stdio interoperability: discover, create, read, list | `TestMCPFilesystemProcessContract` / e2e | Pass; official `@modelcontextprotocol/server-filesystem@2026.8.31`, actual owned temporary files |
| MCP HTTP discovery, tool error recovery, pre-canceled and in-flight canceled calls, request-associated SSE notifications and close | `TestMCPHTTPProcessContract` / e2e | Pass; official SDK `1.32.0` owns wire protocol/session/HTTP transport |
| Save/load/list/delete sessions | `TestFileStore_SaveLoadRoundTrip`, `TestFileStore_ListSorted`, `TestFileStore_DeleteIdempotent` / e2e | Pass; isolated real filesystem |
| Read skill files; reject missing directory, duplicate names, malformed frontmatter | `TestLoadDir*` / e2e | Pass; isolated real filesystem |
| Run agent tool turn and publish correlated history/events | `TestMCPAgentIntegration` / `make test` | Pass; real MCP child, scripted LLM; integration evidence only |
| Permission modes and hooks | `TestPermissionApprovalFlow`, `hooks` and `permission` suites / `make test` | Pass; native tests, scripted LLM where present |
| Coordinate agents, error propagation, cleanup | `TestMultiAgentCoordination`, `TestEndToEndErrorPropagation`, `TestResourceCleanup` / `make test` | Pass; scripted agents, no live provider proof |
| Suspend/resume, approve/deny pending calls, cancellation, checkpoint recovery | `orchestrator` session and tool-batch suites / `make test` | Pass; native integration with scripted LLM and temporary stores |
| Load a skill through an agent | `TestAgentLoadSkillRoundTrip` / `make test` | Pass; scripted LLM integration |
| Provider request mapping, stream parsing, cancellation, context compaction | `llm` and `orchestrator` suites / `make test` | Pass; local protocol fixtures/scripted LLM, not live provider compatibility |
| Recording serialization, validation, golden corpus | `recording` suite / `make test` | Pass; contract/unit evidence |
| Build library and examples | `make build`, `go vet -tags=integration ./...`, `make lint` | Pass; compile/static checks, not interactive usage |
| OpenAI live echo through orchestrator/MCP child | `TestLiveProviderEcho`, OpenAI `gpt-5.2` | Failed: Responses API returned 401 `not_authorized_invalid_project`; configured key belongs to an archived project |
| Anthropic live echo | `TestLiveProviderEcho`, `claude-sonnet-4-6` | Failed: 401 `authentication_error`, configured API key invalid |
| Gemini live echo | `TestLiveProviderEcho`, `gemini-2.5-flash` | Pass: 2 replies, 234 input / 40 output tokens; real tool result and child log |
| OpenRouter live echo | `TestLiveProviderEcho`, `openai/gpt-4o-mini` and `anthropic/claude-sonnet-4.6` | Both pass: 2 replies each; 219/29 and 1324/92 input/output tokens |
| Live tool-input structure and streaming terminal/block identity | `TestLiveProviderStreaming`, OpenRouter `openai/gpt-4o-mini` | Pass: actual two-turn stream, exact tool arguments, process log and result replay |
| Live reasoning and signed replay | `TestLiveProviderStreaming`, Gemini `gemini-2.5-flash` | Passed three full runs; one fresh E2E run failed missing finish reason. Intermittent stream failure remains unresolved; no retries or parser relaxation |
| Live truncation status, direct and stream | `TestLiveProviderTokenLimit`, OpenRouter `openai/gpt-4o-mini` | Pass: 4 output tokens per call; `max_tokens` successful response, not error |
| Live context compaction and marker retention | `TestLiveProviderCompaction`, OpenRouter `openai/gpt-4o-mini` | Pass on final named-public-marker contract: actual summary + answer, shorter history, hook and retained marker; each request capped at 512 tokens. Earlier bare-hex prompts exposed model refusals; no compaction data loss observed |
| JSON-schema structured output/refusal | Public `llm.Request` | No response-format/schema API exists; tool-input schema is covered separately. No fabricated structured-output test |
| Interactive minimal/simple/full examples | `examples/*` | Built; interactive conversation not exercised; selected OpenAI account rejects inference |

## Limits

The seven local e2e cases verify local library boundaries, not every exported method or
remote provider. Existing agent and provider tests contain explicit test doubles;
they remain useful integration/unit checks but do not count as mock-free E2E.
The local Go echo server is a real process but hand-writes protocol replies and
shares mux's protocol revision. Those three cases prove integration, not
independent MCP interoperability. The filesystem server is separately
maintained; the HTTP service registers a real echo callback with the official
SDK, which handles all protocol traffic. Temporary directories and owned
processes are removed at test exit. Run `npm ci --ignore-scripts` before `make test` because
integration now includes these reference servers; CI installs the pinned deps.

HTTP request-associated SSE and in-flight cancellation/recovery are covered.
The blocked handler logs receipt before cancellation; client cancellation
promises no rollback of server-side effects. A standalone GET notification stream,
resumption/event replay and authorization against a remote HTTP server are not
exercised; mux's HTTP client exposes no standalone GET/resumption API. Media
input and all provider/model cross-products remain outside this representative
run. OpenAI and Anthropic credentials still block their live capabilities.
Gemini reasoning has passing evidence and one unresolved intermittent stream
failure, so it cannot yet count as reliable compatibility.

## Default-model fixes and pending choice

Empty Anthropic and OpenRouter model selections now use Sonnet 4.6. The
[Anthropic retirement notice](https://platform.claude.com/docs/en/about-claude/model-deprecations)
names the replacement for retired Sonnet 4. OpenRouter's public model list no
longer advertises `anthropic/claude-3.5-sonnet`; its
[Sonnet 4.6 page](https://openrouter.ai/anthropic/claude-sonnet-4.6) confirms the
supported successor. Both stay at the Sonnet $3/$15 standard input/output rates
per million tokens. Constructor tests failed on all five fallback variants
before the fix and pass afterward. Existing explicit model selections remain
unchanged. Minimal/full examples now use the shared Anthropic default.

**Unresolved:** Gemini's `gemini-2.0-flash` default was
[shut down June 1](https://ai.google.dev/gemini-api/docs/changelog). A replacement
needs Doctor Biz's price/behavior choice: current 3.x models change thinking
controls, and the working 2.5 model restricts new-project access. This run
verified explicit 2.5 usage; it did not fix or verify the default Gemini flow.
