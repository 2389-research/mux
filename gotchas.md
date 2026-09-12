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
