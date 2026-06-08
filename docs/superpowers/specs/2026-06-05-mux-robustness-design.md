# mux Robustness & Polish Pass — Design

- **Date:** 2026-06-05
- **Branch:** `refactor/robustness`
- **Status:** Awaiting review
- **Module:** `github.com/2389-research/mux` (Go 1.24)

## 1. Prime Directive (non-negotiable)

**Do not change intended behavior or the exported API.** "Better is great" — robustness
improvements that fix *broken* behavior (crashes, races, leaks) are welcome. Changing
*intended* behavior, or breaking the public surface that `hex`, `jeff`, `mouse`, and `sysop`
depend on, is not.

The constraint is enforced mechanically, not by assertion:

1. **`go test -race ./...` stays green at every phase boundary.** Baseline captured
   2026-06-05: all 8 library packages pass under `-race` (`agent`, `llm`, `mcp`,
   `orchestrator`, `coordinator`, `permission`, `tool`, `hooks`). The `llm` suite exercises
   real provider APIs (~26s), so the net has teeth.
2. **`go vet ./...` and `golangci-lint run` stay clean.**
3. **Exported API is frozen.** Verified by diffing `go doc` output for every package
   before and after — the exported surface must be byte-identical (doc-comment text aside).
4. **Every Tier-2 bug-fix is TDD.** A test that *reproduces the bug* (fails first, ideally
   under `-race`) lands before the fix. No fix merges without a failing test proving the bug
   was real.

### Verification notes

- Integration tests live in `integration_test.go` behind `//go:build integration`; run them
  with `go test -tags=integration ./...`. They are excluded from the default suite by design.
- Repo is reachable via two firmlinked paths (`/Users/harper/Public/src/2389/mux` and
  `/Users/harper/workspace/2389/mux`) — same inode, same git toplevel. One source of truth.
- Pre-commit (`go fmt`, `go vet`, `go test -race -short`, `go mod tidy`, `golangci-lint`)
  must pass. **Never `--no-verify`.**

## 2. Context

The code is already healthy: it builds, vets, is `gofmt`-clean, and trips only **one**
`golangci-lint` finding. So "Rob Pike level" here is not reformatting — it is three things:
**kill the cruft, document the public surface, and close latent concurrency/error bugs.**

Audit performed 2026-06-05 (two read-only Explore passes + manual verification of the
HIGH-severity findings). Findings below are grouped into three tiers by risk.

## 3. Scope — three tiers, all in scope

### Tier 0 — Hygiene & docs (zero production-logic change)

- **De-cargo-cult lint/CI config.** `.golangci.yml` and `.pre-commit-config.yaml` were
  copied from sibling repos: they reference `cmd/jeff/wizard.go`, `internal/providers/gmail/`,
  `internal/providers/oauth/`, `internal/tools/weather_tool.go`, `generateMonthCalendar`,
  and the pre-commit header literally reads *"Pre-commit hooks for Clem."* None of these
  paths exist in `mux`. Strip the dead exclusions; keep only what applies here.
