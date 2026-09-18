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

## A review finding needs a baseline column too (2026-09-16)

Two of the four findings from ac20's adversarial review measured something real and
attributed it to the wrong cause. Both survived because nobody measured the control.

A reviewer reported the recording codec's cycle guard as exponentially slow on shared
but non-cyclic input, with clean numbers: a diamond DAG (`{"a": prev, "b": prev}` per
level) took 2.6ms at depth 10, 1.2s at depth 20, 19.7s at depth 24. True, reproducible,
and not the guard's fault. JSON has no references, so serializing a shared-structure
value expands it — plain `json.Marshal` on the identical value is exponential too.
Measured side by side, `EncodePayload` costs a flat ~4x the marshal at every depth
(depth 20: 840ms vs 199ms, output 17.8 MB). Same complexity class, constant factor.
Memoizing the guard would drop 840ms to 200ms and still emit 17.8 MB.

The second: a validator that never checks `ToolResultPayload.EvidenceRef` against the
schema's `minLength: 1`. Real gap, but the field is `json:"evidence_ref,omitempty"`, so
our own encoder cannot emit the empty string that would violate it. The bug is narrow —
foreign raw JSON only — not the trust-chain hole it read as.

Both are the schema entry's one-column table in another costume. "Is it slow?" and "is
the constraint unenforced?" are single-column questions. Ask what the same operation
costs without the mechanism, and what the encoder can actually produce.

## A fixture that pins bytes and their hash is never a one-character fix (2026-09-16)

`record_full_roundtrip_utc_and_sha256.json` carried a 63-character `request_sha256` —
the empty-string SHA-256 with its final `5` dropped, inside both its `input` and `output`
strings. The fixture also has a top-level `sha256` that genuinely binds the canonical
output bytes (verified: it matched before the fix). Correcting the digest changes those
bytes, so that field moves too — `d8396f1a…` becomes `abfd5bb1…`. Hand-edit the digit
alone and the fixture contradicts itself.

It shipped because `golden_fixtures_test.go` compares encoded bytes to the pinned output
and never calls `ValidatePayload` or `ValidateRecord`. The package's own `hex64Pattern`
is correct and would have caught it. "Encodes deterministically", "is semantically
well-formed" and "conforms to the frozen schema" are three properties, and a byte-pin
composes only the first. A validator nobody calls cannot be told from one that always
returns nil.

Fixture corpora advertised as a cross-host contract earn the semantic check. Not every
fixture, though: synthetic scalars like `integer_one.json` are deliberately not any real
payload kind and fail such a check spuriously. Tag the realistic ones and skip the rest.

A truncated hash is a *prefix* of the correct one, so grepping for the bad value
finds the fixed value too. Checking whether the 63-char digest survived anywhere,
`grep -rn 'e3b0…b85'` lit up the already-corrected JSON fixtures and read exactly
like a failed fix. Match the class and measure the length instead:

    re.compile(r'[0-9a-f]{60,}')   # then report len(m) per match

That turned a scary ambiguous result into four precise hits, all in `codec_test.go`
— Go string literals holding a second, independent copy of the same golden data,
still passing because those tests also only compare bytes. One corpus got fixed
while its twin kept pinning the invalid value. When golden data lives in two
places, fixing one and not the other is worse than fixing neither: the package now
disagrees with itself and every test is green.

## "Same shape, so the same rule applies" is two claims (2026-09-16)

A review of mux#ac20 flagged that `ToolOutcomeUnknownPayload.EvidenceRef` had no
present-but-empty check while its twin on `ToolResultPayload`, seven lines above in the
same switch, had just gained one. Identical type, identical `omitempty`, identical JSON
name. The prescription was: extend the check.

The shape matched; the rule did not. `record.schema.json` constrains exactly three payload
kinds. Listing every per-kind block settles it in one command:

    grep -nE '"const"\s*:\s*"[a-z_]+\.[a-z_]+"' <schema>

Three hits per schema copy: `message.delta`, `tool.intent`, `tool.result`. `tool.result`'s
`evidence_ref` has `minLength: 1`; `tool.outcome_unknown` has no payload block at all. So
the sibling check could not be justified as "implementing the schema" — and `ValidateRecord`
takes a `Binding`, making it an ingestion gate where extra strictness rejects records a
conforming host may legitimately send.

The gap was still real, on other grounds: `Reason` was already required for that kind with
no schema backing, so the local-strictness decision had already been made, and an evidence
pointer that points nowhere is precisely what the durability contract refuses. Same fix,
different reason, and the reason belongs in the comment — the next reader needs to know
where the schema stops and mux's own rule starts.

A finding that cites a spec makes two claims: the gap exists, and the spec covers it.
Reviewers check the first one.

## A generic fallback error makes every specific check unfalsifiable (2026-09-16)

mux#1kr7 added 11 typed `StreamProtocolError` reasons and a 12-case table test for them.
Each case asserted `errors.As(event.Error, &protocolErr)` and nothing about
`protocolErr.Reason`.

The code also has a post-loop catch-all, `reasonStreamEndedEarly`, which fires whenever a
stream ends without a clean `message_stop` — true in most malformed fixtures. It satisfied
every assertion. Neutralizing the duplicate-start guard (`exists` -> `exists && false`) left
all 12 subtests passing, and `go test -race ./llm/ -count=1` passing too. The named check
could be deleted and no test would notice.

