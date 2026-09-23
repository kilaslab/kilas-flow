---
id: FEAT-vjjs8t
title: 'JS Code runtime P8: docs page, security review, load test'
status: todo
priority: medium
labels:
    - code-node
    - javascript
deps:
    - FEAT-afkx3k
parent: EPIC-tjnr1z
phase: p8
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T01:33:22Z"
---

# Description

Documentation and hardening before the runtime is declared shipped.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 8* section. Read it before starting.

# Acceptance Criteria
- [ ] A docs page "Code (JavaScript)" covers what runs, the differences from n8n, the configuration keys and the limits
- [ ] A security review covers prototype pollution confined to one execution, host bindings exposing plain functions and data only, and an enumeration test of the globals
- [ ] p99 latency under concurrent Code-node load is measured and recorded

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 8*.

# Notes

# Related Files

# Attachments
