---
id: FEAT-f40kg4
title: Code-node JavaScript workers kept per tenant, so a worker never runs two tenants' jobs
status: done
priority: low
created: "2026-09-24T13:51:58Z"
updated: "2026-09-25T10:07:55Z"
---

# Description

FEAT-21h6xp measured a worker's cold start, every confinement layer
included, before deciding on per-tenant workers: about 7.9 ms for a job on a
fresh worker against 1.1 ms on a warm one (Linux arm64 container; 10.7 ms
and 0.9 ms on macOS), and about 0.25 ms of it is the namespaces. Workers are
shared across tenants for up to 1000 jobs, so code that escaped the engine
and persisted in a worker would see later tenants' jobs.

Keeping workers per tenant would cost that cold start each time a worker
changed hands. It was not done in FEAT-21h6xp: it needs the tenant carried
from the executor through jsrun.Task into the pool, and the pool to evict
another tenant's idle worker to stay within
code.javascript_max_concurrent, which touches the engine seam beyond that
ticket's scope.

# Acceptance Criteria
- [x] A worker never runs two tenants' jobs; an idle worker of another
      tenant is stopped to stay within the concurrency cap.
- [x] The cost is measured against BenchmarkAJobOnAFreshWorker and
      recorded.

# Notes

Not a child of EPIC-tjnr1z (2026-09-24): optional hardening beyond the epic's acceptance criteria, which FEAT-21h6xp met with namespaces and landlock. Tracked on its own.

## Plan (2026-09-25)

- `jsrun.Task` gains `Tenant string`: who the task runs for. The in-process
  Runner has no workers and ignores it; it never crosses to a worker (the
  pool reads it from the task, `jsrun.Job` is unchanged).
