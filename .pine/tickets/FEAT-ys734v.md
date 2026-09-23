---
id: FEAT-ys734v
title: "Node picker visual pass: right-rail panel, category glyph rows, no backdrop blur, animated entrance"
status: todo
priority: medium
labels:
    - frontend
    - editor
    - node-picker
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-09-23T03:05:16Z"
---

# Description

The node picker is a centred modal that pops in with no transition and a backdrop-blur-sm over the live canvas, which is costly on large graphs and hides the context the node is being added to. Rows are 44 px with 16 px chips in arbitrary node colours, and the highlighted row is brand-tinted. The v2 picker is a 360 px right-rail panel (the same rail as the inspector) with sticky category headers, 40 px rows carrying category glyph chips, a neutral --kf-hover highlight with a --kf-selection left rule for the keyboard-active row, and a 220/140 ms entrance and exit. Search behaviour stays with BUG-n9a6bz and grouping/aliases with FEAT-4bjfny.

# Acceptance Criteria
- [ ] No backdrop-filter is applied while the picker is open (computed style check); the canvas stays visible and unblurred.
- [ ] The picker opens with translate plus opacity over --kf-dur-panel using --kf-ease-out and closes over --kf-dur-exit; no frame exceeds 33 ms while opening on the 300-node fixture.
- [ ] Rows are 40 px, glyph chips use category tokens, and the keyboard-highlighted row shows --kf-hover plus a 2 px --kf-selection rule that is visible in both themes (≥3:1 against the row background).
- [ ] The Tab/N shortcut and the '+' on a node open the picker without shifting the canvas viewport (the selected node's screen position is unchanged).

# Implementation Plan

Reuse the Sheet primitive with side=right and the v2 timing. Keep the command-palette keyboard model.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
