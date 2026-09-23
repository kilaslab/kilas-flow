---
id: BUG-g7ffj1
title: Impossible cron (e.g. '0 0 31 2 *') is accepted and fires every 15 s; zero times shown as 'Jan 1, 07:07:12'
status: todo
priority: high
labels:
    - scheduler
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

robfig/cron returns the zero time when nothing matches within 5 years. The scheduler stores that as next_run_at, which is always due.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-2). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** A Schedule Trigger with an impossible date never fires, and the editor validates the rule.

# Steps to Reproduce

1. Activate any workflow. I used "[ux-ops] schedulable noop". 2. /schedules → New schedule, pick it, cron `0 0 31 2 *` (or POST /api/v1/schedules {"cron":"0 0 30 2 *","active":true}). 3. Wait 45 s and open Executions.

# Expected

Reject a cron with no future occurrence ("never fires"). Never store or show a zero time; show "—".

# Actual

Saving succeeds and the API returns `"nextRunAt":"0001-01-01T00:00:00Z"`. The workflow then runs on every scheduler tick: executions at 01:14:20 and 01:14:35, and on the second repro at 01:15:35, 01:15:50 and 01:16:05 (trigger=schedule). The list shows "Next Jan 1, 07:07:12 AM", which is year 1 rendered with the +07:07:12 LMT offset. After any edit, the zero `lastRunAt` also renders as "Last Jan 1, 07:07:12 AM".

# Acceptance Criteria
- [ ] A cron with no future occurrence is refused with 422 "this cron never fires" in the API and the UI
- [ ] The zero time is never stored (NULL instead) and never shown as a date ("—")
- [ ] A regression test with `0 0 31 2 *` asserts no executions

# Implementation Plan

In Next, return an error when candidate.IsZero() and answer 422 "this cron never fires". Store NULL instead of the zero time for last and next run, and treat year 1 as absent in formatTimestamp.

# Notes

Related (from the audit): none

# Related Files

agents/ux-ops/38-schedule-feb31.png, agents/ux-ops/39-executions.png, agents/ux-ops/schedule-log-excerpt.txt. internal/scheduler/scheduler.go:112-120 `Next` returns `schedule.Next(after)` unchecked; robfig/cron returns the zero time when nothing matches within 5 years. internal/repository/schedules.go:303 claims `next_run_at <= now`, and the zero time always satisfies that. web/src/lib/workflow-editor/execution.ts:175 formats zero times as real dates.

# Attachments
