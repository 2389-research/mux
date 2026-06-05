# mux Robustness & Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the `github.com/2389-research/mux` Go library robust — eliminate confirmed crashes, data races, goroutine/process leaks, and silent data loss — while changing **zero intended functionality** and keeping the exported API surface frozen.

**Architecture:** Surgical, test-first fixes to existing files. Each fix targets a *confirmed* defect (read and verified in source). No re-architecture, no file splits, no API reshaping. Real subprocesses and real temp files in tests (no mocks, per repo convention). `go test -race` is the safety net on every task.

**Tech Stack:** Go 1.24, `go test -race`, golangci-lint v2 (gosec, revive, errcheck), `node` (real MCP mock server in `mcp/testdata/mock_server.js`), conventional commits.

---

## PRIME DIRECTIVE (read before every task)

> Do not change intended behavior. Robustness fixes that repair *broken* behavior (crashes, races, leaks, data loss) are welcome. Behavior changes that alter a *documented or intended* contract are NOT — they must be flagged for Doctor Biz, not silently applied. Prove green with `-race`; never assume.

**Three rules that follow from this:**
1. The exported API (names, signatures, behavior) is **frozen**. You may *add* doc comments. You may *add* unexported fields/types. You may not rename, remove, or reshape anything exported.
2. If a "fix" changes an observable contract (e.g. `async.Cancel`, the orchestrator lock), it is **gated** — the task says STOP and ask. Honor that.
3. Every task ends green: the named test passes under `-race`, and you have not regressed the rest of the suite.

**Baseline (already confirmed green):** `go test -race ./...` passes for all 8 packages (`agent`, `llm`, `mcp`, `orchestrator`, `coordinator`, `permission`, `tool`, `hooks`). The root package `github.com/2389-research/mux` reports `build constraints exclude all Go files` — that is expected (root has no buildable Go files), not a failure.

---

## Phase Map

| Phase | Theme | Files touched | Risk |
|-------|-------|---------------|------|
| P1 | Unblock commits + lint hygiene | `.golangci.yml` | none |
| P2 | Package doc comments (additive) | per-package `doc.go` or existing files | none |
| P3 | Idioms & dead code | `orchestrator/usage.go`, `agent/async.go` | low |
| P4 | **DEFERRED** — file splits | — | n/a |
| P5 | MCP robustness | `mcp/adapter.go`, `mcp/stdio.go`, `mcp/http.go`, `mcp/testdata/mock_server.js` | low |
| P6 | Core robustness | `agent/transcript.go`, `tool/executor.go`, `agent/async.go` | low / **1 gated** |
| P7 | Orchestrator lock | `orchestrator/orchestrator.go` | **gated — needs ratification** |
| P8 | Final verification sweep | — | none |

**Spec reconciliation note:** The design spec listed a "file splits" phase and an events.go drop-counter. Both are intentionally **out of scope** in this plan:
- *File splits (P4):* churn with no behavior value; raises review risk against the prime directive. Deferred.
- *events.go drops:* `orchestrator/events.go:Publish` drops events non-blockingly **by documented design** (comment at 116-119). Touching it changes intended behavior. Left unchanged.

---

## Phase 1 — Unblock commits & lint hygiene

The pre-commit hook runs `golangci-lint run ./...`, which currently fails on **one** gosec finding, blocking *all* commits (including the committed-design-doc). The lint config already *intends* to exclude `examples/` but the regex is wrong.

### Task 1.1: Fix the `examples/` lint exclusion (unblocks every commit)

**Files:**
- Modify: `.golangci.yml:107` and `.golangci.yml:117`

**Root cause:** `examples$` matches only a path *ending* in the literal `examples`. The offending file is `examples/full/main.go`, which does not end in `examples`, so gosec G122 fires on it (`examples/full/main.go:178`). Correcting the pattern to match the directory *and its contents* honors the config's existing intent.

- [ ] **Step 1: Confirm the finding fails the build**

Run: `golangci-lint run ./...`
Expected: exits non-zero, exactly one issue: `examples/full/main.go:178:30: G122 ... (gosec)`.

- [ ] **Step 2: Fix both exclusion patterns**

In `.golangci.yml`, under `linters.exclusions.paths` (line ~104-107) change:
```yaml
    paths:
      - third_party$
      - builtin$
      - examples$
```
to:
```yaml
    paths:
      - third_party$
      - builtin$
      - (^|/)examples/
```
And make the identical change under `formatters.exclusions.paths` (line ~114-117):
```yaml
  exclusions:
    generated: lax
    paths:
      - third_party$
      - builtin$
      - (^|/)examples/
```

- [ ] **Step 3: Verify lint is now clean**

Run: `golangci-lint run ./...`
Expected: exits 0, no issues. (If `(^|/)examples/` does not clear it, the file path is module-relative — try `^examples/` then `examples/`; verify empirically. Do **not** add a `//nolint` to the example source — fix the exclusion.)

- [ ] **Step 4: Verify the test suite still passes**

Run: `go test -race ./...`
Expected: all 8 packages `ok` (root reports the expected `build constraints exclude all Go files`).

- [ ] **Step 5: Commit the unblock AND the previously-blocked design spec**

```bash
git add .golangci.yml docs/superpowers/specs/2026-06-05-mux-robustness-design.md docs/superpowers/plans/2026-06-05-mux-robustness.md
git commit -m "build: scope golangci examples exclusion to subtree; add robustness spec+plan"
```
Expected: pre-commit hook passes (lint green), commit succeeds. If the hook fails, READ the output, fix the root cause, re-run — never `--no-verify`.

### Task 1.2: Remove cargo-culted lint exclusions

**Files:**
- Modify: `.golangci.yml`

**Root cause:** The config carries exclusions copied from sibling projects (e.g. downstream `jeff`) referencing files/symbols that do not exist in `mux`. Dead config is noise. Remove only entries whose `path`/`text` cannot match anything in this repo.

- [ ] **Step 1: Confirm each candidate truly has no match in-repo**

Run:
```bash
ls cmd/jeff/wizard.go internal/providers/gmail internal/providers/oauth internal/tools/weather_tool.go 2>&1
grep -rn 'generateMonthCalendar\|godotenv.Load\|v.BindEnv\|tx.Rollback\|tmpl.Execute\|r.RegisterAction\|approvalRules.Set' --include=*.go . 2>/dev/null
```
Expected: every path is "No such file or directory" and the grep returns nothing. Any entry that *does* match stays.

