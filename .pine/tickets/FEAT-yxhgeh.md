---
id: FEAT-yxhgeh
title: 'JS Code runtime P2: n8n Code-node globals shim (clean-room), modes, return normalisation, console capture'
status: todo
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-7q13t6
parent: EPIC-tjnr1z
phase: p2
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T01:33:22Z"
---

# Description

The globals are built from `request.ExpressionContext`, so a Code node sees exactly what an expression sees.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 2* section. Read it before starting.

# Acceptance Criteria
- [ ] The all-items and per-item modes behave as documented, and user code may redeclare any root (`const items = $input.all()`)
- [ ] `$input`, `items`, `$json`, `$('X')` (`all`/`first`/`last`/`item`/`itemMatching`/`params`/`isExecuted`), `$node`, `$workflow`, `$execution`, `$env`, `$vars`, `$now`, `$today` and `$jmespath` return what an expression sees
- [ ] Return normalisation wraps plain objects; invalid returns are named errors; explicit `pairedItem` wins
- [ ] `console.*` is captured into `NodeRun.Console`, persisted and emitted live, and capped at `MaxConsoleBytes`
- [ ] Tests are written in KilasFlow's own words; no n8n doc snippets are copied verbatim

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 2*.

# Notes

# Related Files

# Attachments