Map the error constants to their test references before reading a single test body:

    for r in $(grep -hoE 'reason[A-Za-z]+ +=' llm/*.go | sed 's/ *=//' | sort -u); do
      printf "%-30s prod=%-3s test=%s\n" "$r" \
        "$(cat llm/*.go | grep -c "\b$r\b")" "$(cat llm/*_test.go | grep -c "\b$r\b")"
    done

Eleven constants, every one `test=0`. The review that surfaced this reported "5 of 12
subtests are masked" — true, but the constant map showed the entire reason surface was
untested by name. Different size, different fix: not "patch five cases" but "the table needs
a `wantReason` column".

The irony is that a thorough fallback hides more. Code careful enough to never end a stream
silently is exactly the code where "did it error?" becomes a tautology. Assert which error,
not whether.

## A rejected worktree-safety command can hide inside a chain (2026-09-17)

Working kata 29jh, a mutation-testing revert used `cp <snapshot> llm/anthropic.go` chained
with an unrelated `git diff stash@{0}` in the same `&&`-joined Bash call. The harness's
worktree-isolation check rejected the whole call up front ("names git in a form too complex
to verify that it stays inside the worktree. Refusing to run it") because of the git
invocation — and the `cp` never ran, even though it did not touch git at all and sat first
in the chain. The tool call returned only the rejection text, no partial output, so nothing
signaled that the revert was silently skipped.

Re-running the intended test suite immediately after caught it: the mutated (broken)
comparison was still active, so the mutation test kept failing where it should have turned
green. Without that immediate re-check the file would have stayed mutated while every later
step assumed it was clean.

Treat a worktree-safety rejection as "none of this chain ran," not "the git part failed."
Split file edits and git commands into separate Bash calls in this harness, and after any
revert step — mutation testing especially — verify the file state directly rather than
trusting that a prior command in the same call succeeded.

## The worktree-safety check also blocks plain commands with no git in them (2026-09-17)

Re-verifying kata 29jh's gate, two commands that never mention git hit the same "too
complex to verify that it stays inside the worktree" refusal as the chained case above:
`go test -run 'Foo|Bar|Baz' ...` (a `|` inside a quoted `-run` regex) and
`GOLANGCI_LINT_CACHE="$SC/x" cmd ...` (a shell variable used to build a path). Neither
runs git, chains commands, or touches anything outside the worktree — the checker still
declines to prove that from the command text alone.

So the trigger is not "git present" or "commands chained"; it is shell metacharacters
(`|`, `$VAR`) anywhere in the command line, read as text before anything executes. Fix:
write the literal, fully-expanded value in place of the variable, and drop regex
alternation from `-run` (run the whole package and grep the saved output instead).
## `mise exec` commands fail when the same call also has a literal `$variable` (2026-09-17)

Building on the GOROOT-wrapper gotcha above: even the correct `unset GOROOT && mise exec --
<cmd>` form still gets refused with "too complex to verify... cannot be shown not to be
git" once the same Bash call also contains a `$`-expansion — `echo "exit: $status"`,
`${PIPESTATUS[0]}`, any of it. Six data points isolate it: two calls pairing `mise exec`
with `$status` or `${PIPESTATUS[0]}` were refused; five calls using the identical `unset
GOROOT && mise exec -- ...` prefix, redirecting with a plain `> file 2>&1` and reading the
file back afterward via `cat`/`grep`/`wc` with no `$` anywhere in the call, all ran —
including one that chained `cd`, the `mise exec` line, then `cat`, two literal `echo`s, and
`wc -l < file`. So neither multiple statements nor a trailing read-back is the trigger;
piping through `tee` failed too, but only ever in a call that also carried `${PIPESTATUS}`.

Keep every `$variable` — fish's `$status` included — out of any Bash call that also invokes
`mise exec`. The `mise exec` part is incidental: the entry above shows the
same refusal with no mise involved, so the rule is about the metacharacter, not the command. Redirect with `> file 2>&1` as its own statement and inspect the file with
plain, `$`-free commands.
## kata JSON: label shape differs by subcommand, and priority can be null (2026-09-17)

`kata ready --json` and `kata list --json` return `.labels` as an array of plain
strings, on each issue. `kata show <ref> --json` is different twice over: labels are
objects with a `.label` field, AND they sit at the **top level** of the response, not
under `.issue` — `.issue.labels` is absent, so a query for it returns empty and reads
exactly like "no labels" rather than like a wrong path.
A jq filter written against one shape dies against the other with
`Cannot index string with string ("label")`. The portable form handles both:

    [.labels[]? | if type=="object" then .label else . end] | join("|")

`priority` is also `null` on some issues, and jq's `sort_by(.priority)` sorts null
FIRST — so every unset-priority issue floats above real P1s and a "highest priority
first" query silently returns the opposite. Use `sort_by(.priority // 99)`. Priority 0
is the highest; null means unset, not urgent.

Both traps are invisible from the output: the query returns rows, just the wrong ones.

## A stopped workflow leaves its commits behind (2026-09-17)

Killing a workflow mid-run removes nothing. Its agent worktrees and their commits
survive, and the branches they pushed stay on `kata/<ref>-<slug>` names that the next
run will pick again. An agent restarted on the same kata then opens its worktree and
finds the fix already committed, with evidence files in the scratchpad timestamped
minutes before it started — indistinguishable from work someone else finished.

That happened on `29jh`, and two other katas in the same wave hit the older cousin of
it: a stale branch ~60 merges behind main whose diff would have deleted ~9000 lines of
shipped work, and a `needs-review` label left by a prior failed run. All three agents
verified independently instead of trusting the artifact, because the prompt told them
prior claims are leads and not truth. The rule held three for three; the cost was
verification time on every one.

Clean up after a killed run: `git worktree list` for `.claude/worktrees/wf_*` entries
(`git worktree remove --force`, the commits stay reachable from the named branches),
and delete the orphan `kata/` branches it left.
