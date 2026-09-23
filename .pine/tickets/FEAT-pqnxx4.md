---
id: FEAT-pqnxx4
title: "Typography scale v2: one family (Atkinson Hyperlegible Next + Mono), six sizes, three weights, tabular numbers"
status: todo
priority: medium
labels:
    - frontend
    - typography
    - design-system
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:15Z"
updated: "2026-09-23T03:05:15Z"
---

# Description

Computed pages use 8 font sizes (8, 10, 11, 12, 12.8, 13, 14 and 16 px, plus 20 px in source) and 4 weights, and 142 of the text classes are arbitrary. The workflows list sets 76% of its text at 11 px, the executions table uses 14 px body text, and the shadcn sm button adds a stray 12.8 px. Monospace is used decoratively for node subtitles, port labels, edge labels and mode chips. Durations are not tabular, and the executions headers are uppercase. The v2 scale is 11/12/13/14/16/20 with a 13 px base, weights 400/500/600, and mono only for code, expressions and ids. The recommended face, Atkinson Hyperlegible Next with its Mono, is chosen for disambiguating characters in 11–12 px canvas labels at zoom < 1. It is self-hosted via @fontsource so the single binary stays offline-capable.

# Acceptance Criteria
- [ ] measure.js reports ≤6 distinct font sizes and ≤3 weights on every VR-03 surface; no computed font-size is below 11 px or equal to 12.8 px.
- [ ] 0 arbitrary text-[…] classes remain in web/src (baseline 142); the Tailwind text-* scale maps to 11/12/13/14/16/20 with the line heights in audit §7.
- [ ] Node subtitles, port labels, edge labels and badges render in the sans family; font-mono is used only for expressions, code, JSON and ids (a spot-check list in the PR covers canvas-node, execution-canvas-node, property-field and executions table).
- [ ] Durations, counts, timestamps and ids in tables use tabular-nums (the executions Duration column aligns on the decimal point in the screenshot diff); table headers are sentence case (0 `uppercase` in table headers).
- [ ] Fonts are served from the binary (no request to fonts.googleapis.com or fonts.gstatic.com in the network log), and the font payload added is ≤120 KB woff2 for the Latin subset.

# Implementation Plan

Swap @fontsource-variable/geist for the Atkinson Next and Mono variable packages (keep Geist behind a flag for one release if needed). Remap the Tailwind text scale in @theme and codemod the arbitrary sizes to the nearest token.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
