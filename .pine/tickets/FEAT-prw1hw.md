---
id: FEAT-prw1hw
title: 'Events in and out for host apps: triggering workflows from your app, and learning about finished/failed runs'
status: done
priority: medium
labels:
    - docs
    - events
    - webhooks
parent: EPIC-62zt4j
created: "2026-09-23T02:04:35Z"
updated: "2026-11-27T08:51:29Z"
---

# Description

A host backend has no documented way to learn that an execution finished or failed, and the docs don't say that outbound events don't exist.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-18). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

**n8n:** n8n has Error Trigger workflows, and on Enterprise it has Log Streaming to webhook destinations. Embedders commonly need one of the two.

# Steps to Reproduce

1. `/api/openapi.json` has no subscription or callback operation. The only event stream is per execution (`GET /executions/{id}/events`, which needs a stream ticket in the browser).
2. The editor's postMessage events (`ready`, `workflow-saved`, `execution-started`, `execution-finished`) reach only the page that mounted the editor, not the host backend, and they do not cover webhook- or schedule-triggered runs.
3. The docs have no "events out" page. `guides/embedding.md:206-209` suggests polling the newest execution.

# Expected

A page that states plainly that there are no outbound webhooks, and gives the supported patterns: an Error Trigger workflow that calls the host, a final HTTP Request node, polling `list-executions` by status, and a per-execution SSE stream. Record the gap on the roadmap.

# Actual

An integrator who wants "notify my app when a customer's automation fails" has no documented pattern.

# Acceptance Criteria
- [x] A "Trigger workflows from your app" page (webhooks and the run API, with idempotency)
  - Evidence: `docs/src/content/docs/guides/trigger-workflows.md` covers host events (`POST /api/v1/events`), webhooks (`GET /workflows/{id}/webhooks`, `responseMode`, `deliveryIdHeader`) and `POST /workflows/{id}/run`, with a retry table and the per-tenant limits.
- [x] A "Get notified of results" page covering SSE, polling, and an Error Trigger or final HTTP node calling the host, with runnable examples
  - Evidence: `docs/src/content/docs/guides/get-notified.md`. The error workflow (`settings.errorWorkflow` + `kilasflow.errorTrigger` + HTTP Request), the polling function, the Node SSE function, the host-event publish and the respond-with-`$execution.id` webhook were each run against a binary built from this branch (error workflow delivered `{executionId, workflowId, workflowName, node, message}` to a local receiver).
- [x] The page states the absence of outbound execution webhooks honestly, and links FEAT-39ttf6
  - Evidence: a caution box opens `get-notified.md`, naming FEAT-39ttf6 and its dependency FEAT-wdxkvw; the same fact is in "What is not there yet" in `start/what-kilasflow-is.md`.

# Implementation Plan

Write the page and file a product ticket for tenant-level execution webhooks.

# Notes

Related tickets: FEAT-53pa9a

Related (from the audit): FEAT-53pa9a (todo, server log line, adjacent)

# Related Files

the steps above.

# Attachments

## Progress (2026-11-27)

- Both pages are written from the code, not from the audit. Things the code showed that the ticket did not say:
  - n8n's `settings.errorWorkflow` is implemented (`runErrorWorkflow`, `internal/engine/service.go`). It starts the named workflow's latest revision from `kilasflow.errorTrigger` with n8n's item shape, for any failed run, and annotates the failed run's `error` with `errorWorkflowId` and `errorExecutionId` (or `errorWorkflowError`). It never fires for a cancelled run or for a run the queue settles as crashed. The editor has no field for the setting.
  - A webhook in `responseNode` mode can answer with `{{ $execution.id }}` and keep running, which replaces "list executions and take the newest" (`guides/embedding.md` step 5 now says so).
  - A repeated webhook delivery id answers `{executionId, status, duplicate: true}`, not the first run's body.
  - The HTTP Request node refuses private and loopback targets unless the operator lists them in `outbound.allowed_private_endpoints`, which matters for any error workflow or final node that calls a host on a private address.
- The product ticket for tenant-level notifications already exists (FEAT-39ttf6, dependent on FEAT-wdxkvw), so none was filed.
- Not done: nothing in the new examples is run in CI. They were run by hand against a built binary.

## Work Evidence

Closed by `pine close --evidence` on 2026-11-27.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (5):
  - `10693549` — merge: integrator docs IA, events in and out, SaaS playbook (FEAT-1mxtsn, FEAT-prw1hw, FEAT-n010f0)
  - `b9cad511` — FEAT-prw1hw, FEAT-n010f0: state exactly what a run-scoped key can read, and what a missing error workflow reports
  - `7dd4beda` — chore(pine): progress notes and acceptance evidence for FEAT-1mxtsn, FEAT-prw1hw, FEAT-n010f0
  - `bced0324` — FEAT-prw1hw: trigger workflows from your app, and get notified of results
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
 1646 files changed, 276187 insertions(+), 7906 deletions(-)
```
