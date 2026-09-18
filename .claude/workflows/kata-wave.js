export const meta = {
  name: 'kata-wave',
  description: 'Work a wave of ready mux katas end to end: triage, implement each on its own branch, adversarially verify, fix, and report a merge order',
  whenToUse: 'Burning down the mux kata queue. Returns mergeable branches, never merges or closes anything. args: {limit=6, label, refs}',
  phases: [
    { title: 'Scout', detail: 'read kata ready and pick candidates in priority order' },
    { title: 'Triage', detail: 'one read-only agent per candidate: right-sized, or already done?' },
    { title: 'Implement', detail: 'one worktree agent per kata: TDD on a kata/<ref> branch' },
    { title: 'Verify', detail: 'three adversarial lenses per kata: test-reality, scope, gate' },
    { title: 'Fix', detail: 'one bounded round per kata with confirmed findings' },
    { title: 'Report', detail: 'merge order, status table, and prompt-defect scan' },
  ],
}

// ABOUTME: Ultracode workflow that works a wave of ready mux katas end to end.
// ABOUTME: Triages, implements each on a branch, adversarially verifies, fixes, reports merge order.

const A = args || {}
const LIMIT = A.limit || 6
const SLACK = 3

// ---------------------------------------------------------------------------
// House rules. Every dispatched agent gets these verbatim.
//
// A house convention that is not written where the agent will read it WILL be
// reinvented, wrongly, by every agent in the wave. Each paragraph below is here
// because it already cost someone a review cycle. See gotchas.md.
// ---------------------------------------------------------------------------
const HOUSE = `
## House rules for this repo (mux)

Settled conventions. Do not rediscover them, do not improvise around them.

**Go toolchain.** The inherited GOROOT points at a Go install that no longer exists, so bare
\`go\` and \`mise exec -- go\` both fail. Inside an agent worktree the sandbox REFUSES the
\`env -u GOROOT mise exec -- ...\` wrapper. Use this form instead:

    unset GOROOT && mise exec -- go test -race -short ./...

or plain \`go\` after confirming \`go version\` reports go1.26.6. Say which you used.

**Canonical gate.** \`make build\`, \`make test\` (go test -race -short ./...), \`make lint\`
(golangci-lint). Run each through the GOROOT-safe prefix above. Your change adds zero new
warnings. Pre-existing noise is flagged, not fixed.

**Commits.** Conventional commits, concise, imperative, present tense. Write the message to a
file and use \`git commit -F <msgfile>\` — the shell here is fish and inline messages get
mangled. NEVER pass --no-verify, --no-hooks, or --no-pre-commit-hook. Forbidden, no exceptions,
no matter how slow or flaky the hook is.

**The pre-commit hook is slow and sometimes flaky.** A global prek hook runs the FULL non-short
race suite (~170s, not the fast -short one). The mcp package intermittently hangs past its
3-minute timeout and aborts the commit with "Test killed with quit: ran too long (3m0s)". That
is flaky infrastructure, not your bug, and HEAD is unchanged even if git reports success.
Confirm \`go test -race -short ./...\` is green, then RETRY the same commit. Check \`git log\`
to confirm the commit actually landed.

**New hand-written files** open with two \`// ABOUTME: \` comment lines saying what the file does.

**CHANGELOG.md.** Every user-visible change gets a bullet under \`## [Unreleased]\` in the right
section (\`### Added\`, \`### Fixed\`, \`### Breaking\`, \`### Security\`). Entries here are DENSE
PROSE, not one-liners: what the behavior is now, what it was before, and what a caller must do
differently. Name the kata ref. Read the neighbouring entries and match their register. Prefer an
EXISTING heading. Creating a new \`###\` section changes the wave's merge order, so if you create
one you must say so in your report.

**Tests.** TDD, no exceptions. Failing test first, and paste its red output. Never assert mocked
behavior. Never mock in end-to-end tests.

**Proving your test actually catches the bug.** The usual check — revert the fix, watch the test
fail — is INVALID when the test references names the fix introduced: reverting gives a compile
error, which says nothing about the assertion. MUTATE the fix instead. Keep every name, break one
behavior at a time, and confirm the test that should catch it is the one that fails, by its exact
message. If a mutation reports ok, check the mutation actually applied before believing it — a
scripted edit whose pattern silently missed reads exactly like a passing mutation test.

**Assert which error, not whether.** Where the code has a catch-all or fallback error, an
assertion like \`errors.As(err, &target)\` is satisfied by the fallback and proves nothing about
the specific path. Assert the identity: the exact reason, kind, or message.

**Measurements need a baseline column.** "Is it slow?" and "is the constraint unenforced?" are
single-column questions. Measure the same operation without the mechanism before blaming it.

**Prior claims are leads, not truth.** A kata comment saying the work already shipped, or a stale
branch that looks like the fix, has been wrong in this repo before. Reread the current code and
the currently-passing tests.

**"I searched and it isn't there" is a claim, not a result.** Before reporting something absent,
test the negative by counting keyword hits across related issues rather than concluding from the
issues you happened to open:

    kata show <id> --json | jq -r '[.issue.body] + [.comments[]?.body] | join("\\n")' | grep -c '<name>'

**Never merge, never close.** Your work ends on its branch. Do not merge to main, do not rebase
onto another wave branch, do not run \`kata close\`. Merge order is decided after the wave.

**Report honestly.** If the gate is red, say so and paste the output. A report that claims green
while red is the worst possible outcome here — worse than not doing the kata at all.
`

