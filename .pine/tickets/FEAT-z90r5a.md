---
id: FEAT-z90r5a
title: Data table column types and constraints
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

Only `string`, `number`, `boolean` and `date` exist, and there are no unique constraints (`docs/src/content/docs/concepts/datastore-concurrency.md:51-60`), so "upsert by email" is not atomic. A host replacing its own data tables loses enum, integer versus decimal, datetime, reference and constraints.

# Acceptance Criteria
- [ ] New types: `integer`, `decimal`, `datetime`, `options` (enum with allowed values), `json`, and `reference` to another table's row.
- [ ] Per-column `unique`, `required` and `default`.
- [ ] An upsert on a unique column is a single statement.
- [ ] Migration for existing tables, and SDK and node support.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
