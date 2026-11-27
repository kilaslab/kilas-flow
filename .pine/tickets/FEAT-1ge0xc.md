---
id: FEAT-1ge0xc
title: Filter conditions on triggers
status: doing
priority: medium
labels:
    - saas
    - triggers
deps:
    - FEAT-mccadj
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-11-27T07:43:48Z"
---

# Description

Workflows on high-volume events, such as every inbound message, need filters by channel, DM or group, and keywords. Today the filter must run inside the workflow, so every event creates an execution.

# Acceptance Criteria
- [x] Webhook, pack and host-event triggers accept a `filter` (conditions property) evaluated on the trigger item before queueing.
- [x] A non-match answers `filtered` and creates no execution.
- [x] Filtered counts appear in metrics.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Partly done (infrastructure). A pre-queue filter hook exists: `webhook.Kind.Accept` (internal/webhook/shape.go:215-225); a reject answers `200 {"filtered":true,"reason"}` with no execution (webhook.go:198-209). Only Telegram uses it (nodes/telegram.go:391). Plug a user `filter` into this hook, likely backed by internal/conditions.
- The "filtered counts in metrics" AC assumes a metrics subsystem that does not exist (no prometheus/expvar/otel); filtering only logs at Info today. That criterion waits on FEAT-2npfgy; the filter itself does not (no dep set).

# Related Files

# Attachments

## Progress

2026-11-27: landed on `feat/epic-sweep-2026-11-27`.

- The parameter key is `triggerFilter` (label "Only run when"), not `filter`: pack triggers declare their own parameters and a service's own "filter" setting would collide. The key is reserved on pack triggers (a pack declaring it fails to load).
- `internal/triggerfilter`: resolves the filter's expressions against the trigger item (`$json`) and evaluates it with `internal/conditions` (the IF/Filter/Switch evaluator). The rich-shape reader moved to `conditions.Parse`; `nodes.richFilter` delegates to it. A filter that cannot be evaluated filters the event out, with the error as the reason.
- Webhook: checked after the trigger kind's own `Accept`, on the shaped item. A miss answers `200 {"filtered": true, "reason"}`, queues nothing and counts `metrics.WebhookFiltered` (the metrics subsystem exists now, so this AC no longer waits on FEAT-2npfgy).
- Host events: the filter is stored on `host_event_subscriptions.trigger_filter` at activation (migration 000032, both dialects) and checked per target in `hostevents.Service.Publish`. A workflow with several listening triggers runs from the first that admits the event; when none does it is reported once under `filtered` with `reason: "filter"` and a `detail`. A filtered workflow is not claimed for the event id.
- Docs: concepts/webhooks.md, "Trigger filters" plus the host-events paragraph.
- Tests: internal/triggerfilter, internal/webhook (filtered answer, no execution, metric), internal/hostevents (per-target filter, multi-trigger fallthrough, extractor), internal/repository (filter round trip on both drivers), internal/nodepack (property present, reserved key).