// ---------------------------------------------------------------------------
// Schemas
// ---------------------------------------------------------------------------
const CANDIDATES_SCHEMA = {
  type: 'object',
  required: ['candidates'],
  properties: {
    candidates: {
      type: 'array',
      items: {
        type: 'object',
        required: ['ref', 'title'],
        properties: {
          ref: { type: 'string' },
          title: { type: 'string' },
          priority: { type: 'number' },
          labels: { type: 'string' },
          body_chars: { type: 'number' },
        },
      },
    },
    source: { type: 'string' },
  },
}

const TRIAGE_SCHEMA = {
  type: 'object',
  required: ['ref', 'verdict', 'reason', 'slug'],
  properties: {
    ref: { type: 'string' },
    verdict: { type: 'string', enum: ['work', 'defer', 'already-done'] },
    reason: { type: 'string' },
    slug: { type: 'string' },
    scope: { type: 'string' },
    files: { type: 'array', items: { type: 'string' } },
    risks: { type: 'string' },
  },
}

const IMPL_SCHEMA = {
  type: 'object',
  required: ['ref', 'status', 'summary'],
  properties: {
    ref: { type: 'string' },
    status: { type: 'string', enum: ['implemented', 'blocked'] },
    blocked_reason: { type: 'string' },
    branch: { type: 'string' },
    commits: { type: 'array', items: { type: 'string' } },
    files_changed: { type: 'array', items: { type: 'string' } },
    red_evidence: { type: 'string' },
    mutation_evidence: { type: 'string' },
    gate_result: { type: 'string', enum: ['pass', 'fail', 'not-run'] },
    gate_output: { type: 'string' },
    changelog_heading: { type: 'string' },
    new_changelog_section: { type: 'boolean' },
    gotchas_entry: { type: 'boolean' },
    kata_commented: { type: 'boolean' },
    summary: { type: 'string' },
    caveats: { type: 'string' },
  },
}

const VERIFY_SCHEMA = {
  type: 'object',
  required: ['ref', 'lens', 'findings'],
  properties: {
    ref: { type: 'string' },
    lens: { type: 'string' },
    gate_result: { type: 'string', enum: ['pass', 'fail', 'not-run'] },
    notes: { type: 'string' },
    findings: {
      type: 'array',
      items: {
        type: 'object',
        required: ['severity', 'verdict', 'claim', 'evidence'],
        properties: {
          severity: { type: 'string', enum: ['blocker', 'major', 'minor'] },
          verdict: { type: 'string', enum: ['confirmed', 'suspected'] },
          claim: { type: 'string' },
          evidence: { type: 'string' },
          file: { type: 'string' },
          category: { type: 'string' },
          fix: { type: 'string' },
        },
      },
    },
  },
}

