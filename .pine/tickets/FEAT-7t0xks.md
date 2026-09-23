---
id: FEAT-7t0xks
title: Host metadata on workflows, and a workspace-scoped session
status: todo
priority: low
labels:
    - saas
    - embedding
    - workflows
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

A host needs to tag workflows with its own keys (an external reference, a flag such as "this workflow takes over the reply", a folder) and to embed a list and create surface instead of rebuilding one.

# Acceptance Criteria
- [ ] A `metadata` map on workflows, set with the tenant key, filterable in `listWorkflows`, and returned by G2's fan-out.
- [ ] Optional workspace sessions, scoped to a metadata folder, that can list, create and duplicate within it.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
