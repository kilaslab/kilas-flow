---
id: EPIC-r0yg5q
title: 'Agent surface implementation: CLI, skills bundle, scoped tokens, debug primitives, MCP adapter'
status: todo
priority: medium
labels:
    - agent
    - api
    - sdk
created: "2026-09-20T07:46:51Z"
updated: "2026-09-20T07:46:51Z"
---

## Objective

Implement the design in `docs/superpowers/specs/2026-09-20-agent-surface-design.md` (cut from FEAT-mha6a0): an AI agent can create, edit, trigger and debug KilasFlow workflows through one API, one token model, a CLI as the primary surface and an MCP server as a thin adapter over the same operations — with an embedded, CI-gated agent skills bundle so the knowledge cannot drift from the product.

## Phasing (design §7)

1. CLI command tree, `api` escape hatch, skills bundle v1 and its gates — works today with tenant API keys.
2. Scoped agent tokens, guarded verbs, audit columns (blocked by FEAT-hj8pyx).
3. Debug primitives (validate, run a pinned revision, duplicate, retry, eval).
4. MCP adapter.

## Out of scope

Giving an agent the ability to publish or delete anything, any token that is not tenant-bound, a Go library facade, a GUI or daemon. See design §2.
