---
id: FEAT-vxbkhg
title: Canvas node chrome and connection handles closer to n8n
status: done
priority: medium
parent: EPIC-qx56ay
created: "2026-09-12T07:55:05Z"
updated: "2026-09-12T07:57:57Z"
---

# Description

# Acceptance Criteria
- [ ] Define acceptance criteria

# Implementation Plan

# Notes

# Related Files

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-12.

- Base: `75d0441e` (last commit at or before ticket created 2026-09-12)
- Files changed (base → working tree):

```
 internal/web/dist/index.html                       | 12 ++---
 web/package.json                                   |  1 +
 web/pnpm-lock.yaml                                 | 15 ++++++
 web/src/app.css                                    | 29 ++++++++---
 .../components/workflow-editor/canvas-node.svelte  | 57 ++++++++++++++--------
 .../workflow-editor/execution-canvas-node.svelte   | 41 ++++++++++++----
 .../workflow-editor/workflow-editor.svelte         | 18 ++++++-
 web/src/lib/workflow-editor/node-visual.test.ts    | 42 +++++++++++++++-
 web/src/lib/workflow-editor/node-visual.ts         | 44 +++++++++++++++++
 9 files changed, 212 insertions(+), 47 deletions(-)
```
