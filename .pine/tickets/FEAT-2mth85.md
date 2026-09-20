---
id: FEAT-2mth85
title: 'live e2e: backend REST builder (webhook CRUD + Respond)'
status: done
priority: medium
parent: EPIC-87t47t
created: "2026-09-20T03:25:28Z"
updated: "2026-09-20T03:52:31Z"
---

# Description

# Acceptance Criteria
- [ ] Define acceptance criteria

# Implementation Plan

# Notes

# Related Files

# Attachments

## Implemented

`e2e/tests/live-backend-api.spec.ts` (plus the shared `e2e/fixtures/live-backend.ts`):

- A workflow authored over `POST /workflows` (201 + Location), evolved by `PUT`
  (revision 2, `responseHeaders: {x-e2e:'1'}`), activated, and delivered to:
  200 with `echo:'hello backend'`, the header present, execution `trigger:'webhook'`
  succeeded, SSE `execution.completed`, Respond nodeRun succeeded.
- Four minted webhook workflows shaped to webhook → `kilasflow.datastore`
  (insert/get/update/delete) → Respond `firstIncomingItem`, sharing one datastore:
  create(qty 2) → get(qty 2) → update(qty 5) → get(qty 5) → delete → get({}).
  The get execution's datastore nodeRun is asserted redacted:
  `{datastore:{ids,rows}}` and the sku never appears in the trace.
- Lifecycle: stale `baseVersionId` PUT → 409; versions ≥2; restore → revision 3;
  publish v1 → active; publish-events carries `published` + `restored`;
  `DELETE` → 204 then 404; `GET /workflows?limit=2` X-Next-Cursor pages without
  overlap.

Route discovery for natively authored workflows reads `webhook_bindings` from the
instance SQLite (no API exposes it yet — FEAT-cwmw90), instead of inventing a URL.

## Findings

No defects. Two behaviours pinned deliberately: an empty read prunes the Respond
node, so the CRUD Respond node sets `settings.alwaysOutputData` to keep answering
`{}`; and a bare `{{ … }}` string in a parameter is data, not an expression — the
explicit `{mode:'expression'}` marker is what the engine evaluates.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `458b038c` (last commit at or before ticket created 2026-09-20)
- `0cb9eee` — the live-backend e2e suites. The suites were untracked when this ticket was closed, which is why the section above found no changes: this is the commit they landed in, and `git log --grep=EPIC-87t47t` is the durable link.
