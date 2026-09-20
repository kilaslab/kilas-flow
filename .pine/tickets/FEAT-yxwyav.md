---
id: FEAT-yxwyav
title: MCP adapter over the settled CLI verbs
status: todo
priority: low
labels:
    - agent
    - api
    - sdk
deps:
    - FEAT-ew46cb
parent: EPIC-r0yg5q
phase: p4
created: "2026-09-20T07:47:53Z"
updated: "2026-09-20T07:47:53Z"
---

## Scope

Design §6 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): tools map to CLI verbs and their descriptions are generated from `skills/index.json`; read-only tools are unguarded, guarded verbs require `confirm: true`; stdio first, streamable HTTP second.

## Acceptance criteria

- [ ] The hand-rolled versus `modelcontextprotocol/go-sdk` decision is recorded, including the licence-boundary check (`.pine/memory/licensing.md`).
- [ ] Every tool is a mapping onto an existing CLI verb / API operation, never a second implementation; a test walks the tool list against the command tree.
- [ ] Guarded tools refuse without `confirm: true`.
