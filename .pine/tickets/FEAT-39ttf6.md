---
id: FEAT-39ttf6
title: Tenant-level notifications to the host
status: todo
priority: medium
labels:
    - saas
    - events
    - notifications
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

A host wants to alert its users when a production run fails or a workflow is deactivated. The per-execution SSE stream needs a ticket for every run.

# Acceptance Criteria
- [ ] For each tenant, one signed outbound endpoint (configured by the operator or the tenant) receives `execution.failed`, optionally `execution.completed`, `workflow.activated` and `workflow.deactivated`.
- [ ] Deliveries retry with backoff, are signed, and appear in a delivery log.
- [ ] Alternatively, a tenant-wide SSE stream.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