- [ ] **Step 2: Delete the confirmed-dead exclusion rules**

In `.golangci.yml`, remove these `exclusions.rules` entries (the ones proven unmatched in Step 1): the `godotenv.Load`, `v.BindEnv`, `tx.Rollback`, `tmpl.Execute`, `r.RegisterAction`, `json.Marshal`, `io.ReadAll`, `approvalRules.Set(Always|Never)Allow` text rules; the `internal/providers/gmail/`, `internal/providers/oauth/`, `cmd/jeff/wizard.go` path rules; the `generateMonthCalendar` unused rule; the `internal/tools/weather_tool.go` unparam rule; and the `QF1003:` staticcheck rule. **Keep** the `_test\.go$`, `vendor`, and `examples/` rules.

- [ ] **Step 3: Verify lint stays clean and tests stay green**

Run: `golangci-lint run ./... && go test -race ./...`
Expected: lint exits 0; all packages `ok`. If any *new* issue appears, a removed rule was load-bearing — restore that one rule.

- [ ] **Step 4: Commit**

```bash
git add .golangci.yml
git commit -m "build: drop cargo-culted lint exclusions for files absent from mux"
```

### Task 1.3: Surface stray files for Doctor Biz (do NOT auto-delete)

**Files:** none modified — this task produces a **question**, not an edit.

**Rationale:** `git status` / repo scan shows candidate cruft (`.emnv` — 0 bytes; `scenarios.jsonl` — ~21KB, no Go references). Deleting files you did not create is a 🔴 action. Surface, don't delete.

- [ ] **Step 1: Inspect the candidates**

Run:
```bash
wc -c .emnv scenarios.jsonl 2>/dev/null
grep -rn 'scenarios.jsonl\|\.emnv' --include=*.go . 2>/dev/null
head -c 300 scenarios.jsonl 2>/dev/null
```

- [ ] **Step 2: Report findings and ask**

Present what each file is and that nothing in Go references it. Ask Doctor Biz for an explicit go/no-go on `git rm`. Do not delete without a "yes." (If yes: `git rm .emnv scenarios.jsonl && git commit -m "chore: remove unreferenced stray files"`.)

---

## Phase 2 — Package doc comments (additive, zero-risk)

Add a Go package doc comment (`// Package x ...`) to each package that lacks one. This is purely additive prose — no behavior, no API change. Do **not** enable the strict revive `exported` rule (it would cascade into many pre-existing exports and is out of scope).

### Task 2.1: Add missing package doc comments

