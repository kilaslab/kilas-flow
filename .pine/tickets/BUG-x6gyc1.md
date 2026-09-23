---
id: BUG-x6gyc1
title: Timer Wait shown as 'Waiting for approval' with Approve/Reject; cancelled node reads 'Not reached'
status: todo
priority: medium
labels:
    - executions
    - ux
    - wait
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

A 4-second timer wait offers an approval page with Approve and Reject buttons. After Stop, the node where the run was cancelled is never marked.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-debug; finding ids: UXD-23). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** A time-interval Wait shows the execution as Waiting "until <time>". Only the approval-style waits (Wait "On webhook call" or "On form submitted", or the send-and-wait operations) expose a resume URL.

# Steps to Reproduce

1. `kilasflow run wf_01a0cbc7-dc5b-734b-8a52-0d195241bfb3` (Wait A amount 4 s). 2. Open the execution while it waits. 3. Open its `approvalUrl`. 4. Click Stop, then reload.

# Expected

A timer wait says "Resumes at <time>" with no approval link. The cancelled node is marked Cancelled.

# Actual

The page shows a violet `Waiting for approval — This run is suspended … Open approval page` card. The approval page shows `Approval requested … Mode: interval` with Approve and Reject buttons and a "Decided by" field. After Stop, Wait A shows `Waiting` beside an execution status of `Cancelled`, and after a reload it reads `Not reached`, so the node where the run was cancelled is never marked.

# Acceptance Criteria
- [ ] A timer wait shows "Resumes at <time>", and `approvalUrl` is returned only for approval-mode waits
- [ ] The waiting API response includes `resumeAt` for timers
- [ ] Cancelling writes a cancelled node run for the suspended node, which survives a reload

# Implementation Plan

Return approvalUrl only for approval-mode waits, add `resumeAt` for timers, and write a cancelled node run for the suspended node.

# Notes

Related tickets: BUG-6bqh51

Related (from the audit): BUG-6bqh51 (done)

# Related Files

`$SP/agents/ux-debug/s-02-live-exec.png`, `s-03-timer-wait-approval-page.png`, `s-04-stopped.png`. internal/api/handlers/executions.go:666-677 (links returned for every waiting execution).

# Attachments
