---
id: BUG-c3fgw5
title: resume failures outside Runner.Resume still leave the replay all-green
status: done
priority: medium
labels:
    - engine
    - observability
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-10-08T00:00:00Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

`Runner.Resume` refuses some failures through `failedResume`, which writes a failed node run for the suspended node so a replay shows where the execution actually stopped. But several other places that can fail a resume return a bare `Result{}, err` instead, with no failed node run recorded:

- `resumeRun`'s own failures — compile, checkpoint decode, and `adaptStoredResumeOutput` (internal/engine/wait_service.go ~377-398)
- `Runner.Resume`'s early returns before it reaches the two checks that already use `failedResume` — nil executor registry, trigger mismatch, graph preparation (`prepareGraph`), and the suspended node missing from the compiled graph

Because these return an empty `Result{}` rather than a result carrying a failed node run, a replay of the execution shows every node that ran before suspension as succeeded, with nothing marking where it actually stopped.

# Steps to Reproduce

1. Force one of the above failures — for example, resume an execution whose checkpoint's trigger no longer matches the request's trigger node, or whose stored checkpoint fails to decode.
2. Look at the execution's replay/node-run history.

# Expected

Every resume failure, wherever it originates, records a failed node run for the suspended node, the way `failedResume` already does for the two checks that use it.

# Actual

The failures listed above return `Result{}` with no failed node run. The replay shows every node before the suspend point as green, with no visible indication of where or why the resume failed.

# Acceptance Criteria
- [x] `resumeRun`'s compile, checkpoint-decode, and `adaptStoredResumeOutput` failures each record a failed node run for the suspended node
- [x] `Runner.Resume`'s early returns (trigger mismatch, graph preparation, suspended node missing) each record a failed node run for the suspended node
- [x] A test asserts the replay shows a failed node run, not an all-green history, for at least one failure from each group

# Related Files

internal/engine/wait_service.go `resumeRun`, ~377-398 (compile, checkpoint decode, `adaptStoredResumeOutput` — each returns `Result{}, err`)
internal/engine/runner.go `Resume`, ~874-895 (nil registry, trigger mismatch, `prepareGraph`, node-missing early returns)
internal/engine/runner.go `failedResume`, ~965 (the pattern these should follow)

## Fix (2026-10-08)

Root cause: only the two checks after `Runner.Resume` had found the suspended node (already completed, wrong stream count) went through `failedResume`; every earlier refusal, in `Resume` and in the service's `resumeRun`, returned a bare `Result{}`. The service writes the trace from the result before it fails the execution, so those refusals wrote nothing — and since a suspended wait has no row of its own until it completes, the replay showed only the green nodes before it.

Change:
- `resumeRun` (internal/engine/wait_service.go) decodes the checkpoint first, then compiles, and returns `failedResume` for the decode, compile, `adaptStoredResumeOutput` and trigger-input failures. An undecodable checkpoint fails on the node the wait row names (`wait.NodeID`); its run index is unknown, so it is left at 0 and the trace write's existing collision repair (`CreateNodeRuns`) moves it to the next free index.
- `Runner.Resume` (internal/engine/runner.go) returns `failedResume` for the nil registry, trigger mismatch, `prepareGraph` and missing-node refusals.
- `failedResume` returns a bare result only when there is no node to mark (empty suspend node), since a row with no node cannot be drawn.

Proving test: `TestEveryRefusedResumeRecordsAFailedRunOnTheWait` (internal/engine/resume_failure_test.go) suspends start → mark → hold → done, makes the resume fail, resumes on a fresh worker, and checks the execution failed with exactly one failed run on the suspended node carrying the refusal, last in the trace, with `done` never run. Four cases: from `resumeRun`, an undecodable checkpoint (version 999) and a webhook resume posting two streams to a one-output wait; from `Resume`, a checkpoint for another trigger and a suspended node the graph does not have. All four failed before the change with `the trace holds 0 failed runs … [start#0/succeeded mark#0/succeeded]`.

Not directly tested: the compile and `prepareGraph` refusals take the same path, but a document that compiled when the run started cannot be made to fail at resume without stubbing the compiler.
