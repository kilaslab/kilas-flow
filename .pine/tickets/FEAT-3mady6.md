---
id: FEAT-3mady6
title: Implement deterministic core graph execution runtime
status: done
priority: critical
labels:
    - engine
    - execution
    - graph
deps:
    - FEAT-rcm205
    - FEAT-6yef51
parent: EPIC-c7gbdp
phase: p1
created: "2026-08-29T15:40:13Z"
updated: "2026-08-30T14:38:53Z"
---

## Scope

Implement compile/validate/run for the first native graph. The runtime owns scheduling, item propagation, branching, errors, cancellation, timeouts, and durable execution records; it must not depend on Svelte or imported n8n shapes.

## Acceptance criteria

- A valid canonical workflow compiles to IR and executes deterministically from Manual Trigger through Set; item input/output is recorded per node run.
- DAG scheduling executes nodes only after all required incoming dependencies are ready and supports fan-out/fan-in without race-dependent output.
- IF creates separate true/false output streams and Merge has defined, tested merge semantics; invalid cycles and disconnected required paths fail before execution.
- Execution states, node-run states, structured errors, cancellation, and global/per-node timeout behaviour are persisted and surfaced through a service API.
- The runtime is covered by table-driven unit tests plus at least one end-to-end graph test; it has no frontend or ORM dependency beyond repository interfaces.

## References

- PRD: §§20–24, 35, 50–52; Milestone 1.
- Design reference: `12-canvas-wired-manual-set-http.png`, `13-ndv-if-conditions.png`, `14-canvas-if-branching.png`.

## Relevant documentation

- Use `find-docs` only if introducing a queue/concurrency or graph library. Prefer Go standard library primitives unless a documented library is justified.

## Relevant skills

- `pine`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.
- `find-docs` if a library is introduced or its API/configuration is changed.

## Work Evidence

Closed by `pine close --evidence` on 2026-08-30.

- Base: _(none — ticket predates git history or creation time unknown; showing uncommitted changes only)_
- Commits (1):
  - `6a9f56bb` — chore: initialize governance and Pine tracking
- Files changed (base → working tree):

```
 .pine/tickets/FEAT-19f1ny.md              |   4 +-
 .pine/tickets/FEAT-3mady6.md              |   4 +-
 cmd/kilasflow/main.go                     |  34 +++++--
 internal/api/handlers/workflows.go        |  71 ++++++++++++-
 internal/api/routes.go                    |   3 +-
 internal/api/server.go                    |   7 +-
 internal/api/workflows_test.go            |  74 +++++++++++++-
 internal/execution/records.go             |  36 +++----
 internal/node/registry.go                 |  23 +++--
 internal/node/registry_test.go            |  23 ++---
 internal/repository/executions.go         | 159 +++++++++++++++++++++++++++---
 internal/repository/models.go             |  29 +++---
 internal/repository/models_test.go        |  48 +++++++++
 internal/workflow/compiler.go             | 101 +++++++++++++++++--
 internal/workflow/document_test.go        |  72 ++++++++++++++
 nodes/core.go                             |   4 +
 web/src/lib/api/generated/models/index.ts |   2 +
17 files changed, 604 insertions(+), 90 deletions(-)
```

- New untracked implementation files at close: `internal/engine/{runner,runner_test,service,service_test,worker_test}.go`, `nodes/executors.go`, and `internal/api/handlers/executions.go`.
- Verified with `make test`, `make lint`, `make build`, `make smoke-dev`, and a live HTTP Manual → Set execution whose persisted API trace reported two succeeded node runs and `{customer:"Ada",status:"ready"}` output.
