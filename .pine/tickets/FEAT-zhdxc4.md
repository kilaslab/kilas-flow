---
id: FEAT-zhdxc4
title: "Motion system and smoothness: tokens, panel choreography, animated viewport actions, skeletons, a real reduced-motion path"
status: todo
priority: high
labels:
    - frontend
    - motion
    - performance
    - accessibility
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-09-23T03:05:16Z"
---

# Description

Performance is fine (p95 17 ms per frame at 265 nodes, CLS ≤ 0.0007), but motion is either absent or ad hoc. The inspector appears in one frame (the pane jumps 1232→912 px). The picker pops in. Fit view and the zoom buttons jump. Ctrl+wheel zooms ×4 per notch. The editor loads behind a text line. Timing comes from four sources (Tailwind 150 ms, ports 120 ms ease, dialogs 100 ms, sheet 200 ms ease-in-out). prefers-reduced-motion is a global 0.01ms !important kill that also removes useful colour feedback. This ticket implements the motion spec in audit §8. The functional run animations stay with FEAT-ktasef and consume these tokens.

# Acceptance Criteria
- [ ] Every transition and animation in web/src uses --kf-dur-* and --kf-ease-* tokens: lint:tokens reports 0 raw duration-N classes or ms literals outside app.css (baseline: 63 default transitions, 6 duration-100, 1 duration-200, 120 ms ports, 200 ms sheet).
- [ ] The inspector opens and closes with a translate plus opacity over 220/140 ms. The canvas pane resizes once, and the viewport pans so the selected node stays visible. A Playwright trace on the 300-node fixture shows ≤2 frames over 33 ms during open.
- [ ] Fit view, zoom-to-node, the zoom buttons and the +/−/0/1 keys animate the viewport over --kf-dur-canvas, and Ctrl+wheel zoom is clamped to ≤×1.25 per 100 px wheel delta (baseline ×4).
- [ ] The editor, execution detail and inspector loading states render token-driven skeletons instead of 'Loading…' text, and CLS from skeleton to content is ≤0.01.
- [ ] Under prefers-reduced-motion: no transform or keyframe animation runs (checked with getAnimations()), colour and opacity feedback stays at ≤120 ms, and running edges and rings render static.

# Implementation Plan

Tokens first, then a small motion util (enter/exit classes on data-state) shared by Sheet, Dialog, Popover, the picker and the inspector. Use Svelte Flow's fitView({duration}) and zoomIn({duration}). Replace the reduced-motion block in app.css with token overrides.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
