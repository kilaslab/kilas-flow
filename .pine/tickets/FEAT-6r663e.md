---
id: FEAT-6r663e
title: Bulk import that preserves external ids
status: todo
priority: low
labels:
    - saas
    - datastore
    - import
deps:
    - FEAT-z90r5a
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T09:52:33Z"
---

# Description

Migrating an existing store needs JSON rows, asynchronous progress for large files, and the old external id kept on each row.

# Acceptance Criteria
- [ ] `POST /datastores/{id}/rows:import` accepts NDJSON or JSON and runs asynchronously with a status resource.
- [ ] `upsertOn` is supported once FEAT-z90r5a lands unique columns.
- [ ] The response is a per-row report.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Partly done. A synchronous CSV import exists at `POST /datastores/{id}/rows/import` (internal/api/handlers/datastores_csv.go:135-146, handler 209-243) with a per-row report `{inserted, skipped, failed[{line,column,reason}]}`; extend it rather than add `rows:import` (repo uses slash paths).
- It inserts row by row outside a transaction after validation (225-233), so a mid-import failure is partial, and it refuses `id`/system columns in the header (~392).
- Integer external ids can already be kept via upsert-by-id (1..2^53-1); string ids need FEAT-z90r5a's unique columns. "G18" (source-review label) corrected in place.

# Related Files

# Attachments
