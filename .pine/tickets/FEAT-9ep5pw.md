---
id: FEAT-9ep5pw
title: Embeddable data table grid
status: todo
priority: low
labels:
    - saas
    - datastore
    - embedding
deps:
    - FEAT-5g42rz
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T09:57:34Z"
---

# Description

A datastore embed session has no UI (`embedUrl` is empty; `sdk/src/browser.ts:84-96`). A host that retires its own grid has nothing to embed instead.

# Acceptance Criteria
- [ ] `mountDatastore({session})` renders a branded grid with filters, sort, inline editing (with `datastore:write`), and CSV import and export.
- [ ] It follows the same confinement rules as the editor.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid. Empty `embedUrl` for datastore sessions (internal/api/handlers/embed.go:141-147); SDK throws at sdk/src/browser.ts:84-99; no datastore embed route. Server authz is complete (scope.go:299-341, CSV import/export datastores_csv.go:120, :137). The dashboard page under web/src/routes/(dashboard)/datastores is a starting point.
- **Needs clarification:** "same confinement rules as the editor" doesn't map — a datastore session is already bound to one datastore, while FEAT-r267jj confines workflow documents. The dep on FEAT-r267jj was dropped; re-add it only if the grid should switch between several declared datastores.

# Related Files

# Attachments
