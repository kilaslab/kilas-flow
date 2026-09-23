---
id: FEAT-xj5tv6
title: "Canvas visual language: category glyph colours, universal selection, neutral ports and edges, edge-label pills, dark stickies"
status: todo
priority: high
labels:
    - frontend
    - editor
    - canvas
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-09-23T03:05:16Z"
---

# Description

Node tile tint, chip border, ports, the '+' hover and the selection ring all take definition.iconColor. That is a free hex string with 24 distinct values in nodes/*.go, including #000000, #ef4444 and #ea4335. As a result one 9-node canvas paints 7 tint colours and 25 border colours, a selected IF node gets an orange 'warning' ring, and Gmail looks like an error. Edges (L 0.74, 8.3:1) compete with labels, and stickies use light pastels on the dark canvas. This ticket applies the v2 canvas grammar: 6 categories coloured by glyph and chip only (jade trigger, sky AI, leaf flow, steel data, neutral app and utility; no violet, magenta, pink or orange); a harmoniser that clamps brand glyph colours into the palette's lightness and chroma band; one selection ring for every node (--kf-selection: turmeric on dark, deep ochre on light); neutral ports whose shape carries the kind; quieter edges; sans edge labels on pills; and token-driven sticky fills. Node geometry is 64 px for steps and 44 px for sub-nodes. It is coordinated with BUG-15st2k (port and label geometry, placeholders, fallback icons), BUG-rbask0 (sticky readability and z-order) and BUG-sgrxhh (error branch drawing), which own the functional fixes.

# Acceptance Criteria
- [ ] Tile chrome (border, ports, selection, hover affordances) uses no per-node colour. On the 'Customer support agent' canvas measure.js finds ≤6 node glyph colours (one per category) and ≤8 distinct border colours (baseline 7 tints, 25 borders).
- [ ] Selecting any node (trigger, AI, flow, data, app, utility) draws the same --kf-selection ring and halo (turmeric on dark, ochre on light, ≥3:1 against the canvas in both themes); a screenshot test selects one node of each category and compares ring pixels.
- [ ] Edges pass ≥3:1 against the canvas and stay below the node label contrast; edge labels render as sans 11 px on --kf-raised pills, offset from the line (label placement logic stays with BUG-15st2k).
- [ ] Sticky notes render from the --kf-sticky formula in both themes; node labels on a sticky pass ≥4.5:1 in dark (templates 1750, 1747 and 6270 in the visual suite).
- [ ] Brand glyph colours from iconColor or iconURL pass through the harmoniser (oklch(from <c> var(--kf-glyph-l) min(c,.12) h)); no glyph renders black-on-dark or in a red hue within 20° of --kf-danger unless the node is in an error state.
- [ ] No category token has a hue in 0–60 or 270–360 (competitor exclusion zones, audit §6.1); the hue test parses --kf-cat-* in both themes.

# Implementation Plan

Add a category field to node-visual.ts, derived from the registry group and codex rather than from iconColor (mapping table in audit §7). Move the inline shadow and border computation into CSS on data-attributes. Replace sticky.ts PALETTE hex with hue indices plus the token formula. Share one decorator stylesheet between canvas-node and execution-canvas-node.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
