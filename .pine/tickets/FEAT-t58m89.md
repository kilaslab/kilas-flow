---
id: FEAT-t58m89
title: Embeddable execution history and inspector
status: todo
priority: medium
labels:
    - saas
    - embedding
    - executions
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

The embed shows only the last run of the current session. A host has to build its own execution list and cannot show a past run's trace in the editor.

# Acceptance Criteria
- [ ] `/embed/{workflowId}?execution={id}` opens a read-only trace, owner-checked.
- [ ] `mountExecutions({session})` lists the session workflow's runs with status and trigger filters and cursor paging.
- [ ] Both are branded and localised.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
