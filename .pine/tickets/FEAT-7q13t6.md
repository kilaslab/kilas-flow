---
id: FEAT-7q13t6
title: 'JS Code runtime P1: internal/jsrun core — goja engine seam, limits, interrupts, watchdog, error mapping'
status: doing
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-rkj8ry
parent: EPIC-tjnr1z
phase: p1
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T04:27:44Z"
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

**2026-09-23: implemented.** What the build found beyond the epic's amendments:

- **goja bug: destructuring parameters and direct `eval`.** A direct `eval()` in a function nested inside one with a destructuring parameter list panics inside goja ("index out of range"). The wrapper therefore takes the roots as plain positional parameters (`wrapperVersion` jsrun-2).
- **Every VM entry is guarded.** goja re-panics anything it does not recognise as a JavaScript error, and without a guard that crashed the test process. `guard` in `engine.go` turns such a panic into `ErrEngineFault`: one run fails and the server survives.
- **How the regex timeout is set.** `coverTimeLimit` only ever raises `regexp2.DefaultMatchTimeout`, to `limit + limit/10 + 100ms`. It runs at `init`, for the default 10 s, and again in every `NewRunner`. The current timeout is part of the program cache key and of the library cache key, because goja compiles a regex literal when it compiles the program. A test that lowers it must build its runner first, since `NewRunner` raises it again.
- **Watchdog.** When the heap passes the ceiling, it stops every script that is running, because goja cannot attribute memory to a VM. It then waits for those scripts to be released, collects once, and only then samples again. That way the garbage of a script it just stopped cannot kill the next one.
- **Per-item roots** are read from the parsed input before any user code runs. Between items, nothing touches an object the script can reach, so no accessor can run off the clock.
- **Benchmarks** (M4, no race; each includes a fresh VM):

  | Mode | 10 items | 1000 items |
  |---|---|---|
  | All items | 0.10 ms | 4.0 ms |
  | Each item | 0.09 ms | 7.5 ms |
- **Binary size.** Nothing links jsrun until P4 wires the node, so the size delta is measured at P4.

# Related Files

# Attachments
