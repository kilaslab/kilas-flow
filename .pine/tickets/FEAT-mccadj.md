---
id: FEAT-mccadj
title: Host event ingestion with fan-out to active workflows
status: todo
priority: high
labels:
    - saas
    - events
    - triggers
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

A host raises domain events per org/workspace, such as "message received", "contact tagged" or "booking created", and wants every active workflow listening to that event to run.
- Today it must keep one webhook route, secret and lifecycle registration per workflow.
- The alternative is to call `POST /workflows/{id}/run` per workflow, which runs the latest revision and ignores activation (`docs/src/content/docs/reference/api/workflows.md:164-186`).

# Acceptance Criteria
- [ ] `POST /api/v1/events`, authenticated with the tenant key, idempotent on `Idempotency-Key` or `eventId`, with body `{ type, payload, occurredAt? }`.
- [ ] It queues one execution per active workflow whose event trigger lists `type`, running the active revision. The trigger can be a built-in host-event trigger or a pack trigger that opts in.
- [ ] The response is `{ matched: [{workflowId, executionId}], filtered: [...] }`.
- [ ] Deduplication is per (eventId, workflow).
- [ ] A pack can declare the host's event catalogue (type, JSON schema, sample), so the editor offers the events and sample data.
- [ ] The SDK gains `publishEvent`.
- [ ] Documented as the recommended host → workflow path.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
