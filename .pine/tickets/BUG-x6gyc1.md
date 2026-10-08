---
id: BUG-x6gyc1
title: Timer Wait shown as 'Waiting for approval' with Approve/Reject; cancelled node reads 'Not reached'
status: done
priority: medium
labels:
    - executions
    - ux
    - wait
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-10-08T15:51:12Z"
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
- [x] A timer wait shows "Resumes at <time>", and `approvalUrl` is returned only for approval-mode waits
- [x] The waiting API response includes `resumeAt` for timers
- [x] Cancelling writes a cancelled node run for the suspended node, which survives a reload

# Implementation Plan

Return approvalUrl only for approval-mode waits, add `resumeAt` for timers, and write a cancelled node run for the suspended node.

# Notes

Related tickets: BUG-6bqh51

Related (from the audit): BUG-6bqh51 (done)

# Related Files

`$SP/agents/ux-debug/s-02-live-exec.png`, `s-03-timer-wait-approval-page.png`, `s-04-stopped.png`. internal/api/handlers/executions.go:666-677 (links returned for every waiting execution).

# Attachments

## Fix

**Root cause.** Two separate gaps. (1) `engine.Service.WaitingLinks` built both the resume URL and the approval URL from any active wait's token, whatever its mode. So `GET /executions/{id}` returned `approvalUrl` for a timer wait, and the page showed the violet approval card whenever `approvalUrl` was present. Nothing exposed when a timer resumes. (2) `GORMExecutionStore.Cancel` moved a waiting execution straight to `cancelled` and wrote no trace row. A suspended node has not finished, so the runner never recorded one. The wait node therefore had no run, and the inspector reads a missing run as `skipped`, shown as "Not reached". On the live page the last event about the node was `execution.waiting` (status `waiting`), and `execution.cancelled` carries no node, so the node stayed on "Waiting". A side effect: the wait row was never consumed, so every sweep listed it again.

**Change.**
- `internal/engine/wait_service.go`: `WaitingLinks` returns `engine.ResumeLinks{ResumeURL, ApprovalURL, ResumeAt}`. `ApprovalURL` is set only for `approval` mode. `ResumeAt` (the wait's deadline) is set only for the timer modes (`interval`, `until`). Every mode keeps `ResumeURL`, because a call-resumed wait with a limit is stored as a timer and a call to that URL ends it early.
- `internal/api/handlers/executions.go` and `workflows.go`: the anonymous assertion became a named `waitingLinker` interface. `ExecutionResource` gains `resumeAt`. The web client, the SDK types and the docs were regenerated or updated to match.
- `internal/repository/executions.go`: a new `cancelWaiting` runs one transaction, guarded on `status = waiting` so a race with a resume is harmless. It marks the execution cancelled and consumes the active wait with the new `WaitOutcomeCancelled`, which spends the token and drops the wait from the sweep. It also inserts a `cancelled` node run for the suspended node, with error code `execution.cancelled`, a sequence after every stored row and the node's next free run index.
- `internal/engine/service.go`: `Service.Cancel` publishes `node.failed` (status `cancelled`) for that row before `execution.cancelled`, so an open page converges without a reload.
- Web: `waitingNotice()` in `execution.ts` picks approval, timer or call. The execution page shows "Resumes at <time>" with only a Stop button for a timer. `applyEvents` settles any node still `waiting` or `running` to `cancelled` when `execution.cancelled` arrives, as a fallback. The `/approve/{token}` page shows "Nothing to approve" instead of Approve and Reject for a non-approval mode. Messages were added in en and id.

**Tests.** The repository and vitest tests were written first and seen failing. The engine and handler tests were written before the engine change.
- Go: `TestCancelWaitingRecordsTheSuspendedNodeAsCancelled` (repository: the row survives a fresh `Get`, the wait is consumed and drops out of the sweep, and a second cancel adds no row). `TestWaitingLinksOfferAnApprovalPageOnlyToApprovalWaits` (engine, all four modes). `TestCancellingAWaitingRunMarksItsWaitNodeCancelled` (engine: the node event arrives before `execution.cancelled`, and the row persists). `TestAWaitingTimerExecutionAnswersWithItsResumeTimeAndNoApprovalPage` (handler JSON, plus a compile-time check that `*engine.Service` satisfies `waitingLinker`).
- Vitest: `waitingNotice` (3 cases) and `applyEvents` (cancel settles waiting and running nodes).
- `go test ./internal/engine/ ./internal/repository/ ./internal/api/... ./nodes/`: all ok. `pnpm check`: 0 errors, 0 warnings. `pnpm test`: 59 files, 725 tests passed.

**Playwright self-test.** Real backend on :18091 with a temp SQLite DB and vite dev on :15191. The workflow was Manual → Wait A (5 minutes, `timeInterval`) → No-op.
- The waiting API response had `approvalUrl` absent and `resumeAt` set.
- The page showed "Resumes at Oct 8, 10:54:45 PM", the body "This is a timer wait, not an approval…" and only "Stop waiting run". There was no Open approval page, Approve or Reject.
- After Stop, Wait A read "Cancelled" live and still "Cancelled" after a reload. The execution read Cancelled.
- `/approve/<token>` for a second timer run showed "Nothing to approve" with no buttons.
- Screenshots are in /tmp/kf-x6gyc1/ and are not committed.
