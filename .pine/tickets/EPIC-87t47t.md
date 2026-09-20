---
id: EPIC-87t47t
title: 'Live backend E2E: REST builder, webhooks, datastore CRUD, queue/schedule'
status: done
priority: medium
created: "2026-09-20T03:25:25Z"
updated: "2026-09-20T03:52:37Z"
---

# Description

# Goals

# Description

Prove KilasFlow serves as an HTTP backend builder on the real binary: a workflow
authored over REST becomes a callable HTTP API (webhook in, datastore/SQL out,
Respond out), every webhook delivery path works (auth, response modes, dedupe,
lifecycle), the datastore REST surface and a workflow's datastore node agree on
the same rows, an external PostgreSQL is reachable through a credential, and the
queue/scheduler/wait machinery runs end to end.

# Delivered

Four live suites on the real `bin/kilasflow` (each test boots its own instance
from `e2e/fixtures.ts`), sharing `e2e/fixtures/live-backend.ts`:

- `e2e/tests/live-backend-api.spec.ts` — FEAT-2mth85
- `e2e/tests/live-backend-webhook.spec.ts` — FEAT-41m8dj
- `e2e/tests/live-backend-datastore.spec.ts` — FEAT-ds4e0m
- `e2e/tests/live-backend-queue.spec.ts` — FEAT-adyeh0

Verification: 16/16 green with `KILASFLOW_E2E_POSTGRES_DSN` set (pgvector/pg17);
15 passed + 1 gated skip without a DSN, the skip naming the gate exactly;
`make e2e-skip-budget` 0/24 skipped on the DSN run.

# Findings

No defects were found in the product. Gaps recorded, not invented around:
`GET /workflows/{id}/webhooks` (FEAT-cwmw90) remains the missing read path for a
natively authored webhook address — the API suite reads the binding row from the
instance SQLite meanwhile. The Respond node's durable response copy is not
exposed over REST (Go-covered per BUG-cq4yk3).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `458b038c` (last commit at or before ticket created 2026-09-20)
- _(no file changes detected since ticket creation)_
