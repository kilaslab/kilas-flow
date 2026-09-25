---
id: FEAT-wdxkvw
title: Durable outbox and dispatcher for signed outbound deliveries
status: todo
priority: medium
labels:
    - saas
    - events
    - reliability
parent: EPIC-7c3ry9
created: "2026-09-25T10:08:12Z"
updated: "2026-09-25T10:08:12Z"
---

# Description

The only event mechanism is `internal/events`, an in-process broker that the package itself describes as best-effort (events.go:8-10). It is keyed per (tenant, execution), and workers can run as separate processes (internal/repository/wake.go). An event raised on one worker never reaches a subscriber on another, and an event is lost if the process dies.

FEAT-39ttf6 (signed tenant notifications with retry and a delivery log) cannot be built on it. It needs a durable, transactional outbox, which the codebase does not have.

Found by the 2026-09-25 audit of this epic's tickets against the code.

# Acceptance Criteria
- [ ] An outbox table, written in the same transaction as the state change: execution terminal status, workflow activation and deactivation.
- [ ] A dispatcher that claims rows with leases (like `ClaimNext` or the leased poll triggers), delivers each one at least once, retries with capped exponential backoff and jitter, and dead-letters a row after N attempts.
- [ ] Deliveries are signed (HMAC over timestamp and body, in the same scheme FEAT-hxztwz adds for inbound) and carry a stable event id, so the receiver can deduplicate.
- [ ] A delivery log per tenant: attempts, response status, the next retry. Retention is bounded like execution retention.
- [ ] Outbound calls go through `safehttp` and the egress policy.
- [ ] Tests cover a crash between commit and delivery, a retry after a 5xx, dead-lettering, and two dispatchers never delivering the same row concurrently.

# Implementation Plan

# Notes

FEAT-39ttf6 depends on this. Its "tenant-wide SSE stream" alternative can read from the outbox too, and then sees runs on every worker. That is not true of the in-process broker.

# Related Files

internal/events/events.go (best-effort in-process broker)
internal/repository/wake.go (workers in separate processes)
internal/safehttp (egress)

# Attachments
