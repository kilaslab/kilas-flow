---
id: FEAT-xr75b9
title: 'Deploy-for-SaaS operations guide: shared Postgres with table_prefix, schema/role isolation, worker sizing, pinning'
status: todo
priority: low
labels:
    - docs
    - operate
    - postgres
parent: EPIC-62zt4j
created: "2026-09-23T02:04:35Z"
updated: "2026-09-23T02:04:35Z"
---

# Description

The deployment material is scattered, and there is no recipe for running KilasFlow inside a host's existing Postgres.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-28). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

**n8n:** n8n documents queue-mode scaling and database setup.

# Steps to Reproduce

1. `operate/deployment.md:150-169` explains that a prefix is not isolation, but never shows how to set it (`KILASFLOW_DATABASE_TABLE_PREFIX=kflow_`). It does not show the dedicated-schema recipe it recommends (role, `search_path` in the DSN), nor how `max_concurrent`, the pool and worker count relate for N tenants.

# Expected

A "Deploy for SaaS" page covering Postgres schema and role SQL, DSN with `search_path`, prefix, api and worker roles, sizing, backups, upgrades with version pinning, and the tenant lifecycle.

# Actual

The recommended isolated topology has no copy-paste recipe.

# Acceptance Criteria
- [ ] An SQL recipe (schema, role, search_path) plus `table_prefix` boots on Postgres 16
- [ ] A sizing example relates `execution.max_concurrent`, the DB pool and the worker count
- [ ] Version pinning and upgrades are on one page
- [ ] The duplicated deployment paragraph and the misleading security heading are fixed
- [ ] Deploying beside a host application: KilasFlow needs its own origin (no base path); which routes browsers and webhook senders need; `server.public_url`; `embed.allowed_origins` for several domains and for dev; CSP and `frame-ancestors`; a separate database and role versus `table_prefix`
- [ ] Multi-tenant capacity: no per-tenant quotas yet (FEAT-rdfjh1); the JS sidecar runs one process per tenant, so size `sidecar.max_processes` for hundreds of tenants

# Implementation Plan

Write the page, largely from existing material.

# Notes

Also from the host-SaaS integration review (D6, D9).

Related (from the audit): none
---

# Related Files

the file and line references above.

# Attachments
