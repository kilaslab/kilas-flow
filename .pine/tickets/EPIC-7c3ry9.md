---
id: EPIC-7c3ry9
title: 'Host-SaaS integration readiness: confinement, host events, pack credentials/pickers, tenancy limits'
status: todo
priority: high
labels:
    - saas
    - embedding
    - tenancy
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

KilasFlow is meant to be embedded by SaaS products: one KilasFlow tenant per host organisation or workspace. The host backend holds each tenant's key and mints embed sessions server-side. The host ships its own node packs for its product's actions and domain events, and keeps its own identity, billing and channels.

A 2026-09-23 review walked a real host SaaS application's replacement plan: a multi-tenant customer-messaging and CRM product retiring an in-house workflow engine and user-defined data tables. That review found what KilasFlow still lacks for this model. Every child ticket is written generically, because any SaaS that embeds KilasFlow meets the same gaps.

**Blocking today:**
- the cross-origin embed save/run 403, BUG-b3p8va (EPIC-8rbys7);
- confinement: an embedded user cannot pick credentials or tables the host provisioned (FEAT-r267jj);
- no host-event fan-out into active workflows (FEAT-mccadj);
- packs cannot declare credential types or dynamic pickers (FEAT-n12211, FEAT-pt6ge9).

# Goals

- A host can embed the editor, provision per-organisation tenants, feed its domain events in, and extend the catalogue with its own nodes. None of it needs code changes in KilasFlow.
- Security boundaries hold in a multi-tenant host: confinement, signed triggers with secrets kept in credentials, actor identity, per-tenant limits.
- A SaaS can operate it commercially: quotas and fair scheduling, usage metering, and notifications back to the host.
- Data tables are good enough to replace a host's own user-defined tables: types, constraints, querying, an embeddable grid, file columns and bulk import.

# Priorities

- **P0 (high):** FEAT-r267jj, FEAT-mccadj, FEAT-n12211, FEAT-pt6ge9.
- **P1:** FEAT-hxztwz, BUG-vsmnby and FEAT-0xsc1s are high because they are security- or session-critical. The rest are medium.
- **P2 (low):** data-table depth and the long tail.

The documentation for all of this is tracked in EPIC-62zt4j, including the SaaS integration playbook.

# Related tickets elsewhere

- **EPIC-8rbys7:** BUG-b3p8va (cross-origin embed 403), FEAT-bfrkyk (listen for test event), FEAT-70j6dn (pinned data), FEAT-nq1vsx (MCP Server Trigger), FEAT-a3dwj2 (theme toggle in the dashboard), FEAT-zn5rqy (executions UI), BUG-e7dwpk (datastore name collisions), BUG-rytwy7 (datastore search loop).
- **EPIC-tjnr1z:** the JavaScript Code runtime.