- **Remove tracked junk (propose, don't silently nuke — these are not mine):** `.emnv`
  (0 bytes, typo'd `.env`), `scenarios.jsonl` (21 KB orphaned scratch data, no Go references).
  **Keep `architecture.dot`** — it is the source for the gitignored `architecture.png/svg`.
- **Declutter the gitignored-but-present working-dir files:** `output.txt` (588 KB),
  `coverage.out`, `architecture.png/svg`, `code-review.md`, `missing-tests.md`. Leave the
  untracked `posts/` alone.
- **Package docs.** All 8 packages lack a `// Package x ...` comment — the `// ABOUTME:`
  lines are invisible to `go doc`/pkg.go.dev. Add one canonical package comment per package.
- **godoc the exported surface.** Undocumented exported const blocks: `agent.RunStatus*`,
  `orchestrator.State*`, `orchestrator.Event*`, `llm.Event*`, `llm.StopReason*`, `llm.Role*`,
  `llm.ContentType*`, `permission.Mode*`, `hooks.Event*`, plus stragglers
  (`DefaultMaxIterations`, etc.). Doc-only; never reshape.
- **Fix the one real lint hit:** `examples/full/main.go:178` gosec G122 (filepath.Walk
  TOCTOU) — scope the read or annotate.

### Tier 1 — Safe internal refactors (behavior-preserving, gated by `-race`)

- **Delete dead code:** `orchestrator/usage.go` hand-rolls `sprintf`/`itoa`/`replaceFirst`/
  `indexOf` to avoid importing `fmt` — which the package already imports. Replace with
  `fmt.Sprintf`. Remove `mcp/sse.go:parseSSEEvents` (never called) and `mcp` `ErrTransportClosed`
  (never referenced) — but confirm zero references first (incl. the 4 dependents).
- **MCP sentinel errors:** `mcp/stdio.go` and `mcp/http.go` use bare
  `fmt.Errorf("client closed")` / `"client already running")`; the package already has
  `ErrSessionExpired`/`ErrNotConnected`. Add `ErrClientClosed`/`ErrAlreadyRunning` for
  consistency. (Internal — does not alter the exported error *values* relied upon, but
  confirm no dependent matches these strings.)
- **Idiom fixes:** `agent/async.go:133` `err == context.Canceled` → `errors.Is`;
  `coordinator/coordinator.go:36` `"context cancelled"` → `"context canceled"` (match stdlib);
  `mcp/stdio.go:191` diagnostic `fmt.Printf` → `fmt.Fprintf(os.Stderr, …)` (stdout corrupts
  the JSON-RPC framing — this one straddles Tier 1/2); rename `httpClient.http` field (shadows
  the `net/http` import).
- **Internal file splits (same package, no API change):** `llm/openai.go` (524 LOC →
  `openai.go` client + `openai_convert.go` conversions); `orchestrator/orchestrator.go`
  (522 LOC → carve out `orchestrator_thinking.go` / `orchestrator_tools.go`); extract the
  7 structurally-identical `hooks.Fire*` methods (~130 LOC) behind a shared dispatch helper.

### Tier 2 — Robustness bug-fixes (TDD: reproduce → fix → `-race`)

All verified or audit-identified. Each gets a failing test first.

- **MCP `ToolManager` data race** (`mcp/adapter.go:84-127`) — `tools` map has **no mutex**;
  `Refresh()` writes while `Tools()/Get()/RegisterAll()` read. Add an unexported `sync.RWMutex`.
  *(Verified.)*
- **MCP `httpClient` send-on-closed-channel panic** (`mcp/http.go:147-150` vs `269-277`) —
  `post()` sends to `notifications` while `Close()` closes it; `closeOnce` only guards
  double-close. Guard the send/close with the existing `mu` + a `closed` flag (or drop on a
  `done` channel). *(Verified.)*
- **MCP `stdio` goroutine leak on init failure** (`mcp/stdio.go:~84`) — `go readResponses()`
  starts before `initialize()`; an init error leaks the goroutine. Start the reader only after
  a successful handshake, or ensure cleanup. *(Audit-identified; confirm via test.)*
- **MCP `stdio` 64 KB scanner truncation** — default `bufio.Scanner` buffer silently rejects
  large MCP results, causing silent hangs. Raise the buffer / switch to a sized reader.
- **MCP `stdio` zombie processes & pipe leaks** — `Process.Kill()` without `Wait()`; pipes
  leaked on `Start()` error paths. Reap and clean up.
- **MCP HTTP no timeout** (`mcp/http.go:38`) — zero-value `&http.Client{}`. Add a sane default
  timeout (context still honored; this catches `context.Background()` callers).
- **`tool` afterHooks panic crashes the loop** (`tool/executor.go:127-130`) — `beforeHooks`
  are `recover()`-wrapped, `afterHooks` are not. Wrap symmetrically.
- **`agent.transcript` swallows `Close()` errors** (`transcript.go:~211-238`) — `defer
  f.Close()` on write paths discards flush errors → silent data loss. Capture via named return.
- **`llm/openai` drops `json.Marshal` error** (`openai.go:186,294`) — `argsJSON, _ := …`
  sends `""` args on failure. Surface the error.
- **`orchestrator` events silently dropped** (`events.go:119-133`) — non-blocking `Publish`
  drops on a full buffer with no signal. Add a drop counter / documented contract (no API
  change; observability only).

### Tier 2 (isolated, high-risk) — Orchestrator lock rework

- **Re-entrant deadlock** (`orchestrator/orchestrator.go:181-209`) — `Run()`/`Continue()` hold
  `o.mu` across the *entire* loop, including every hook fire and `executor.Execute`. A hook or
  tool that calls back into `Messages()`/`SetMessages()`/`ClearMessages()` (all take `o.mu`)
  deadlocks. *(Verified.)*
- **Approach:** separate the two jobs the lock currently conflates — (a) serializing
  `Run`/`Continue` against concurrent entry, and (b) protecting `o.messages`. Introduce a
  lightweight "running" guard (e.g. `atomic`/`TryLock` semantics returning a clear error on
  concurrent entry, matching the documented "not safe for concurrent Run" contract) and use
  `o.mu` *only* around `messages` access. Net effect: re-entrant reads from hooks/tools no
  longer deadlock, and the single-caller behavior is preserved.
- **Guardrails:** its own phase, its own PR-sized review. Tests: (1) reproduce the re-entrant
  deadlock (hook calls `Messages()`), (2) confirm concurrent `Run` still errors/serializes as
  documented, (3) full `-race` + integration suite. If the safe shape can't be proven, **stop
  and escalate** rather than ship a risky lock change.

## 4. Phasing

Max ~5 logic files per phase. Each phase ends with: green `go test -race`, clean
`vet`+`golangci-lint`, `go doc` API-diff = empty, commit. Approval gate between phases.
Execution runs through the **subagent-driven-development** flow.

| Phase | Content | Files (approx) | Risk |
|------:|---------|----------------|------|
| **P1** | Hygiene: lint/CI config, cruft removal, `.gitignore` | configs + deletions, no `.go` logic | none |
| **P2** | `// Package` docs + godoc on exported symbols | doc-only, all pkgs | none |
| **P3** | Dead-code removal + idiom fixes (`fmt`, sentinels, `errors.Is`, stderr, field rename) | `usage.go`, `sse.go`, `stdio.go`, `http.go`, `async.go`, `coordinator.go` | low |
| **P4** | Internal file splits (`openai.go`, `orchestrator.go`, `hooks.go`) | 3 splits | low |
| **P5** | MCP robustness (TDD): ToolManager race, http channel race, stdio leaks/scanner/zombie/timeout | `adapter.go`, `http.go`, `stdio.go` | med |
| **P6** | Other robustness (TDD): afterHooks recovery, transcript Close, openai marshal, event drops | `executor.go`, `transcript.go`, `openai.go`, `events.go` | med |
| **P7** | Orchestrator lock rework (TDD, isolated, max scrutiny) | `orchestrator.go` | high |
| **P8** | Final sweep: real `golangci-lint` green, full `-race` + `-tags=integration`, `go doc` diff, CHANGELOG, file deferred issues | — | — |

## 5. Definition of Done

- `go test -race ./...` green; `go test -tags=integration ./...` green (where keys available).
- `go vet` + `golangci-lint` clean under a config that reflects *this* repo.
- Exported API byte-identical (`go doc` diff empty across all packages).
- Every package has a `// Package` doc; key exported symbols documented.
- Zero tracked cruft; lint/CI config references only real paths.
- Every Tier-2 fix backed by a reproducing test.
- `CHANGELOG.md` updated; every deferred item filed as a GitHub issue (no silent skips).

## 6. Out of Scope / Deferred

- No public API renames, signature changes, or new exported features.
- No dependency upgrades unless required by a fix.
- No behavior changes to provider request/response semantics (e.g., the OpenAI Responses-vs-
  Chat-Completions split is documented as a known inconsistency, not "fixed," unless we can
  prove behavior-equivalence).
- Anything discovered mid-flight that can't be done behavior-preservingly → GitHub issue.

## 7. Risk Register

| Risk | Mitigation |
|------|------------|
| Lock rework introduces a new race | Isolated phase; reproduce-first tests; full `-race`; escalate if unproven |
| A "dead" symbol is used by a dependent | Grep the 4 dependent repos before deleting any exported symbol |
| Sentinel-error swap breaks a string-matching caller | Keep error *text* stable; add `errors.Is` support additively |
| Internal file split changes build/test surface | Pure move; `go doc` diff + `-race` after each split |
| `llm` tests cost real API calls | Use `-short` in inner loops; full run at phase boundaries |
