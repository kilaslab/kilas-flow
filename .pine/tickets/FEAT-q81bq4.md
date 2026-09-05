---
id: FEAT-q81bq4
title: Reach parity on the time and scheduling node family
status: todo
priority: medium
labels:
    - nodes
    - parity
deps:
    - FEAT-fw0m2q
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T05:01:57Z"
updated: "2026-09-05T05:01:57Z"
---

## Scope

The Schedule trigger is one string. `scheduleTrigger()` in `nodes/webhook.go` declares a single `cron` parameter whose own description reads "Standard five-field cron, evaluated in UTC", and `internal/scheduler/scheduler.go` builds `cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)` with seconds and descriptors deliberately off, then forces `after.UTC()` in `Next`. There is no timezone anywhere in the schedule path, no interval builder, and no way to express "every 30 seconds" or "the 1st of the month at 09:00 Asia/Jakarta". n8n's Schedule Trigger carries typeVersions 1 through 1.3 and seven interval fields — seconds, minutes, hours, days, weeks, months and a custom cron — each with `triggerAtDayOfMonth`, `triggerAtDay`, `triggerAtHour` and `triggerAtMinute`, and a rule may hold several intervals at once.

The importer therefore throws almost everything away. `scheduleToKilas` in `internal/interop/n8n/parameters.go` walks `rule.interval` and returns on the first entry that carries a non-empty `expression`; every other interval in that rule is discarded without a word, and a rule built entirely in n8n's visual builder becomes hourly `0 * * * *` with one generic notice. Worse, an imported Schedule Trigger never fires at all: activation syncs webhook bindings and only webhook bindings — `syncWebhookBindings` in `internal/repository/webhooks.go`, called from `Activate` in `internal/repository/workflows.go` — and there is no schedule equivalent. A schedule row exists only when somebody calls `POST /api/v1/schedules` by hand (`internal/api/handlers/schedules.go`). The trigger item is wrong too: `Tick` in the scheduler queues `{"scheduledAt", "scheduleId"}`, while n8n emits `{timestamp, "Readable date", "Readable time", "Day of week", Year, Month, "Day of month", Hour, Minute, Second, Timezone}`, and corpus workflows read those fields.

There is no DateTime node and no Wait node. The expression engine has no time functions either — the roots in `internal/expression/expression.go` are `$json`, `$input`, `$node`, `$env`, `$execution` and `$itemIndex`, with no `$now` and no `$today` until p1-10 lands them.

This ticket closes the family: Schedule Trigger at n8n's parity including timezone and the trigger item shape, a DateTime node, and a bounded Wait node. The Wait node's durable forms — resume on webhook, resume on form, waits that outlive a process — are p8-4 and stay there; what lands here is the in-execution wait.

## Acceptance criteria

- [ ] Schedule Trigger accepts n8n's rule shape: several intervals per node, each one of seconds / minutes / hours / days / weeks / months / custom cron, with `triggerAtDayOfMonth`, `triggerAtDay`, `triggerAtHour` and `triggerAtMinute`, and rejects out-of-range intervals the way n8n 1.3 does.
- [ ] A workflow carries a timezone, the scheduler computes the next run in that zone, and a schedule crossing a daylight-saving boundary fires once and only once — covered by a test with a fixed clock.
- [ ] Activating a workflow that contains a Schedule Trigger creates its schedule rows, deactivating removes them, and editing the trigger updates them, without anybody calling `/api/v1/schedules`.
- [ ] The scheduled trigger item carries n8n's field set, so a corpus expression reading `$json['Day of week']` resolves.
- [ ] `scheduleToKilas` maps every interval in a rule, not only the first one with a cron expression, and any interval it cannot represent produces a named diagnostic.
- [ ] A DateTime node is registered covering n8n's operations — current date, add/subtract, format, round, extract, and comparison between two dates — with an explicit timezone per operation.
- [ ] A Wait node is registered for "after time interval" and "at specified time", bounded by the execution timeout, and refuses the resume-on-webhook and resume-on-form modes with a diagnostic naming p8-4.
- [ ] The p0 corpus counts improve and the new figure is recorded in the ticket's work evidence.

