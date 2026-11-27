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
- [x] An outbox table, written in the same transaction as the state change: execution terminal status, workflow activation and deactivation.
- [x] A dispatcher that claims rows with leases (like `ClaimNext` or the leased poll triggers), delivers each one at least once, retries with capped exponential backoff and jitter, and dead-letters a row after N attempts.
- [x] Deliveries are signed (HMAC over timestamp and body, in the same scheme FEAT-hxztwz adds for inbound) and carry a stable event id, so the receiver can deduplicate.
- [x] A delivery log per tenant: attempts, response status, the next retry. Retention is bounded like execution retention.
- [x] Outbound calls go through `safehttp` and the egress policy.
- [x] Tests cover a crash between commit and delivery, a retry after a 5xx, dead-lettering, and two dispatchers never delivering the same row concurrently.

# Implementation Plan

# Notes

FEAT-39ttf6 depends on this. Its "tenant-wide SSE stream" alternative can read from the outbox too, and then sees runs on every worker. That is not true of the in-process broker.

## Progress (2026-11-27)

Landed on the integration branch feat/epic-sweep-2026-11-27 (worktree branch):

- Migration 000034 `outbound_events` (both dialects): one row per event bound
  for one endpoint, which is also the delivery log (attempts, last status and
  error, next_attempt_at, lease columns). Registered in tenant purge (triggers
  step), the purge docs and the migrate test's post-baseline list.
- `repository.OutboxRouter` + `enqueueOutbound`: rows are written inside the
  transaction of UpdateRuntime (succeeded/failed), settleCrashedExecution,
  SettleExpiredWait (failed), publish (workflow.activated) and Deactivate
  (workflow.deactivated). The router gets the tenant's settings as the same
  transaction read them, so it never reads the DB itself (SQLite holds one
  connection per transaction). A refused publish writes nothing (tested).
- `GORMOutboxStore`: ClaimDue (SKIP LOCKED + predicate-repeating UPDATE, like
  poll cursors), RecordAttempt fenced on lease owner, List per tenant,
  PruneOutbound by created_at.
- `internal/outbound` Dispatcher: one claim per transaction, safehttp client
  plus Policy.CheckURL, no redirects, never unsigned, capped exponential
  backoff with jitter in the upper half, dead after MaxAttempts. Signature is
  the inbound standardWebhooks preset byte for byte (webhook-id,
  webhook-timestamp, `v1,` base64 HMAC-SHA256 over `timestamp.body`); a test
  verifies it with webhook.HMACSchemeVerifier.
- `GET /api/v1/notifications/deliveries` (scope.go: refused to embed sessions
  and workflow-bound keys, workflow:read for tenant keys). Retention
  `notifications.retention` (default 168h, 1h..90d) swept every 15 min.
- Tests: crash after commit and crash mid-delivery (expired lease), retry after
  503 with the same id and bytes, dead-lettering, four concurrent dispatchers
  over 12 rows (each delivered exactly once, -race), loopback port not admitted
  is refused while the admitted one works (no allow_private_networks).

Remains: the PostgreSQL halves of the repository tests ran only as skips
locally (no KILASFLOW_TEST_POSTGRES_DSN); CI runs them.

# Related Files

internal/events/events.go (best-effort in-process broker)
internal/repository/wake.go (workers in separate processes)
internal/safehttp (egress)

# Attachments
