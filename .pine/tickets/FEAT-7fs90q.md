---
id: FEAT-7fs90q
title: Compact editor density / smaller nodes
status: done
priority: medium
parent: EPIC-8n8aq8
created: "2026-09-13T12:36:42Z"
updated: "2026-09-13T13:01:45Z"
---

# Description

# Acceptance Criteria
- [ ] Define acceptance criteria

# Implementation Plan

# Notes

# Related Files

# Attachments

## Goal
Editor density closer to n8n compact: smaller nodes, tighter chrome, optional zoom/density so long chains fit better.

## Acceptance
- Smaller default node footprint (or a Compact density mode) vs current card chrome from EPIC-qx56ay.
- Connection handles remain usable when zoomed out.
- Tidy/layout spacing can stay or get a compact preset; don't break FEAT-c71wy8 API.
- Dark brand; tidy/save/run guards intact.
- Visual check on Create Trip workflow.

## Deps note
Can run after or in parallel with sidebar collapse; prefer sidebar first for canvas width.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-13.

- Base: `75d0441e` (last commit at or before ticket created 2026-09-13)
- Files changed (base → working tree):

```
 internal/web/dist/index.html                       | 16 ++---
 web/package.json                                   |  1 +
 web/pnpm-lock.yaml                                 | 15 +++++
 web/src/app.css                                    | 33 ++++++----
 .../lib/components/dashboard/dashboard-nav.svelte  | 15 +++--
 .../components/workflow-editor/canvas-node.svelte  | 68 +++++++++++++--------
 .../workflow-editor/execution-canvas-node.svelte   | 49 +++++++++++----
 .../workflow-editor/workflow-editor.svelte         | 70 +++++++++++++++++++---
 web/src/lib/embed/embed-editor.svelte              |  3 +
 web/src/lib/workflow-editor/document.test.ts       | 14 ++---
 web/src/lib/workflow-editor/document.ts            | 15 +++--
 web/src/lib/workflow-editor/node-visual.test.ts    | 45 +++++++++++++-
 web/src/lib/workflow-editor/node-visual.ts         | 60 +++++++++++++++++--
 web/src/routes/(dashboard)/+layout.svelte          | 67 +++++++++++++++++++--
 .../(dashboard)/app/workflows/[id]/+page.svelte    | 13 ++--
 15 files changed, 381 insertions(+), 103 deletions(-)
```
