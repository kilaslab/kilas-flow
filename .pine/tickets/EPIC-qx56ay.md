---
id: EPIC-qx56ay
title: KilasFlow editor n8n-feel UX (dark brand)
status: done
priority: medium
created: "2026-09-12T07:55:05Z"
updated: "2026-09-12T08:00:12Z"
---

# Description

# Goals

## Work Evidence

Closed by `pine close --evidence` on 2026-09-12.

- Base: `75d0441e` (last commit at or before ticket created 2026-09-12)
- Files changed (base → working tree):

```
 internal/web/dist/index.html                       | 16 +++---
 web/package.json                                   |  1 +
 web/pnpm-lock.yaml                                 | 15 +++++
 web/src/app.css                                    | 29 +++++++---
 .../components/workflow-editor/canvas-node.svelte  | 57 ++++++++++++-------
 .../workflow-editor/execution-canvas-node.svelte   | 41 +++++++++++---
 .../workflow-editor/workflow-editor.svelte         | 66 +++++++++++++++++++---
 web/src/lib/embed/embed-editor.svelte              |  3 +
 web/src/lib/workflow-editor/document.test.ts       | 12 ++--
 web/src/lib/workflow-editor/document.ts            |  9 ++-
 web/src/lib/workflow-editor/node-visual.test.ts    | 42 +++++++++++++-
 web/src/lib/workflow-editor/node-visual.ts         | 44 +++++++++++++++
 .../(dashboard)/app/workflows/[id]/+page.svelte    | 13 +++--
 13 files changed, 278 insertions(+), 70 deletions(-)
```
