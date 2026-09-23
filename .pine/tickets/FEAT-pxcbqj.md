---
id: FEAT-pxcbqj
title: 'JS Code runtime P4: kilasflow.jsCode node, executor, importer mapping, catalogue availability'
status: todo
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-yxhgeh
    - FEAT-zjrw76
parent: EPIC-tjnr1z
phase: p4
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T01:33:22Z"
---

# Description

Make imported JavaScript Code nodes runnable, while Python stays refused.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 4* section. Read it before starting.

# Acceptance Criteria
- [ ] `n8n-nodes-base.code` with JavaScript (or no language) imports as `kilasflow.jsCode` with no blocking issue, and exports back byte-identical
- [ ] Existing `foreignCode` nodes with `language: javaScript` run without re-import
- [ ] Python is still refused with the same sentence. foreignCode is not stamped `unavailable`, because its JavaScript form now runs (EPIC amendment 13)
- [ ] The analyser refuses `\p{}`, the `v`/`d` flags, async generators, `import`/`export`, unlisted `require` and `this.getCredentials`, at import and at validate, through the `jsrun.Refusal` template
- [ ] `kilasflow.jsCode` is stamped unavailable only when `code.javascript_enabled=false`, and it can be added from the palette

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 4*.

# Notes

# Related Files

# Attachments
