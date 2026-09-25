---
id: FEAT-mngmn1
title: Allow or deny built-in node types per deployment or tenant
status: todo
priority: medium
labels:
    - saas
    - tenancy
    - node-catalog
deps:
    - FEAT-4fp51b
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T10:08:26Z"
---

# Description

`packs.visible_to` cannot scope built-in node types (`docs/src/content/docs/guides/tenant-scoped-nodes.md:137-139`). A SaaS may need to hide nodes its customers should not use: SQLite (file paths on the server), Code, Postgres/MySQL, or raw HTTP.

# Acceptance Criteria
- [ ] `nodes.deny` and `nodes.allow` config entries, written `type` or `type=tenant`, plus a tenant API field.
- [ ] Hidden types behave like `node.not_available` in the catalogue, at compile, at activation and at claim.
- [ ] Engine-required types (error trigger, sub-workflow trigger) cannot be denied.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid. tenant-scoped-nodes.md:137-138 still says built-ins cannot be scoped; no `nodes.deny`/`nodes.allow` anywhere. Reuse `node.not_available` from internal/workflow/compiler.go:153 (RestrictedCatalog).
- Coordinate with FEAT-rdfjh1: both add a field to the tenant API, which has no update endpoint today (FEAT-4fp51b, recorded as a dep).

# Related Files

# Attachments
