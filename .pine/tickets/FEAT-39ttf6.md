---
id: FEAT-39ttf6
title: Tenant-level notifications to the host
status: done
priority: medium
labels:
    - saas
    - events
    - notifications
deps:
    - FEAT-wdxkvw
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-11-27T09:03:16Z"
---

# Description

A host wants to alert its users when a production run fails or a workflow is deactivated. The per-execution SSE stream needs a ticket for every run.

# Acceptance Criteria
- [x] For each tenant, one signed outbound endpoint (configured by the operator or the tenant) receives `execution.failed`, optionally `execution.completed`, `workflow.activated` and `workflow.deactivated`.
- [x] Deliveries retry with backoff, are signed, and appear in a delivery log.
- [ ] Alternatively, a tenant-wide SSE stream. (Not taken: the signed endpoint above is the chosen form of this requirement. A stream can be a later ticket if a host asks for one.)

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

Config: notifications.signing_key_env, max_attempts (8), retention (168h),
timeout (10s). The log hides the endpoint's path and query from tenant keys.

Not done: the "tenant-wide SSE stream" alternative (the criterion is an
alternative to the delivered endpoint, so left open). No CLI helper prints a
tenant's derived secret yet; the docs give the openssl pipeline.

# Related Files

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-11-27.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (11):
  - `325d6d59` — FEAT-39ttf6: list tenant notifications in the sidebar and point the get-notified guide at them
  - `2b8440ec` — merge: durable outbox and tenant notifications (FEAT-wdxkvw, FEAT-39ttf6)
  - `a7a1f165` — chore(pine): FEAT-wdxkvw and FEAT-39ttf6 review round and verification
  - `ee4ce6c2` — FEAT-39ttf6: bound attempts by notifications.timeout and document the review changes
  - `8487163e` — chore(pine): FEAT-wdxkvw and FEAT-39ttf6 acceptance and progress
  - `fa301de1` — FEAT-39ttf6: document tenant notifications, the signature and the delivery log
  - `f5101218` — FEAT-39ttf6: expose the delivery log in the SDK and regenerate clients and references
  - `b10f6750` — FEAT-39ttf6: deliver tenant notifications to the endpoint in their settings
  - `bced0324` — FEAT-prw1hw: trigger workflows from your app, and get notified of results
  - `bf841006` — chore(pine): EPIC-7c3ry9 records its dependencies and audit notes, and files four infra tickets
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .env.example                      |    8 +-
 .github/assets/editor.png         |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml          |  108 +++-
 .github/workflows/code-corpus.yml |   54 ++
 .gitignore                        |    4 +
 .pine/MEMORY.md                   |    2 +
 .pine/memory/code-node.md         |   30 +-
 .pine/memory/docs.md              |    5 +-
 .pine/memory/e2e.md               |    3 +
 .pine/memory/licensing.md         |   15 +-
 .pine/memory/n8n-reference.md     |   10 +-
 .pine/memory/persistence.md       |    6 +-
 .pine/memory/testing.md           |    4 +-
 .pine/memory/web-editor.md        |    7 +-
 .pine/roadmap.md                  |    8 +-
 .pine/tickets/BUG-0592hz.md       |   62 ++
 .pine/tickets/BUG-0bzsa1.md       |  464 +++++++++++++++
 .pine/tickets/BUG-0grt9g.md       |  427 +++++++++++++
 .pine/tickets/BUG-0xv7bg.md       |   68 +++
 .pine/tickets/BUG-14gp8r.md       |  198 +++++++
 .pine/tickets/BUG-15st2k.md       |  198 +++++++
 .pine/tickets/BUG-1vh6k6.md       |   51 ++
 .pine/tickets/BUG-2d1pdx.md       |   49 ++
 .pine/tickets/BUG-2eryxn.md       |   95 +++
 .pine/tickets/BUG-2mes2k.md       |   83 +++
 .pine/tickets/BUG-2n4rfz.md       |   58 ++
 .pine/tickets/BUG-2vcwjf.md       |  126 ++++
 .pine/tickets/BUG-2xrz6c.md       |   51 ++
 .pine/tickets/BUG-2z8geh.md       |  136 +++++
 .pine/tickets/BUG-3k12ky.md       |  187 ++++++
 .pine/tickets/BUG-3mem9s.md       |  105 ++++
 .pine/tickets/BUG-3qxx0j.md       |   70 +++
 .pine/tickets/BUG-46g75c.md       |  219 +++++++
 .pine/tickets/BUG-49vf3j.md       |  428 ++++++++++++++
 .pine/tickets/BUG-4ch186.md       |   85 +++
 .pine/tickets/BUG-548bk9.md       |  179 ++++++
 .pine/tickets/BUG-56qqgx.md       |   88 +++
 .pine/tickets/BUG-5bgx5c.md       |   81 +++
 .pine/tickets/BUG-5dn8hr.md       |   39 ++
 .pine/tickets/BUG-5fhcx7.md       |   50 ++
 .pine/tickets/BUG-5xkexq.md       |   63 ++
 .pine/tickets/BUG-605n21.md       |  154 +++++
 .pine/tickets/BUG-66fhea.md       |  112 ++++
 .pine/tickets/BUG-6d6wbg.md       |  115 ++++
 .pine/tickets/BUG-6gkd12.md       | 1183 +++++++++++++++++++++++++++++++++++++
 .pine/tickets/BUG-719gaz.md       |  117 ++++
 .pine/tickets/BUG-745mh2.md       |   60 ++
 .pine/tickets/BUG-8xz9t1.md       |  511 ++++++++++++++++
 .pine/tickets/BUG-9296bf.md       |  532 +++++++++++++++++
 .pine/tickets/BUG-9dw5me.md       |   66 +++
 ...
 1665 files changed, 280083 insertions(+), 7953 deletions(-)
```
