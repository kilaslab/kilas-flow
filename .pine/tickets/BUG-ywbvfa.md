---
id: BUG-ywbvfa
title: "Focus, hover and pressed states are inconsistent: three focus styles, and the primary button's ring is invisible"
status: todo
priority: high
labels:
    - frontend
    - accessibility
    - focus
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-09-23T03:05:16Z"
---

# Description

Keyboard focus is drawn three ways. Nav and links get a 2 px solid outline at 2–4 px offset. shadcn buttons and inputs get a 3 px box-shadow ring at 50% of the ring colour, and on the teal primary button that ring is teal on teal, which is effectively invisible (74-focus-button-dark.png). Hand-rolled inputs and selects (search and filters) fall back to the browser's `outline: auto 1px`. The pressed state exists only on buttons (translate 1 px), and canvas node selection takes each node's own colour. v2 defines one focus ring (2 px solid --kf-focus, 2 px offset, inset −1 px in dense lists and grids), one hover (--kf-hover), one pressed (--kf-pressed plus 1 px translate on buttons) and one selection (--kf-selection).

# Acceptance Criteria
- [ ] Tabbing through /app/workflows, the editor toolbar, the inspector and a dialog shows the same focus outline on every stop: a Playwright test records the outline style, width and colour on 30 consecutive Tab stops and finds exactly one combination (baseline 3).
- [ ] The focus ring passes ≥3:1 against the surface it is drawn on in both themes: turmeric oklch(.82 .13 95) on dark, ochre oklch(.56 .11 75) on light. Around filled controls, including the turmeric primary button, the 2 px offset gap shows the surface between ring and fill. This is verified by a zoomed screenshot of the focused primary button in both themes.
- [ ] Every interactive element has distinct hover, pressed, focus-visible and disabled states; a state-matrix story for Button, IconButton, Input, Select, Switch, Tab, MenuItem, ListRow and canvas Node is captured in the VR-03 suite.
- [ ] No focus indicator is removed without a replacement: 0 `outline-none` without a matching focus-visible style (lint).

# Implementation Plan

Set the global :focus-visible rule from the token block and remove the ring-3/ring-ring/50 variants from the shadcn primitives. Hand-rolled inputs adopt the Input primitive (VR-11).

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
