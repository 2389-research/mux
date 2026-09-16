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