const FIX_SCHEMA = {
  type: 'object',
  required: ['ref', 'status', 'summary'],
  properties: {
    ref: { type: 'string' },
    status: { type: 'string', enum: ['fixed', 'partial', 'failed'] },
    addressed: { type: 'array', items: { type: 'string' } },
    left_open: { type: 'array', items: { type: 'string' } },
    gate_result: { type: 'string', enum: ['pass', 'fail', 'not-run'] },
    gate_output: { type: 'string' },
    commits: { type: 'array', items: { type: 'string' } },
    summary: { type: 'string' },
  },
}

// ---------------------------------------------------------------------------
// Stage 0 — Scout
// ---------------------------------------------------------------------------
phase('Scout')

let discovery
if (A.refs && A.refs.length) {
  discovery = `Work exactly these refs, in this order: ${A.refs.join(', ')}.
For each, read metadata with \`kata show <ref> --json\`. Fields live under \`.issue\`.`
} else {
  const labelFlags = A.label ? ` --label ${A.label}` : ''
  discovery = `Run this, exactly:

    kata ready --json${labelFlags} | jq -r '.issues | sort_by((.priority // 99), (.body|length)) | .[:${LIMIT + SLACK}] | .[] | [.short_id, (.priority // "unset"|tostring), (.body|length), ([.labels[]? | if type=="object" then .label else . end]|join("|")), .title] | @tsv'

Three things about that command, so you do not have to go and find them out:

- \`kata ready\` lists open issues with no open blocking predecessor. Priority runs 0..4 and
  **0 is the highest**, so ascending sort is correct.
- \`priority\` is sometimes **null**, meaning nobody set one — NOT "most urgent". A bare
  \`sort_by(.priority)\` puts null first in jq and floats unset issues ahead of real P1s, which
  is why the expression coalesces to 99 and sorts them last. Report those as priority "unset".
- \`.labels\` from \`kata ready\` and \`kata list\` is an array of **strings**. Only
  \`kata show <ref> --json\` returns label **objects** with a \`.label\` field. The
  \`if type=="object"\` branch above handles both, so the same expression works either way.

Sorting by priority then body length puts the most urgent, most tractable work first.`
}

const scout = await agent(
  `You are picking the work for a wave of mux katas. Read-only: change nothing, commit nothing.

Run from the mux repo root (your cwd). \`.kata.toml\` binds the project; from any other
directory \`kata\` silently finds nothing, which reads like lost data rather than an error.

${discovery}

Return one entry per candidate: ref (the 4-character short_id), title, priority, labels
(pipe-joined), and body_chars. Keep the order above — it is the order they will be worked.

Do not read the issue bodies. You need the metadata only.`,
  { label: 'scout:kata ready', phase: 'Scout', schema: CANDIDATES_SCHEMA },
)

const candidates = (scout && scout.candidates) || []
if (!candidates.length) {
  log('No ready katas matched. Nothing to do.')
  return { katas: [], report: 'No ready katas matched the selection.' }
}
log(`${candidates.length} candidates; the wave will take the first ${LIMIT} that clear triage.`)

// ---------------------------------------------------------------------------
// Stages 1-4 — triage, implement, verify, fix.
//
// Pipelined, no barriers: one kata can be in Verify while another is still in
// Triage. `started` is a greedy admission counter — the wave fills with the
// first LIMIT candidates that clear triage, and later arrivals drop out rather
// than overshooting the wave size. Safe without a lock: JS runs single-threaded
// between awaits.
// ---------------------------------------------------------------------------
let started = 0
const deferred = []

