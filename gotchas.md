## Quality audit (2026-09-11)

The audit of `6f7305e` filed 38 issues in Kata under `audit-2026-09-11`.
See `docs/audits/2026-09-11/README.md` for evidence and reproduction sources.
Kata owns current status; the report records the audited baseline, not fixes.

The root integration suite needs `-tags=integration`; ordinary `go test ./...`
does not run it. Its MCP-agent test always skips at the audited baseline.
Passing unit/race tests do not establish live provider compatibility.

Approved contracts permit only one writer per session ID and require separate
tool registries for agents with different skill catalogs. Keep those constraints
in view when testing persistence or agent isolation.

## Relevance audit (2026-09-11)

See `docs/audits/2026-09-11/relevance.md` and Kata label
`relevance-2026-09-11` for 11 additional issues. Recommendation: retain the
embeddable Go core; repair provider replay, controls and consumer integration
before expanding orchestration. This is an audit recommendation, not an
approved architecture change.

Local consumer pins differ and Elves uses a local replace. Source imports are
evidence of reuse, not live compatibility. Local Jeff uses mux-rs; the old
DEPENDENTS.md must not be treated as a current Go adoption inventory.

Protocol/API status is date-sensitive: this audit checked MCP 2026-07-28 and
Gemini Interactions GA documentation. Recheck primary sources before changing
support policy; GenerateContent remains supported in the reviewed docs.

## Evener comparison (2026-09-11)

`docs/audits/2026-09-11/evener.md` maps Evener 96973838a implementations to mux
issues. Prefer small adaptations of replay state, provider resolution, stream
settlement and persistence tests. Added Kata r6tc (recoverable bounded tool
output) and d1dj (invariant corpus checks); architecture remains a proposal.

Evener is a reference, not a correctness oracle: it silently changes some
request controls/schema guarantees, its temporary artifact handles expire,
and its Go 1.27 agent module imports app code despite older boundary docs.
Check current code and preserve mux's explicit contracts when porting.

## SIFT audit pass 2 (2026-09-12)

`docs/audits/2026-09-11/sift-pass2.md` is the second-pass audit of `9a94996`:
79 Kata issues under `audit-2026-09-11-pass2` (P1: a2j0, nrhn, 5ez6, yhen)
and 19 comments on first-pass issues. Read-only: nothing was run or fixed.
Four breaking changes (865b, j6kd, 2zdv, conditionally aag7) belong in one
release with one CHANGELOG entry.

Run `kata` from the repo root. `.kata.toml` binds the project; from another
directory `kata show` finds nothing and looks like lost data. `kata show
<ref> --json` carries a `comments` array.

DEPENDENTS.md is stale. The corrected local-consumer inventory is appendix
B.2 of the pass-2 report: eight unlisted Go consumers, and
`agent-class/agents/mux` is a copy of mux, not a consumer.

## Subagent worktrees refuse the GOROOT wrapper (2026-09-16)

The Go toolchain here needs `env -u GOROOT mise exec -- <cmd>`: the inherited
`GOROOT` points at a Go 1.26.5 install that no longer exists, so both bare `go`
and `mise exec -- go` fail. That prefix works in the main checkout.

Inside a harness agent worktree it does not. The sandbox rejects `env -u ... --`
as an unverifiable wrapper around worktree-isolated git operations. Four
subagents hit this independently. Use `unset GOROOT && mise exec -- <cmd>`, or
bare `go` after confirming `go version` reports go1.26.6. Hand the substitute to
subagents in their prompt; otherwise each one rediscovers it and improvises.

## Merging several branches that all edit CHANGELOG.md (2026-09-16)

When a wave of branches each append a bullet to `## [Unreleased]`, expect every
pair to conflict on CHANGELOG.md and do not read that as trouble. The conflicts
have an empty base — both sides add where BASE has nothing — so a sequential
merge resolves them by keeping both. `git merge-tree --write-tree --name-only
<a> <b>` measures this in seconds; predicting it from file names gets it wrong.

