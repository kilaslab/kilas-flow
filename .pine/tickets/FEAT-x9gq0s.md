---
id: FEAT-x9gq0s
title: 'JS Code runtime P5: this.helpers (httpRequest, binary) and $getWorkflowStaticData'
status: todo
priority: medium
labels:
    - code-node
    - javascript
deps:
    - FEAT-pxcbqj
parent: EPIC-tjnr1z
phase: p5
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T01:33:22Z"
---

# Description

Host helpers that go through the tenant's egress policy, plus a workflow static-data store.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 5* section. Read it before starting.

# Acceptance Criteria
- [ ] `this.helpers.httpRequest` returns a Promise, goes through `internal/safehttp`, and counts against `MaxHostCalls`
- [ ] `getBinaryDataBuffer` and `prepareBinaryData` are tenant-scoped
- [ ] `$getWorkflowStaticData('global'|'node')` uses a new tenant-scoped table; it is saved only after a successful non-manual run, capped at 256 KiB
- [ ] The editor output panel has a Console tab (live for manual runs, persisted in execution detail)

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 5*.

# Notes

# Related Files

# Attachments
