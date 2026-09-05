---
id: FEAT-ezeap5
title: Revamp the editor UI to a compact, density-first canvas
status: done
priority: medium
created: "2026-09-05T03:21:41Z"
updated: "2026-09-05T04:20:00Z"
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
trigger, so what a node provides is settled before "has no inputs" — but what it
*consumes* is settled first of all, because a node that both takes and provides
attachments needs the width a hub has and a circle does not.

## Connection kind carries meaning

`main` ports are circles on the left and right; `ai_*` attachment ports are
diamonds on the bottom (consumer) and top (provider). Attachment edges are
dashed curves without an arrowhead, because they carry configuration and nothing
flows along them during a run. Main edges are stepped with an arrow.

## Colour is information

Five accents by registry category — Triggers amber, Core jade, AI violet,
Database azure, Imported red. On a dense canvas the hue says what a node *is*
before the name is readable. A border takes the accent's hue at a muted
lightness and chroma; only the icon takes it at full strength, so a busy graph
does not turn into a colour chart. The hue is held exactly rather than mixed
toward another colour — see the Review for why mixing was wrong.

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
- Verified live against a running binary at 1440px and 390px: a nine-node graph
  covering all four silhouettes and all three attachment kinds; add-from-port
  producing 10 nodes / 9 edges from 9 / 8; the replay showing per node status
  rings, check badges, and edge item counts including `0 items` on the untaken
  branch; the toolbar measured at exactly 40px on a phone. The light palette was
  exercised by setting `data-theme` by hand — nothing in the app sets it yet, so
  that is a smoke test of the tokens, not of a reachable product state.
- After review, re-verified against the production build: handle geometry (8px,
  border 0, transparent, library centring intact), border hue per category
  (78°/170°/240° preserved), two branches from one node landing 190px apart, and
  focus resting on the inspector after an add and the canvas after a delete.
- `go test ./...`, `go vet ./...`, `pnpm test` (75), `pnpm check` (1267 files, 0
  errors, 0 warnings), `pnpm generate:api:check` (no drift), `pnpm build`, SDK
  `check` and `test` (23).

# Related Files

- `web/src/lib/workflow-editor/node-visual.ts`
- `web/src/lib/workflow-editor/canvas-actions.ts`
- `web/src/lib/components/workflow-editor/{canvas-node,execution-canvas-node,node-icon,node-picker,properties-panel,property-field,workflow-editor}.svelte`
- `web/src/app.css`

# Review

Five reviewers ran in parallel over `a049b60..b80890e`, scoped so their contexts
did not overlap: node logic, editor state, CSS/theme, accessibility, and
cross-boundary integration. Every finding below was independently verified
before being acted on.

## Defects that had shipped

**The Svelte Flow port reset never applied.** `app.css` and the library declare
`.svelte-flow__handle` at the same specificity, unlayered, so source order
decides — and the library's sheet was imported from two *components*, which put
it in the lazily-appended route chunk while `app.css` sat in the layout chunk.
The library therefore always won. Every port shipped as its default 6px
jade-bordered circle with the intended 8px port drawn on top, and edges
terminated ~2px off the visible dot. The same defect silently killed the
controls-button sizing and the main-edge stroke width. The comment claiming the
default dot "is reset to nothing" was false.

The import now lives in `app.css` above the overrides, so the order is a
property of one file rather than of chunking. The reset itself was also wrong:
it cleared `transform`, which is what the library uses to centre a handle on its
edge. Only paint is overridden now; position stays with the library. Verified in
a browser against the production build — handle computes to 8px, border 0,
transparent, with centring intact.

**Two branches from one node landed on the same pixel.** `positionAfter` fanned
a branch out by counting the connections already leaving that *port* — but the
`+` button only exists while a port has none, so the count was structurally
always zero. An IF's second branch landed exactly on its first. The comment
asserted it prevented precisely that. It now walks the destination down a row at
a time until it clears every existing node, which also covers a node the user
had dragged there. Pinned by a test.

**Every add and delete stranded keyboard focus on `<body>`** (WCAG 2.4.3, Level
A). The `+` stub restores focus to itself, but a successful add connects the
port and destroys that button; deleting unmounts the toolbar and the inspector
holding focus; the delete-connection button removes itself. All three now name a
survivor — the inspector after an add, the canvas region after a delete.

## Corrections

- Node borders mixed the accent into `--border`, which carries its own alpha and
  hue: the accent landed at ~68% rather than the stated 22%, and every hue was
  dragged toward the border's — amber and red came out green, violet and azure
  cyan. Now `oklch(from var(--node-accent) 0.42 0.045 h)`, which holds hue
  exactly. Verified: 78°→78°, 170°→170°, 240°→240°.
- Focus rings inherited `outline-ring/50`, which measures 2.3–3.0:1 against every
  surface in both palettes, under the 3:1 SC 1.4.11 requires. Base is now full
  opacity, which fixes the eight new call sites and the pre-existing ones.
- Light-mode `--warning` measured 2.36:1 as badge text; darkened.
- `failed`, `cancelled` and `cancelling` shared one glyph, leaving hue as the
  only difference. Distinct glyphs now.
- The expression switch was named for its state (`aria-checked` said off while
  the name said "fixed"); it now has a stable name.
- Run status and node validation messages were absent from the accessible name
  that Svelte Flow actually exposes. Both are folded into `ariaLabel`, matching
  what edges already did.
- Active/Draft was colour-only below `sm`; category was hue-only in the
  inspector. Both have text equivalents.
- `prefers-reduced-motion` was honoured nowhere, including a badge that spins for
  the length of an execution.
- Tile geometry was duplicated across the editor and replay nodes and had already
  drifted; it lives in `node-visual.ts` now, so "the replay keeps the editor's
  geometry" holds by construction.
- `.svelte-flow__edge-textbkg`/`-text` targeted SVG classes that v1.6 does not
  render; retargeted to `.svelte-flow__edge-label`.
- `bezier` is not a registered edge type and rendered only through an
  undocumented fallback; now `default`.
- The documented density scale was wrong in two of six values on the day it was
  written. Corrected.
- `--accent` on node roots shadowed the theme token of the same name for the
  whole subtree — the same class of collision as `.light`. Renamed
  `--node-accent`.
- Node placement fanned out by a magic `150` two lines below `ROW = 190`.
- The embed branding bar was the one surface the density sweep missed.

## Tests added

`node-visual.test.ts` (19 cases) pins the shape derivation — including the
ordering that makes a tool an attachment rather than a trigger — and
`nodeSubtitle` against expression values, malformed URLs, arrays where objects
are expected, and missing parameters. `document.test.ts` gains the branch
collision case and an attachment-edge round trip. 53 → 75 tests.

## Not fixed, carried forward

The projection rebuild discards node measurements and handle bounds on every
selection change, forcing a full re-measure. Real and well-evidenced, but both
the `$effect` and `replaceDraft`'s eager rebuild predate this work, and
`replaceDraft`'s comment says it exists to fix a selection-loss bug. Reworking
that during a review pass risks regressing something deliberate.
