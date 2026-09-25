---
id: FEAT-5g42rz
title: Data table querying
status: todo
priority: low
labels:
    - saas
    - datastore
deps:
    - FEAT-z90r5a
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T09:52:33Z"
---

# Description

Rows are returned in id order only (`docs/src/content/docs/reference/api/datastores.md:237`), and filters are flat any/all.

# Acceptance Criteria
- [ ] Multi-key `sort`, nested AND/OR groups with a depth cap, `select`, and `offset` or `count`.
- [ ] One model shared by the API, SDK, Data table node and datastore tool.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid; ref datastores.md:237 accurate. `ORDER BY id` hard-coded at internal/datastore/rows.go:561/587/612; the keyset cursor is id-based (filter.go ~338), so `sort` needs a new cursor format; `Filter` is flat (filter.go:46-56).
- GET list-rows takes filters as positional repeated query params (internal/api/handlers/datastores.go:151-159) — nested groups need a POST query endpoint or JSON param.
- Keep in step: nodes/datastore.go and the datastore tool (nodes/ai.go:1048, :2124).

# Related Files

# Attachments
