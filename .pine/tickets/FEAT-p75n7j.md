---
id: FEAT-p75n7j
title: 'Design tokens v2 ("Kunyit") and theme plumbing: namespaced --kf-* tokens, dark-first, one-value accent override'
status: doing
priority: high
labels:
    - frontend
    - design-system
    - theming
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:15Z"
updated: "2026-10-09T11:03:29Z"
---

# Description

Every colour token in web/src/app.css sits on hue 170. Background, card, hover surface, primary, success, ring and chart-1 are all one hue, and --success is literally --primary. As a result nothing in the UI carries meaning by colour, and it reads as a green-black cast. The tokens also stop at colour: radius is a single multiplier (which yields 7 radii in use), and there are no motion, density or elevation tokens. This ticket replaces lines 83–184 of app.css with the v2 "Kunyit" token block in the audit (§7): - cool slate neutrals at h 235 (C ≤ 0.012, never purple) in 5 surface steps; - the kunyit (turmeric) accent oklch(.82 .14 95) = #e1c34b (dark) and oklch(.82 .15 95) = #e3c23b (light), used as a fill with ink text and derived from one --kf-accent-src; - line-weight accent uses (links, focus, selection) shifting to deep ochre in light; - success 150, warning moved to h 60 (jingga) so it never shares the accent's hue, danger 25 and info 245; - categories, run states, canvas, stickies, data-viz, elevation, radii, type, motion and density. It keeps the dark-first arrangement (bare :root is dark; light is opt-in via [data-theme='light'] and .theme-light), and the shadcn names are kept as aliases so existing utilities keep working. The accent must stay outside the competitor exclusion zones measured in audit §6.1: n8n h 0–60 and qonflo.ai h 270–360 and 203.

# Acceptance Criteria
- [x] app.css defines the v2 token groups (surfaces ×5, borders ×3, text ×4, accent steps derived from --kf-accent-src, success/warning/danger/info each with text, fill, fill-fg and subtle, 6 categories, 7 run states, canvas, sticky formula, viz ×6, shadows ×3, radii ×4, type ×6, motion, density) in both :root (dark) and [data-theme='light'], with --kf-accent-src = oklch(0.82 0.14 95) dark and oklch(0.82 0.15 95) light.
  - Evidence: `web/src/lib/design-system/tokens.groups.test.ts` ("defines every v2 group in dark/light", "seeds the accent from kunyit in each theme", "re-declares in the light block every token the dark block declares", "declares every alias on the boundary list").
- [x] The contrast unit test (a port of contrast.py over the tokens parsed from app.css) passes all 92 pairs in audit contrast-report-v2.txt in both themes. Minimums: text ≥4.5:1 (fg, fg-muted, fg-subtle on bg/raised/overlay; accent-fg on accent, accent-hover and accent-press; accent-text on bg; each status on raised and on its subtle; each *-fill-fg on its fill); focus, selection, edges and category glyphs ≥3:1.
  - Evidence: `tokens.contrast.test.ts` over `contrast-pairs.ts`: 44 pairs per theme (88), plus 15 alias guards per theme, all pass. contrast-report-v2.txt and contrast.py are not on this machine, so the list was rebuilt from this criterion's wording (see Progress); reconcile to the 92 if the report is recovered, by adding pairs, never dropping one (the count test fails on a removal).
- [x] The hue test asserts these deltas: accent (95) vs success (150) ≥50°, vs danger (25) ≥60°, vs info (245) ≥90°, vs warning (60) ≥30°. Warning is additionally rendered only with a triangle glyph and never as a button fill. The accent hue is ≥40° from every competitor hue recorded in audit §6.1 (qonflo.ai 292/321/351/203, n8n 10/30/44/55). Neutral tokens have C ≤ 0.012 at h 235.
  - Evidence: `tokens.hue.test.ts` (status and fill hues per theme, competitor hues, neutral chroma on 19 tokens and on every oklch() literal written on h 235, and an AST source scan with fixture self-tests: no solid warning fill on a button or in buttonVariants, and every file with a meaningful solid warning fill imports TriangleAlert).
