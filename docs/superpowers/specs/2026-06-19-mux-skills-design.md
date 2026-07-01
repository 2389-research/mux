<!-- ABOUTME: Design spec for mux gene #2 — Skills: markdown procedures loaded from a
     ABOUTME: directory, advertised via an auto-injected catalog, loaded on demand by a load_skill tool. -->

# mux Gene #2 — Skills (Progressive Disclosure)

- **Date:** 2026-06-19
- **Status:** Approved design; pending spec review → implementation plan
- **Author:** Harper + Claude
- **Context:** Sub-project #2 of the mux "gene transfer" program (see
  `2026-06-18-mux-durable-sessions-design.md`, Part A). Gene #1 (durable sessions) shipped in
  PR #25. This gene transplants eve's *skills* idea: reusable, file-authored procedures the
  model can pull in on demand, instead of stuffing every instruction into one giant system
  prompt. We stay faithful to the Claude Code `SKILL.md` model rather than inventing a new
  format.

## Goal

Let a mux caller drop a folder of skill files (`<dir>/<name>/SKILL.md`, YAML frontmatter +
markdown body) on disk and have an agent (a) advertise them cheaply via a catalog injected into
the system prompt, and (b) load any one skill's full instructions on demand via a `load_skill`
tool. **Progressive disclosure:** the catalog (name + description) is always visible and cheap;
the full body is loaded only when the model asks for it.

A skill is *instructions*, not a new execution surface. `load_skill` is an ordinary `tool.Tool`
whose output is the skill body; it returns as a `tool_result` block, and the model follows the
procedure. Nothing in the orchestrator changes.

## Non-Goals (YAGNI)

- No hot reload / file watching — skills load once at startup.
- No catalog token budgeting, truncation, or ranking.
- No cross-skill references, includes, or templating.
- No per-skill tool/permission scoping, bundled scripts, or executable skill assets.
- No remote/network skill sources — local filesystem only.

## Architecture & Dependency Direction

A new self-contained `skill` package, mirroring how `session` was added in gene #1: a leaf that
imports core types and is imported only by the integration layer.

```
agent  ──►  skill  ──►  tool
```

- `skill` imports `tool` (for `tool.Tool`, `tool.Result`, `tool.SchemaProvider`).
- `agent` imports `skill` (for the `Config.Skills` field).
- `skill` imports neither `agent` nor `orchestrator`. No import cycle. The orchestrator is
  untouched — it only ever sees a larger system prompt and one more registered tool.

New direct dependency: `gopkg.in/yaml.v3` is promoted from transitive (already in `go.sum`) to a
direct `require`. No new download; it becomes a first-class dependency for frontmatter parsing.

## Components

### `skill.Skill`

```go
// Skill is a single file-authored procedure: frontmatter metadata + a markdown body.
type Skill struct {
    Name        string // unique identifier, from frontmatter `name`
    Description string // one-line "what + when", from frontmatter `description`
    Body        string // the markdown instructions (everything after the closing `---`)
}
```

### On-disk format

Claude-Code-faithful. Each skill is a directory under the skills root containing a `SKILL.md`:

```
skills/
  commit-message/
    SKILL.md
  review-pr/
    SKILL.md
```

```markdown
---
name: commit-message
description: Write a conventional commit from staged changes. Use when asked to commit.
---

# Commit Message

1. Run `git diff --cached` to see what's staged.
2. Summarize the change in the imperative mood.
3. ...
```

Frontmatter is parsed into exactly `{ Name, Description string }` via yaml.v3; **extra keys are
ignored** so skills carrying additional Claude Code fields still load. `Body` is the verbatim
text following the closing `---` fence (leading blank line trimmed).

### `skill.Registry`

Holds loaded skills and mirrors `tool.Registry`'s vocabulary so the two read alike:

```go
func LoadDir(dir string) (*Registry, error)

func (r *Registry) Get(name string) (Skill, bool)
func (r *Registry) All() []Skill          // sorted by name
func (r *Registry) List() []string        // sorted names
func (r *Registry) Count() int
func (r *Registry) Catalog() string       // the system-prompt menu; "" when empty
func (r *Registry) Tool() tool.Tool       // the load_skill tool, bound to this registry
```

`LoadDir` scans `<dir>/*/SKILL.md`, parses each, and returns a populated `*Registry` or an error
(see Error Handling). Files other than `SKILL.md` are ignored.

### `Catalog()` — the cheap, always-on half

Renders the progressive-disclosure menu (name + description only). Returns `""` for an empty
registry so the wiring never injects a dangling header.

```
## Available Skills

Load full instructions with the load_skill tool before acting on one.

- **commit-message** — Write a conventional commit from staged changes. Use when asked to commit.
- **review-pr** — Review a pull request for correctness, tests, and style.
```

Entries are emitted in sorted-name order for deterministic output.

### `Tool()` — the on-demand half (`load_skill`)

Returns a `tool.Tool` bound to the registry:

- `Name()` → `"load_skill"`
- `Description()` → instructs the model to call it with a skill name from the catalog before
  acting on that skill.
- `RequiresApproval(_)` → `false` (pure read; no side effects).
- Implements `tool.SchemaProvider`:
  `InputSchema()` → object with one required string property `name`.
