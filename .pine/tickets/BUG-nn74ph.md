---
id: BUG-nn74ph
title: 'Go Code node: panics report only ''exited with status 2''; compile errors use wrapper line numbers'
status: todo
priority: medium
labels:
    - code-node
    - debugging
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

The panic message is captured on stderr and then dropped. Compile errors point 31 lines past the user's code.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-11). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** A Code node error shows the exception message with the line in the user's code (`Cannot set properties of undefined [line 2]`) and the stack in the error details.

# Steps to Reproduce

1. `[ux-debug] b2`: set Make items to `var m map[string]any\nm["boom"] = 1\nreturn items, nil` and run it. 2. Set Explode to 4 lines where line 3 is `return itemz, nil` and run it.

# Expected

`panic: assignment to entry in nil map (line 2)`, with compile errors remapped to the user's line numbers (line 3).

# Actual

The panic reports only `node "Make items": code exited with status 2`, because the stderr text (`panic: assignment to entry in nil map`) is captured but dropped. The compile error reports `code did not compile: # kilasflow.usercode\n./main.go:34:9: undefined: itemz` for the user's line 3, since the wrapper puts 31 lines before the body. Neither is caught by `workflow validate`.

# Acceptance Criteria
- [ ] A panic shows its message and the user's line (e.g. `panic: assignment to entry in nil map (line 2)`)
- [ ] Compile errors are remapped to user line numbers (for example with a `//line user.go:1` directive)
- [ ] `workflow validate` reports compile errors before a run

# Implementation Plan

Include the first panic line (and the user frame) from Stderr in the ExecutionError. Rewrite `main.go:N` to `line N-31`, or add a `//line user.go:1` directive before the body.

# Notes

Related (from the audit): none

# Related Files

`$SP/agents/ux-debug/cli/run_b2.json`, `cli/run_b2b.json`, `b2-01-code-panic.png`, `b2-02-code-compile-error.png`. internal/runcode/sandbox.go:239-256 (`outcome.Stderr` ignored on a non-zero exit), internal/runcode/runcode.go:333-365 (`Wrap` prefix).

# Attachments