- [x] Setting only --kf-accent-src on an element with [data-kf-brand] (the embed shell) recolours the primary button, its hover and pressed states, links, the focus ring, node selection and row selection in that subtree; a Playwright check on /embed with accent #0ea5e9 finds none of those at the default kunyit value. For host accents #0ea5e9, #1d4ed8, #16a34a, #dc2626, #0891b2 and #111827, the derived --kf-accent-fg reaches ≥4.5:1 on the accent fill (flip threshold L 0.60).
  - Evidence: `e2e/tests/embed-branding.spec.ts` (passes; red on main: no brand shell). Host accents: `tokens.contrast.test.ts` "host accents", both themes: 6.23, 6.51, 5.21, 4.69, 4.68, 17.24.
- [x] A [data-theme='light'] subtree inside a dark page renders light surfaces, including the Svelte Flow canvas background and edges, because aliases are re-declared at theme boundaries. Verified by a component screenshot.
  - Evidence: `e2e/visual/theme-subtree.spec.ts` (computed canvas L 0.972, edge stroke equals the light --kf-edge 0.62, body stays dark at 0.172, plus the screenshot). darwin baseline generated locally (gitignored); the linux baseline comes from the first CI run (see Progress).
- [x] The unused tokens --node-trigger/core/ai/data/imported and the --color-surface*/--color-ink* aliases are removed; `rg -e '--node-(trigger|core|ai|data|imported)'` returns 0 matches.
  - Evidence: `rg -e '--node-(trigger|core|ai|data|imported)' web docs` → 0 (`.pine/` keeps the history); `tokens.groups.test.ts` "v1 leftovers".

# Implementation Plan

Land the token block behind the existing names first (alias shadcn → --kf-*), so the swap is one PR with screenshot diffs from VR-03. Declare derived tokens on ':root,[data-theme],.theme-dark,.theme-light,[data-kf-brand]'. Update routes/embed/[id]/+page.svelte to set --kf-accent-src instead of --primary/--ring (the API itself stays with FEAT-yrnkz0). Make the theme-color meta follow --kf-bg per theme. Coordinate with FEAT-a3dwj2 (toggle) and FEAT-yrnkz0 (embed branding API).

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments

## Progress (2026-10-09)

Track I, worktree branch `worktree-agent-aa56c84abae43f91e`. Commits: the token swap (`FEAT-p75n7j: app.css carries the Kunyit v2 tokens…`), plus the BUG-ywbvfa commits that share the harness.