const LENSES = [
  {
    key: 'test-reality',
    worktree: true,
    brief: `Does the test suite actually catch this bug, or does it only appear to?

Check out the branch in your worktree WITHOUT taking the branch ref:

    git checkout --detach <branch>

Then MUTATE the production change — keep every name, break one behavior at a time — and confirm
the new test is the one that fails, by its exact message. Run the mutation loop yourself; do not
take the implementer's pasted evidence on trust. If a mutation reports ok, first check the
mutation actually applied.

Specifically hunt for:
- a test satisfied by a catch-all or fallback error rather than the specific path it claims to
  cover. Map every error constant the change touches to its test references before reading a
  single test body; a constant with zero test references is untested by name however green the
  suite looks.
- an assertion that checks only that an error occurred, not which error.
- a measurement or performance claim with no baseline: what does the same operation cost, or
  what does the same check reject, WITHOUT the mechanism being blamed?
- a golden or byte-pinned fixture that is never validated against the schema it exemplifies.

Never commit, never push, never touch the branch ref. Your mutations are throwaway: leave the
worktree clean with \`git checkout -- .\` when you are done.`,
  },
  {
    key: 'scope-and-contract',
    worktree: false,
    brief: `Did this change do what the kata asked, and only that?

Read the diff from the main repo — you need no checkout:

    git diff main...<branch>
    git log --oneline main..<branch>
    kata show <ref> --json | jq -r '.issue.body'

Check:
- every changed line traces to this kata. Flag drive-by refactoring and scope creep.
- no public contract broke silently. A changed exported signature, error type, field, or
  behavior needs a \`### Breaking\` CHANGELOG entry, not an \`### Added\` one.
- the CHANGELOG bullet sits under the heading this change belongs to, matches the dense-prose
  register of its neighbours, and names the kata ref.
- new hand-written files carry two ABOUTME lines; commits are conventional; nothing bypassed a hook.
- no functionality was disabled, skipped, or weakened instead of fixed.

A finding that cites a specification makes TWO claims: that the gap exists, and that the spec
covers it. Check the second one. Quote the spec line, or downgrade the finding to a judgment
call and say so. Two things with the same shape do not necessarily fall under the same rule.`,
  },
  {
    key: 'gate',
    worktree: true,
    brief: `Does it actually build, test, and lint? Run them yourself; trust no pasted output.

    git checkout --detach <branch>
    unset GOROOT && mise exec -- go build ./...
    unset GOROOT && mise exec -- go test -race -short ./...
    unset GOROOT && mise exec -- golangci-lint run --timeout=2m

Report the real result. A failure here is a blocker finding with the output quoted. Also check
for NEW warnings in test output that the branch introduced — pre-existing noise is not a finding,
so compare against main when you are unsure.

If the branch does not exist, report gate_result "not-run" and one blocker finding saying so.
Never commit, never push. Leave the worktree clean.`,
  },
]

