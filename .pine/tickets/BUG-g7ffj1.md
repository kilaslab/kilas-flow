---
id: BUG-g7ffj1
title: Impossible cron (e.g. '0 0 31 2 *') is accepted and fires every 15 s; zero times shown as 'Jan 1, 07:07:12'
status: doing
priority: high
labels:
    - scheduler
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T04:34:04Z"
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
- [x] A cron with no future occurrence is refused with 422 "this cron never fires" in the API and the UI
- [x] The zero time is never stored (NULL instead) and never shown as a date ("—")
- [x] A regression test with `0 0 31 2 *` asserts no executions

# Progress

`scheduler.Next` now refuses a zero candidate (robfig/cron's answer for a date that never exists) with `ErrNeverFires`, "this cron never fires" — on both the plain and the DST-guarded path, now one shared check. `scheduler.Validate` delegates to `Next(expr, time.Now().UTC())`, so an inactive schedule's impossible cron is refused too, not just an active one's. `ErrNeverFires` lives in `internal/repository` (not `scheduler`, which imports `repository`) so `ClaimDue` can recognise it without an import cycle; `scheduler.ErrNeverFires` is an alias of the same value for callers in that package.

`ClaimDue` now repairs rather than fires a row whose stored due time is zero, or whose cron turns out to have no occurrence after it (`next()` returns `ErrNeverFires`): `active=false, next_run_at=NULL`, skip, continue — no migration needed, and a bad row no longer aborts the whole claim transaction for every other schedule.

`Workflows.problem` maps a `syncSchedules` `ErrNeverFires` (hit when activating a workflow whose Schedule Trigger node carries an impossible cron) to 422 instead of the previous 500.

`formatTimestamp` (web) treats a year-1 timestamp as absent ("—"), and the schedules list row's tooltip now goes through `formatTimestamp` instead of concatenating the raw ISO strings.

Tests: `internal/scheduler/scheduler_test.go` — `TestNextRefusesACronWithNoFutureOccurrence` (both Next paths), `TestValidateRejectsUnusableExpressions` extended with `0 0 31 2 *`, `TestTickRepairsAnImpossibleCronRatherThanFiringItAndDoesNotBlockOthers` (a zero-due-time row and a non-zero-due-time row for the same impossible cron, plus a healthy schedule that must still fire). `internal/api/schedules_test.go` — `TestCreatingAScheduleWithAnImpossibleCronIsRefused` (POST /api/v1/schedules, active and inactive, both 422; RED reproduced the ticket's exact `"nextRunAt":"0001-01-01T00:00:00Z"`). `web/src/lib/workflow-editor/execution.test.ts` — `describe('formatTimestamp', ...)` (RED reproduced the ticket's exact `"Jan 1, 07:07:12 AM"`).

All green: `go build ./...`, `go vet ./...`, `go test ./internal/scheduler/... ./internal/repository/... ./internal/api/...`, `cd web && pnpm check && pnpm test` (627 tests).

# Implementation Plan

In Next, return an error when candidate.IsZero() and answer 422 "this cron never fires". Store NULL instead of the zero time for last and next run, and treat year 1 as absent in formatTimestamp.

# Notes

Related (from the audit): none

# Related Files

agents/ux-ops/38-schedule-feb31.png, agents/ux-ops/39-executions.png, agents/ux-ops/schedule-log-excerpt.txt. internal/scheduler/scheduler.go:112-120 `Next` returns `schedule.Next(after)` unchecked; robfig/cron returns the zero time when nothing matches within 5 years. internal/repository/schedules.go:303 claims `next_run_at <= now`, and the zero time always satisfies that. web/src/lib/workflow-editor/execution.ts:175 formats zero times as real dates.

# Attachments
