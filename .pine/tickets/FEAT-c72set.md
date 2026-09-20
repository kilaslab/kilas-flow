---
id: FEAT-c72set
title: 'Router skill: using-kilasflow-skills, the always-on non-negotiables and command reference'
status: todo
priority: medium
labels:
    - agent
    - docs
deps:
    - FEAT-x5qqpm
parent: EPIC-r0yg5q
phase: p1
created: "2026-09-20T07:47:52Z"
updated: "2026-09-20T07:47:52Z"
---

## Scope

Design §5.5 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): the always-on router with the non-negotiables, red-flag rationalisations, skill index, compact command/tool reference and protocol order.

## Acceptance criteria

- [ ] States the guardrails of FEAT-mha6a0: `activate` publishes a public endpoint; `delete` and `import` create or destroy authority; secrets are referenced by credential id only; an embed session can never activate/delete/import/manage datastores; anything unverifiable is fetched from the instance.
- [ ] Idempotency guidance matches what shipped (FEAT-hj8pyx): `Idempotency-Key` on run and datastore writes.
- [ ] The compact command reference is generated from the command tree, not typed.
- [ ] Passes the same gates as the domain skills (skills drift gates ticket).
