---
id: FEAT-rdfjh1
title: Per-tenant quotas, limit overrides and fair scheduling
status: todo
priority: medium
labels:
    - saas
    - tenancy
    - quotas
deps:
    - FEAT-4fp51b
    - FEAT-g33qf6
    - FEAT-2npfgy
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T10:08:26Z"
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

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Stale ref, gap real. The quota line moved to tenancy-and-embedding.md:281-284. Shared pool `execution.max_concurrent` (internal/config/config.go:503); `ClaimNext` is global FIFO (internal/repository/executions.go:528, order ~570); datastore limits deployment-wide (internal/datastore/limits.go:19-40).
- Missing groundwork: `/tenants/{id}` has only GET/DELETE (no update endpoint for `limits`), `tenantModel` has no config columns, only sign-in is throttled (in-memory `LoginLimiter`, internal/api/middleware/loginlimit.go), so `webhookRatePerMinute` needs FEAT-g33qf6, and "per-tenant metrics" needs FEAT-2npfgy. Tenant settings storage and endpoint: FEAT-4fp51b.
- Coordinate with FEAT-mngmn1 (tenant API field) and FEAT-8zgwp6 (per-tenant counters).

# Related Files

# Attachments
