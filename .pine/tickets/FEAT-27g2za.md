---
id: FEAT-27g2za
title: File columns in data tables
status: todo
priority: low
labels:
    - saas
    - datastore
    - binary
deps:
    - FEAT-z90r5a
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T09:52:33Z"
---

# Description

Hosts store attachments per row.

# Acceptance Criteria
- [ ] A `file` column type holding per-tenant binary-store references.
- [ ] Upload and download endpoints for backend keys and datastore sessions.
- [ ] Size and MIME limits.
- [ ] Files are purged when the row, table or tenant is deleted.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, with a constraint. The binary store is keyed (tenant, execution) (internal/binary/binary.go:63-71, 192-193) and pruned with executions/retention — a file column needs a new non-execution scope that survives pruning, plus row/table delete hooks. Tenant purge already clears the tenant dir (internal/tenantpurge/purge.go:49-52).
- Binary storage is optional per deployment; only a global `binary.max_bytes` (config.go:648), no MIME allow-list.

# Related Files

# Attachments
