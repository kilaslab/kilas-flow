---
id: FEAT-rkj8ry
title: 'JS Code runtime P0: decision record, vendored Luxon/lodash, guardrails'
status: todo
priority: high
labels:
    - code-node
    - javascript
parent: EPIC-tjnr1z
phase: p0
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T01:33:22Z"
---

# Description

Record the partial supersession of FEAT-8qyfh1 and the new licence position, vendor the MIT libraries, and add the guardrails, before any runtime code lands.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 0* section. Read it before starting.

# Acceptance Criteria
- [x] The owner signs off in EPIC-tjnr1z on running JS (not translating it) and on the narrowed licence position
- [ ] `.pine/memory/code-node.md` and `.pine/memory/licensing.md` carry dated entries describing the change
- [ ] Luxon 3.7.x and lodash 4.17/4.18 are vendored under `third_party/` with `LICENSE`, `PROVENANCE.md` (URL, version, sha256) and an `embed.go`; `TestVendoredThirdPartyCarriesItsLicence` passes
- [ ] A guardrail test forbids `internal/jsrun` and `nodes/jscode*.go` from importing `sidecar`, `os/exec`, `net`, `os` (file access) or `syscall`; `TestNoN8NDependency*` still passes

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 0*.

# Notes

**2026-09-23: owner sign-off received:** "yes approve at least javascript can run and can import n8n workflow that already using js code". Imported n8n JavaScript Code nodes run on an embedded engine, and JS is never translated to Go. Python stays refused. The memory and licensing entries are still to be written in this phase.

# Related Files

# Attachments