const results = await pipeline(
  candidates,

  // --- Stage 1: triage (read-only, cheap) ---
  (cand) =>
    agent(
      `Triage mux kata ${cand.ref} for a work wave. Read-only: change nothing, commit nothing,
create no branch.

Kata: mux#${cand.ref} — ${cand.title}

Read it in full, comments included:

    kata show ${cand.ref} --json | jq -r '.issue.body'
    kata show ${cand.ref} --json | jq -r '.comments[]? | "--- \\(.author)\\n\\(.body)"'

Decide exactly one verdict:

- **work** — one focused agent can finish this end to end: read it, write a failing test, fix it,
  pass the gate. Roughly a single coherent change to a bounded set of files.
- **defer** — too big for one agent. The tell: the body is ITSELF a multi-task implementation
  plan, with several \`### Task N\` sections and \`- [ ]\` checklists, or it specifies contracts
  that other open katas must land first. Also defer anything that cannot be done without
  inventing a type, field, or API shape nobody specified — inventing a contract is a failure,
  declining is correct.
- **already-done** — the work is already on main. Prove it: name the file, symbol, and test that
  demonstrate it, and quote them. A kata comment claiming the work shipped is NOT proof; this
  repo has carried two such comments that were both wrong, alongside a stale branch with a
  plausible-looking implementation that was also wrong. Read the current code.

Then read enough of the codebase to sketch the change: which files, what shape, what could go
wrong. A later agent implements it — give it a lead, not a specification.

Propose a branch slug: kebab-case, two to four words, from the title. The branch will be
\`kata/${cand.ref}-<slug>\`.

${HOUSE}`,
      {
        label: `triage:${cand.ref}`,
        phase: 'Triage',
        schema: TRIAGE_SCHEMA,
        effort: 'medium',
      },
    ),

  // --- Stage 2: implement (worktree) ---
  async (triage, cand) => {
    if (!triage) return { ref: cand.ref, status: 'blocked', blocked_reason: 'triage returned nothing', summary: '' }
    if (triage.verdict !== 'work') {
      deferred.push({ ref: cand.ref, title: cand.title, verdict: triage.verdict, reason: triage.reason })
      log(`${cand.ref} ${triage.verdict}: ${triage.reason}`)
      return { ref: cand.ref, status: 'blocked', blocked_reason: `${triage.verdict}: ${triage.reason}`, summary: triage.reason }
    }
    if (started >= LIMIT) {
      deferred.push({ ref: cand.ref, title: cand.title, verdict: 'not-started', reason: `wave full at ${LIMIT}` })
      log(`${cand.ref} cleared triage but the wave is full — not started.`)
      return { ref: cand.ref, status: 'blocked', blocked_reason: 'wave full', summary: '' }
    }
    started++

    const branch = `kata/${cand.ref}-${triage.slug}`
    return agent(
      `Implement mux kata ${cand.ref} on an isolated git worktree. You own it end to end.

Kata: mux#${cand.ref} — ${cand.title}
Branch to create: \`${branch}\`

Read the kata in full before touching anything:

    kata show ${cand.ref} --json | jq -r '.issue.body'
    kata show ${cand.ref} --json | jq -r '.comments[]? | "--- \\(.author)\\n\\(.body)"'

Triage read it first and concluded:
  scope: ${triage.scope || '(none given)'}
  likely files: ${(triage.files || []).join(', ') || '(none given)'}
  risks: ${triage.risks || '(none given)'}

That is a lead. The kata body is the specification.

## Steps

1. \`git checkout -b ${branch}\` from main. Confirm with \`git status\`.
2. Read the kata and the CURRENT code. If main already satisfies it, stop and report status
   "blocked" with your evidence — do not write a redundant branch. If finishing it would mean
   inventing a contract nobody specified, stop and report "blocked"; test that negative claim
   first, per the house rules.
3. Write the failing test FIRST. Run it. Paste the red output verbatim into red_evidence.
4. Implement the smallest change that makes it pass. Every changed line traces to this kata.
5. Prove the test catches the bug by MUTATING the fix, not reverting it. Paste the mutation and
   the exact failure message into mutation_evidence.
6. Run the full gate. Paste the output.
7. Add the CHANGELOG bullet. Report which \`###\` heading, and whether you had to create it.
8. If you learned something genuinely non-obvious that would bite the next person, append one
   short section to gotchas.md. Never regenerate that file — other agents own entries in it.
   Skip this if you learned nothing surprising; a padded entry is worse than none.
9. Commit. Retry on the flaky mcp hook hang. Verify with \`git log\` that it landed.
10. Comment on the kata with the branch name and your evidence, then LEAVE IT OPEN:

        kata comment ${cand.ref} --body-stdin <<'EOF'
        Branch: ${branch}
        ...what you did, the red evidence, the mutation evidence, the gate result...
        EOF

11. Finally, run \`git checkout --detach\` so downstream verifiers can take the branch. This
    matters: leaving the branch checked out in your worktree blocks the next stage.

Do NOT close the kata. Do NOT merge. Do NOT rebase onto another branch.

${HOUSE}`,
      {
        label: `impl:${cand.ref}`,
        phase: 'Implement',
        schema: IMPL_SCHEMA,
        isolation: 'worktree',
      },
    )
  },

  // --- Stage 3: verify (three diverse lenses) ---
  async (impl, cand) => {
    if (!impl || impl.status !== 'implemented' || !impl.branch) {
      return { cand, impl, reviews: [] }
    }
    const reviews = await parallel(
      LENSES.map((lens) => () =>
        agent(
          `Adversarially review the implementation of mux kata ${cand.ref} through ONE lens:
**${lens.key}**.

Branch: \`${impl.branch}\`
Kata: mux#${cand.ref} — ${cand.title}
The implementer reported: ${impl.summary}

${lens.brief.split('<branch>').join(impl.branch).split('<ref>').join(cand.ref)}

## Reporting rules

Your job is to find real defects, not to produce findings. An empty findings list is a fine
result and is far better than a padded one.

Every finding needs:
- **verdict "confirmed"** only if you REPRODUCED it — ran the command, saw the output, quoted
  it. Anything you reason your way to but could not reproduce is "suspected". Only confirmed
  findings are acted on, so guessing "confirmed" corrupts the wave.
- **evidence**: the actual command and its actual output, quoted. Not a description of what
  you believe would happen.
- **severity**: blocker (wrong, broken, or the gate is red), major (a real gap a reviewer would
  send back), minor (style, naming, polish).

Default to refuting your own finding before filing it. Measure the control: if you are blaming a
mechanism, measure the same operation without it first. Two of the four findings on a previous
kata here measured something real and blamed the wrong cause, because nobody ran the baseline.

Never commit, never push, never merge.

${HOUSE}`,
          verifyOpts(cand.ref, lens),
        ),
      ),
    )
    return { cand, impl, reviews: reviews.filter(Boolean) }
  },

  // --- Stage 4: one bounded fix round ---
  async (prev, cand) => {
    if (!prev || !prev.impl || prev.impl.status !== 'implemented') return prev

    const all = prev.reviews.flatMap((r) => (r.findings || []).map((f) => ({ ...f, lens: r.lens })))
    const actionable = all.filter((f) => f.verdict === 'confirmed' && f.severity !== 'minor')
    const gateRed = prev.reviews.some((r) => r.gate_result === 'fail')

    if (!actionable.length && !gateRed) {
      log(`${cand.ref}: clean — ${all.length} finding(s), none confirmed above minor.`)
      return { ...prev, findings: all, actionable: [], fix: null }
    }
    log(`${cand.ref}: ${actionable.length} confirmed finding(s) to fix${gateRed ? ' (gate red)' : ''}.`)

    const list = actionable
      .map((f, i) => `${i + 1}. [${f.severity}/${f.lens}] ${f.claim}\n   file: ${f.file || 'n/a'}\n   evidence: ${f.evidence}\n   suggested: ${f.fix || 'none given'}`)
      .join('\n\n')

    const fix = await agent(
      `Fix the confirmed review findings on mux kata ${cand.ref}. One bounded round.

Branch: \`${implBranch(prev.impl)}\`
Kata: mux#${cand.ref} — ${cand.title}

Take the branch in your worktree:

    git checkout ${implBranch(prev.impl)}

If git refuses because the branch is checked out in another worktree, run \`git worktree prune\`
and retry. If it still refuses, report status "failed" saying so rather than working around it.

## Confirmed findings

${list}

## Rules

Address ONLY these findings. Do not re-review, do not refactor, do not expand scope. Each one is
already reproduced, so start by reproducing it yourself — a finding you cannot reproduce goes in
left_open with that stated, not silently fixed.

Any behavior change needs a test that fails before your fix, proved by MUTATION per the house
rules. Update the CHANGELOG bullet if the described behavior changed. Run the full gate. Commit
conventionally.

Then \`git checkout --detach\` so the branch is free.

${HOUSE}`,
      {
        label: `fix:${cand.ref}`,
        phase: 'Fix',
        schema: FIX_SCHEMA,
        isolation: 'worktree',
      },
    )
    return { ...prev, findings: all, actionable, fix }
  },
)

