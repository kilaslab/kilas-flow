---
id: FEAT-p75n7j
title: "Design tokens v2 (\"Kunyit\") and theme plumbing: namespaced --kf-* tokens, dark-first, one-value accent override"
status: todo
priority: high
labels:
    - frontend
    - design-system
    - theming
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:15Z"
updated: "2026-09-23T03:05:15Z"
---

# Description

Every colour token in web/src/app.css sits on hue 170. Background, card, hover surface, primary, success, ring and chart-1 are all one hue, and --success is literally --primary. As a result nothing in the UI carries meaning by colour, and it reads as a green-black cast. The tokens also stop at colour: radius is a single multiplier (which yields 7 radii in use), and there are no motion, density or elevation tokens. This ticket replaces lines 83–184 of app.css with the v2 "Kunyit" token block in the audit (§7): - cool slate neutrals at h 235 (C ≤ 0.012, never purple) in 5 surface steps; - the kunyit (turmeric) accent oklch(.82 .14 95) = #e1c34b (dark) and oklch(.82 .15 95) = #e3c23b (light), used as a fill with ink text and derived from one --kf-accent-src; - line-weight accent uses (links, focus, selection) shifting to deep ochre in light; - success 150, warning moved to h 60 (jingga) so it never shares the accent's hue, danger 25 and info 245; - categories, run states, canvas, stickies, data-viz, elevation, radii, type, motion and density. It keeps the dark-first arrangement (bare :root is dark; light is opt-in via [data-theme='light'] and .theme-light), and the shadcn names are kept as aliases so existing utilities keep working. The accent must stay outside the competitor exclusion zones measured in audit §6.1: n8n h 0–60 and qonflo.ai h 270–360 and 203.

# Acceptance Criteria
- [ ] app.css defines the v2 token groups (surfaces ×5, borders ×3, text ×4, accent steps derived from --kf-accent-src, success/warning/danger/info each with text, fill, fill-fg and subtle, 6 categories, 7 run states, canvas, sticky formula, viz ×6, shadows ×3, radii ×4, type ×6, motion, density) in both :root (dark) and [data-theme='light'], with --kf-accent-src = oklch(0.82 0.14 95) dark and oklch(0.82 0.15 95) light.
- [ ] The contrast unit test (a port of contrast.py over the tokens parsed from app.css) passes all 92 pairs in audit contrast-report-v2.txt in both themes. Minimums: text ≥4.5:1 (fg, fg-muted, fg-subtle on bg/raised/overlay; accent-fg on accent, accent-hover and accent-press; accent-text on bg; each status on raised and on its subtle; each *-fill-fg on its fill); focus, selection, edges and category glyphs ≥3:1.
- [ ] The hue test asserts these deltas: accent (95) vs success (150) ≥50°, vs danger (25) ≥60°, vs info (245) ≥90°, vs warning (60) ≥30°. Warning is additionally rendered only with a triangle glyph and never as a button fill. The accent hue is ≥40° from every competitor hue recorded in audit §6.1 (qonflo.ai 292/321/351/203, n8n 10/30/44/55). Neutral tokens have C ≤ 0.012 at h 235.
- [ ] Setting only --kf-accent-src on an element with [data-kf-brand] (the embed shell) recolours the primary button, its hover and pressed states, links, the focus ring, node selection and row selection in that subtree; a Playwright check on /embed with accent #0ea5e9 finds none of those at the default kunyit value. For host accents #0ea5e9, #1d4ed8, #16a34a, #dc2626, #0891b2 and #111827, the derived --kf-accent-fg reaches ≥4.5:1 on the accent fill (flip threshold L 0.60).
- [ ] A [data-theme='light'] subtree inside a dark page renders light surfaces, including the Svelte Flow canvas background and edges, because aliases are re-declared at theme boundaries. Verified by a component screenshot.
- [ ] The unused tokens --node-trigger/core/ai/data/imported and the --color-surface*/--color-ink* aliases are removed; `rg -e '--node-(trigger|core|ai|data|imported)'` returns 0 matches.

# Implementation Plan

Land the token block behind the existing names first (alias shadcn → --kf-*), so the swap is one PR with screenshot diffs from VR-03. Declare derived tokens on ':root,[data-theme],.theme-dark,.theme-light,[data-kf-brand]'. Update routes/embed/[id]/+page.svelte to set --kf-accent-src instead of --primary/--ring (the API itself stays with FEAT-yrnkz0). Make the theme-color meta follow --kf-bg per theme. Coordinate with FEAT-a3dwj2 (toggle) and FEAT-yrnkz0 (embed branding API).

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
