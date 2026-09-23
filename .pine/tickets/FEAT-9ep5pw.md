---
id: FEAT-9ep5pw
title: Embeddable data table grid
status: todo
priority: low
labels:
    - saas
    - datastore
    - embedding
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

A datastore embed session has no UI (`embedUrl` is empty; `sdk/src/browser.ts:84-96`). A host that retires its own grid has nothing to embed instead.

# Acceptance Criteria
- [ ] `mountDatastore({session})` renders a branded grid with filters, sort, inline editing (with `datastore:write`), and CSV import and export.
- [ ] It follows the same confinement rules as the editor.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