- `nodes/jscode.go` (Code) and `nodes/transform.go` (Sort's comparator) set it
  from `request.Execution.TenantID`, the tenant the execution record belongs
  to (the same field the sidecar and AI memory scope by).
- `internal/jsworker` pool: each worker belongs to the tenant of its first
  job. `take(ctx, tenant)` reuses only an idle worker of that tenant (newest
  first, as today). With none, it starts one if fewer than
  `MaxConcurrent` workers exist; at the cap it stops the idle worker that has
  waited longest (necessarily another tenant's) and starts a fresh one in its
  place. A counter of live-or-starting workers, kept under `pool.mu`, is what
  "at the cap" reads. The empty tenant is a group of its own and keeps
  today's path: the newest idle worker matches first time.
- `Start()` takes a slot too (so the counter's invariant holds), and the
  worker it starts has run nothing, so the first job of any tenant may claim
  it.
- MaxRuns, idle retirement, Close unchanged in behaviour.

## Decisions (2026-09-25)

- The tenant is `request.Execution.TenantID` as given, compared exactly,
  not trimmed: it is the key the execution record and every store scope by.
- Only an idle worker that has already run a job belongs to a tenant. The
  one `Start` starts at boot (when a worker user is configured) has run
  nothing, so the first job of any tenant claims it; otherwise it would be
  kept for jobs with no tenant, which a multi-tenant deployment never has.
  An idle worker has either run a job (runs > 0) or come from Start: a job
  that reaches its worker either finishes (runs++) or marks it unhealthy.
- At the cap the evicted worker is the one idle longest (`idle[0]`), not a
  random one: the tenant most likely to come back soon keeps its warm worker.
- Under the cap no worker is evicted: other tenants' idle workers stay warm
  until the cap forces a choice or they idle out after five minutes.
- `took`, a nil-by-default hook on the Pool, is what the tests use to see
  which worker served which tenant; a job cannot observe its worker's
  identity from inside the VM.

## Progress (2026-09-25)

- `jsrun.Task.Tenant`; `nodes/jscode.go` and the Sort comparator in
  `nodes/transform.go` pass `request.Execution.TenantID`.
- `internal/jsworker/pool.go`: per-tenant take with eviction at the cap, a
  live-worker count under `pool.mu`, `Start` holds a slot.
- Tests: `internal/jsworker/tenants_test.go` (no worker serves two tenants
  under 40 concurrent jobs of five tenants incl. the empty one, -race;
  longest-idle other-tenant worker evicted at the cap and the others kept;
  a job waiting on the only slot is served on a fresh worker; job cap still
  replaces a tenant's worker; Start's worker claimed by the first tenant);
  `nodes/jscode_run_test.go` `TestTheRuntimeIsToldTheExecutionsTenant`.
- Docs: safety-boundaries (worker bullets + cold-start sentence), the Code
  (JavaScript) guide's pollution bullet, `internal/jsworker/doc.go`,
  CHANGELOG (Security).

## Measurements (2026-09-25, macOS, Apple M4, under load average 7-10)

`go test ./internal/jsworker/ -run '^$' -bench 'AJobOnAFreshWorker|AJobOnAWarmWorker|AJobForTheOtherTenant' -benchtime=3s -count=3`
(and again at 200x, count 5):

| Benchmark | ns/op (runs) |
| --- | --- |
| BenchmarkAJobOnAFreshWorker | 16.3, 23.1, 35.1 ms; 26.2-50.8 ms at 200x |
| BenchmarkAJobOnAWarmWorker | 1.47, 1.49, 1.64 ms; 2.06-2.62 ms at 200x |
| BenchmarkAJobForTheOtherTenantAtTheCap (new) | 25.3, 26.3, 27.2 ms; 22.8-28.1 ms at 200x |

The machine was shared with other agents' builds, so the absolute numbers
run about twice FEAT-21h6xp's quiet-machine 10.7 ms / 0.9 ms on macOS; the
ratios are what carry over. A tenant switch at the cap (stop the other
tenant's idle worker, start a fresh one) costs the same as a cold start,
within noise: killing the evicted worker is a signal and two closes. So a
job whose tenant has no worker pays one cold start, about 8 ms on Linux
arm64 by FEAT-21h6xp's numbers, once; its later jobs are warm. The empty-
tenant path is the warm benchmark and did not change shape: the newest idle
worker matches on the first comparison, as it was popped before.

# Related Files
- internal/jsworker/pool.go
- internal/jsworker/confine_test.go

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `1c516014` (last commit at or before ticket created 2026-09-24)
- Commits (3):
  - `4ee3d1a9` — FEAT-f40kg4: the ticket records the plan, the decisions and the cost of a tenant switch
  - `d934d015` — FEAT-f40kg4: the safety page, the Code guide and the changelog say workers are kept per tenant
  - `3f86d7a9` — FEAT-f40kg4: a JavaScript worker runs one tenant's jobs, and at the cap the longest idle worker of another tenant makes way
- Files changed (the ticket's own commits, e494b26..worktree-agent-ac5456655b7226941):

```
 .pine/tickets/FEAT-f40kg4.md                        |  82 ++++++++++++++++-
 CHANGELOG.md                                        |   7 ++
 docs/src/content/docs/concepts/safety-boundaries.md |   9 +-
 docs/src/content/docs/guides/code-javascript.md     |   7 +-
 internal/jsrun/jsrun.go                             |   6 ++
 internal/jsworker/confine_test.go                   |  20 ++++-
 internal/jsworker/doc.go                            |   5 +-
 internal/jsworker/pool.go                           | 111 ++++++++++++++++-------
 internal/jsworker/security_test.go                  |  13 +--
 internal/jsworker/tenants_test.go                   | 194 ++++++++++++++++++++++++++++++++++++++++
 nodes/jscode.go                                     |   2 +
 nodes/jscode_run_test.go                            |  27 ++++++
 nodes/transform.go                                  |   1 +
 13 files changed, 436 insertions(+), 48 deletions(-)
```
