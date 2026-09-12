# Mux relevance audit — 2026-09-11

Mux still merits investment as an embeddable Go library for provider access,
tool execution and an agent turn loop. Its main weakness is the information and
control lost at provider boundaries. Adding more orchestration features before
repairing those boundaries would make the library larger without addressing
what its callers currently need.

This is a recommendation, not an approved architecture change. The review filed
**11 new Kata issues: 1 P1, 9 P2 and 1 P3**, labeled `relevance-2026-09-11`.
They supplement the [38 correctness findings](README.md); none of those defects
has been fixed by this audit. Kata owns current issue status.

The production baseline is `6f7305ecbcb2ab6d4ebe7ea87ba39ab019dbd5c0`.
The project began in December 2025 but gained streaming, skills and durable
sessions through June/July 2026. Comparing everything with its first release
would overstate how far behind it is.

## What has changed around mux

| Change verified in current primary sources | Implication for mux |
| --- | --- |
| Gemini Interactions became GA in June 2026. Google directs new models/features there; GenerateContent remains supported but legacy. | Decide how to follow that API without forcing hosted conversation state. [Google documentation](https://ai.google.dev/gemini-api/docs/interactions-overview) |
| Published MCP revision `2026-07-28` changes discovery, negotiation and the session model. | Choose a supported protocol deliberately. Updating a version constant cannot implement this revision. [MCP changes](https://modelcontextprotocol.io/specification/2026-07-28/changelog) |
| Official MCP Go SDK v1.7.0 is available and requires Go 1.25. | Compare an internal SDK adapter against maintaining custom transport code; mux currently declares Go 1.24. [Release](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0), [go.mod](https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/v1.7.0/go.mod) |
| ADK Go 2.0, announced June 30, provides graph workflows, durable human input and node-level execution controls. | A general-purpose agent framework is a much more crowded target. Prefer a small composable library unless consumers need a broader runtime. [Google announcement](https://developers.googleblog.com/announcing-adk-go-20/) |
| Current OpenAI APIs support persisted reasoning, assistant phase, async tools and mid-turn steering. | Preserve replay state now; live steering needs a separate lifecycle contract and demonstrated demand. [Reasoning](https://developers.openai.com/api/docs/guides/reasoning), [async tools](https://developers.openai.com/api/docs/guides/async-tool-calling), [steering](https://developers.openai.com/api/docs/guides/steering) |
| Claude supports adaptive thinking and effort; Gemini recommends thinking levels for Gemini 3. | One universal token budget is no longer a sufficient control surface. [Claude](https://platform.claude.com/docs/en/build-with-claude/thinking), [Gemini](https://ai.google.dev/gemini-api/docs/generate-content/thinking) |

These sources establish current behavior, not the launch date of every feature.
Structured outputs, prompt caching, rich MCP results and basic reasoning replay
are older omissions. Their importance has grown; calling all of them new would
be inaccurate. Claude structured outputs now uses its normal API contract,
while its compaction and MCP connector remain beta in the reviewed docs.
[Structured outputs](https://platform.claude.com/docs/en/build-with-claude/structured-outputs),
[compaction](https://platform.claude.com/docs/en/build-with-claude/compaction),
[MCP connector](https://platform.claude.com/docs/en/agents-and-tools/mcp-connector).

## Evidence that mux still has a job

Local sibling repositories contain dependencies and reachable execution paths,
including changes through August 2026. This establishes source reuse, not
production traffic or successful compatibility with today's services.

| Consumer | Declared mux version | Inspected execution path |
| --- | --- | --- |
| Hex | v0.7.0 | `hex/cmd/hex/mux_runner.go`; provider creation and orchestration |
| Mush (`mux-mush`) | v0.9.0 | `internal/agentloop/agentloop.go:268`; agent construction and continuation |
| Hawk | v0.8.1 | `internal/muxrt/runtime.go:739`; orchestrator with controlled executor |
| Glassspider | v0.9.0 | `internal/stages/common.go:147`; agents used by multiple stages |
| Elves | v0.6.0, replaced by `../mux` | `internal/village/village.go:298,421`; worker and manager agents |
| Vertex bridge | v0.6.2 | `vertex/vertex-bridge/internal/agent/agent.go:23`; per-turn agent |

Paths are relative to each named sibling checkout unless the project prefix is
shown. `gateway/scenario/agents/mux-go` and `mux-evals/runners/go` add scenario
runner dependencies. They are assets to update, not proof of current model evals.
`DEPENDENTS.md` is stale: local Jeff uses mux-rs, and the documented Mouse/Sysop
sibling paths are absent here. That does not prove those projects do not exist
elsewhere. Inventory correction is included in `mux#zee0`.

The strongest demand is concrete duplication:

- Hex `mux_runner.go:301`, Hawk `runtime.go:667` and Elves `village.go:127`
  repeat provider factories. Glassspider `internal/config/provider.go:30,89`
  builds a provider catalog and mirrors mux's reasoning thresholds.
- Hawk `runtime.go:811` wraps streaming as a synchronous call, reconstructs a
  final response and suppresses duplicate text. Glassspider
  `internal/runner/runner.go:56` independently handles output/events/usage.

The paused provider-support notes dated June 26 already identify a useful
boundary: shared provider IDs, configuration and construction in mux; setup UI,
credential persistence and application policy in callers. That direction
deserves a small implementation proven in two consumers.

Durability has a different demand signal. A search of these six consumers found
no use of `SessionStore`, `ApprovalSuspend`, `session.NewFileStore` or
`orchestrator.Suspended` in non-test Go code. Mush saves/restores transcripts;
Hawk owns session queues and cancellation. Integrate the existing session API
with one real consumer before building a scheduler around it. A snapshot does
not make tool side effects exactly-once; mux's one-writer session contract still
matters.

## New issues, ordered by practical value

Priority is implementation urgency, not a claim that every item blocks the
current release. P1 denotes replay correctness. Most P2 entries are enhancements
or decisions; they should follow the earlier approval, persistence and transport
repairs. Each Kata body contains evidence, scope and testable acceptance criteria.

| Kata | Priority | Gap and smallest useful outcome |
| --- | --- | --- |
| `mux#e451` | P1 | Preserve OpenAI reasoning items and assistant phase across conversion, tool turns, persistence and replay. |
| `mux#1g9g` | P2 | Expose provider-supported reasoning mode/effort, with visible validation of unsupported settings. |
| `mux#rjqv` | P2 | Add optional response schemas and strict function schemas, including refusal/truncation outcomes. |
| `mux#zee0` | P2 | Consolidate provider factories and behavior profiles; demonstrate removal of duplication in two consumers. |
| `mux#38f5` | P2 | Expose incremental agent progress with canonical final response, ordering, correlation and cancellation. |
| `mux#kjsz` | P2 | Populate cache-read/write usage from provider responses before tuning caching policies. |
| `mux#v43c` | P2 | Define a complete request budget that accounts for unknown media costs, tool/system input and output reserve. |
| `mux#kzkx` | P2 | Decide the MCP protocol/support policy and compare the official SDK against custom transports. |
| `mux#jstq` | P2 | Preserve structured and multimodal MCP tool results through the tool adapter and model history. |
| `mux#eq0g` | P2 | Plan Gemini Interactions coverage with explicit state, storage and lifecycle ownership. |
| `mux#qp4d` | P3 | Retain skill source locations so host file tools can resolve bundled relative references. |

**Provider state is the first constraint.** `llm/types.go:93-146` retains visible
content but lacks a representation for opaque replay state or assistant phase.
`llm/openai.go:604-630` keeps text and function calls while dropping other output
items. The OpenAI contract recommends preserving original phase and reasoning
items across calls. Test the whole response-to-persistence-to-request path;
adding a field to an adapter alone will not preserve it.
[OpenAI reasoning guidance](https://developers.openai.com/api/docs/guides/reasoning).
Existing Anthropic/Gemini signature tickets `mux#w9xj` and `mux#62ba` cover those
providers; the new OpenAI finding should inform a shared design without replacing
their regression cases. OpenAI already uses Responses, so another endpoint
migration is unnecessary.

**Provider controls and output guarantees are too narrow.**
`ThinkingConfig` is only Enabled/Budget. OpenAI reduces budgets to three effort
tiers; Claude uses manual thinking; Gemini receives an integer budget.
Mux's `ThinkingAdaptive` is a loop heuristic, not Claude adaptive thinking.
`Request` also lacks a response schema and OpenAI explicitly sets tool strictness
false. Introduce optional controls with honest capability checks, not silent
fallbacks. Strict schemas have provider-specific constraints and should not
be forced onto all existing tools.
[OpenAI structured outputs](https://developers.openai.com/api/docs/guides/structured-outputs).

**Context and cost need evidence.** `llm.Usage` cannot carry cache counters even
though `orchestrator/usage.go` already defines them. OpenAI can cache prompts
automatically today; mux currently loses the accounting, rather than preventing
all caching. `orchestrator/tokens.go` counts media/thinking blocks as zero and
`compact.go` budgets only message history. Keep a documented fallback, but do
not represent unknown input cost as free. Native provider compaction can be a
later optional strategy after replay is lossless and task-retention evals exist.
[OpenAI caching](https://developers.openai.com/api/docs/guides/prompt-caching),
[Claude caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching),
[OpenAI compaction](https://developers.openai.com/api/docs/guides/compaction).

**MCP needs a support decision and a lossless result path.**
Stdio advertises `2024-11-05`; HTTP pins `2025-06-18`. The newer protocol has
substantial lifecycle differences. Fixes for supported older revisions remain
valid while that support contract remains in force. Separately,
`mcp/adapter.go:68` turns image/resource results into labels and ignores other
content; `structuredContent` and output schemas are absent from the types.
Preserve useful data through `tool.Result` and the LLM request, not merely at JSON
decoding. Current structured content can be any JSON value.
[MCP tools](https://modelcontextprotocol.io/specification/2026-07-28/server/tools).
Hawk's sandbox-backed MCP implementation is a useful test of an injectable
transport boundary; its sandbox itself belongs in Hawk.

**Skills need a narrow scope extension.** `skill.LoadDir` discards the source
path and the loading tool returns only the body. The original body-only design
was deliberate. Retaining location would let existing host file tools resolve
relative references as described by the format. It need not introduce script
execution, new permissions or a marketplace.
[Agent Skills specification](https://agentskills.io/specification),
[client guidance](https://agentskills.io/client-implementation/adding-skills-support).

## Innovations to defer until a caller needs them

| Area | Decision and trigger |
| --- | --- |
| Graph workflows, distributed scheduling, cross-session memory | Keep these in applications or a dedicated runtime. Revisit after a consumer demonstrates a workflow the present loop/session boundary cannot express. |
| Native hosted search, deferred tool discovery, code execution | Support one requested provider feature with typed results/citations and clear execution ownership. Avoid a universal union of every hosted tool. [OpenAI tools](https://developers.openai.com/api/docs/guides/tools) |
| Provider-native compaction | First repair replay and measure constraint retention against portable summaries. Claude's offering is beta in the reviewed docs. |
| Async tool calls and live steering | First identify an interactive workload needing work to continue while tools run. Current mux assumes one terminal result followed by local tool execution. |
| MCP OAuth, elicitation and Tasks | Add only for named integrations. Host-owned auth hooks are preferable to an auth UI inside the loop. Tasks is a separately versioned official extension; it is not required core behavior. [Authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization), [Tasks extension](https://tasks.extensions.modelcontextprotocol.io/specification/2026-07-28/tasks) |
| A2A and ACP | A2A 1.0 targets independent agents; ACP targets editor-to-coding-agent integration. Neither is automatically useful for internal child-agent calls. Revisit for external delegation or a concrete editor integration. [A2A release](https://a2a-protocol.org/dev/blog/2026/03/12/a2a-protocol-ships-v10-production-ready-standard-for-agent-to-agent-communication/), [ACP scope](https://agentclientprotocol.com/get-started/introduction) |
| Realtime voice/video and broad CLI-provider parity | Require a product workload and honest capability reporting. A CLI producing text is not evidence of structured tool-call or resumable-session parity. |
| Hosted tracing/eval dashboards | Expose useful correlation and outcomes; let callers choose export/storage. GenAI conventions are evolving, so pin the conventions used by any future adapter. [OpenTelemetry GenAI conventions](https://github.com/open-telemetry/semantic-conventions-genai) |

## Recommended sequence and release evidence

1. Repair earlier approval, persistence, replay and streaming defects. Add the
   OpenAI replay case. Keep permission decisions bound to exact tool calls and
   prove persisted sessions preserve complete provider history.
2. Deliver reasoning controls, schema output and provider construction as small
   slices. Update two consumers and remove their duplicate adaptation code.
3. Stabilize incremental outcomes and usage/context accounting. Demonstrate
   cancellation and partial-failure behavior in Hawk or Glassspider.
4. Decide MCP protocol/SDK and Gemini API support with compatibility evidence.
   Test an actual target MCP server and a real provider tool turn before changing
   advertised support. Preserve or change the Go floor through an explicit
   project decision.

The existing `mux#sask` integration ticket receives the relevance-validation
follow-up rather than a duplicate test-framework issue. Provider conformance
should cover stream/nonstream parity, reasoning replay, schema failures and
near-limit requests. Consumer tests must record exact released versions versus
local replacements. Task evals should measure successful tool completion,
retention after compaction, cancellation, usage and output validity, with explicit
credential gating. Passing tests with fake provider replies cannot establish
live API compatibility.

## Method and limits

Three independent reviews covered provider evolution, MCP/skills, and actual
consumer source. The synthesis checked key code paths and current primary
documentation, compared plans with implementation, searched Kata for overlap,
and retrieved the filed records. No provider calls, consumer deployment checks,
or performance/cost benchmarks ran for this relevance review. Tests and race
checks from the preceding quality audit remain baseline evidence only.

This review changes documentation and issue tracking. It does not certify the
library as current or release-ready, approve architectural changes, or claim
that every innovation in the agent ecosystem has been surveyed.
