---
id: FEAT-afkx3k
title: 'JS Code runtime P7: Code-node compatibility corpus scoreboard and no-Node e2e'
status: todo
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-pxcbqj
parent: EPIC-tjnr1z
phase: p7
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T01:33:22Z"
---

# Description

Prove compatibility against real templates, and prove that no Node.js process exists.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 7* section. Read it before starting.

# Acceptance Criteria
- [ ] `scripts/code-corpus-sync.sh` fetches Code nodes from the top templates into a gitignored directory, pinned by digest in `MANIFEST.json`
- [ ] The scoreboard records the parse, analyser-accept and runtime-error rates plus median/p95 time in `BASELINE.md`; at least 97% of the top-500 JS Code nodes parse and pass analysis; CI fails on regression
- [ ] `make js-diff` (dev-only, needs Node) diffs jsrun against Node with a KilasFlow-authored harness
- [ ] The e2e imports, activates and runs a Code-node workflow (both modes, Luxon, console, `$('Node')`, Buffer, crypto) and asserts that no `node` process exists; it is wired into FEAT-5fhj6p

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 7*.

# Notes

# Related Files

# Attachments