- `Execute(ctx, params)`:
  - `name` present and known → `tool.NewResult("load_skill", true, skill.Body, "")`.
  - `name` missing, empty, or not a string → `tool.NewErrorResult("load_skill", <message>)`.
  - `name` unknown → `tool.NewErrorResult("load_skill", "unknown skill: " + name)`.

A failed result is a recoverable `tool_result` — the model can read the error and retry with a
valid name. The body of a found skill lands as the tool's output, becomes a `tool_result` block,
and the model proceeds with the instructions in hand.

## Integration (agent layer)

`agent.Config` gains one field:

```go
// Skills, when non-nil, exposes its skills to this agent: the catalog is injected into the
// system prompt and the load_skill tool is registered and permitted.
Skills *skill.Registry
```

The caller constructs the registry at startup and handles the load error there — the same style
as `SessionStore` (a constructed value, not a path):

```go
skills, err := skill.LoadDir("./skills")
if err != nil { /* fail startup */ }

ag := agent.New(agent.Config{
    Name:      "root",
    Registry:  registry,
    LLMClient: client,
    Skills:    skills,
})
```

In `agent.init()`, when `Skills != nil` (Approach A — agent auto-registers + auto-allows):

1. **Inject the catalog.** Append `Skills.Catalog()` to the *effective* system prompt
   (`orchConfig.SystemPrompt`, which is the orchestrator default or the agent's custom prompt),
   separated by a blank line. Skip if `Catalog()` returns `""`.
2. **Register the tool.** `a.config.Registry.Register(Skills.Tool())`. `Register` is
   overwrite-by-name, so re-registration with the same bound tool is harmless.
3. **Ensure reachability.** When building this agent's `FilteredRegistry`, use an allowlist that
   includes `"load_skill"` (an empty allowlist already permits everything, so this only matters
   when the caller set an explicit allowlist). This augmentation applies only to the allowlist
   passed to `NewFilteredRegistry`; it does **not** mutate the stored `Config.AllowedTools`, so
   `Config()` still reflects exactly what the caller passed and `SpawnChild` does not silently
   copy `load_skill` into children. This makes "set `Skills`, it works" true without the caller
   hand-editing the allowlist.

**Multi-agent constraint (documented):** the `load_skill` tool is registered on the (often
shared) `tool.Registry`. Set `Skills` on the root agent; children inherit the registry — and thus
the same `load_skill` — but `SpawnChild` does not propagate `Skills`, so the default topology has
exactly one skill set per registry. Agents that share one `tool.Registry` must not be given
*different* skill sets; give them separate registries if their skills must differ. A child can opt
out of skills entirely by denying `"load_skill"` via `DeniedTools`.

## Data Flow

1. **Startup:** `skill.LoadDir(dir)` parses every `SKILL.md` into a `*skill.Registry`.
2. **Construction:** caller sets `agent.Config.Skills`; `agent.init()` injects the catalog,
   registers `load_skill`, and permits it.
3. **Run:** the model sees the catalog in the system prompt, decides a skill applies, and calls
   `load_skill(name)`.
4. **Execute:** the executor runs the tool; the skill body returns as a `tool_result`.
5. **Follow:** the model reads the body and carries out the procedure.

## Error Handling

Fail fast at `LoadDir` time; never fail mid-run for a load reason.

| Condition | Result |
|---|---|
| Skills dir missing / unreadable | `LoadDir` error (misconfiguration is loud) |
| `SKILL.md` with no frontmatter fences | `LoadDir` error |
| Empty `name`, empty `description`, or empty `body` | `LoadDir` error |
| Duplicate `name` across directories | `LoadDir` error |
| Non-`SKILL.md` file in a skill dir | ignored |
| Extra frontmatter keys | ignored (forward-compatible) |
| `load_skill` called with unknown/missing name (runtime) | recoverable error `tool_result` |

## Testing

Real files and real parsing throughout — no mocks. The only test double is the existing scripted
`llm.Client` used across the agent suite (the house pattern for driving a deterministic turn).

**`skill` package (unit):**
- Frontmatter parsing: valid; missing fences; missing/empty `name`; missing/empty `description`;
  empty body; extra keys ignored; body preserved verbatim.
- `LoadDir`: loads multiple skills; sorted order; duplicate-name error; missing-dir error;
  non-`SKILL.md` files ignored; nested directory layout.
- `Catalog()`: format (header + `- **name** — description` lines), sorted order, empty registry
  → `""`.
- `Tool()`: `Name`/`Description`/`RequiresApproval`/`InputSchema`; `Execute` happy path returns
  the body; unknown name → error result; missing / empty / non-string `name` → error result.

**`agent` package (integration):**
- `Config.Skills` set → the agent's system prompt contains the catalog.
- `load_skill` is registered and reachable (including for an agent that uses an allowlist).
- A scripted turn that calls `load_skill` receives the skill body as the tool result.
- `Skills == nil` → no catalog, no `load_skill` registered (backward compatibility).

## Backward Compatibility

Callers that do not set `Config.Skills` are entirely unaffected: no catalog is injected, no tool
is registered, and the allowlist is untouched. The orchestrator has no new code paths. The only
new public surface is the `skill` package and the single `agent.Config.Skills` field.
