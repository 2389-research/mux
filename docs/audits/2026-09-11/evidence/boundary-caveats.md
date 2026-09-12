# Boundary audit caveats

- No production or repository files edited. Scratch reproduction programs use local HTTP servers, their own helper subprocesses and disposable files; no provider APIs or secrets.
- FileStore concurrent same-session writers can corrupt snapshots via the shared temporary filename, but this is excluded from findings because orchestrator/session.go:60-61 explicitly requires only one orchestrator at a time per session ID. The scratch repro demonstrates the behavior; it is not an in-contract defect.
- Agents sharing a tool.Registry can overwrite one another's load_skill handler when given different Skills. The approved docs/superpowers/specs/2026-06-19-mux-skills-design.md explicitly prohibits different skill sets on a shared tool registry and requires separate registries, so this is excluded. Parent may choose a public-documentation caveat; it is not a newly identified supported behavior defect.
- No additional actionable defects confirmed in tool filtering, permission.Checker, or skill parser/registry under their supported contracts. Mutable registration/setup APIs do not advertise concurrent mutation, so no speculative races filed.
- Raw scratch repro includes concurrent FileStore Save evidence solely to investigate the boundary; that case is intentionally excluded above.
- Parent reports baseline build/tests/race/vet/lint passed. This reviewer ran focused local reproductions, not provider integration tests. Existing tests permit malformed notifications and only assert a generic quota error, allowing several defects to pass the baseline.
