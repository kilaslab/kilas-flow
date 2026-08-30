---
id: FEAT-3mady6
title: Implement deterministic core graph execution runtime
status: todo
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
updated: "2026-08-29T15:40:13Z"
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
