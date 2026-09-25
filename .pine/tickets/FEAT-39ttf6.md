---
id: FEAT-39ttf6
title: Tenant-level notifications to the host
status: todo
priority: medium
labels:
    - saas
    - events
    - notifications
deps:
    - FEAT-wdxkvw
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T10:08:26Z"
---

# Description

A host wants to alert its users when a production run fails or a workflow is deactivated. The per-execution SSE stream needs a ticket for every run.

# Acceptance Criteria
- [ ] For each tenant, one signed outbound endpoint (configured by the operator or the tenant) receives `execution.failed`, optionally `execution.completed`, `workflow.activated` and `workflow.deactivated`.
- [ ] Deliveries retry with backoff, are signed, and appear in a delivery log.
- [ ] Alternatively, a tenant-wide SSE stream.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, with a constraint. `execution.failed`/`execution.completed` exist (internal/events/events.go:24-34); no `workflow.activated`/`deactivated`.
- The broker is in-process, best-effort, keyed per (tenant, execution) (events.go:8-10, :172), and workers can run as separate processes. A durable signed outbound feed needs its own outbox; a tenant-wide SSE stream built on the broker would miss runs on other workers. Outbox tracked in FEAT-wdxkvw (dep).

# Related Files

# Attachments
