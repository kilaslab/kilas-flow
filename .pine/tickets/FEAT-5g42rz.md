---
id: FEAT-5g42rz
title: Data table querying
status: todo
priority: low
labels:
    - saas
    - datastore
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

Rows are returned in id order only (`docs/src/content/docs/reference/api/datastores.md:237`), and filters are flat any/all.

# Acceptance Criteria
- [ ] Multi-key `sort`, nested AND/OR groups with a depth cap, `select`, and `offset` or `count`.
- [ ] One model shared by the API, SDK, Data table node and datastore tool.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
