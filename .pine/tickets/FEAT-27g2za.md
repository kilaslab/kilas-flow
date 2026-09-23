---
id: FEAT-27g2za
title: File columns in data tables
status: todo
priority: low
labels:
    - saas
    - datastore
    - binary
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
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

# Related Files

# Attachments