function verifyOpts(ref, lens) {
  const opts = { label: `verify:${ref}:${lens.key}`, phase: 'Verify', schema: VERIFY_SCHEMA, effort: 'high' }
  if (lens.worktree) opts.isolation = 'worktree'
  return opts
}

function implBranch(impl) {
  return impl && impl.branch ? impl.branch : ''
}

// ---------------------------------------------------------------------------
// Stage 5 — Report.
//
// A genuine barrier: merge order and the prompt-defect scan are cross-item
// questions that cannot be answered one kata at a time.
// ---------------------------------------------------------------------------
phase('Report')

const done = results.filter(Boolean)
const landed = done.filter((r) => r && r.impl && r.impl.status === 'implemented' && r.impl.branch)

// Merge order is deterministic, so compute it here rather than asking an agent.
// A branch that adds a whole new `###` section sits at the end of [Unreleased];
// merged early it swallows every later addition that anchors at end-of-section,
// silently filing those bullets under the wrong heading. So it goes LAST.
const mergeOrder = landed
  .map((r) => ({
    ref: r.cand.ref,
    branch: r.impl.branch,
    newSection: !!r.impl.new_changelog_section,
    heading: r.impl.changelog_heading || '(none)',
  }))
  .sort((a, b) => (a.newSection === b.newSection ? 0 : a.newSection ? 1 : -1))

