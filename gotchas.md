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
