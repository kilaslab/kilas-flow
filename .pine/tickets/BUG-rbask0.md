---
id: BUG-rbask0
title: 'Sticky notes: node names unreadable on stickies (dark theme), selected sticky covers nodes, raw colour number, markdown links raw'
status: done
priority: high
labels:
    - editor
    - canvas
    - sticky-notes
    - accessibility
parent: EPIC-8rbys7
created: "2026-09-23T01:48:15Z"
updated: "2026-10-08T15:58:00Z"
---

# Description

Templates group nodes on section stickies. In the dark theme, node labels on pastel stickies are near-invisible. This shows on the #2 most-viewed template and most others.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: n8n-templates, ux-editor; finding ids: TPL-6, UXE-16, TPL-14). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## TPL-6: Node names are unreadable on sticky notes: white labels over pastel sticky fills in the dark theme

*ux · high · editor-canvas*

**n8n:** Templates group their nodes inside section stickies. Node names stay readable because n8n's dark theme uses dark-tinted sticky backgrounds, and its light theme uses dark label text.

**Steps to reproduce:**

1. Import template 1750 "Creating an API endpoint", the #2 most-viewed template.
2. Open it at the default zoom.
3. Repeat with 1747, 1934 and 3066.

**Actual:**

- The Webhook, "Create URL string" and "Respond to Webhook" names are near-invisible: white 12px text on `#fef3c7`.
- The same happens to every node that sits on a sticky: all 9 in 1747, "Send error message" in 1934, and most of 3066.
- Subtitles are only slightly more readable.

**Expected:**

Node labels have AA contrast wherever the node sits. Either use dark-mode sticky fills, or give labels a contrasting chip or background when they overlap an annotation.

**Suggested fix:**

Add dark variants to `PALETTE`, keyed off the theme tokens, or render node labels on a semi-opaque `bg-background` pill.

**Evidence:**

- `ui_1750_canvas.png`, `ui_1747_canvas.png`, `ui_1747_zoomed.png`, `ui_1934_canvas.png`
- `web/src/lib/workflow-editor/sticky.ts:15-23` (light-only PALETTE)
- `web/src/lib/components/workflow-editor/canvas-node.svelte:214` (label inherits the dark-theme foreground)

**Related:**

none (FEAT-jvembs delivered sticky rendering; contrast was not covered). OPS-19 covers other low-contrast text.


## UXE-16: A selected sticky note jumps above the nodes it contains and hides them; colour is a raw number field

*bug · medium · editor-canvas / sticky notes*

**n8n:** Stickies always stay behind nodes. Colour is picked from a palette on hover, and content is edited inline with double-click.

**Steps to reproduce:**

1. Add a Sticky Note (it lands over existing nodes, see UXE-5). 2. Click the sticky.

**Actual:**

Before selection, each node is topmost at its centre. After selection, `elementFromPoint` over the Wait node returns the STICKY, which now covers Wait and HTTP Request. Double-clicking the sticky does nothing. Content is a one-line input (UXE-2), Colour is a free number field ("1"), and white node labels on the pastel fill are unreadable (TPL-6).

**Expected:**

Stickies keep a z-index below steps even when selected. A palette control and multi-line content.

**Suggested fix:**

Pass `elevateNodesOnSelect={false}` (or re-apply zIndex -1 to selected annotations). Render Colour as swatches (n8n's 1-7).

**Evidence:**

SD/40-sticky.png, SD/42-sticky-click.png, SD/74-sticky-selected-z.png, SD/stickyz4.js (`"Wait -> topmost: STICKY"`). document.ts:127 gives stickies `zIndex: -1`, but Svelte Flow's `elevateNodesOnSelect` (default true) lifts selected nodes.

**Related:**

FEAT-jvembs (claims "zIndex below steps"), TPL-6


## TPL-14: Sticky notes show markdown links and images as raw text

*ux · low · editor-canvas*

**n8n:** Sticky notes render markdown links, images and GIFs, which templates use for setup instructions, video links and "click here" buttons. Examples: 5170 and 2753.

**Steps to reproduce:**

1. Open imported 5170.
2. Zoom into the "Execute Workflow" sticky.

**Actual:**

The sticky shows `[![Execute Workflow](https://supastudio.ia2s.app/…/execute_workflow_json_tutorial.gif)](https://www.youtube.com/watch?v=PAmgrwYnzWs)` literally. Bullet lists are also plain text. Only headings, bold and inline code are styled.

**Expected:**

At least render link text and drop the image syntax, for example as an "image" chip. Ideally show safe links (open in a new tab, `rel=noopener`).

**Suggested fix:**

Extend `markdownRuns` with link and image tokens rendered as text runs, keeping it markup-free: link text styled as a link, and images as alt text.

**Evidence:**

`ui_5170_sticky_markdown_zoom.png`, `ui_5170_canvas.png`, `web/src/lib/workflow-editor/sticky.ts:40-73` (tokenizer deliberately keeps links as text)

**Related:**

FEAT-jvembs (done; chose the "safe markdown subset")


# Acceptance Criteria
- [x] Node labels reach AA contrast wherever the node sits (dark sticky variants, or a label chip)
- [x] A selected sticky stays below the steps it contains
- [x] Sticky colour is a palette control, not a number field
- [x] Markdown links render as safe links (new tab, `rel=noopener`), images as alt-text chips, and lists as lists

## Verification (2026-10-08)

Fix commit: `bf512d6`.

- **Node labels reach AA contrast (met).** Sticky fills are theme-tinted (`web/src/app.css` `--kf-sticky-*`). Measured in the app on a colour-5 sticky that contains four steps:
  - Dark theme: the fill is `rgb(22,40,63)`, node labels reach 12.92:1 and the note text 12.17:1.
  - Light theme (`data-theme="light"`): the fill is `rgb(219,234,254)`, node labels reach 14.31:1 and the note text 12.21:1.

  AA for normal text is 4.5:1. Screenshots: `03-sticky-selected-dark.png` and `14-sticky-light.png`.
- **A selected sticky stays below its steps (met).** The canvas sets `elevateNodesOnSelect={false}` (`workflow-editor.svelte:1263`). In the app, after the sticky is selected its node keeps `z-index: -1`, the contained Call API stays at 0, and every step stays visible on top of the note.
- **Colour is a palette control (met).** `properties-panel.svelte:291` renders a radiogroup of `STICKY_SWATCHES`. Test: `sticky.test.ts` "offers one swatch per palette index for the inspector control". In the app, the sticky's panel shows a "Colour" radiogroup of 7 swatches with "Colour 5" checked, and no number field (`15-sticky-colour-control.png`).
- **Markdown links, images and lists (met).** Links render at `canvas-node.svelte:158`. Tests: `sticky.test.ts:43`, `:54` and `:62`. In the app:
  - `[the docs](https://docs.n8n.io/)` renders as a link with `target="_blank"` and `rel="noopener noreferrer nofollow"`.
  - `![diagram](…)` renders as a "diagram" chip.
  - `- one` and `- two` render as bullets.

Self-test setup: `make build-web` + `go build ./cmd/kilasflow` from this branch, served on 127.0.0.1:18095 with an isolated SQLite file (`KILASFLOW_DATABASE_DSN=/tmp/kf-verify/data/kilasflow.db`), driven with playwright-cli. Probe workflows were imported through `POST /api/v1/workflows/import`. Screenshots are in `/tmp/kf-verify/` and are not committed.

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-jvembs

# Related Files

# Attachments
