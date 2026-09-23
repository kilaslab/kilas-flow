---
id: FEAT-mammrz
title: 'JS Code runtime P6: Sort node code comparator on the JS runtime'
status: todo
priority: low
labels:
    - code-node
    - javascript
deps:
    - FEAT-pxcbqj
parent: EPIC-tjnr1z
phase: p6
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T01:33:22Z"
---

# Description

Sort's `code` comparator is the other JavaScript escape hatch. It runs on the same runtime.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 6* section. Read it before starting.

# Acceptance Criteria
- [ ] `sortToKilas` carries `type: code`; the executor compiles the comparator once per node run and sorts under the same limits
- [ ] The test asserting that both escape hatches refuse in the same words now asserts that Python and unsupported constructs still share one sentence

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 6*.

# Notes

# Related Files

# Attachments
