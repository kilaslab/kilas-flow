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
updated: "2026-10-08T15:57:50Z"
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
- [x] A panic shows its message and the user's line (e.g. `panic: assignment to entry in nil map (line 2)`)
- [x] Compile errors are remapped to user line numbers (for example with a `//line user.go:1` directive)
- [ ] `workflow validate` reports compile errors before a run

# Implementation Plan

Include the first panic line (and the user frame) from Stderr in the ExecutionError. Rewrite `main.go:N` to `line N-31`, or add a `//line user.go:1` directive before the body.

# Notes

Related (from the audit): none

# Related Files

`$SP/agents/ux-debug/cli/run_b2.json`, `cli/run_b2b.json`, `b2-01-code-panic.png`, `b2-02-code-compile-error.png`. internal/runcode/sandbox.go:239-256 (`outcome.Stderr` ignored on a non-zero exit), internal/runcode/runcode.go:333-365 (`Wrap` prefix).

# Attachments

## Fix

- `Wrap` puts `//line code.go:1:1` before the body, so the compiler and the runtime both count the body's lines the way the editor does. `ToolchainCompiler.Compile` passes the build output through `atUserLines`: the `# kilasflow.usercode` header is dropped, `./code.go:3:8: undefined: itemz` becomes `undefined: itemz (line 3, column 8)`, and a message past the last line (`missing return` at the wrapper's closing brace) reads `(at the end of the code)`.
- `Sandbox` reads a Go runtime crash from stderr on exit status 2 (`describeCrash`): the last `panic: ` / `fatal error: ` header with its continuation lines, plus `(line N)` from the innermost `main.run` frame in `code.go`. A pack module has no such frame, so it gets the message alone. The out-of-memory naming still wins, and a crash with nothing readable still says `exited with status 2`.
- `RuntimeVersion` is unchanged on purpose. A bump would invalidate every cached artifact, and a deployment without a toolchain would lose Code nodes that already work. An artifact built before this change still reports the panic message. It has no line number, because its frames are in `main.go`.
- Tests: `TestACompileErrorNamesTheLineInTheUsersOwnCode`, `TestACompileErrorAfterTheLastLineIsNamedAsTheEndOfTheCode`, `TestAPanicReportsItsMessageAndTheLineInTheUsersCode` (toolchain), `TestACrashIsReadFromWhatTheGoRuntimePrinted` (canned stderr, no toolchain).

## Progress

Criterion 3 (`workflow validate` reports compile errors) is not done. `POST /workflows/validate` (`internal/api/handlers/workflow_validate.go`) runs only `workflow.Compile` and cannot reach the Code executor. `validateCodeConfiguration` refuses to build on purpose, so that a save never blocks on a build. The next step is to give the validate handler a compile-status seam, such as `nodes.CodeExecutor.Status`, which compiles once and caches by source hash. It should turn a `CompileError` into a blocking diagnostic on that node. When the toolchain is missing it should report a warning, not block. This needs wiring in `cmd/kilasflow`.
