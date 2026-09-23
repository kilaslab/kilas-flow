---
id: BUG-bw2zc1
title: Wait inside Loop Over Items fails on the 2nd iteration ("suspended node already completed")
status: doing
priority: high
labels:
    - engine
    - regression
    - loops
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T04:08:38Z"
---

# Description

Loop → HTTP → Wait → back to the loop is n8n's standard rate-limit pattern. With a timer Wait, the run fails after the first batch, and the failed execution still shows every node green. This is a regression, or a partial fix, of BUG-ysvmaa.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-5). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Loop → HTTP → Wait → back to the loop is the standard rate-limit pattern. Every batch waits and is processed, and a failure marks the failing node red.

**Correction (2026-09-23, on fixing):** the fault is not specific to timers. Every Wait mode fails the same way (interval, until, webhook and approval), and so does a per-item Wait (a Wait set to continue on failure, which resolves one item at a time) that suspends on more than one item. `Resume` refused whenever `checkpoint.Completed` held the suspended node, and that map keeps a node's latest output forever, so from the node's second suspension on the refusal always fired. BUG-ysvmaa's approval-mode test (`TestResumeInsideALoopProcessesEveryBatch`) passed only because its fake, `waitSuspendOnce`, suspends once and passes every later batch straight through, whereas a real Wait suspends on every batch.

# Steps to Reproduce

1. `kilasflow run wf_01a0cbc2-0b24-7278-8aa1-837b331ee14b --wait` (5 items, batch size 1, Wait 2 s, Set, back to the loop). 2. Also click Execute in the editor. 3. Open the execution.

# Expected

All five batches processed with a 2 s pause each, as n8n does. On any engine failure, the node that failed should be marked.

# Actual

Both runs fail after about 4 s with `suspended node "wait" already completed` (code `execution.failed`). The trace ends at `loop-over-items run 1` dispatching item 2, and no failed node run is recorded. The replay shows the execution as Failed while every node, including Wait, is green "Succeeded". The error names no node, and items 2 to 5 are never processed.

# Acceptance Criteria
- [x] A Loop Over Items (batch 1, 5 items) with a 2 s timer Wait processes all 5 batches
- [x] Resume checks completion by run index, or clears the Wait from `Completed` when the loop re-enters its body
- [x] When a resume fails, a failed node run is recorded for the suspended node, so the replay marks it red
- [x] A test for timer-mode resume inside a loop sits next to the existing approval-mode one

# Implementation Plan

Key the completion check in Resume by run index, or clear the Wait node from `Completed` when the loop re-enters its body. Add a timer-mode resume-inside-loop test next to the approval-mode one. When a resume fails, write a failed node run for the suspended node.

# Notes

Related tickets: BUG-ysvmaa

This affects every Wait mode, not only timers, and also a per-item Wait that suspends on more than one item. The old approval-mode loop test passed only because its fake suspended once. See the correction under Description.

Related (from the audit): BUG-ysvmaa (done; "A Wait inside a loop…" is claimed fixed with an approval-token test; with a timer Wait it still fails, so this is a regression or a partial fix)

# Related Files

`$SP/agents/ux-debug/cli/run_e.json`, `e-01-running-canvas.png` (editor banner "Run failed: suspended node "wait" already completed" after the first iteration), `e-03-exec-loop.png`. internal/engine/runner.go:889-891 (Resume refuses when `checkpoint.Completed` already holds the Wait node from iteration 1).

# Attachments

## Progress 2026-09-23 (the fix)

The completion check in `Resume` is now keyed by run index. `Checkpoint` gains an optional `SuspendRun` (checkpoint version stays 1), which is how many runs of the suspending node had completed when it suspended. `snapshotCheckpoint` sets it from `len(state.runs[node])`, and `Resume` refuses only when `len(Runs[SuspendNode]) > SuspendRun`, meaning the suspended run itself is already recorded. Checkpoints written before the field existed read 0. That is exact for a first suspension, and it refuses every later one as before, but the refusal is now recorded.

A refused resume (either refusal after the node is found: already completed, or the resume output's stream count) now returns a `Result` holding one failed `NodeRun` for the suspended node. It carries the checkpoint input, `SuspendAttempt`, `RunIndex = len(Runs[SuspendNode])`, code `node.failed` and the refusal. `runOnce` already writes a failed run's rows before settling it as failed, so the replay marks the Wait red instead of showing every node green.

Tests in `internal/engine/wait_service_test.go`, next to the approval-mode one:
- `TestATimerWaitInsideALoopProcessesEveryBatch`: 5 items, batch 1, a 2 s interval wait on every batch, driven by the test clock and timer stub. Asserts 5 body runs, all 5 items reach `done` once each, 5 succeeded wait runs at run indexes 0 to 4, and a succeeded execution.
- `TestAnApprovalWaitInsideALoopProcessesEveryBatch`: the approval suspender fires on every call.
- `TestAPerItemWaitThatSuspendsOnTwoItemsProcessesBoth`: a per-item wait that suspends on both of its items.
- `TestAResumeThatFailsRecordsAFailedRunOnTheWait`: a second-batch checkpoint rewritten without `suspendRun` is still refused, and the trace ends on one failed run of the wait (run index 1, attempt 1).

RED: all four failed before the change with `suspended node "hold" already completed`, or for the fourth, the missing `suspendRun`. With the failed run dropped from `failedResume`, the fourth fails with "the trace holds 0 failed runs". GREEN: `go test ./internal/engine/ -count=1` passes, and the six loop and per-item resume tests pass `-race -count=3`.
