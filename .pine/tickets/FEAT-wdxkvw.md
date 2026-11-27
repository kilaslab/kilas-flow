---
id: FEAT-wdxkvw
title: Durable outbox and dispatcher for signed outbound deliveries
status: done
priority: medium
labels:
    - saas
    - events
    - reliability
parent: EPIC-7c3ry9
created: "2026-09-25T10:08:12Z"
updated: "2026-11-27T09:03:16Z"
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

After one review round: the lease owner is a per-claim token; Deactivate
re-reads under the row lock (one workflow.deactivated per transition, and
Delete of a live workflow writes one); the log shows the endpoint as scheme
and host and egress refusals without the address; last_error is cut on a
UTF-8 boundary; four dispatch lanes per process with notifications.timeout
(default 10s) bounding an attempt; batched pruning; outbox rows are purged in
the definitions step, after runs and workflows.

Verified: make test, go vet, generator checks, SDK tests, docs build; the
PostgreSQL halves (repository, database, tenantpurge, dispatcher race) passed
against a throwaway pg17 container.

Remains (decide before release): the signature does not cover webhook-id
(kept identical to the inbound preset; docs say deduplicate on the signed
body id), and off-the-shelf Standard Webhooks libraries will not verify these
deliveries as they are.

# Related Files

internal/events/events.go (best-effort in-process broker)
internal/repository/wake.go (workers in separate processes)
internal/safehttp (egress)

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-11-27.

- Base: `57f67797` (last commit at or before ticket created 2026-09-25)
- Commits (10):
  - `2b8440ec` — merge: durable outbox and tenant notifications (FEAT-wdxkvw, FEAT-39ttf6)
  - `a7a1f165` — chore(pine): FEAT-wdxkvw and FEAT-39ttf6 review round and verification
  - `612a2a14` — FEAT-wdxkvw: fence by claim token, guard deactivation, redact the log and purge outbox rows last
  - `8487163e` — chore(pine): FEAT-wdxkvw and FEAT-39ttf6 acceptance and progress
  - `d613a8cc` — FEAT-wdxkvw: cover the wait-expiry event, the PostgreSQL dispatcher race and the contract row
  - `93d9d95e` — FEAT-wdxkvw: route by the transaction's own settings read and serve the delivery log
  - `dc78b26a` — FEAT-wdxkvw: deliver outbox rows with a leased, signed dispatcher
  - `ebed3d46` — FEAT-wdxkvw: write outbox rows in the state change's own transaction
  - `bced0324` — FEAT-prw1hw: trigger workflows from your app, and get notified of results
  - `bf841006` — chore(pine): EPIC-7c3ry9 records its dependencies and audit notes, and files four infra tickets
- Files changed (base → working tree):

```
 .env.example                  |    8 +-
 .github/workflows/ci.yml      |   83 ++-
 .pine/MEMORY.md               |    2 +
 .pine/memory/docs.md          |    5 +-
 .pine/memory/e2e.md           |    3 +
 .pine/memory/licensing.md     |    3 +-
 .pine/memory/n8n-reference.md |    6 +-
 .pine/memory/persistence.md   |    6 +-
 .pine/memory/testing.md       |    4 +-
 .pine/memory/web-editor.md    |    7 +-
 .pine/tickets/BUG-0bzsa1.md   |  464 +++++++++++++++
 .pine/tickets/BUG-0grt9g.md   |  427 ++++++++++++++
 .pine/tickets/BUG-0xv7bg.md   |   24 +-
 .pine/tickets/BUG-15st2k.md   |   50 +-
 .pine/tickets/BUG-1vh6k6.md   |   51 ++
 .pine/tickets/BUG-2d1pdx.md   |   49 ++
 .pine/tickets/BUG-2mes2k.md   |   39 +-
 .pine/tickets/BUG-2n4rfz.md   |   15 +-
 .pine/tickets/BUG-2xrz6c.md   |   18 +-
 .pine/tickets/BUG-3k12ky.md   |   34 +-
 .pine/tickets/BUG-3qxx0j.md   |   23 +-
 .pine/tickets/BUG-49vf3j.md   |  428 ++++++++++++++
 .pine/tickets/BUG-4ch186.md   |   59 +-
 .pine/tickets/BUG-56qqgx.md   |   44 +-
 .pine/tickets/BUG-5bgx5c.md   |   32 +-
 .pine/tickets/BUG-5dn8hr.md   |   39 ++
 .pine/tickets/BUG-5fhcx7.md   |   32 +-
 .pine/tickets/BUG-5xkexq.md   |   63 ++
 .pine/tickets/BUG-605n21.md   |   35 +-
 .pine/tickets/BUG-66fhea.md   |   27 +-
 .pine/tickets/BUG-6d6wbg.md   |   25 +-
 .pine/tickets/BUG-6gkd12.md   | 1088 +++++++++++++++++++++++++++++++++-
 .pine/tickets/BUG-719gaz.md   |   41 +-
 .pine/tickets/BUG-745mh2.md   |   60 ++
 .pine/tickets/BUG-8xz9t1.md   |  511 ++++++++++++++++
 .pine/tickets/BUG-9296bf.md   |  489 +++++++++++++++-
 .pine/tickets/BUG-9dw5me.md   |   21 +-
 .pine/tickets/BUG-9hf565.md   |   40 ++
 .pine/tickets/BUG-9pmv8y.md   | 1097 +++++++++++++++++++++++++++++++++-
 .pine/tickets/BUG-9wpy6m.md   |   59 ++
 .pine/tickets/BUG-a7p6c8.md   |  487 ++++++++++++++++
 .pine/tickets/BUG-argnka.md   |   34 ++
 .pine/tickets/BUG-asdh5q.md   |  570 +++++++++++++++++-
 .pine/tickets/BUG-b4cb1c.md   | 1145 +++++++++++++++++++++++++++++++++++-
 .pine/tickets/BUG-b8bwhw.md   | 1293 ++++++++++++++++++++++++++++++++++++++++-
 .pine/tickets/BUG-bcahaj.md   |   23 +-
 .pine/tickets/BUG-c3fgw5.md   |   23 +-
 .pine/tickets/BUG-c73h98.md   |   17 +-
 .pine/tickets/BUG-c7s5ss.md   |   20 +-
 .pine/tickets/BUG-cmnsfz.md   |  457 +++++++++++++++
 ...
 1346 files changed, 194722 insertions(+), 7520 deletions(-)
```
