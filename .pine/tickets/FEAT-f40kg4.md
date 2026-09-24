---
id: FEAT-f40kg4
title: Code-node JavaScript workers kept per tenant, so a worker never runs two tenants' jobs
status: todo
priority: low
created: "2026-09-24T13:51:58Z"
updated: "2026-09-24T13:51:58Z"
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
- [ ] A worker never runs two tenants' jobs; an idle worker of another
      tenant is stopped to stay within the concurrency cap.
- [ ] The cost is measured against BenchmarkAJobOnAFreshWorker and
      recorded.

# Notes

Not a child of EPIC-tjnr1z (2026-09-24): optional hardening beyond the epic's acceptance criteria, which FEAT-21h6xp met with namespaces and landlock. Tracked on its own.

# Related Files
- internal/jsworker/pool.go
- internal/jsworker/confine_test.go
