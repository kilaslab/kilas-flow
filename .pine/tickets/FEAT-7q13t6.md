---
id: FEAT-7q13t6
title: 'JS Code runtime P1: internal/jsrun core — goja engine seam, limits, interrupts, watchdog, error mapping'
status: todo
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-rkj8ry
parent: EPIC-tjnr1z
phase: p1
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T01:33:22Z"
---

# Description

The engine core: a fresh goja VM per node execution, compiled programs cached by source hash, a user-time-only deadline, a layered memory policy and user-line error mapping.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 1* section. Read it before starting.

# Acceptance Criteria
- [ ] `github.com/dop251/goja` pinned. Only `engine*.go` imports the runtime; `analyze*.go` may import the syntax-only packages (EPIC amendment 2)
- [ ] `Runner.Run` with `Limits` and the named errors `ErrTimeLimit`, `ErrMemoryLimit`, `ErrOutputLimit`, `ErrInputLimit`, `ErrHostCallLimit`, `ErrCallDepth`, `ErrInvalidReturn`, plus `*ScriptError`
- [ ] `while(true)`, a loop in a promise job, and ReDoS all stop within the deadline + 50 ms (ReDoS: + `regexp2.DefaultMatchTimeout`, which is at least the ceiling, so a timeout never reads as "no match"). VMs are never reused (EPIC amendment 15)
- [ ] The heap watchdog stops a runaway allocation with `ErrMemoryLimit`, and the process survives
- [ ] The time limit covers the user's program only: a 50 ms limit with a preloaded lodash body passes under `-race` (the Luxon version is in P3)
- [ ] Errors map to `[line N]` / `[line N, for item I]` in user coordinates
- [ ] A source-map comment never reads the filesystem, and a body that closes its wrapper is refused (EPIC amendments 3 and 6)
- [ ] The AST analyser (`internal/jsrun/analyze.go`) refuses unsupported constructs by name (EPIC amendment 7)
- [ ] The flat `code.javascript_*` config keys (EPIC amendment 1) are generated into config.example.yaml and the docs

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 1*.

# Notes

# Related Files

# Attachments
