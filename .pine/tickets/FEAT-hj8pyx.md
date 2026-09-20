---
id: FEAT-hj8pyx
title: Idempotent execution requests and datastore writes
status: todo
priority: high
labels:
    - api
    - engine
    - correctness
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:52Z"
updated: "2026-09-20T05:26:52Z"
---

## Problem

A retried request is a second side effect. A host backend or an agent that retries after a
timeout queues a duplicate execution, and a retried datastore insert writes a second row.
There is no idempotency mechanism anywhere in the tree.

## Evidence

- No `internal/idempotency` package and no `Idempotency-Key` handling anywhere (repo-wide
  grep). The only dedupe that exists is webhook-delivery collapse by sender-supplied delivery
  id (`internal/repository/webhooks.go`, `ClaimDelivery`) and single-use wait tokens
  (`internal/repository/waits.go`, `ResumeWait`).
- `POST /api/v1/workflows/{id}/run` mints a fresh execution per call
  (`internal/repository/executions.go:112` `QueueManualLatest`), so a retry is a new run with
  no link to the first.
- Datastore `InsertRow`/`UpsertRow` (`internal/api/handlers/datastores.go`) have no
  request-level guard either.
- The only concurrency guard on the API today is optimistic concurrency on workflow save
  (`If-Match`/`baseVersionId` → 409), which is a different problem.

## Acceptance criteria

- [ ] `POST /api/v1/workflows/{id}/run` honours an `Idempotency-Key` header: the same key from
      the same tenant within the retention window returns the **first** execution id and does
      not queue a second run.
- [ ] Datastore `POST /datastores/{id}/rows` and `.../rows/upsert` honour the same header.
- [ ] Keys are tenant-scoped and the store is durable (survives restart), not an in-process
      map — a multi-replica deployment must not depend on which replica handled the retry.
- [ ] A conflicting reuse (same key, different request body hash) answers 409 rather than
      silently returning the earlier result.
- [ ] Retention is bounded and configurable, and the table is covered by the tenant purge.
- [ ] Tests cover: replay returns the same execution id; different key queues a second run;
      another tenant cannot replay or observe a key.

## Out of scope

Idempotency for every mutating route. Start with run + datastore writes; add others only when
a host asks.
