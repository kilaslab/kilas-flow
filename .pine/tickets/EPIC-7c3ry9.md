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

# Sequencing (recorded 2026-09-25)

No blocker outside this epic remains open: BUG-b3p8va, FEAT-2f68r8 and BUG-vsmnby are done. Recorded `deps`:

- FEAT-mccadj → FEAT-1ge0xc, FEAT-7t0xks
- FEAT-n12211 → FEAT-pt6ge9, FEAT-hxztwz
- FEAT-r267jj → FEAT-pt6ge9
- BUG-mzk0xn → FEAT-0xsc1s
- FEAT-z90r5a → FEAT-5g42rz, FEAT-27g2za, FEAT-6r663e
- FEAT-5g42rz → FEAT-9ep5pw

Coordinate, not deps: FEAT-rdfjh1 / FEAT-mngmn1 / FEAT-8zgwp6 all touch the tenant API and per-tenant counters. Parallel branches collide on migration numbers.

Audit (each child carries its own "Audit 2026-09-25" note):
- Tickets to fix before starting: FEAT-pt6ge9 (an HTTP `OptionsLoader` already exists, so the AC shape is wrong), FEAT-9ep5pw (confinement AC unclear), FEAT-gzd32h (declared output undefined), FEAT-4jhtny (the admin surface refuses every session by design).
- Partly done: FEAT-1ge0xc (`Kind.Accept` filter hook), FEAT-6r663e (CSV import exists), FEAT-hxztwz (`secretCapture` exists), FEAT-s3sfx5 / FEAT-t58m89 (server plumbing exists).
- Missing infrastructure, now ticketed:
  - FEAT-4fp51b, tenant settings and update endpoint → FEAT-rdfjh1, FEAT-mngmn1
  - FEAT-g33qf6, per-tenant rate limiter (only sign-in is throttled today) → FEAT-rdfjh1
  - FEAT-2npfgy, metrics endpoint → FEAT-rdfjh1; FEAT-1ge0xc's metrics criterion only
  - FEAT-wdxkvw, durable outbox → FEAT-39ttf6

# Related tickets elsewhere

- **EPIC-8rbys7:** BUG-b3p8va (cross-origin embed 403), FEAT-bfrkyk (listen for test event), FEAT-70j6dn (pinned data), FEAT-nq1vsx (MCP Server Trigger), FEAT-a3dwj2 (theme toggle in the dashboard), FEAT-zn5rqy (executions UI), BUG-e7dwpk (datastore name collisions), BUG-rytwy7 (datastore search loop).
- **EPIC-tjnr1z:** the JavaScript Code runtime.