**Merge the branch that adds a whole new section LAST.** A branch adding, say,
`### Security` sits at the end of `## [Unreleased]`. Merged early, it swallows
every later addition that anchors at end-of-section: those bullets land under
the new heading instead of their own, and nothing complains, because the merge
is genuinely additive at the text level and wrong only in meaning. This bit us
once — a `Fixed` entry landed under `Security`. After any additive resolution,
check each bullet sits under the heading its own branch filed it under.

## Never auto-resolve a code conflict by keeping both sides (2026-09-16)

Git's diff3 output puts text the two sides share *after* the conflict, as
context. For a Go test file that shared text is the trailing `\t}\n}` — the
closing braces. Stacking ours-then-theirs leaves one copy of those braces to
close two function bodies, so the first one never closes:

    agent/transcript_test.go:422:6: expected '(', found TestTranscript...
    agent/transcript_test.go:445:3: expected '}', found 'EOF'

Both sides were pure additions and the conflict was additive by every textual
test. It still produced a file that does not compile. An automated "keep both"
pass is safe only for prose whose units are whole lines; code conflicts get
resolved by hand, or by rebasing one branch onto the other so there is no
conflict left to resolve.

## Go toolchain pinning and what govulncheck can see (2026-09-16)

`GOTOOLCHAIN=auto` (the default) silently upgrades any `go` invocation to the
`toolchain` line in go.mod, whatever is installed. Measured: against a go.mod
declaring `go 1.25.0` + `toolchain go1.26.6`, a 1.25.0 binary reports
`go1.26.6`. So pinning a CI job by Go version alone does NOT test that version —
it needs `GOTOOLCHAIN=local`, which honours the base binary and ignores a higher
`toolchain` directive as long as the `go` line is satisfied.

The `toolchain` directive applies only to the main module. Nothing that requires
mux as a dependency inherits it.

govulncheck v1.8.0 itself requires Go >= 1.26 to run, so `make vulncheck` always
executes under the pinned toolchain and scans that standard library — never the
declared floor's. A floor below a stdlib advisory's fix threshold is therefore
invisible to every gate in this repo. Say so in writing rather than implying CI
covers it.

## Proving a new test reproduces the bug, when it needs new names (2026-09-16)

The standard check is to revert the production fix and watch the new test fail.
That only works if the test compiles without the fix. When the test references
names the fix introduced — new exported types, constants, methods — reverting
gives a compile error, which says nothing about whether the assertion is any
good. Both look red in the terminal.

Mutate the fix instead. Keep every name; break one behaviour at a time; confirm
the test that should catch it is the one that fails, by its exact message. On
kata yhen three mutations each landed on the right test: restoring the discarded
cancel path failed both cancellation tests, forcing the already-ran check to
false failed with `audit ran 2 times across both Resumes, want exactly 1`, and
dropping the call list from `CheckpointError` failed with
`CheckpointError.Calls = [], want [call-1]`.

If a mutation reports `ok`, check the mutation actually applied before believing
it. A scripted edit whose pattern silently missed reports a clean run of
unmutated code, which reads exactly like a passing mutation test.

## A whole wave making the same mistake is a prompt defect (2026-09-16)

All six wave-1 agents filed their CHANGELOG entry the wrong way, and each was
corrected at review — six times, as six findings. It was one defect: the
dispatch prompt never stated the standard. Wave 2 stated it up front and both
units got it right on the first try.

Agents dispatched with clean contexts cannot infer a house convention that is
not written where they will read it. When a review finding repeats across a
wave, stop fixing instances and amend the dispatch template, then check on the
next wave that it took. The GOROOT substitution above went the same way: four
agents rediscovered it one at a time before anyone put it in the prompt.

## Amending a schema you extracted from somewhere else (2026-09-16)

Two checks, both cheap, both caught something real on kata 1fqg.

**Reproduce the source's declared hash before you touch the copy.** The frozen durability
schemas live inside a kata comment on mux#x3hz, each with a `SHA-256:` line. Extracting
them with `sed -n` and re-hashing matched both declared values exactly, which is what made
it safe to amend them — without that step you can amend a copy that lost a trailing
newline or a fence line and never know. If the hashes disagree, fix the extraction; do not
start editing.

