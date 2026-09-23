---
id: FEAT-mngmn1
title: Allow or deny built-in node types per deployment or tenant
status: todo
priority: medium
labels:
    - saas
    - tenancy
    - node-catalog
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
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

# Related Files

# Attachments
