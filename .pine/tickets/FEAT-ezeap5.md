---
id: FEAT-ezeap5
title: Revamp the editor UI to a compact, density-first canvas
status: done
priority: medium
created: "2026-09-05T03:21:41Z"
updated: "2026-09-05T03:45:00Z"
---

# Description

The editor had drifted to a card-based canvas: every node was a 256px-wide box
carrying its category, name, and description as body text, and the workspace
around it was scaled for a marketing page rather than a working surface. Three
stacked chrome bars cost 140px before the canvas began.

The reference is `design-refs/n8n/` — for interaction and information
architecture only. The visual identity stays KilasFlow's own jade-on-graphite,
dark-first palette.

# Acceptance Criteria

- [x] A node is an icon tile, not a card; its name sits outside the tile.
- [x] Node role is legible without reading text.
- [x] Connection kind is legible without hovering.
- [x] Chrome is one bar, not three.
- [x] One density scale, applied across every page.
- [x] Adding the next step takes one click from the port it connects to.
- [x] The replay view keeps the editor's geometry so a graph reads the same in both.

# Implementation Plan

## Silhouette carries role

Three shapes, no legend: a rounded left edge means a trigger (the flow enters
here), a square means a step, a circle means an attachment (it configures
another node rather than running in line). A node that takes attachments becomes
a wide pill so it has an edge to host them.

Every shape is derived in `node-visual.ts` from the ports the registry already
declares, never from a list of known type names — so a node type added on the
server arrives with the right silhouette and no frontend change. Order matters
in that derivation: a tool has no inputs at all and would otherwise read as a
trigger, so what a node *provides* is settled before what it consumes.

## Connection kind carries meaning

`main` ports are circles on the left and right; `ai_*` attachment ports are
diamonds on the bottom (consumer) and top (provider). Attachment edges are
dashed bezier curves without an arrowhead, because they carry configuration and
nothing flows along them during a run. Main edges are stepped with an arrow.

## Colour is information

Five accents by registry category — Triggers amber, Core jade, AI violet,
Database azure, Imported red. On a dense canvas the hue says what a node *is*
before the name is readable. Borders stay near-neutral (22% accent mixed into
the border colour); only the icon takes the full accent, so a busy graph does
not turn into a colour chart.

## One line of text worth reading

The tile has room for one line, and it goes to whatever most distinguishes this
node from another of the same type: `POST /tickets`, `GET api.shop.test`,
`0 7 * * *`, `2 fields`. Set in mono, because it is what the engine owns.
Anything unset is omitted rather than padded with a placeholder.

## Density scale

Stated once at the top of `app.css` and applied everywhere: 2.75rem app header,
2.5rem editor toolbar, 2.75rem list row, 1.75rem form control, 13rem sidebar,
1.25rem page padding, 5.5rem node tile, 3.75rem attachment circle. Canvas
spacing follows from the tile rather than from round numbers: 220px across
clears the widest label, 190px down clears a label plus a port row.

# Notes

## Findings

**Svelte Flow's colour-mode class collided with the theme selector.** The canvas
container is stamped `light` by the library, and `app.css` used bare `.light` as
the light-theme opt-in. Every element inside the editor therefore resolved the
*light* palette while `getComputedStyle` still reported the dark values, which is
why node names rendered dark on dark and the muted subtitles looked brighter than
the names above them. The `dark:` variant was disabled inside the canvas for the
same reason. Fixed by namespacing the class hooks to `.theme-light` /
`.theme-dark`; the `[data-theme]` attribute selectors were always correct and are
unchanged. Bare `.light` and `.dark` are too common in third-party components to
use as application selectors.

**A permanently mounted inspector cost the canvas 20rem to say nothing.** It now
mounts only while a node is selected. On a 1440px screen that moved the canvas
from 912px to 1232px and raised the default fit zoom from 0.71 to 0.86, which is
the difference between labels being legible and not.

**The toolbar wrapped on a phone.** Fixed a 2.5rem bar with wrapping buttons
inside is a bar that is silently 3.5rem. Buttons are now `shrink-0
whitespace-nowrap`, the save-state words drop below `sm` (the Save button's
enabled state already carries it, and the text stays in a `sr-only` span), and
the breadcrumb yields width before the actions do.

## Deliberate limits

- The inspector stays a side panel rather than n8n's full-screen three-panel
  NDV. A modal that covers the host page is the wrong default for an embeddable
  editor, and input/output data panes are a feature, not a density fix.
- No required-port asterisk. `Port` carries `Name` and `Kind` and nothing about
  necessity, so drawing one would be inventing information the API does not have.

# Work Evidence

- `node-visual.ts` is the single source of truth for icon, accent, shape, and
  subtitle; the canvas node, the replay node, the picker, and the inspector
  header all read it, so a node looks the same everywhere it appears.
- `canvas-actions.ts` carries the few actions a node needs to the editor that
  owns the document. Svelte Flow renders nodes, so there is no prop path; each
  member is a function so a node reads the current value rather than capturing a
  stale one.
- Add-from-port builds its connection through `connectionFromCanvas`, the same
  validator a dragged connection uses, so a step added from a port can never
  produce an edge the canvas would refuse. The picker filters to nodes with a
  main input while connecting — verified live as "11 of 17 nodes", correctly
  excluding the three triggers and three AI sub-nodes.
- Verified live against a running binary at 1440px and 390px, in both themes: a
  nine-node graph covering all four silhouettes and all three attachment kinds;
  add-from-port producing 10 nodes / 9 edges from 9 / 8; the replay showing per
  node status rings, check badges, and edge item counts including `0 items` on
  the untaken branch; the toolbar measured at exactly 40px on a phone.
- `go test ./...`, `go vet ./...`, `pnpm test` (53), `pnpm check` (1264 files, 0
  errors), `pnpm generate:api:check` (no drift), `pnpm build`, SDK `check` and
  `test` (23).

# Related Files

- `web/src/lib/workflow-editor/node-visual.ts`
- `web/src/lib/workflow-editor/canvas-actions.ts`
- `web/src/lib/components/workflow-editor/{canvas-node,execution-canvas-node,node-icon,node-picker,properties-panel,property-field,workflow-editor}.svelte`
- `web/src/app.css`
