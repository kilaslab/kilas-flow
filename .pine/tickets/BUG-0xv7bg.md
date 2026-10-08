---
id: BUG-0xv7bg
title: Execution inspector shows only a node's last run; loop bodies read "Not reached" with 0-item edges
status: done
priority: high
labels:
    - executions
    - debugging
    - loops
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-10-08T15:58:00Z"
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
- [x] Every node run is kept, grouped by node, with a "Run N of M" selector
- [x] The default is the latest run that has data, not a pruned or skipped one
- [x] Edge item counts are summed across runs

## Verification (2026-10-08)

Fix commit: `11e8717`.

Reproduced with an imported probe: Manual, then Set `list=[1..5]`, then Split Out, then Loop Over Items (batch size 2), with Tag looping back to the loop. As in the ticket, the trace holds Tag runs of 2, 2 and 1 items, followed by a pruned run with status `skipped`.

- **Every run kept, grouped by node, with a "Run N of M" selector (met).** `nodeRunsByNode` (`web/src/lib/workflow-editor/execution.ts:108`) keeps every run. The selector is in `executions/[id]/+page.svelte` (around `:475`). In the app, selecting Tag shows "Runs of Tag" with previous and next buttons around "Run 3 of 4" (`13-loop-tag-run-selector.png`).
- **Default is the latest run with data (met).** `preferredNodeRun` (`execution.ts:130`) skips `skipped` rows. Test: `execution.test.ts` "does not let a pruned tail row erase a node that ran". In the app, Tag opens on Run 3 (Succeeded, `list: 5`, `tagged: true`), not on the pruned fourth run, and no longer reads "Not reached".
- **Edge counts summed across runs (met).** `edgeItemCounts` (`execution.ts:223`) sums the latest attempt of each sequence. Tests: "sums a loop body's iterations instead of reading the last one" and "counts a retried sequence once, by its latest attempt". In the app: "Loop Over Items loop to Tag main, 5 items", "Tag main to Loop Over Items main, 5 items" and "Loop Over Items done to Done main, 5 items".

Not blocking, cosmetic: M includes the runner's pruned `skipped` tail row, so the selector reads "Run 3 of 4" and run 4 reads skipped. n8n would show 3 runs.

Self-test setup: `make build-web` + `go build ./cmd/kilasflow` from this branch, served on 127.0.0.1:18095 with an isolated SQLite file (`KILASFLOW_DATABASE_DSN=/tmp/kf-verify/data/kilasflow.db`), driven with playwright-cli. Probe workflows were imported through `POST /api/v1/workflows/import`. Screenshots are in `/tmp/kf-verify/` and are not committed.

# Implementation Plan

Keep every node run, group them by nodeId, and add a "Run N of M" selector to the inspector. Default to the latest non-skipped run, and sum the edge counts.

# Notes

Related tickets: BUG-6bqh51

Related (from the audit): BUG-6bqh51 (done, inspector); none for the run selector

# Related Files

`$SP/agents/ux-debug/e2-01-loop-tag-not-reached.png`, `e-03-exec-loop.png`, `cli/run_e2.json`. web/src/lib/workflow-editor/execution.ts:104-115 (`latestNodeRuns` keeps one row per node), web/src/routes/(dashboard)/executions/[id]/+page.svelte:89.

# Attachments
