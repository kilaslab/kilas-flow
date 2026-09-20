---
id: FEAT-fpqvwx
title: 'Tenant lifecycle: delete a customer and purge everything it owns'
status: doing
priority: high
labels:
    - api
    - storage
    - security
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:31Z"
updated: "2026-09-20T07:42:30Z"
---

## Problem

A SaaS host cannot honour a data-deletion request. Two halves of a purge exist and neither is
called from anywhere, no API operation deletes a tenant, and most of a tenant's data is not
covered by either half.

## Evidence

- `internal/repository/tenant_purge.go:32` (`GORMExecutionStore.PurgeTenant`) deletes
  executions and node runs only. Callers: none outside its test.
- `internal/datastore/isolation.go:36` (`Engine.PurgeTenant`) drops the tenant's catalogue
  rows and physical tables. Callers: none outside tests.
- Not covered by either: `workflows`, `workflow_versions`, `workflow_publish_events`,
  `credentials`, `secret_bindings`, `schedules`, `webhook_bindings`, `webhook_routes`,
  `webhook_deliveries`, `execution_waits`, `users`, `api_keys`, and binary payload
  directories (`internal/binary/binary.go:176` — only per-execution deletion exists).
- `users` and `api_keys` reference `tenants(id)` `ON DELETE RESTRICT`
  (`migrations/*/000003_identity.up.sql`), so deleting the tenant row first fails.
- `webhook_deliveries` has no tenant column at all (`migrations/*/000001_baseline.up.sql`),
  so its rows are unreachable by a tenant purge even in principle.
- `datastore_columns` has no tenant column either; it is only safe today because every read
  is preceded by a tenant-scoped catalogue lookup.
- The operator surface (`internal/api/handlers/admin.go`) exposes list/get/create tenant,
  users and keys — no delete.

## Acceptance criteria

- [ ] `DELETE /api/v1/tenants/{id}` on the operator surface, refused to any other principal,
      answering with what was removed.
- [ ] One orchestrator deletes, in a single documented order: binaries, node runs, executions,
      waits, schedules, webhook deliveries/routes/bindings, secret bindings, credentials,
      workflow versions, publish events, workflows, datastore catalogue + physical tables,
      api keys, users, tenant row.
- [ ] A test seeds two tenants across every table above, purges one, and proves the other's
      rows and cell values are intact and the purged tenant's are gone — including
      `webhook_deliveries`.
- [ ] A retried delete converges (idempotent), and an empty tenant id is refused.
- [ ] `webhook_deliveries` gains a tenant column (new migration, both dialects), and
      `datastore_columns` does too, so no future caller can cross tenants by id alone.

## Out of scope

Soft-delete/restore, export-before-delete, and per-tenant retention policy.
