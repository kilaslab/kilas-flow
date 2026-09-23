---
id: FEAT-6r663e
title: Bulk import that preserves external ids
status: todo
priority: low
labels:
    - saas
    - datastore
    - import
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

Migrating an existing store needs JSON rows, asynchronous progress for large files, and the old external id kept on each row.

# Acceptance Criteria
- [ ] `POST /datastores/{id}/rows:import` accepts NDJSON or JSON and runs asynchronously with a status resource.
- [ ] `upsertOn` is supported after G18.
- [ ] The response is a per-row report.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
