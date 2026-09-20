---
id: FEAT-ew46cb
title: 'Agent debug primitives: workflow validate, run --revision, workflow duplicate, exec retry, debug eval'
status: todo
priority: medium
labels:
    - agent
    - api
    - engine
deps:
    - FEAT-m4d2y1
parent: EPIC-r0yg5q
phase: p3
created: "2026-09-20T07:47:53Z"
updated: "2026-09-20T07:47:53Z"
---

## Scope

Design §4.2 and §9 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): the primitives an agent needs for the create → validate → run → observe → patch loop that do not exist as API operations today.

## Acceptance criteria

- [ ] `workflow validate` (validate a document without saving it), `run --revision` (run a pinned revision), `workflow duplicate`, `exec retry` — each an API operation first, then a CLI verb, then covered by the SDK operation gate.
- [ ] `debug eval` is decided per design §9 question 1 and recorded on this ticket: a read-only expression evaluator over an execution's context (with its attack surface reviewed) or replaced by re-run-with-a-patched-node.
- [ ] Every new operation respects tenant scoping and the scoped-token refusals of the scoped-tokens ticket.
