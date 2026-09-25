---
id: FEAT-1ge0xc
title: Filter conditions on triggers
status: todo
priority: medium
labels:
    - saas
    - triggers
deps:
    - FEAT-mccadj
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T09:52:33Z"
---

# Description

Workflows on high-volume events, such as every inbound message, need filters by channel, DM or group, and keywords. Today the filter must run inside the workflow, so every event creates an execution.

# Acceptance Criteria
- [ ] Webhook, pack and host-event triggers accept a `filter` (conditions property) evaluated on the trigger item before queueing.
- [ ] A non-match answers `filtered` and creates no execution.
- [ ] Filtered counts appear in metrics.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Partly done (infrastructure). A pre-queue filter hook exists: `webhook.Kind.Accept` (internal/webhook/shape.go:215-225); a reject answers `200 {"filtered":true,"reason"}` with no execution (webhook.go:198-209). Only Telegram uses it (nodes/telegram.go:391). Plug a user `filter` into this hook, likely backed by internal/conditions.
- The "filtered counts in metrics" AC assumes a metrics subsystem that does not exist (no prometheus/expvar/otel); filtering only logs at Info today. That criterion waits on FEAT-2npfgy; the filter itself does not (no dep set).

# Related Files

# Attachments