**Files:** one file per package missing a package comment (place the comment above the existing `package` clause in the package's most central file, e.g. `mcp/client.go`, `tool/tool.go`, etc. — do **not** create `doc.go` files unless a package has no obvious home file).

- [ ] **Step 1: Find packages lacking a package comment**

Run:
```bash
for d in agent llm mcp orchestrator coordinator permission tool hooks; do
  if ! grep -rlz "// Package $d" $d/*.go >/dev/null 2>&1; then echo "MISSING: $d"; fi
done
```

- [ ] **Step 2: Add a one-line package comment for each missing package**

For each missing package, add directly above its `package <name>` line (keeping the existing `// ABOUTME:` lines below or above per file convention) a comment like:
```go
// Package mcp implements Model Context Protocol clients (stdio and HTTP/SSE transports)
// and adapts MCP tools into the mux tool registry.
package mcp
```
Write an accurate one-liner per package describing what it does. Keep `// ABOUTME:` lines intact.

- [ ] **Step 3: Verify build, vet, lint, tests**

Run: `go build ./... && go vet ./... && golangci-lint run ./... && go test -race ./...`
Expected: all green.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "docs: add package-level doc comments"
```

---

## Phase 3 — Idioms & dead code

### Task 3.1: Replace hand-rolled formatting in `usage.go` with `fmt`

**Files:**
- Modify: `orchestrator/usage.go` (imports at 5-9; `formatTokenUsage` 97-100; delete `sprintf` 102-110, `itoa` 112-125, `replaceFirst` 127-133, `indexOf` 135-142)
- Test: `orchestrator/usage_test.go` (existing `TestTokenUsageString` at line 103 is the regression guard)

**Root cause:** `usage.go` hand-rolls `sprintf`/`itoa`/`replaceFirst`/`indexOf` solely "to avoid importing fmt." That is gratuitous complexity. The standard library does it correctly. Confirmed these four helpers are used **only** in `usage.go` (the `indexOf` in `orchestrator_test.go:1346` is a separate definition in the external `orchestrator_test` package).

- [ ] **Step 1: Confirm existing String() test pins the output format**

Read `orchestrator/usage_test.go:103-125` (`TestTokenUsageString`). It asserts the exact `"%d input + %d output = %d total (%d requests)"` string. This is your guard — the output must not change.

Run: `go test ./orchestrator/ -run TestTokenUsageString -v`
Expected: PASS (against current code).

- [ ] **Step 2: Re-confirm the helpers are local to usage.go**

Run: `grep -rn 'sprintf\|itoa\|replaceFirst\|indexOf' orchestrator/*.go | grep -v '_test.go'`
Expected: only matches inside `orchestrator/usage.go`. (If any other non-test file matches, STOP — they are shared; do not delete.)

- [ ] **Step 3: Edit usage.go — add fmt, rewrite format, delete helpers**

Change the import block (5-9) to add `"fmt"`:
```go
import (
	"fmt"
	"sync"

	"github.com/2389-research/mux/llm"
)
```
Replace `formatTokenUsage` (97-100) with:
```go
func formatTokenUsage(input, output, requests int64) string {
	return fmt.Sprintf("%d input + %d output = %d total (%d requests)",
		input, output, input+output, requests)
}
```
Delete the four helper functions `sprintf`, `itoa`, `replaceFirst`, `indexOf` (current lines 102-142) entirely.

- [ ] **Step 4: Verify format unchanged + suite green**

Run: `go test -race ./orchestrator/ -run TestTokenUsage -v && go vet ./orchestrator/`
Expected: all `TestTokenUsage*` PASS (string identical), vet clean.

- [ ] **Step 5: Commit**

```bash
git add orchestrator/usage.go
git commit -m "refactor: use fmt.Sprintf in token usage formatting"
```

### Task 3.2: Idiomatic context-cancellation check in `async.go`

**Files:**
- Modify: `agent/async.go:133` (inside `setComplete`)
- Test: `agent/async_test.go` (existing `TestRunAsync_Cancellation` at line 97)

**Root cause:** `setComplete` uses `err == context.Canceled`. Direct equality misses wrapped errors. `errors.Is` is the idiom. This does not change behavior for the unwrapped case the test exercises; it only *adds* correctness for wrapped errors.

- [ ] **Step 1: Confirm the line and current import**

Read `agent/async.go` around 130-140 and the import block. If `errors` is not imported, you will add it.

- [ ] **Step 2: Run the cancellation test (baseline green)**

Run: `go test ./agent/ -run TestRunAsync_Cancellation -v`
Expected: PASS.

- [ ] **Step 3: Change equality to errors.Is**

Add `"errors"` to `agent/async.go` imports if absent. Change:
```go
	if err == context.Canceled {
```
to:
```go
	if errors.Is(err, context.Canceled) {
```

- [ ] **Step 4: Verify**

Run: `go test -race ./agent/ -run TestRunAsync -v`
Expected: all `TestRunAsync*` PASS.

- [ ] **Step 5: Commit**

```bash
git add agent/async.go
git commit -m "refactor: use errors.Is for context.Canceled check in async setComplete"
```

---

## Phase 4 — DEFERRED (file splits)

No action. Splitting large files (e.g. `orchestrator/orchestrator.go`, 522 LOC) is pure churn with no behavior value and raises review risk against the prime directive. Recorded here for spec traceability. If Doctor Biz later wants structural splits, they get their own plan.

---

## Phase 5 — MCP robustness

Three confirmed defects in real network/process code. Each is a genuine crash/race/leak repair, behavior-preserving for the normal path.

### Task 5.1: Fix the `ToolManager` map data race

**Files:**
- Modify: `mcp/adapter.go:84-127` (`ToolManager` struct + 4 methods)
- Test: `mcp/mcp_test.go` (new `TestToolManagerConcurrentRefresh`)

**Root cause (confirmed):** `ToolManager.tools` (a `map[string]*ToolAdapter`) has no mutex. `Refresh` (95-105) *replaces* the map while `Tools`/`Get`/`RegisterAll` read it — a data race under `-race`.

- [ ] **Step 1: Write the failing race test**

Add to `mcp/mcp_test.go`:
```go
func TestToolManagerConcurrentRefresh(t *testing.T) {
	provider := &stubProvider{tools: []mcp.ToolInfo{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}}
	m := mcp.NewToolManager(provider)
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = m.Refresh(context.Background()) }()
		go func() { defer wg.Done(); _ = m.Tools(); _, _ = m.Get("a") }()
	}
	wg.Wait()
}

type stubProvider struct{ tools []mcp.ToolInfo }

func (s *stubProvider) ListTools(ctx context.Context) ([]mcp.ToolInfo, error) {
	return s.tools, nil
}
func (s *stubProvider) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.ToolCallResult, error) {
	return &mcp.ToolCallResult{}, nil
}
```
Add `"sync"` to the test file's imports if absent. (Confirm `ToolProvider`'s method set in `mcp/adapter.go` and match it — adjust `stubProvider` if the interface differs.)

- [ ] **Step 2: Run it under -race; expect a race**

Run: `go test -race ./mcp/ -run TestToolManagerConcurrentRefresh -v`
Expected: FAIL with `DATA RACE` on the `tools` map.

- [ ] **Step 3: Add an unexported RWMutex and guard every access**

In `mcp/adapter.go`, change the struct and methods:
```go
type ToolManager struct {
	provider ToolProvider
	mu       sync.RWMutex
	tools    map[string]*ToolAdapter
}

// NewToolManager creates a manager for MCP tools.
func NewToolManager(provider ToolProvider) *ToolManager {
	return &ToolManager{provider: provider, tools: make(map[string]*ToolAdapter)}
}

// Refresh reloads tools from the MCP server.
func (m *ToolManager) Refresh(ctx context.Context) error {
	infos, err := m.provider.ListTools(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]*ToolAdapter, len(infos))
	for _, info := range infos {
		next[info.Name] = NewToolAdapter(info, m.provider)
	}
	m.mu.Lock()
	m.tools = next
	m.mu.Unlock()
	return nil
}

// Tools returns all available tool adapters.
func (m *ToolManager) Tools() []*ToolAdapter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tools := make([]*ToolAdapter, 0, len(m.tools))
	for _, t := range m.tools {
		tools = append(tools, t)
	}
	return tools
}

// Get retrieves a specific tool adapter.
func (m *ToolManager) Get(name string) (*ToolAdapter, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tools[name]
	return t, ok
}

// RegisterAll adds all MCP tools to a tool registry.
func (m *ToolManager) RegisterAll(registry *tool.Registry) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, t := range m.tools {
		registry.Register(t)
	}
}
```
Add `"sync"` to `mcp/adapter.go` imports. Note `Refresh` now builds `next` *before* taking the lock — the ListTools call (which may block on the network) happens unlocked, exactly as before.

- [ ] **Step 4: Verify the race is gone and mcp suite is green**

Run: `go test -race ./mcp/ -run TestToolManagerConcurrentRefresh -v && go test -race ./mcp/`
Expected: new test PASS, no DATA RACE; whole `mcp` package `ok`.

- [ ] **Step 5: Commit**

```bash
git add mcp/adapter.go mcp/mcp_test.go
git commit -m "fix: guard ToolManager tool map with RWMutex to remove data race"
```

### Task 5.2: Fix stdio scanner truncation + swallowed scanner error + stdout diagnostic

**Files:**
- Modify: `mcp/stdio.go` (scanner setup 80; `readResponses` 178-200)
- Modify: `mcp/testdata/mock_server.js` (add a tool that returns a >64KB result)
- Test: `mcp/mcp_test.go` (new `TestStdioLargeToolResult`)

**Root cause (confirmed):** `bufio.NewScanner(c.stdout)` (80) keeps the default 64KB max token size. A tool result line larger than 64KB makes `Scan()` return false with `bufio.ErrTooLong`; `readResponses` treats that as EOF (181-184), never checks `c.scanner.Err()`, and exits — all pending calls then hang until their context deadline. Separately, the unmarshal-failure diagnostic at 191 writes to **stdout** (`fmt.Printf`), polluting the host program's stdout, while `Close` already uses stderr (229).

- [ ] **Step 1: Add a large-payload tool to the mock server**

In `mcp/testdata/mock_server.js`, inside the `tools/call` switch (after the `echo_tool` branch, before `error_tool`), add:
```js
      } else if (toolName === "big_tool") {
        // Emit a payload well over the 64KB default scanner token size.
        response.result = {
          content: [
            { type: "text", text: "x".repeat(200000) }
          ]
        };
```
And add its descriptor to the `TOOLS` array:
```js
  {
    name: "big_tool",
    description: "Returns a large payload",
    inputSchema: { type: "object", properties: {} }
  }
```

- [ ] **Step 2: Write the failing test (real node subprocess)**

Add to `mcp/mcp_test.go` (mirror the existing `testdata/mock_server.js` tests — `Command: "node"`, `Args: []string{"testdata/mock_server.js"}`):
```go
func TestStdioLargeToolResult(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "big",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	result, err := client.CallTool(ctx, "big_tool", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) == 0 || len(result.Content[0].Text) < 100000 {
		t.Fatalf("expected large payload, got %d content blocks", len(result.Content))
	}
}
```
(Confirm `client.CallTool`/`result.Content[0].Text` match the exported `ToolCallResult`/`ContentBlock` shape used elsewhere in this test file — they do per `TestToolAdapter`.)

- [ ] **Step 3: Run it; expect a hang→timeout failure**

Run: `go test ./mcp/ -run TestStdioLargeToolResult -v`
Expected: FAIL — `CallTool` returns a context-deadline error because the 64KB scanner truncates the response and `readResponses` exits silently.

- [ ] **Step 4: Raise the scanner buffer, check scanner.Err(), route diagnostics to stderr**

In `mcp/stdio.go`, replace the scanner construction at line 80:
```go
	c.scanner = bufio.NewScanner(c.stdout)
```
with:
```go
	c.scanner = bufio.NewScanner(c.stdout)
	// MCP tool results routinely exceed bufio.Scanner's 64KB default; raise the
	// per-line ceiling so large responses are not silently truncated.
	const maxResponseBytes = 16 * 1024 * 1024
	c.scanner.Buffer(make([]byte, 0, 64*1024), maxResponseBytes)
```
Then update `readResponses` (178-200). Replace the scan-stopped block (181-184) and the unmarshal diagnostic (191):
```go
func (c *stdioClient) readResponses() {
	defer close(c.done)
	for {
		if !c.scanner.Scan() {
			// Scanner stopped - distinguish real error from EOF/close.
			if err := c.scanner.Err(); err != nil {
				fmt.Fprintf(os.Stderr, "mcp: stdio read error: %v\n", err)
			}
			return
		}
		line := c.scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			fmt.Fprintf(os.Stderr, "mcp: failed to unmarshal response: %v (line: %s)\n", err, string(line))
			continue
		}
		c.mu.Lock()
		if ch, ok := c.pending[resp.ID]; ok {
			ch <- &resp
		}
		c.mu.Unlock()
	}
}
```
(`os` is already imported in `stdio.go`.)

- [ ] **Step 5: Verify the large payload round-trips, whole package green**

Run: `go test -race ./mcp/ -run TestStdioLargeToolResult -v && go test -race ./mcp/`
Expected: new test PASS; `mcp` package `ok`.

- [ ] **Step 6: Commit**

```bash
git add mcp/stdio.go mcp/testdata/mock_server.js mcp/mcp_test.go
git commit -m "fix: raise stdio scanner limit, surface scan errors, route diagnostics to stderr"
```

### Task 5.3: Reap the child process on Close (no zombies)

**Files:**
- Modify: `mcp/stdio.go:Close` (232-235)
- Test: `mcp/mcp_test.go` (new `TestStdioCloseReapsProcess`)

**Root cause (confirmed):** `Close` calls `c.cmd.Process.Kill()` (234) but never `Wait()`. Without `Wait`, the killed child becomes a zombie (defunct) until the parent exits, leaking a process-table slot and file descriptors. `Wait` after `Kill` returns promptly (SIGKILL can't be caught).

**Test strategy:** The child PID is not exposed (and the API is frozen — do **not** add an accessor). The reaping is therefore verified two ways: (1) an automated guard test that `Close()` returns promptly even after we add `Wait()` (a blocking `Wait` would be caught as a timeout), and (2) a manual `ps` check in Step 5 that no zombie lingers.

- [ ] **Step 1: Write the guard test (Close must not hang once Wait is added)**

Add to `mcp/mcp_test.go`:
```go
func TestStdioCloseReapsProcess(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "reap",
		Transport: "stdio",
		Command:   "node",
		Args:      []string{"testdata/mock_server.js"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- client.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("Close did not return; Wait may be blocking")
	}
}
```

- [ ] **Step 2: Run it (passes today, but guards against a Wait-induced hang)**

Run: `go test ./mcp/ -run TestStdioCloseReapsProcess -v`
Expected: PASS (this is a guard test ensuring the upcoming `Wait` does not hang Close).

- [ ] **Step 3: Add `Wait()` after `Kill()` in Close**

In `mcp/stdio.go`, replace the kill block (232-235):
```go
	// Kill process
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	return nil
```
with:
```go
	// Kill the process and reap it so it does not linger as a zombie.
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return nil
```

- [ ] **Step 4: Verify Close still returns promptly; confirm no zombie manually**

Run: `go test -race ./mcp/ -run 'TestStdioCloseReapsProcess|TestClientClose|TestClientDoubleClose' -v && go test -race ./mcp/`
Expected: all PASS within the deadline. Optional manual check: in another shell during a longer run, `ps -o pid,stat,command | grep mock_server` shows no `Z` (zombie) state lingering after tests.

- [ ] **Step 5: Commit**

```bash
git add mcp/stdio.go mcp/mcp_test.go
git commit -m "fix: reap MCP stdio child process on Close to avoid zombies"
```

### Task 5.4: Clean up on initialize failure (no orphaned goroutine/process)

**Files:**
- Modify: `mcp/stdio.go:Start` (84-86)
- Test: `mcp/mcp_test.go` (new `TestStdioInitializeFailureCleansUp`)

**Root cause (confirmed):** `Start` launches `go c.readResponses()` then `return c.initialize(ctx)` (84-85). If `initialize` fails, `Start` returns the error but leaves `c.running == true`, the reader goroutine alive, and the child process running — an orphaned goroutine + process. The caller has no handle to clean up because `Start` failed.

- [ ] **Step 1: Write the failing test (a server that never answers initialize)**

A server that reads but never writes a response makes `initialize` fail by context deadline. Use `cat </dev/null` style? No — use a server that consumes stdin and never replies. `sh -c 'cat >/dev/null'` reads and discards stdin, never writes stdout, so `initialize` times out. Add to `mcp/mcp_test.go`:
```go
func TestStdioInitializeFailureCleansUp(t *testing.T) {
	config := mcp.ServerConfig{
		Name:      "noinit",
		Transport: "stdio",
		Command:   "sh",
		Args:      []string{"-c", "cat >/dev/null"},
	}
	client, err := mcp.NewClient(config)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err == nil {
		client.Close()
		t.Fatal("expected initialize to fail (server never responds)")
	}
	// A second Close must be safe and prompt even though Start failed.
	done := make(chan error, 1)
	go func() { done <- client.Close() }()
	select {
	case <-done:
	case <-time.After(7 * time.Second):
		t.Fatal("Close after failed Start hung; cleanup did not run")
	}
}
```

- [ ] **Step 2: Run it; expect failure (Start leaves client un-closeable cleanly / hangs or errors)**

Run: `go test ./mcp/ -run TestStdioInitializeFailureCleansUp -v`
Expected: FAIL or hang — today `Start` returns the init error but leaves `running==true` with a live goroutine; the subsequent `Close` path behavior is unverified. (If it happens to pass today, the test still locks in the contract.)

- [ ] **Step 3: Make Start clean up on initialize failure**

In `mcp/stdio.go`, replace the tail of `Start` (84-86):
```go
	go c.readResponses()
	return c.initialize(ctx)
```
with:
```go
	go c.readResponses()
	if err := c.initialize(ctx); err != nil {
		// initialize failed - tear down the goroutine and child process so we
		// do not leak them; the caller only sees the error.
		_ = c.Close()
		return err
	}
	return nil
```
(`Close` is idempotent via the `running` guard at 205-208, so the test's later `Close` remains a safe no-op.)

- [ ] **Step 4: Verify cleanup + suite green**

Run: `go test -race ./mcp/ -run TestStdioInitializeFailureCleansUp -v && go test -race ./mcp/`
Expected: test PASS; `mcp` package `ok` (no `-race` goroutine-leak complaints).

- [ ] **Step 5: Commit**

```bash
git add mcp/stdio.go mcp/mcp_test.go
git commit -m "fix: tear down stdio goroutine and process when initialize fails"
```

### Task 5.5: Fix the send-on-closed-channel panic in the HTTP transport

**Files:**
- Modify: `mcp/http.go` (struct fields ~24-32; notification send 146-152; `Close` 269-277)
- Test: `mcp/http_test.go` (new `TestHTTPCloseDuringNotificationStress`)

**Root cause (confirmed):** `post` sends to `c.notifications` (148) inside a `select { case c.notifications <- notif: default: }`. `Close` does `close(c.notifications)` (274) under `closeOnce`. Sending on a closed channel **panics** — and the non-blocking `default` does not help, because a send on a *closed* channel panics regardless of buffer state. A `post` in flight concurrently with `Close` can crash the program.

- [ ] **Step 1: Read the struct to place the new fields**

Read `mcp/http.go:24-40`. Note the existing fields (`notifications chan Notification`, `closeOnce sync.Once`). You will add `notifyMu sync.Mutex` and `notifyClosed bool`.

- [ ] **Step 2: Write the failing stress test**

Add to `mcp/http_test.go` a test that streams notifications while closing concurrently, run under `-race` and enough iterations to hit the window. Model the SSE server on the existing `http_test.go` server helpers (it already builds `httptest.Server`s returning `text/event-stream`). Minimal version:
```go
func TestHTTPCloseDuringNotificationStress(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fl, _ := w.(http.Flusher)
			// Stream several notifications, then the response.
			for i := 0; i < 20; i++ {
				fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"note\",\"params\":{}}\n\n")
				if fl != nil {
					fl.Flush()
				}
			}
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n")
			if fl != nil {
				fl.Flush()
			}
		}))
		config := mcp.ServerConfig{Transport: "http", URL: srv.URL}
		client, err := mcp.NewClient(config)
		if err != nil {
			srv.Close()
			t.Fatalf("NewClient: %v", err)
		}
		ctx := context.Background()
		go func() { _, _ = client.ListTools(ctx) }() // drives post() -> notification sends
		client.Close()                               // concurrent close
		srv.Close()
	}
}
```
Add imports `net/http`, `net/http/httptest`, `fmt` if absent. (Match the exact constructor/method names used elsewhere in `http_test.go`; adjust `ListTools` to whichever exported call routes through `post`.)

- [ ] **Step 3: Run it under -race; expect a panic or race**

Run: `go test -race ./mcp/ -run TestHTTPCloseDuringNotificationStress -v`
Expected: FAIL — `panic: send on closed channel` on some iteration (or a `-race` report).

- [ ] **Step 4: Guard sends and close with a mutex + closed flag**

In `mcp/http.go`, add to the struct:
```go
	notifyMu     sync.Mutex
	notifyClosed bool
```
Replace the notification send (146-152) — currently:
```go
				if err := json.Unmarshal([]byte(event.Data), &notif); err == nil && notif.Method != "" {
					select {
					case c.notifications <- notif:
					default:
					}
					continue
				}
```
with:
```go
				if err := json.Unmarshal([]byte(event.Data), &notif); err == nil && notif.Method != "" {
					c.notifyMu.Lock()
					if !c.notifyClosed {
						select {
						case c.notifications <- notif:
						default:
						}
					}
					c.notifyMu.Unlock()
					continue
				}
```
And in `Close` (269-277), make the close happen under the same mutex/flag:
```go
func (c *httpClient) Close() error {
	c.closeOnce.Do(func() {
		c.notifyMu.Lock()
		c.notifyClosed = true
		close(c.notifications)
		c.notifyMu.Unlock()
		// ... preserve the rest of the existing Close body (cancel, etc.) ...
	})
	return nil
}
```
Preserve every other statement already in `Close`. The send is non-blocking (keeps the `default`), so holding `notifyMu` across it cannot deadlock.

- [ ] **Step 5: Verify no panic across iterations + package green**

Run: `go test -race ./mcp/ -run TestHTTPCloseDuringNotificationStress -count=3 -v && go test -race ./mcp/`
Expected: PASS all iterations, no panic, no race; `mcp` package `ok`.

- [ ] **Step 6: Commit**

```bash
git add mcp/http.go mcp/http_test.go
git commit -m "fix: prevent send-on-closed-channel panic in HTTP notification path"
```

### Task 5.6 (optional, low priority): Unexported sentinel errors for stdio client states

**Files:**
- Modify: `mcp/stdio.go` (errors at 51, 155, 172)
- Test: existing `TestClientStartAlreadyRunning` (asserts `err.Error() == "client already running"`) is the guard.

**Root cause:** `fmt.Errorf("client already running")` / `fmt.Errorf("client closed")` are bare strings, not matchable with `errors.Is`. Define **unexported** sentinels (keeps the API frozen) preserving the exact strings.

- [ ] **Step 1: Define unexported sentinels and use them**

At the top of `mcp/stdio.go` (after imports) add:
```go
var (
	errClientRunning = errors.New("client already running")
	errClientClosed  = errors.New("client closed")
)
```
Add `"errors"` to imports. Replace `fmt.Errorf("client already running")` (51) with `errClientRunning`, and both `fmt.Errorf("client closed")` (155, 172) with `errClientClosed`. The `.Error()` strings are identical, so `TestClientStartAlreadyRunning` still passes.

- [ ] **Step 2: Verify**

Run: `go test -race ./mcp/ -run TestClientStart -v && go test -race ./mcp/`
Expected: PASS; package `ok`.

- [ ] **Step 3: Commit**

```bash
git add mcp/stdio.go
git commit -m "refactor: use unexported sentinel errors for stdio client states"
```

---

## Phase 6 — Core robustness (agent + tool)

### Task 6.1: Capture close errors on transcript save (no silent data loss)

**Files:**
- Modify: `agent/transcript.go:SaveToFile` (208-216) and `SaveToFileJSONL` (230-238)
- Test: `agent/transcript_test.go` (existing `TestTranscriptSaveLoadFile` 154, `TestTranscriptSaveLoadFileJSONL` 186 are regression guards; add `TestSaveToFileErrorOnBadPath`)

**Root cause (confirmed):** Both save methods `os.Create` then `defer f.Close()` without inspecting the close error. On a write path, `Close` can surface a flush/write failure (disk full, I/O error) that `defer f.Close()` swallows — the caller is told the save succeeded when bytes were lost. (The `LoadFromFile*` read paths are fine; leave them.)

- [ ] **Step 1: Read the two methods**

Read `agent/transcript.go:205-240`. Confirm both follow the `f, err := os.Create(path); defer f.Close(); ...SaveJSON(f)` shape.

- [ ] **Step 2: Write a test that exercises the error return on an unwritable path**

Add to `agent/transcript_test.go`:
```go
func TestSaveToFileErrorOnBadPath(t *testing.T) {
	tr := agent.NewTranscript() // match the existing constructor used in this file
	// A path whose parent is a regular file (not a dir) makes os.Create fail.
	tmp := t.TempDir()
	notADir := filepath.Join(tmp, "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(notADir, "nested.json")
	if err := tr.SaveToFile(badPath); err == nil {
		t.Fatal("expected error saving under a non-directory path")
	}
	if err := tr.SaveToFileJSONL(badPath); err == nil {
		t.Fatal("expected error saving JSONL under a non-directory path")
	}
}
```
Match `agent.NewTranscript()` to however transcripts are constructed in this test file (see `TestNewTranscript` at line 19); add `"os"`/`"path/filepath"` imports if absent.

- [ ] **Step 3: Run it (passes today for the os.Create path, but pins the error contract)**

Run: `go test ./agent/ -run TestSaveToFileErrorOnBadPath -v`
Expected: PASS (os.Create already errors here). This locks the contract before we touch the close handling.

- [ ] **Step 4: Capture close errors via named returns**

In `agent/transcript.go`, rewrite both methods to a named `err` return and a deferred close that promotes a close error when the write itself succeeded:
```go
// SaveToFile writes the transcript to path as JSON.
func (t *Transcript) SaveToFile(path string) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	return t.SaveJSON(f)
}

// SaveToFileJSONL writes the transcript to path as JSON Lines.
func (t *Transcript) SaveToFileJSONL(path string) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	return t.SaveJSONL(f)
}
```
Keep the surrounding doc comments. The signature stays `func(path string) error` (named return is the same exported type) — API frozen.

- [ ] **Step 5: Verify round-trips still work + error path holds**

Run: `go test -race ./agent/ -run 'TestTranscriptSaveLoad|TestSaveToFileError' -v`
Expected: all PASS — successful saves still round-trip, error path still returns an error.

- [ ] **Step 6: Commit**

```bash
git add agent/transcript.go agent/transcript_test.go
git commit -m "fix: capture file close errors when saving transcripts"
```

### Task 6.2: Recover from panics in after-hooks (parity with before-hooks)

**Files:**
- Modify: `tool/executor.go:127-130` (after-hook loop)
- Test: `tool/tool_test.go` (model on existing `TestHookErrorHandling` at 503 / `TestExecutorHooks` at 207); add `TestAfterHookPanicRecovered`

**Root cause (confirmed):** Before-hooks run inside a `func(){ defer recover() ... }()` (112-122) so a panicking before-hook is logged and execution continues. After-hooks (128-130) run bare — a panicking after-hook propagates up and crashes the tool call (and the orchestrator loop). Inconsistent and fragile.

- [ ] **Step 1: Write the failing test**

Add to `tool/tool_test.go` (match the executor/registry construction used by `TestExecutorHooks`):
```go
func TestAfterHookPanicRecovered(t *testing.T) {
	exec := tool.NewExecutor() // match the constructor used elsewhere in this file
	exec.Register(&echoTool{})  // match the test tool type used elsewhere in this file
	exec.AddAfterHook(func(ctx context.Context, name string, params map[string]any, result *tool.Result, err error) {
		panic("after hook boom")
	})

	// Must not panic; the tool result must still come back.
	result, err := exec.Execute(context.Background(), "echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a result despite panicking after-hook")
	}
}
```
Adjust `tool.NewExecutor()`, `exec.Register`, `echoTool`, `AddAfterHook`, and the `Execute` signature to the exact names already used in `tool/tool_test.go` (see `TestExecutorHooks`/`TestHookExecutionOrder`). The hook signature must match `AfterHook` in `tool/executor.go`.

- [ ] **Step 2: Run it; expect a panic-crash**

Run: `go test ./tool/ -run TestAfterHookPanicRecovered -v`
Expected: FAIL — the test panics (`after hook boom`) and crashes.

- [ ] **Step 3: Wrap after-hooks in the same recover pattern as before-hooks**

In `tool/executor.go`, replace the after-hook loop (127-130):
```go
	// Run after hooks
	for _, hook := range e.afterHooks {
		hook(ctx, toolName, params, result, err)
	}
```
with:
```go
	// Run after hooks with panic recovery to prevent hook failures from crashing execution
	for _, hook := range e.afterHooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// Hook panicked - log and continue execution
					fmt.Fprintf(os.Stderr, "Warning: after hook panicked for tool %s: %v\n", toolName, r)
				}
			}()
			hook(ctx, toolName, params, result, err)
		}()
	}
```
(`fmt` and `os` are already imported — the before-hook block uses them.)

- [ ] **Step 4: Verify recovery + existing hook tests green**

Run: `go test -race ./tool/ -run 'Hook|Executor' -v`
Expected: `TestAfterHookPanicRecovered` PASS; all existing hook/executor tests still PASS.

- [ ] **Step 5: Commit**

```bash
git add tool/executor.go tool/tool_test.go
git commit -m "fix: recover from panics in tool after-hooks to match before-hook safety"
```

### Task 6.3 (GATED — changes an observable contract): Make `async.Cancel` actually cancel

**Files:**
- Modify: `agent/async.go` (the async handle type, `RunAsync`/`ContinueAsync`/`RunChildAsync` context setup, `Cancel` 116-123)
- Test: existing `agent/async_test.go:TestRunAsync_Cancellation` (97) is the gate.

**Root cause (confirmed):** `Cancel` sets the status to Cancelled via atomic CAS but never cancels a context (the doc even says it "requires the original context to have been cancellable"). So the underlying agent keeps running, and when it finishes, `setComplete` can overwrite the Cancelled status with Completed/Failed. `Cancel` does not cancel.

> **GATE — do this first.** Read `agent/async_test.go:97-128` (`TestRunAsync_Cancellation`). Determine what it asserts:
> - If it only asserts that `Cancel()` returns `true` and status becomes Cancelled (and does **not** assert the agent keeps running), the real fix is safe — implement it.
> - If any existing test *relies on the agent continuing to run after Cancel* (the broken behavior), **STOP** and ask Doctor Biz before changing it. Report exactly which test and line encodes the old behavior.

- [ ] **Step 1: Read the gate test and the async handle struct**

Read `agent/async_test.go:97-128` and the handle/struct definition in `agent/async.go` (fields, the three `*Async` constructors, `setComplete`, `Cancel`). Decide per the gate above. If gated-stop, halt here and report.

- [ ] **Step 2: Write/strengthen the failing test (only if gate passed)**

Add to `agent/async_test.go` a test that proves cancellation actually stops the run and the status stays Cancelled:
```go
func TestRunAsync_CancelActuallyStops(t *testing.T) {
	// Use a real agent wired to a slow/long operation via the same harness
	// TestRunAsync_Cancellation uses. Start it, Cancel, then assert:
	//   (1) the run reaches a terminal Cancelled status promptly, and
	//   (2) it STAYS Cancelled (no later overwrite to Completed).
	// Mirror the exact agent/orchestrator construction from TestRunAsync_Cancellation.
}
```
Fill the body by mirroring `TestRunAsync_Cancellation`'s setup exactly (same constructors, same fake-clock/slow mechanism). Assert `handle.Status()` is the Cancelled constant after `Cancel()` and remains so after `handle.Wait()`/a short delay.

- [ ] **Step 3: Run it; expect failure**

Run: `go test ./agent/ -run TestRunAsync_CancelActuallyStops -v`
Expected: FAIL — status is overwritten or the run does not stop.

- [ ] **Step 4: Store a CancelFunc and call it in Cancel**

In each `*Async` constructor, derive a cancellable context and store its cancel on the handle:
```go
	ctx, cancel := context.WithCancel(ctx)
	h := &asyncHandle{ /* existing fields */ cancel: cancel}
