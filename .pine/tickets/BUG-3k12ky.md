---
id: BUG-3k12ky
title: 'Canvas input parity: Cmd+A/edge selection broken, wheel zooms instead of pans, n8n shortcuts missing, Cmd+S opens browser dialog'
status: todo
priority: medium
labels:
    - editor
    - keyboard
    - regression
parent: EPIC-8rbys7
created: "2026-09-23T01:48:14Z"
updated: "2026-09-23T01:48:14Z"
---

# Description

Muscle memory from n8n fails on the most basic interactions. Some of them (select-all, deleting an edge) were claimed done in FEAT-jvembs.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-7, UXE-9, UXE-10, UXE-11). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## UXE-7: Cmd+A selects nothing, and clicking a connection does not select it, so Delete cannot remove it

*bug · medium · editor-canvas / selection*

**n8n:** Ctrl/Cmd+A selects every node. Clicking a connection selects it, and Delete removes it.

**Steps to reproduce:**

1. Open a 5-node workflow and click empty canvas. 2. Press Cmd+A. 3. Click exactly on an edge (the hit test returns `path.svelte-flow__edge-interaction`) and press Delete or Backspace.

**Actual:**

After Cmd+A, 0 nodes are selected and no panel opens, although the keydown is consumed (defaultPrevented=true). Cmd+C afterwards copies nothing. After clicking an edge, no edge has `.selected` and Delete leaves it in place (4 edges before, 4 after). Only the hover trash icon deletes wires. Cmd+click and Shift+drag multi-select do work.

**Expected:**

Select-all and edge selection work as the shortcut overlay says.

**Suggested fix:**

Make Svelte Flow's node/edge `selected` flags the single source of truth (write them in selectAll and in edge clicks), or ignore selection events that follow a programmatic selection.

**Evidence:**

SD/33-select-all.png, SD/selall.js (`afterCmdA: 0`), SD/edgesel.js (`selected:[...false]`, edges after Delete: 2 of 2). workflow-editor.svelte:602-607 `selectAll` writes state that the next canvas selection event overwrites. onpaneclick (1173) resets the selection.

**Related:**

FEAT-jvembs (done; claimed "Mod+A select all" and "select + Delete" for wires). This is a regression.


## UXE-9: A plain mouse wheel or two-finger scroll zooms the canvas instead of panning it

*ux · medium · editor-canvas / navigation*

**n8n:** The canvas pans on scroll (`pan-on-scroll` in n8n's Canvas.vue), and Ctrl/Cmd+wheel zooms ("Ctrl/Cmd + Mouse wheel: zoom in/out" in the keyboard-shortcut docs).

**Steps to reproduce:**

1. Open any workflow. 2. Scroll the mouse wheel (or use a two-finger trackpad scroll) over the canvas.

**Actual:**

40 wheel notches took the viewport from fit to `scale(0.1)`, the minimum zoom. The view never pans. Trackpad users cannot move around a large workflow without drag-panning.

**Expected:**

Wheel pans vertically, Shift+wheel pans horizontally, and Ctrl/Cmd+wheel (plus trackpad pinch) zooms.

**Suggested fix:**

Set `panOnScroll` and `zoomActivationKey="Meta"`/`"Control"` on SvelteFlow (xyflow keeps pinch-zoom).

**Evidence:**

SD/perf-panzoom.json (`vpAfterWheel: … scale(0.1)` for all sizes). The n8n source at packages/frontend/editor-ui/src/features/workflows/canvas/components/Canvas.vue sets `pan-on-scroll` and `zoom-activation-key-code`. workflow-editor.svelte's `<SvelteFlow>` sets neither `panOnScroll` nor `zoomActivationKey`.

**Related:**

none


## UXE-10: Core n8n shortcuts are missing or mean something else: Ctrl+Enter, D, P, Shift+S, Cmd+X, Shift+Alt+T, "=", double-click

*gap · medium · editor-canvas / keyboard*

**n8n:** Ctrl/Cmd+Enter executes the workflow. D deactivates, P pins, Shift+S adds a sticky, Ctrl/Cmd+X cuts, Shift+Alt+T tidies up, and arrow keys move between nodes. "=" in an empty parameter switches it to an expression. Double-clicking a node opens its NDV, and F2 renames it.

**Steps to reproduce:**

1. Select a node and press each key: `d`, `p`, `Shift+S`, `Meta+Enter`, `Control+Enter`, `Meta+X`, `Meta+K`, `Shift+Alt+T`. 2. Type `=` into an empty URL field. 3. Double-click a node.

**Actual:**

None of the keys in step 1 has any effect (SD/misc1.js). Tidy is bound to ⌘⇧T, which Chrome reserves for "Reopen closed tab", so real Chrome users cannot use it. "=" stays in fixed mode. Double-click opens an inline **rename** box, placed over the neighbouring edge, and never the inspector. The help overlay prints ⌘ glyphs on every OS.

**Expected:**

n8n's key map, or at least Ctrl/Cmd+Enter, D, Shift+S and Shift+Alt+T. Double-click opens the node, and the overlay shows Ctrl on non-Mac systems.

**Suggested fix:**

Extend `canvasShortcut` (execute, deactivate, sticky, cut, Shift+Alt+T) and move tidy off ⌘⇧T. Make double-click open the inspector. Render key labels per platform.

**Evidence:**

SD/46-shortcuts.png, SD/15-dblclick.png, shortcuts.ts:47-107, canvas-node.svelte:112 and 132 (`ondblclick={() => actions?.rename(node.id)}`).

**Related:**

FEAT-jvembs (listed D/P/Shift+Alt+T as n8n behaviour)


## UXE-11: Cmd/Ctrl+S while typing in a parameter does not save and opens the browser's "Save page" dialog

*bug · medium · ndv / saving*

**n8n:** Ctrl/Cmd+S saves the workflow from anywhere in the editor, including inside the NDV.

**Steps to reproduce:**

1. Open a node and click into any text parameter. 2. Type something and press Cmd+S.

**Actual:**

The keydown reaches the window with `defaultPrevented: false` and the status stays "Unsaved changes". In a real browser this opens Chrome's native "Save page as…" dialog. The same happens whenever focus is in the sidebar or the language select.

**Expected:**

Save is handled, and the event prevented, whenever focus is in the editor, including text inputs.

**Suggested fix:**

Treat `save` (and `undo` when not in a text field) as a global editor shortcut. Always preventDefault Mod+S inside the editor section, then flush the pending input and save.

**Evidence:**

SD run-code output `{"ks":["Meta:false:INPUT","s:false:INPUT"],"status":"Unsaved changes"}`. workflow-editor.svelte:875-882 `handleShortcut` returns early for `controlOwnsKey` targets before the save case. shortcuts.ts:109-122 suppresses every shortcut in inputs, "including ⌘S".

**Related:**

none


# Acceptance Criteria
- [ ] Cmd/Ctrl+A selects all; clicking an edge selects it, and Delete removes it
- [ ] A mouse wheel pans vertically, Shift+wheel pans horizontally, and Ctrl/Cmd+wheel or pinch zooms
- [ ] n8n's key map: Ctrl/Cmd+Enter executes, D disables, P pins, Shift+S adds a sticky, Cmd+X cuts, Shift+Alt+T tidies, double-click opens the node; the overlay shows Ctrl on non-Mac
- [ ] Cmd/Ctrl+S saves (with preventDefault) even when focus is in a parameter input

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-jvembs

# Related Files

# Attachments
