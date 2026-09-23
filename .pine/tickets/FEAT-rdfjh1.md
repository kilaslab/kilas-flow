---
id: FEAT-rdfjh1
title: Per-tenant quotas, limit overrides and fair scheduling
status: todo
priority: medium
labels:
    - saas
    - tenancy
    - quotas
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

There are no per-tenant quotas (`docs/src/content/docs/concepts/tenancy-and-embedding.md:265-268`), and the worker pool is shared (`execution.max_concurrent`), so one tenant can starve the rest. Data-table limits are deployment-wide, while a SaaS sells plan tiers.

# Acceptance Criteria
- [ ] The tenant API accepts `limits { maxConcurrentExecutions, maxActiveWorkflows, maxDatastores, maxRowsPerDatastore, webhookRatePerMinute }`, enforced with named problem codes.
- [ ] Execution claiming is fair across tenants.
- [ ] Per-tenant metrics are exposed.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
