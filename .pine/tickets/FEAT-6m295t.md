---
id: FEAT-6m295t
title: "Light theme parity: a composed light palette (not an inversion), the same brand hue, and contrast-clean on every surface"
status: todo
priority: medium
labels:
    - frontend
    - theming
    - light-theme
    - accessibility
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-09-23T03:05:16Z"
---

# Description

The light palette ships but is unreachable (FEAT-a3dwj2 adds the toggle). Forced on, it mostly works, but the brand inverts from L 0.74 mint to L 0.44 dark jade, node borders are nearly black (L 0.42) on white tiles, the Svelte Flow attribution is 2.72:1, datastore type labels are 3.27:1, and the replay's Failed badge is 4.37:1. This ticket delivers the v2 Kunyit light values from audit §7 as a composed theme: - turmeric stays the fill (oklch(.82 .15 95) = #e3c23b, ink text 9.9:1); - line-weight accent uses move to deep ochre: links oklch(.48 .10 80) = #7b5600, focus oklch(.56 .11 75) = #9a6a17, selection oklch(.62 .12 85), because turmeric lines on white fall under 3:1; - white raised surfaces on a cool 0.985 page (h 235), a 0.962 panel for chrome, and soft two-layer shadows.

# Acceptance Criteria
- [ ] measure.js reports 0 text contrast failures on all 12 VR-03 surfaces with data-theme='light' (baseline: 1–3 per editor, replay and datastore page).
- [ ] --kf-accent-src uses the same hue (95) in both themes with ΔL ≤ 0.01 and ΔC ≤ 0.01. The test parses both theme blocks, and also asserts that in light, links, focus and selection resolve to the ochre values (≥4.5:1, ≥3:1 and ≥3:1 respectively).
- [ ] Node tiles in light use --kf-border-strong (L ≈ 0.80), not a near-black border, and the light canvas screenshot has no border darker than L 0.60 except on selected or error nodes.
- [ ] Stickies, the minimap, controls, the attribution, the chat panel and the node picker all follow the light tokens (visual suite covers each).
- [ ] The theme-color meta and color-scheme switch with the theme (checked in both themes).

# Implementation Plan

Pair with FEAT-a3dwj2 for the toggle and FEAT-yrnkz0 for embed theme selection. Keep the light theme opt-in; do not follow the OS preference unless the toggle is set to 'system'.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
