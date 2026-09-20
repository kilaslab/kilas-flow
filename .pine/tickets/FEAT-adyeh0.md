---
id: FEAT-adyeh0
title: 'live e2e: queue, schedule firing, wait automation'
status: done
priority: medium
parent: EPIC-87t47t
created: "2026-09-20T03:25:28Z"
updated: "2026-09-20T03:52:32Z"
---

# Description

# Acceptance Criteria
- [ ] Define acceptance criteria

# Implementation Plan

# Notes

# Related Files

# Attachments

## Implemented

`e2e/tests/live-backend-queue.spec.ts`:

- Queue: `POST /workflows/{id}/run` → 202 `{status:'queued'}`, terminal succeeded,
  SSE `execution.completed`, `GET /executions?workflowId=…&limit=2` pages by
  `nextCursor` without overlap, unknown `triggerNodeId` → 422.
- Cancel: a Wait node parks the execution (`waiting`/`resumeUrl`, 15 s poll),
  `POST /executions/{id}/cancel` → 202, terminal `cancelled` + SSE
  `execution.cancelled`.
- Schedule: an activated `kilasflow.schedule` (legacy `cron:'*/20 * * * * *'`)
  writes a `schedules` row at activation and fires a `trigger:'schedule'`
  execution within the 90 s budget on the fixed 15 s tick; the trigger item
  carries `scheduleId` and `timestamp`. REST CRUD beside it: inactive → no
  `nextRunAt`, activate → `nextRunAt`, delete → 204.
- Wait timer: a 2 s Wait resumes by itself; wall and record durations ≥2 s.

## Findings

The 6-field cron was accepted (`cron.SecondOptional`) — no 5-field fallback was
needed. The wait `resume` value is read from `GET /node-types`
(`kilasflow.wait`) at run time, so the document is built from the served
definition.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `458b038c` (last commit at or before ticket created 2026-09-20)
- _(no file changes detected since ticket creation)_
