---
id: FEAT-7t0xks
title: Host metadata on workflows, and a workspace-scoped session
status: todo
priority: low
labels:
    - saas
    - embedding
    - workflows
deps:
    - FEAT-mccadj
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-25T09:52:32Z"
---

# Description

A host needs to tag workflows with its own keys (an external reference, a flag such as "this workflow takes over the reply", a folder) and to embed a list and create surface instead of rebuilding one.

# Acceptance Criteria
- [ ] A `metadata` map on workflows, set with the tenant key, filterable in `listWorkflows`, and returned by FEAT-mccadj's `/events` fan-out.
- [ ] Optional workspace sessions, scoped to a metadata folder, that can list, create and duplicate within it.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid. The AC's "G2's fan-out" was a label from the source review (in this repo G2 is FEAT-bb4s6e's docs gate); corrected in place to FEAT-mccadj.
- No metadata on workflows; `listWorkflowsInput` (workflows.go:284-287) takes only limit/cursor.
- A workspace session needs new arms in scope.go: listing is refused to every embed and bound key (:208-210), duplicate at :232-240.

# Related Files

# Attachments
