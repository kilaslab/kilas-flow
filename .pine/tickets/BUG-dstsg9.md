---
id: BUG-dstsg9
title: Every node run records 0 ms (startedAt == finishedAt); BUG-6bqh51 still reproduces
status: todo
priority: medium
labels:
    - executions
    - observability
    - regression
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
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
- [ ] A NodeRun's startedAt is the moment the node started (from NodeStartSink or the runner)
- [ ] The inspector and the CLI show a real per-node duration; a 30 s agent run shows about 30 s
- [ ] The same holds for sub-workflow traces

# Implementation Plan

Carry the node's start time from `NodeStartSink`, or record it in the runner, into the NodeRun row.

# Notes

Related tickets: BUG-6bqh51

Related (from the audit): BUG-6bqh51 (done; its finding "…with startedAt equal to finishedAt… per-node durations are always 0 ms" still reproduces)

# Related Files

`$SP/agents/ux-debug/cli/run_h.json`, `cli/trace_h.json`, `a-06-exec-node.png`. internal/engine/service.go:638-655 (`now := time.Now()` … `StartedAt: now, FinishedAt: &now`) and :1356-1366 (same for sub-workflow traces).

# Attachments