What changed:
- `web/src/app.css`: theme-independent scales (type, radii, density, motion, focus geometry; `--radius` moved here), the dark and light colour blocks, and the derived/alias block on `:root,[data-theme],.theme-dark,.theme-light,[data-kf-brand]`, with an `@supports not (color: oklch(from red l c h))` block of static default-kunyit values for the six relative-colour tokens (held within ΔE(OK) 0.01 by a test). Colour utilities are `@theme inline`, so `bg-primary` follows a light or brand subtree. The `.svelte-flow` block follows the draft; `::selection` uses `--kf-text-selection`.
- Tests and tools in `web/src/lib/design-system/`: `color.ts` (dependency-free OKLCH/hex/rgb/oklab/color-mix/relative-colour engine, WCAG contrast, gamma-sRGB compositing), `css-tokens.ts` (reads app.css into scales/dark/light/derived/fallback and resolves var() per theme and per override), `contrast-pairs.ts`, `tokens.contrast|hue|groups.test.ts`, `theme-color.ts`.
- Embed: `data-kf-brand` on the shell and `--kf-accent-src: <accent>` (sanitiser unchanged); the embedding guide says what `accent` recolours and that the text on it flips between ink and white.
- Node selection uses `var(--kf-selection)` and `0 0 0 2px var(--kf-selection-halo), var(--kf-shadow-1)` (node-visual.ts).
- The theme-color meta follows `--kf-bg` (watchThemeColor observes `data-theme`/`class` on `<html>`, so FEAT-a3dwj2's toggle needs no wiring).
- The root page uses the aliases instead of `--color-ink*`/`--color-surface*`; sonner uses `var(--popover)` etc. (the `--color-*` variables are no longer emitted for unused entries under `@theme inline`).

Rebuilt contrast pair list (44 per theme; text ≥4.5, non-text ≥3):
- text: fg, fg-muted, fg-subtle on bg, raised, overlay (9); accent-fg on accent, accent-hover, accent-press (3); accent-text on bg (1); success, warning, danger, info on raised (4) and on their own subtle composited over raised (4); each *-fill-fg on its fill (4);
- non-text: focus and selection on canvas, bg, panel, raised, overlay (10); edge, edge-hover, edge-attachment on canvas (3); the six categories on raised (6).
- Measured worst cases: dark fg-subtle on overlay 4.87, danger on its subtle 4.83, danger-fill-fg 4.70, edge-attachment 3.26; light success on its subtle 4.63, warning on its subtle 4.69, focus on panel 4.25, selection on panel 3.29, edge-attachment 3.22.

Deviations from the audit draft (all measured by the tests above):
- Light `--kf-warning` .54 → .52 (on its subtle the draft was 4.34:1, now 4.69).
- `--kf-edge-attachment` dark .48 → .50, light .66 → .63 (draft 2.996 and 2.87:1, now 3.26 and 3.22).
- `--kf-accent-fg`: the white branch has no chroma, `clamp(0,(l - 0.60)*1000,0.03)` (the draft's tinted white gave 4.495:1 on #dc2626, now 4.69).
- C .015 → .012 for light `--kf-scrim`, `--kf-shadow-1/2/3` and `--kf-node-shadow`; also light `--kf-fg-muted` (.014) and `--kf-fg-subtle` (.013) → .012, which the draft missed against its own "neutral C ≤ .012" rule (contrast 7.42 and 5.26 on bg).
- Aliases: `--muted: var(--kf-hover)` (not panel: 55 `hover:bg-muted` sites would go darker than the cards they hover); `--destructive: var(--kf-danger)` (text weight, `text-destructive` is common; 6.58:1 on bg) with `--destructive-foreground` ink `oklch(.20 .04 25)` in dark and `.99` in light, declared on the theme blocks; `--warning-foreground` is white in light (ink on the .52 warning was ~3.6:1).
- `--color-border-subtle` is kept, re-pointed at `--kf-border-subtle` (a `border-border-subtle` utility); the root page now uses `border-border`.
- `--code-comment` and `--code-property` moved from h 170 to the neutral hue at C .012.
- The lossy import badge on a canvas node is an outlined warning chip with the TriangleAlert glyph instead of a solid warning fill: it is a `<button>`, and warning is never a button fill.
- The editor toolbar's two primary buttons (Add step, Execute) use `--kf-accent-hover`/`--kf-accent-press` instead of `hover:opacity-90`, so an embed's hover and pressed shades follow the host accent.
- Typography, radii and motion tokens are defined but not applied (FEAT-pqnxx4, FEAT-npc3ge, FEAT-zhdxc4); the category ramp, ports and edge chrome are FEAT-xj5tv6's; sticky formula parameters are added next to the seven consumed `--kf-sticky-N-*` (BUG-rbask0's successor).

Not in scope, recorded where it belongs:
- Light `text-primary` (26 sites) is 1.7:1 on white: FEAT-r8ph93 moves them to `--kf-accent-text` (note added there). Light is reachable only by forcing `data-theme` until FEAT-a3dwj2. Only the Button `link` variant was moved here.
- Visual baselines: darwin ones are generated locally and gitignored; the linux ones come from the first CI run's `visual-regression` artefact and must be committed to `e2e/visual/__screenshots__/linux/` (until then the CI visual step is `continue-on-error`).