```
Add a `cancel context.CancelFunc` field to the handle struct. In `Cancel` (116-123), after the status CAS, call it:
```go
func (h *asyncHandle) Cancel() bool {
	if h.status.CompareAndSwap(int32(RunStatusRunning), int32(RunStatusCancelled)) {
		if h.cancel != nil {
			h.cancel()
		}
		return true
	}
	return false
}
```
Match the real field/type/constant names in `async.go` (the snippet uses placeholders `asyncHandle`/`status`/`RunStatusRunning`). Ensure the goroutine's `setComplete` still records terminal state; with real cancellation it returns `context.Canceled` and `setComplete` (now using `errors.Is`, Task 3.2) keeps it Cancelled.

- [ ] **Step 5: Verify cancellation stops the run, status durable, suite green**

Run: `go test -race ./agent/ -run 'TestRunAsync|TestContinueAsync|TestRunChildAsync' -v`
Expected: new test PASS; all existing async tests still PASS. If any existing test now fails because it depended on the old behavior, STOP and report to Doctor Biz.

- [ ] **Step 6: Commit**

```bash
git add agent/async.go agent/async_test.go
git commit -m "fix: make async Cancel cancel the run context and keep Cancelled status durable"
```

---

## Phase 7 — Orchestrator lock (GATED: needs Doctor Biz ratification)

**This is the one place where "everything incl. lock rework" and "change NO functionality" genuinely collide.** Read carefully; do not silently re-architect.

**Findings (confirmed by reading the code):**
- `Run`/`Continue` hold `o.mu.Lock()` across the *entire* think-act loop, including hooks, LLM calls, tool execution, and compaction (182-249).
- `Messages`/`SetMessages`/`ClearMessages` also take `o.mu` (153-175).
- Internal loop code touches `o.messages` **directly** (187, 204) — it never calls the public lockers — so there is **no currently-firing deadlock** in the library's own paths.
- The coarse lock's behavior ("one `Run` at a time; not safe for concurrent `Run`") is a **documented, deliberate contract** (doc comment at 180), not a bug.
- The only real hazard is *latent*: a user-supplied hook or tool, invoked synchronously inside the loop, that calls back into `o.Messages()` would deadlock (Go mutexes are non-reentrant).

**Why this is gated:** Any fine-grained "lock rework" that lets `Messages()` return *during* a `Run` changes an observable contract (today it blocks until `Run` finishes). That is a behavior change. The prime directive says behavior changes to intended contracts must be flagged, not chosen unilaterally — even though Doctor Biz selected the aggressive scope, the two instructions conflict *here specifically*.

### Task 7.1 (default, zero-behavior-change): Document the locking contract + lock in a regression test

**Files:**
- Modify: `orchestrator/orchestrator.go` (doc comments on `Messages`/`SetMessages`/`ClearMessages`/`Run`/`Continue`)
- Test: `orchestrator/orchestrator_test.go` (new `TestOrchestratorLockContract`)

- [ ] **Step 1: Document the reentrancy hazard precisely (no code behavior change)**

Add to the godoc of `Messages`/`SetMessages`/`ClearMessages` a sentence:
```go
// Messages must not be called from within a hook or tool that runs during
// Run/Continue: those hold the orchestrator lock for the duration of the loop,
// and this method would deadlock waiting for it.
```
Add to `Run`/`Continue` godoc a note that they hold the lock for the entire loop (so observers should snapshot before/after, not during).

- [ ] **Step 2: Add a regression test pinning the current contract**

Add a test asserting the *current* guarantees: `SetMessages` then `Messages` round-trips; `Run` replaces history; `Continue` appends; and concurrent `Run` calls are serialized (the second observes a consistent final state, no race under `-race`). Mirror the orchestrator construction used by existing tests in `orchestrator_test.go`.

- [ ] **Step 3: Verify**

Run: `go test -race ./orchestrator/ -run TestOrchestratorLockContract -v && go test -race ./orchestrator/`
Expected: PASS; package `ok`.

- [ ] **Step 4: Commit**

```bash
git add orchestrator/orchestrator.go orchestrator/orchestrator_test.go
git commit -m "docs: document orchestrator locking contract; add lock-contract regression test"
```

### Task 7.2 (HOLD): Aggressive lock rework — present, do not execute

**Do not implement without explicit ratification.** When you reach this phase, present Doctor Biz with the two options and let him choose:

- **Option A (default, recommended): keep the coarse lock.** Zero behavior change. The documented "one Run at a time" contract holds. Task 7.1 ships; 7.2 is dropped.
- **Option B (aggressive): fine-grained locking** — split a short-held `messagesMu` (guarding only `o.messages` snapshot/restore) from a `runMu` that serializes `Run`/`Continue`. This makes `Messages()` return a consistent snapshot *during* a run and removes the reentrancy deadlock. It **changes observable semantics** (`Messages()` no longer blocks until `Run` finishes) and risks introducing races the coarse lock currently prevents, so it needs its own test pass (concurrent `Messages()` during `Run` under `-race`) and Doctor Biz's sign-off.

Present this as a question at execution time. Default to A.

---

## Phase 8 — Final verification sweep

### Task 8.1: Full green + frozen-API proof

- [ ] **Step 1: Full race suite**

Run: `go test -race ./...`
Expected: all 8 packages `ok`; root reports the expected `build constraints exclude all Go files`.

- [ ] **Step 2: Vet + lint**

Run: `go vet ./... && golangci-lint run ./...`
Expected: both exit 0, no issues.

- [ ] **Step 3: Prove the exported API surface is unchanged**

Capture the public surface on `main` and on the branch and diff:
```bash
git stash --include-untracked 2>/dev/null; true
for p in agent llm mcp orchestrator coordinator permission tool hooks; do
  echo "== $p =="; go doc -all ./$p 2>/dev/null | grep -E '^(func|type|var|const|    [A-Z])' ;
