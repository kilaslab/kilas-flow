---
id: BUG-2xrz6c
title: a Wait with onError continueErrorOutput can never resume
status: done
priority: medium
labels:
    - engine
    - wait
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-10-08T00:00:00Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

The compiler adds an error output to any node whose `onError` setting is `continueErrorOutput` (`withErrorPort`, internal/workflow/compiler.go ~885-899), including a Wait node. `adaptStoredResumeOutput` pads the single stream the approval surface and the timer sweeper store to whatever port count the node declares (internal/engine/wait_service.go ~405-431), so a Wait with an error output pads its stored output to 2 streams. But `Runner.Resume`'s own port-count check expects `expectedPorts(node)` to already match what was stored, and a Wait's resume path independently expects 1 stream — the two disagree once an error output is added.

# Steps to Reproduce

1. Add a Wait node with `onError: continueErrorOutput`.
2. Suspend it (an approval wait or a timed wait) and resume it.

# Expected

The Wait resumes normally, routing to its regular or error output as its outcome decides.

# Actual

The execution fails with `resume output for node … has 2 streams, want 1`.

# Acceptance Criteria
- [x] A Wait with `onError: continueErrorOutput` resumes successfully on both its regular and error paths
- [x] A regression test covers a Wait with an error output through suspend and resume

# Related Files

internal/workflow/compiler.go `withErrorPort`, ~885-899 (adds the error output whenever `onError` is `continueErrorOutput`)
internal/engine/wait_service.go `adaptStoredResumeOutput`, ~405-431 (pads the stored single stream to the node's declared port count)
internal/engine/runner.go — the resume output stream-count check (`resume output for node %q has %d streams, want %d`, ~line 897)

## Fix (2026-10-08)

Root cause: `adaptStoredResumeOutput` padded the stored single-stream resume output to `len(node.Definition.Outputs)`, which counts the error output the compiler adds for `continueErrorOutput`. `Runner.Resume` checks the stream count against `expectedPorts(node)` — the executor's arity, which leaves the error output out because the runner adds it itself (`withErrorPort`) as it completes the node. The two counts disagreed by exactly the error output, so every resume of such a Wait failed with `resume output for node … has 2 streams, want 1`.

Change: `adaptStoredResumeOutput` (internal/engine/wait_service.go) pads to `expectedPorts(node)`, the same count `Runner.Resume` takes; `Resume` then adds the empty error output as it does for any completed node. The over-length refusal now reads `node %q returns %d`.

Scope of "both paths": a resume always completes the Wait on its regular output (timer pass-through, approval decision, webhook payload); its error output stays empty and the error branch is pruned. The Wait's error path is a failure before it suspends, which the runner already routed to the error output. An approval/webhook wait that expires is still failed by name by the sweeper, not routed by `onError`; that is unchanged and outside this bug.

Proving test: `TestAWaitWithAnErrorOutputResumes` (internal/engine/wait_error_output_test.go) — a Wait set to `continueErrorOutput` with its error output wired, suspended and resumed on a fresh worker by an approval decision and by its deadline (test clock + `SweepWaits`, no wall time): the execution succeeds, the wait's row is succeeded, the regular branch runs and the error branch does not. A third case fails the wait before it suspends and checks the item reaches the error branch. The two resume cases failed before the fix with the ticket's exact error.
