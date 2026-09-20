---
id: BUG-1tj5wy
title: Worker lease never renewed and equals run timeout; duplicate concurrent execution
status: todo
priority: critical
labels:
    - engine
    - workers
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T12:06:09Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:engine-runtime.

---
### Worker lease is never renewed and equals the run timeout, so persistence runs after the lease expires and another worker re-runs the same execution concurrently, possibly forever [find:engine-runtime] (critical/bug) · area: engine/service lease + repository ClaimNext · confidence: high

runOnce sets the lease to claim time + defaultTimeout (service.go:213) and gives the run the same timeout, started later (:252). Node runs are written row by row only after the whole graph finishes (:305-358), and nothing extends the lease. When run time plus persist time crosses the lease, an idle worker reclaims the execution (executions.go:403-439), deletes the trace and runs it again from the trigger. The original worker's writes then fail with 'record not found'.

Evidence: Private :8101 (timeout 15s), script <W>/t_lease.py 100 13.5: a SIB loop over 100 x 2 KB items with a NoOp body, then done -> HTTP stub /sleep?s=13.5. server.log: `persist node "l" run: repository record not found: execution` (15:42:43), `persist node "b" ...` (15:42:58), `persist node "so" ...` (15:43:13). The stub /sleep was hit 7 times in 50 s, including two calls 2 s apart (two workers running the same execution). The execution stayed `running` with 0 node runs until cancelled. The loop-runaway run shows the same pattern: timeout at 15:35:58, then an immediate reclaim and a fresh run.

n8n behavior: An execution runs exactly once. An interrupted execution is marked crashed, not re-run.

Impact: Any execution whose run plus persist time exceeds execution.default_timeout (60 s stock) is duplicated, and may never settle. The same applies to one that times out with a sizeable trace. Every re-run repeats side effects, and this also applies across processes on PostgreSQL.

Suggested fix: Heartbeat or extend the lease while running and while persisting. Decouple lease duration from the run timeout. Persist node runs incrementally, or in one batched transaction. Cap reclaim attempts and mark an execution crashed/failed instead of silently re-running it.

Files: internal/engine/service.go, internal/repository/executions.go

Existing tickets: FEAT-9555xz, BUG-br7ggc

## Acceptance criteria

- [ ] Worker lease is never renewed and equals the run timeout, so persistence runs after the lease expires and anot
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Work (EngineCore 2026-09-20)
- Root cause confirmed in code: `runOnce` set the lease to claim time + `defaultTimeout` and gave the run the same budget, and the whole trace was written row by row *after* the graph finished, with nothing renewing the claim. A run that used its budget plus its persist time outlived its own lease, and `ClaimNext` handed the execution to the next idle worker, which deleted the trace and ran the graph from the trigger again — side effects included — once per lease period.
- Fix, engine: lease duration decoupled from the run timeout (`ServiceDeps.LeaseDuration`, default `max(2 × defaultTimeout, 30 s)`), a heartbeat renews the claim from the claim until `runOnce` has settled the execution — through the run *and* through the trace write — and a lost fence (renewal returns false: reclaimed or already settled) cancels the run so the graph cannot run twice (`startLeaseHeartbeat` in internal/engine/service.go). Sub-workflow children get the same lease and heartbeat (`InvokeWorkflow`), since a child runs inline but owns a durable lease of its own.
- Fix, persistence: the trace is written in one transaction (`GORMExecutionStore.CreateNodeRuns`) instead of a round trip per node inside the window the lease has to survive; `UpdateRuntime`/fence semantics are unchanged.
- Fix, reclaim cap: `reclaim_count` on `executions` (migration 000010, both dialects), incremented by the claim when it reclaims an expired lease; past `MaxExecutionReclaims` (2) the claim settles the row as terminal `failed` with `{"code":"execution.crashed"}` instead of claiming it, keeps the partial trace as evidence, releases the lease, and the scan moves on to the execution queued behind it. A row past the cap therefore costs one crashed record, not a worker re-running it for ever.
- Tests (regression, red before the fix — verified in a pre-fix worktree at HEAD): `TestAClaimedExecutionOutlivesItsRunTimeout` (claim has 199.61 ms left against a 200 ms run timeout before; 30 s now), `TestAWorkerThatOutlivesItsLeaseIsNotReclaimed` (a second service cannot claim a run held 3 lease periods), `TestExecutionStoreSettlesAnExecutionPastTheReclaimCapAsCrashed`, `TestExecutionStoreExtendLeaseOnlyRenewsTheHolder`. Pre-fix probe recorded 5 of 5 reclaims accepted with an expired lease (unbounded); post-fix the third is settled crashed.
- Scoped proof: `go test ./internal/engine/ -count=1` green, `go test ./internal/repository/ -count=1` green, `go test ./internal/database/ -count=1` green (migration 000010 up and the rollback-all path).
- Remaining: the ticket's adversarial live re-verify (stub/private instance, `t_lease.py` shape) is Main's final gate — this slice is the code plus scoped proof, with no live instance run from here. Residual risk: a worker killed *between* the trace write and the terminal write is still reclaimed (unchanged recovery path); with the heartbeat a live worker is no longer reclaimed, and a poison execution is now bounded by the cap instead of looping.
