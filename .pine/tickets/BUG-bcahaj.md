---
id: BUG-bcahaj
title: 'Editor chrome collisions: minimap covers zoom/fit/tidy (>12 nodes), Chat covers minimap/controls, inspector hides canvas at 1024-1280 px'
status: done
priority: medium
labels:
    - editor
    - layout
    - responsive
parent: EPIC-8rbys7
created: "2026-09-23T01:48:15Z"
updated: "2026-10-08T16:16:00Z"
---

# Description

Overlays fight for the same corner, and on a common laptop width the inspector covers the node being edited.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-8, UXE-18). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## UXE-8: On any workflow with more than 12 nodes, the minimap covers the zoom, fit and tidy controls, and the Chat button covers the minimap

*bug · medium · editor-canvas*

**n8n:** The zoom controls and minimap never overlap.

**Steps to reproduce:**

1. Open "[ux-editor] Perf 300 nodes" (wf_01a0cbdb-69da-7ca6-994b-68581758c00f) or any workflow with more than 12 nodes. 2. Try to click Zoom In, Zoom Out, Fit View or Tidy up at the bottom left.

**Actual:**

`document.elementFromPoint` at the centre of each of the four control buttons returns `svelte-flow__minimap-svg`. The minimap (223,735 200x150) sits exactly over the Controls, so they cannot be clicked. On a workflow with a chat trigger, the "Chat" pill is drawn over the minimap.

**Expected:**

The minimap and the controls occupy different corners.

**Suggested fix:**

Put the minimap at bottom-right (as n8n does) or offset the Controls. Move the Chat pill so it clears both.

**Evidence:**

SD/53-perf300-fit.png, SD/56-imported-100-canvas.png. workflow-editor.svelte:1176 `<EditorControls>` (Svelte Flow Controls, bottom-left by default) and 1184-1185 `<MiniMap position="bottom-left">`.

**Related:**

FEAT-jvembs (minimap added there)


## UXE-18: At 1024-1280 px the editor keeps the full sidebar, and the inspector covers the canvas and the selected node

*ux · medium · editor-canvas / responsive*

**n8n:** The sidebar collapses in the editor, and the NDV is a modal, so the canvas keeps the whole width.

**Steps to reproduce:**

1. Resize to 1024x768 (and 1280x800). 2. Open a workflow and select a node.

**Actual:**

At 1024 the 207 px sidebar and the 320 px inspector leave about 500 px of canvas. The selected node is behind the inspector and the view does not move, so the canvas looks empty. The workflow name truncates to "[ux-editor] First flow". There is no horizontal page scroll (scrollWidth 1024).

**Expected:**

The sidebar auto-collapses on editor routes below about 1400 px, and selection pans into the visible area.

**Suggested fix:**

Collapse the dashboard sidebar by default on /app/workflows/[id]. Offset fitView and setCenter by the inspector width.

**Evidence:**

SD/63-1280-editor.png, SD/64-1024-editor.png.

**Related:**

none


# Acceptance Criteria
- [x] The minimap, the zoom controls and the Chat button occupy distinct positions at every node count
- [x] Below about 1400 px the sidebar auto-collapses on editor routes
- [x] Selecting a node pans it into the area not covered by the inspector

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-jvembs

# Fix

- The minimap moved to the bottom-right corner (as in n8n) and is lifted 26 px so it also clears Svelte Flow's bottom-right attribution; the zoom/fit/tidy controls keep the bottom-left. The closed Chat button docks bottom-right *above* the minimap whenever the minimap is shown (`chatDockBottom`); the open Chat panel, a surface the user asked for, still docks at the bottom. Placement constants and the >12-node rule live in `web/src/lib/workflow-editor/canvas-chrome.ts`.
- The dashboard sidebar starts as the icon rail on `/app/workflows/[id]` below 1400 px (`web/src/lib/dashboard/sidebar.ts`). The stored preference still governs every other page; expanding the rail on a narrow editor lasts the session and is not written to localStorage.
- Selecting a node while the inspector is open runs `viewportRevealing` after the inspector has taken its column: a node outside the visible canvas is centred on the axis it was out of, at the same zoom, via `setViewport`. Only the selection and the panel are tracked, so a dragged node is never chased.
- Tests: `pnpm vitest run src/lib/workflow-editor/canvas-chrome.test.ts src/lib/dashboard/sidebar.test.ts` (15 pass; the chrome test asserts no overlap of controls/minimap/Chat/attribution across canvas widths and node counts). `pnpm check` 0 errors; `pnpm vitest run src/lib/workflow-editor src/lib/dashboard src/lib/components` 667 pass.
- Playwright (real build, 16-node workflow with a chat trigger, element bounding boxes + `elementFromPoint`): at 1024x768, 1280x800 and 1440x900 the four control buttons, the minimap and the Chat button each hit themselves, with no pairwise overlap (minimap 200x150 at the canvas's bottom-right, Chat 52 px above it). Sidebar width is 56 px at 1024 and 1280 and 208 px at 1440 (and 208 px on /app/workflows at 1280; toggling on the editor expands it without storing). Clicking the rightmost node (screen x 942/1177/1336, under where the inspector opens) moved it to x 371/496/652, inside the 648/904/912 px canvas left of the inspector.

# Related Files

- web/src/lib/workflow-editor/canvas-chrome.ts
- web/src/lib/dashboard/sidebar.ts
- web/src/lib/components/workflow-editor/workflow-editor.svelte
- web/src/routes/(dashboard)/+layout.svelte

# Attachments
