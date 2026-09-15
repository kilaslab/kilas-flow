---
id: FEAT-x35nqx
title: Compact workflow editor header like n8n
status: done
priority: medium
parent: EPIC-8n8aq8
created: "2026-09-15T02:32:37Z"
updated: "2026-09-15T02:34:43Z"
---

# Description

# Acceptance Criteria
- [ ] Define acceptance criteria

# Implementation Plan

# Notes

# Related Files

# Attachments

## Goal
Workflow editor top header as compact as n8n: shorter bar, icon-first secondary actions, less horizontal clutter. Dark brand; guards intact.

## Acceptance
- Header height ~36px (h-9) or visually matching n8n density
- Secondary actions icon-only with aria-label/title
- Primaries (Add step / Execute) may keep short labels
- Tests/check/build green; no push

## Work Evidence

Closed by `pine close --evidence` on 2026-09-15.

- Base: `75d0441e` (last commit at or before ticket created 2026-09-15)
- Files changed (base → working tree):

```
 internal/web/dist/index.html                       | 16 ++--
 web/package.json                                   |  1 +
 web/pnpm-lock.yaml                                 | 15 ++++
 web/src/app.css                                    | 33 +++++---
 .../lib/components/dashboard/dashboard-nav.svelte  | 15 +++-
 .../components/workflow-editor/canvas-node.svelte  | 68 ++++++++++------
 .../workflow-editor/execution-canvas-node.svelte   | 49 ++++++++---
 .../workflow-editor/workflow-editor.svelte         | 94 +++++++++++++++++-----
 web/src/lib/embed/embed-editor.svelte              |  3 +
 web/src/lib/workflow-editor/document.test.ts       | 14 ++--
 web/src/lib/workflow-editor/document.ts            | 15 ++--
 web/src/lib/workflow-editor/node-visual.test.ts    | 45 ++++++++++-
 web/src/lib/workflow-editor/node-visual.ts         | 60 ++++++++++++--
 web/src/routes/(dashboard)/+layout.svelte          | 67 +++++++++++++--
 .../(dashboard)/app/workflows/[id]/+page.svelte    | 17 ++--
 .../app/workflows/[id]/export-dialog.svelte        |  6 +-
 16 files changed, 399 insertions(+), 119 deletions(-)
```
