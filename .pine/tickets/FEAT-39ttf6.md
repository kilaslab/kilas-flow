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
- [x] For each tenant, one signed outbound endpoint (configured by the operator or the tenant) receives `execution.failed`, optionally `execution.completed`, `workflow.activated` and `workflow.deactivated`.
- [x] Deliveries retry with backoff, are signed, and appear in a delivery log.
- [ ] Alternatively, a tenant-wide SSE stream.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, with a constraint. `execution.failed`/`execution.completed` exist (internal/events/events.go:24-34); no `workflow.activated`/`deactivated`.
- The broker is in-process, best-effort, keyed per (tenant, execution) (events.go:8-10, :172), and workers can run as separate processes. A durable signed outbound feed needs its own outbox; a tenant-wide SSE stream built on the broker would miss runs on other workers. Outbox tracked in FEAT-wdxkvw (dep).

## Progress (2026-11-27)

Landed on top of FEAT-wdxkvw's outbox:

- Tenant settings section `notifications` (`url`, `events`; default
  `execution.failed` only), registered in the server. It is written through
  `PATCH /api/v1/tenants/{id}` with the operator credential, so the endpoint
  is operator-configured per tenant.
- `outbound.Router()` routes the outbox from the settings the state change's
  transaction read; an invalid or missing section is no subscription and never
  fails the run.
- Per-tenant secret derived from `notifications.signing_key_env`:
  base64(HMAC-SHA256(key, "kilasflow-notifications:" + tenantId)); no secret
  is stored in a settings document. Without a key nothing is written.
- The dispatcher runs in every worker process; the delivery log is
  `GET /api/v1/notifications/deliveries` (SDK `listNotificationDeliveries`).
- Docs: operate/notifications.md, tenants.md, API contract; CHANGELOG and SDK
  CHANGELOG entries. End-to-end test: settings -> failed run -> signed
  delivery verified with the derived secret; a succeeded run sends nothing.

Not done: the "tenant-wide SSE stream" alternative (the criterion is an
alternative to the delivered endpoint, so left open). No CLI helper prints a
tenant's derived secret yet; the docs give the openssl pipeline.

# Related Files

# Attachments