**Run every test case against the old schema and the new one, side by side.** A one-column
"does it validate now" table cannot tell a working amendment from a broken fixture. Four
cases failed on the first run here and the amendment was fine — the fixture was missing
`tool_call_id` and `operation_id`, which `record.schema.json` requires on every `tool.*`
record through an `allOf` keyed on the `^tool\.` *pattern* rather than an exact `kind`, so
it is easy to miss when reading the per-kind blocks. The tell is a baseline row: a case
that should be valid under the *frozen* schema and is not means the fixture is wrong.
Include those rows even though they assert nothing about your change.

The useful shape is `frozen -> amended` per row. `REJECT -> valid` is the defect you are
fixing, measured rather than argued; `valid -> REJECT` is a new restriction biting;
`valid -> valid` on the source's own examples is your regression check.

## Gemini's range-over-func stream needs no explicit Close (2026-09-16)

Fixing mgpj (cancellable provider stream sends) touched five providers. Four
build an explicit `stream := client....NewStreaming(...)` handle and need
`defer stream.Close()`. Gemini does not: `for resp, err := range
g.client.Models.GenerateContentStream(...)` is a range-over-func iterator,
and the SDK's own iterator body runs `defer rs.rc.Close()` before it returns
control on any exit, early return included. There is no handle to close.
Confirmed by reading the genai SDK source, not assumed.

The kata carried two prior "Handoff" comments claiming this fix already
shipped, plus a stale sibling branch with a plausible-looking implementation.
Both were wrong: the stale branch's OpenAI SSE test fixture omitted the
`event:` line real OpenAI streaming sends (caught by checking this repo's own
passing openai_test.go), and neither prior attempt left behind a red test
that reproduced the leak before claiming done. Treat "someone already did
this" claims and reference branches as leads, never as source of truth —
reread the actual current code and actual currently-passing tests before
trusting either.

## "I searched exhaustively and it isn't there" is a claim, not a result (2026-09-16)

An agent implementing kata ac20 reported that three types named in its own plan —
`RestoreInput`, `RestoredState`, `CommittedCheckpoint` — had no field-level specification
anywhere in the tracker, and declined to invent them. Declining was right; inventing a
"reviewed contract" type is exactly what the review gate exists to stop. The search was
wrong. It swept ac20, 1rky, e91h and x3hz, because x3hz names 1rky as the owner of these
definitions. The complete Go structs were in `9cg6`, under "## Exact additional types".

Ownership metadata told the agent where to look and was misleading. The spec lived with the
consumer that needed the types, not with the issue nominated as their owner.

Testing the negative claim cost one command — count keyword hits per issue rather than
re-reading the issues already read:

    for id in 1rky e91h x3hz ac20 9cg6 twht; do
      printf "%-6s " "$id"
      kata show $id --json | jq -r '[.issue.body] + [.comments[]?.body] | join("\n")' \
        | grep -cE 'RestoreInput|RestoredState|CommittedCheckpoint'
    done

Five issues returned 0 or 1; `9cg6` returned 18. `kata list --json | jq -r '.issues[]?.short_id'`
enumerates all of them when the suspect set isn't obvious. A subagent that reports something
absent has usually searched a sensible-looking subset — verify the subset, not the conclusion.

## Auditing a mechanical conversion: over-flag on purpose (2026-09-16)

mux#mgpj routed all 44 provider channel sends through one cancellable helper. The count
matching is not the property that matters; what matters is whether any caller ignores the
`false` return and keeps working as if the send landed. A grep gives the count, not that.

A paren-balancing parse over the five provider files split the sites 22 checked
(`if !sendStreamEvent(...) { return }`) and 22 discarding the return, then flagged every
discard whose next statement was not `return`. Ten flagged. All ten turned out fine — they
sit inside the `recover()` defer just before `close(eventChan)`, or are the last statement
before `}()`, so the goroutine unwinds regardless. The rule was too narrow: the real
property is "the goroutine ends next", not "the next token is `return`".

The script still did its job. It reduced 44 sites to the 10 worth reading by hand, and
reading them settled the question in one pass. Write the crude rule, accept the false
positives, and read what it hands you — a checker that under-flags tells you nothing, and
one that over-flags costs a few minutes.
