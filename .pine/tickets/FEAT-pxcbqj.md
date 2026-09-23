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
- [ ] Python is still refused with the same sentence, and the catalogue stamps foreignCode as `unavailable`
- [ ] An AST-based analyser refuses `\p{}`, the `v` flag, async generators, `import`/`export`, unlisted `require` and `this.getCredentials`, at import and at validate, with one sentence
- [ ] `kilasflow.jsCode` is stamped unavailable only when `code.javascript.enabled=false`

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 4*.

# Notes

# Related Files

# Attachments