done > /tmp/mux_api_branch.txt
git switch main >/dev/null 2>&1
for p in agent llm mcp orchestrator coordinator permission tool hooks; do
  echo "== $p =="; go doc -all ./$p 2>/dev/null | grep -E '^(func|type|var|const|    [A-Z])' ;
done > /tmp/mux_api_main.txt
git switch refactor/robustness >/dev/null 2>&1
diff /tmp/mux_api_main.txt /tmp/mux_api_branch.txt
```
Expected: the diff shows **only additions** (new doc comments do not change `go doc` signatures; new unexported sentinels/fields do not appear). **Any removed or changed exported signature is a prime-directive violation — fix before proceeding.**

- [ ] **Step 4: Confirm clean tree and branch state**

Run: `git status && git log --oneline main..HEAD`
Expected: clean working tree; the commit list matches the tasks above.

### Task 8.2: Open the PR

- [ ] **Step 1: Push and open PR to main**

```bash
git push -u origin refactor/robustness
gh pr create --title "Robustness & polish: fix confirmed races, leaks, and crashes (no behavior change)" \
  --body "Surgical robustness pass on mux. Fixes confirmed data races (ToolManager map, HTTP notification channel), goroutine/process leaks (stdio init failure, zombie reaping), silent data loss (transcript close errors), and crash paths (after-hook panics, stdio scanner truncation). Idiomatic cleanups (fmt, errors.Is, sentinels). Exported API frozen — see API-diff in Task 8.1. Orchestrator lock left coarse by default (Phase 7, gated)."
```

- [ ] **Step 2: Report PR URL to Doctor Biz.**

---

## Definition of Done

- `go test -race ./...` green across all 8 packages.
- `go vet ./...` and `golangci-lint run ./...` clean (commits no longer blocked).
- Exported API surface diff (Task 8.1 Step 3) shows additions only — zero removals/changes.
- Every confirmed defect fixed with a test that fails before and passes after.
- The two gated items (async.Cancel, orchestrator lock rework) either implemented with Doctor Biz's explicit go, or deferred with his acknowledgement.
- events.go and the read paths of transcript.go left unchanged (intended behavior).
