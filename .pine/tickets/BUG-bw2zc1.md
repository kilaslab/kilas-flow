---
id: BUG-bw2zc1
title: Wait inside Loop Over Items fails on the 2nd iteration ("suspended node already completed")
status: todo
priority: high
labels:
    - engine
    - regression
    - loops
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

Loop → HTTP → Wait → back to the loop is n8n's standard rate-limit pattern. With a timer Wait, the run fails after the first batch, and the failed execution still shows every node green. This is a regression, or a partial fix, of BUG-ysvmaa.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-5). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Loop → HTTP → Wait → back to the loop is the standard rate-limit pattern. Every batch waits and is processed, and a failure marks the failing node red.

# Steps to Reproduce

1. `kilasflow run wf_01a0cbc2-0b24-7278-8aa1-837b331ee14b --wait` (5 items, batch size 1, Wait 2 s, Set, back to the loop). 2. Also click Execute in the editor. 3. Open the execution.

# Expected

All five batches processed with a 2 s pause each, as n8n does. On any engine failure, the node that failed should be marked.

# Actual

Both runs fail after about 4 s with `suspended node "wait" already completed` (code `execution.failed`). The trace ends at `loop-over-items run 1` dispatching item 2, and no failed node run is recorded. The replay shows the execution as Failed while every node, including Wait, is green "Succeeded". The error names no node, and items 2 to 5 are never processed.

# Acceptance Criteria
- [ ] A Loop Over Items (batch 1, 5 items) with a 2 s timer Wait processes all 5 batches
- [ ] Resume checks completion by run index, or clears the Wait from `Completed` when the loop re-enters its body
- [ ] When a resume fails, a failed node run is recorded for the suspended node, so the replay marks it red
- [ ] A test for timer-mode resume inside a loop sits next to the existing approval-mode one

# Implementation Plan

Key the completion check in Resume by run index, or clear the Wait node from `Completed` when the loop re-enters its body. Add a timer-mode resume-inside-loop test next to the approval-mode one. When a resume fails, write a failed node run for the suspended node.

# Notes

Related tickets: BUG-ysvmaa

Related (from the audit): BUG-ysvmaa (done; "A Wait inside a loop…" is claimed fixed with an approval-token test; with a timer Wait it still fails, so this is a regression or a partial fix)

# Related Files

`$SP/agents/ux-debug/cli/run_e.json`, `e-01-running-canvas.png` (editor banner "Run failed: suspended node "wait" already completed" after the first iteration), `e-03-exec-loop.png`. internal/engine/runner.go:889-891 (Resume refuses when `checkpoint.Completed` already holds the Wait node from iteration 1).

# Attachments