## Implementation Plan

Start at the storage end, because the trigger's shape is decided there. `scheduleModel` in `internal/repository/models.go` has one `Cron` column and one `NodeID`, and `repository.Schedule` mirrors it, so a node with three intervals has nowhere to live. Two options: add a `Rule` JSON column and teach `ClaimDue` to compute the earliest next run across a rule, or keep one row per interval and widen the row's identity to `(workflow, node, intervalIndex)`. Take the second — `ClaimDue` is already a correct transactional claim that advances one `NextRunAt` per row, and one row per interval keeps it untouched, at the cost of a fan-out on sync. Either way this is a schema change, so it wants p6-1's versioned migrations rather than AutoMigrate.

Timezone next. `robfig/cron` v3.0.1 already parses a `TZ=` or `CRON_TZ=` prefix on the spec string (`parser.go`), so a per-schedule zone can be carried without changing the parser — store the zone on the schedule row, prefix the spec when calling `Next`, and validate the zone name against `time.LoadLocation` at save time. Loosen the parser to allow seconds only if the seconds interval is actually implemented; otherwise map "every N seconds" to a diagnostic rather than silently rounding it up to a minute, which is the failure mode a user notices last.

Then wire activation. Add a schedule extractor beside `store.webhooks` in `internal/repository/workflows.go` and a `syncSchedules` next to `syncWebhookBindings`, so a Schedule Trigger in an activated document becomes rows in the same transaction that pins the version. This is the single change that makes an imported schedule-driven workflow work at all, and it is easy to leave out because the node already exists and the editor already renders it.

The trigger item is a small change in the wrong place. `Tick` in `internal/scheduler/scheduler.go` marshals the payload and `executeSchedule` in `nodes/webhook.go` emits `request.Input` verbatim, so build n8n's field set in `Tick` where the fire time and the zone are both known, rather than in the executor where the zone is not.

DateTime and Wait go in a new `nodes/datetime.go` and `nodes/wait.go`. DateTime overlaps p1-10's `$now`/`$today` — the node must not grow a second date implementation, so put the parsing and formatting in one package both can call. Wait's bounded modes are a `time.Sleep` against the node context, which means a wait longer than the execution timeout fails as a timeout rather than hanging; enforce a documented ceiling and say what it is in the node description instead of letting the user discover it.

## References

- Plan: `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, section p4 (time and control); p1-10 expression engine v2; p6-1 versioned migrations; p8-4 human approval / Wait with durable resume.
- PRD `gflow-prd-v1.md` §24 Native V1 Nodes, §33 Expressions, §35 Workflow Execution Model.
- Verified in this repository: `nodes/webhook.go` (`scheduleTrigger`, `executeSchedule`, `validateScheduleConfiguration`), `internal/scheduler/scheduler.go` (`parser`, `Next`, `Validate`, `Tick`), `internal/repository/models.go` (`scheduleModel`), `internal/repository/schedules.go` (`Schedule`, `ClaimDue`), `internal/repository/workflows.go` (`Activate` calls `syncWebhookBindings` and nothing else), `internal/api/handlers/schedules.go`, `internal/interop/n8n/parameters.go` (`scheduleToKilas`, `scheduleToN8N`), `internal/expression/expression.go` (root allowlist).
- n8n 2.34.0 reference checkout (read-only, outside this repo): `/Users/izzadev/projects/mitrachat/n8n/packages/nodes-base/nodes/Schedule/ScheduleTrigger.node.ts` (`version: [1, 1.1, 1.2, 1.3]`, the emitted item's field names) and `.../Schedule/GenericFunctions.ts` (`withIntervalDefaults` defaults, `validateInterval` ranges).
- `github.com/robfig/cron/v3@v3.0.1` `parser.go`: `TZ=` / `CRON_TZ=` spec prefix.
