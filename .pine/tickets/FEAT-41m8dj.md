---
id: FEAT-41m8dj
title: 'live e2e: webhook handling (auth, response modes, dedupe, lifecycle)'
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

`e2e/tests/live-backend-webhook.spec.ts`:

- Creation matrix: empty path → activate 422; `httpMethod:'BREW'` → 422; imported
  `jwtAuth` → activate 422; a valid workflow deactivates → delivery 404 with no
  new execution.
- Auth: basicAuth → 401 + `WWW-Authenticate: Basic realm="webhook"` without a
  header, `<300` + succeeded with the credential; headerAuth → 401 on a wrong key.
- Response modes: immediate (201 + `{message:'Workflow was started'}` + custom
  header), lastNode (`responseData:'firstEntryJson'`, Respond node removed, the
  Set item verbatim as the body), responseNode (202 with the Respond node's own
  status/body; nodeRun succeeded).
- Dedupe: one delivery identifier → one execution and `{duplicate:true}` with the
  original `executionId`; a distinct identifier queues its own; no header never
  dedupes.
- Routing: wrong method 404, OPTIONS preflight 204 with
  `Access-Control-Allow-Methods: POST` + echoed origin, >1 MiB body 413 with no
  execution queued.

## Findings

No defects. The durable `execution_node_runs.response` copy is not exposed over
REST (matching BUG-cq4yk3's store-level coverage), so the live half asserted here
is `awaitResponse`; the durable half stays Go-covered.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `458b038c` (last commit at or before ticket created 2026-09-20)
- `0cb9eee` — the live-backend e2e suites. The suites were untracked when this ticket was closed, which is why the section above found no changes: this is the commit they landed in, and `git log --grep=EPIC-87t47t` is the durable link.
