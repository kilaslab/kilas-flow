---
id: BUG-c3fgw5
title: resume failures outside Runner.Resume still leave the replay all-green
status: todo
priority: medium
labels:
    - engine
    - observability
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-09-23T07:35:37Z"
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
- [ ] `resumeRun`'s compile, checkpoint-decode, and `adaptStoredResumeOutput` failures each record a failed node run for the suspended node
- [ ] `Runner.Resume`'s early returns (trigger mismatch, graph preparation, suspended node missing) each record a failed node run for the suspended node
- [ ] A test asserts the replay shows a failed node run, not an all-green history, for at least one failure from each group

# Related Files

internal/engine/wait_service.go `resumeRun`, ~377-398 (compile, checkpoint decode, `adaptStoredResumeOutput` — each returns `Result{}, err`)
internal/engine/runner.go `Resume`, ~874-895 (nil registry, trigger mismatch, `prepareGraph`, node-missing early returns)
internal/engine/runner.go `failedResume`, ~965 (the pattern these should follow)
