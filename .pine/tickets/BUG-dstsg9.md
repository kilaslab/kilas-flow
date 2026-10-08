---
id: BUG-dstsg9
title: Every node run records 0 ms (startedAt == finishedAt); BUG-6bqh51 still reproduces
status: done
priority: medium
labels:
    - executions
    - observability
    - regression
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-10-08T15:44:31Z"
---

# Description

Per-node timing is how a slow node gets found. The SSE events carry the real start time, but the NodeRun row overwrites it.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-7). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Each node shows its execution time ("Executed in 1.2 s"), and the logs panel shows a timeline, which is how slow nodes are found.

# Steps to Reproduce

1. Run any workflow, for example the AI agent run exec_01a0cbc4-134f-791c-86eb-4b354917f44f, which took 30 s in the agent node, or the Go Code node, which takes about 1 s to compile. 2. `kilasflow exec get <id>`, or click a node in the replay.

# Expected

A node run's startedAt set when the node starts, and a per-node duration in the inspector and the CLI.

# Actual

Every `nodeRuns[].startedAt` equals its `finishedAt` (for example `"startedAt":"…32.080408Z","finishedAt":"…32.080408Z"`). The inspector footer reads `Sep 23, 07:58:18 AM · 0 ms` for every node, including a 30 s agent. The SSE events show the real gap (two-urls `node.started` 08.237 → `node.completed` 09.205), so the timing exists but is thrown away.

# Acceptance Criteria
- [x] A NodeRun's startedAt is the moment the node started (from NodeStartSink or the runner)
- [x] The inspector and the CLI show a real per-node duration; a 30 s agent run shows about 30 s
- [x] The same holds for sub-workflow traces

# Implementation Plan

Carry the node's start time from `NodeStartSink`, or record it in the runner, into the NodeRun row.

# Notes

Related tickets: BUG-6bqh51

Related (from the audit): BUG-6bqh51 (done; its finding "…with startedAt equal to finishedAt… per-node durations are always 0 ms" still reproduces)

# Related Files

`$SP/agents/ux-debug/cli/run_h.json`, `cli/trace_h.json`, `a-06-exec-node.png`. internal/engine/service.go:638-655 (`now := time.Now()` … `StartedAt: now, FinishedAt: &now`) and :1356-1366 (same for sub-workflow traces).

# Attachments

## Fix (2026-10-08)

Root cause: the runner never timed a node. `engine.NodeRun` had no timestamps, so every place the service turned a run into a stored row (`traceRows` for the live writer and the terminal flush, `persistChild` for sub-workflow traces, `persistTraceRow` for a suspended segment) did `now := time.Now()` and wrote `StartedAt: now, FinishedAt: &now`: the moment the row was rendered, after the node had finished. BUG-6bqh51 made rows durable as each node finishes (live progress), which moved the stamp closer to the real finish but still gave start == finish, so it never held.

Change: the runner stamps the row. `engine.NodeRun` gains `StartedAt`/`FinishedAt`. `runNode` reads the clock as the node starts (just before `NodeStartSink`) and every row the invocation writes carries it; each failed retry attempt is timed from its own attempt start; a per-item run carries the start in `perItemProgress` (a resumed per-item run is timed from the resume). `runState.record`, the one path every row takes, stamps the finish, and a row with no start (skipped branch, untaken arm, disabled pass-through) starts when it finishes. The clock is `Request.Now`, which the service sets to `Service.clock`, so tests move time through `SetClockForTest` instead of asserting wall time. The three service row builders now use `service.runTimes(run)`, which reads the runner's stamps and falls back to the render time only for a row the runner did not stamp (a resume refused before the graph). The inspector already renders `finishedAt - startedAt` per run (`executionDurationMs(selectedRun)`) and `kilasflow exec get` returns the rows' `startedAt`/`finishedAt`, so both now show the real duration with no client change.

Not done here: the human (non-JSON) view of `kilasflow exec get` prints no per-node table at all (it never did), so a per-node duration column there would be a new feature; the API node-run shape has no `durationMs` field (derive it from the two timestamps).

Tests (internal/engine/node_timing_test.go, service clock owned by the test, no wall-time assertions):
- `TestANodeRunRecordsWhenItStartedAndWhenItFinished`: a step that takes 30 s of the test clock is stored with startedAt = the moment it was called and a 30 s duration; the trigger reads 0 and finishes before the step starts. Failed before the fix with 0s.
- `TestASubWorkflowTraceRecordsEachNodesOwnDuration`: the child's slow step reads 30 s in the child's trace, and the parent's Execute Workflow row at least 30 s. Failed before the fix with 0s on both.
- `TestARetriedNodeTimesEachAttemptAndTheWholeNode`: a failed first attempt reads its own 5 s; the answering row reads the node's whole 10 s.

`go test ./internal/engine/ -count=1` ok; `go vet ./internal/engine/` clean; `go build ./...` ok.
