---
id: BUG-0xv7bg
title: Execution inspector shows only a node's last run; loop bodies read "Not reached" with 0-item edges
status: todo
priority: high
labels:
    - executions
    - debugging
    - loops
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

A node that ran three times inside a loop shows "Not reached" and empty output, because the inspector keeps one row per node and the last run was pruned.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-4). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Nodes that ran several times show "Run 1 of 4" with a run selector in the NDV, and the edges show total item counts across runs.

# Steps to Reproduce

1. `kilasflow run wf_01a0cbc7-dc58-7ac1-b576-a9614a0b7f6a --input '{"list":[1,2,3,4,5]}' --wait` (Split Out → Loop Over Items, batch size 2 → Tag → back to the loop). 2. Open the execution (exec_01a0cbc8-07bb-7069-9128-7f004dffa691) and click `Tag`.

# Expected

A run selector per node (all attempts and run indexes), the default showing the last run that has data, and edge counts summed across runs.

# Actual

The trace has Tag runs 0, 1 and 2 with 2, 2 and 1 items, plus a pruned run 3 with status `skipped`. The replay shows `Tag: Not reached` with `Input {}` and `Output [[]]`, and edges `Loop Over Items loop to Tag main, 0 items` and `Tag main to Loop Over Items main, 0 items`. Every earlier iteration's data is unreachable in the UI. On the loop-with-Wait run, Loop Over Items shows only run 1's input.

# Acceptance Criteria
- [ ] Every node run is kept, grouped by node, with a "Run N of M" selector
- [ ] The default is the latest run that has data, not a pruned or skipped one
- [ ] Edge item counts are summed across runs

# Implementation Plan

Keep every node run, group them by nodeId, and add a "Run N of M" selector to the inspector. Default to the latest non-skipped run, and sum the edge counts.

# Notes

Related tickets: BUG-6bqh51

Related (from the audit): BUG-6bqh51 (done, inspector); none for the run selector

# Related Files

`$SP/agents/ux-debug/e2-01-loop-tag-not-reached.png`, `e-03-exec-loop.png`, `cli/run_e2.json`. web/src/lib/workflow-editor/execution.ts:104-115 (`latestNodeRuns` keeps one row per node), web/src/routes/(dashboard)/executions/[id]/+page.svelte:89.

# Attachments
