---
id: FEAT-3t112f
title: 'New schedule dialog: searchable active-workflow picker, cron description, next-run preview, timezone'
status: todo
priority: low
labels:
    - scheduler
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

The default path of the dialog ends in "That workflow is not activated". Cron gets no preview and no timezone.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-25). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Schedule Trigger offers interval modes (every X minutes, days or weeks) or cron, shows the workflow or instance timezone, and runs only while active.

# Steps to Reproduce

1. /schedules → New schedule.

# Expected

A searchable picker limited to activated workflows (or with those first), a cron description, a next-run preview and a timezone selector.

# Actual

"Workflow" is a native <select> listing every workflow (60+ here), preselected on "[ux-debug] bin binary download (not activated)", so the default path ends in "That workflow is not activated". The cron field shows no human reading ("Every hour at :00") and no "next 3 runs". The page says "Times are evaluated in UTC" and runs are listed in browser-local time with no zone label ("Next Sep 23, 04:00:00 PM" for `0 9 * * 1-5`). The scheduler does support a `TZ=Area/City` prefix (internal/scheduler/scheduler.go:101-103), but the form never mentions it.

# Acceptance Criteria
- [ ] A searchable picker, with active workflows first or only
- [ ] A human cron description and the next 3 runs, from the server's Next()
- [ ] A timezone select that writes the `TZ=` prefix, with times labelled with their zone

# Implementation Plan

Filter or sort the options by active, render a cron description and next runs from the server's Next(), and add a TZ select that writes the TZ= prefix.

# Notes

Related tickets: BUG-esb9sh

Related (from the audit): BUG-esb9sh (done) covered the inactive-workflow refusal, which now works.

# Related Files

agents/ux-ops/36-schedule-new.png, agents/ux-ops/37-schedule-errors.png.

# Attachments