const digest = done.map((r) => {
  if (!r) return { ref: '?', status: 'lost' }
  const impl = r.impl || r
  return {
    ref: (r.cand && r.cand.ref) || impl.ref,
    title: (r.cand && r.cand.title) || '',
    status: impl.status,
    blocked_reason: impl.blocked_reason,
    branch: impl.branch,
    gate: impl.gate_result,
    changelog_heading: impl.changelog_heading,
    new_changelog_section: impl.new_changelog_section,
    summary: impl.summary,
    caveats: impl.caveats,
    findings: (r.findings || []).map((f) => ({ lens: f.lens, severity: f.severity, verdict: f.verdict, claim: f.claim, file: f.file })),
    fix: r.fix ? { status: r.fix.status, addressed: r.fix.addressed, left_open: r.fix.left_open, gate: r.fix.gate_result } : null,
  }
})

log(`${landed.length} branch(es) ready, ${deferred.length} not worked.`)

const report = await agent(
  `Write the wave report for a batch of mux katas. You are summarizing finished work — do not
change any code, do not merge anything, do not close any kata.

## Per-kata results (JSON)

${JSON.stringify(digest, null, 2)}

## Not worked

${JSON.stringify(deferred, null, 2)}

## Merge order (already computed — present it as given)

${JSON.stringify(mergeOrder, null, 2)}

Branches that create a NEW \`###\` CHANGELOG section are placed last on purpose: such a section
sits at the end of \`## [Unreleased]\`, so merging it early swallows every later bullet that
anchors at end-of-section, filing them under the wrong heading with no conflict and no complaint.

## Write

A markdown report for the repo owner, in this order:

1. **Branches ready to merge**, in the given order, one line each: branch, kata ref, title, what
   it does, gate result. Mark any branch whose gate is not "pass" as NOT ready and say why.
2. **What to check by hand at merge time.** After any additive CHANGELOG resolution, each bullet
   must still sit under the heading its own branch filed it under. Name the specific collisions
   you can see — branches that touched the same file, or filed under the same heading.
3. **Findings left open**: confirmed findings nobody fixed, and suspected findings worth a look.
   Severity and kata ref for each. Say plainly if there are none.
4. **Not worked**, with the reason: deferred as too big, already done on main, or the wave filled.
   Deferred-as-too-big katas are the ones that need splitting before the next wave — name them.
5. **Prompt defects.** This is the important section. Scan the findings for a mistake that shows
   up in TWO OR MORE katas. A mistake a whole wave makes is ONE defect in the dispatch template,
   not N findings: agents with clean contexts cannot infer a convention that is not written where
   they read it. For each repeated mistake, name it, list the katas, and write the sentence that
   should be added to the workflow's house rules to stop it recurring. If nothing repeats, say so
   — do not invent a pattern from a single instance.

Be concrete and honest. If the wave went badly, the report says the wave went badly. Return the
markdown itself as your result, with no preamble.`,
  { label: 'report:wave', phase: 'Report', effort: 'high' },
)

return {
  wave: { requested: LIMIT, started, landed: landed.length, deferred: deferred.length },
  mergeOrder,
  katas: digest,
  notWorked: deferred,
  report,
}
