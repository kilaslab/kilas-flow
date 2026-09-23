---
id: FEAT-prw1hw
title: 'Events in and out for host apps: triggering workflows from your app, and learning about finished/failed runs'
status: todo
priority: medium
labels:
    - docs
    - events
    - webhooks
parent: EPIC-62zt4j
created: "2026-09-23T02:04:35Z"
updated: "2026-09-23T02:04:35Z"
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
- [ ] A "Trigger workflows from your app" page (webhooks and the run API, with idempotency)
- [ ] A "Get notified of results" page covering SSE, polling, and an Error Trigger or final HTTP node calling the host, with runnable examples
- [ ] The page states the absence of outbound execution webhooks honestly, and links FEAT-39ttf6

# Implementation Plan

Write the page and file a product ticket for tenant-level execution webhooks.

# Notes

Related tickets: FEAT-53pa9a

Related (from the audit): FEAT-53pa9a (todo, server log line, adjacent)

# Related Files

the steps above.

# Attachments
