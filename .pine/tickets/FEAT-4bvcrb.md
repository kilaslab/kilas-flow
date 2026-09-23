---
id: FEAT-4bvcrb
title: "Run-state visual language shared by the editor canvas and execution replay (running, success, warning, error, waiting, disabled, pinned)"
status: todo
priority: high
labels:
    - frontend
    - editor
    - canvas
    - execution
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-09-23T03:05:16Z"
---

# Description

Execution replay marks success by turning the node's border, ports and badge brand-teal, which is the same colour as the brand, and the editor has no run states at all. Disabled nodes look active (FEAT-ez6xtm) and pinned data has no visual. This ticket defines the visuals only, as one component both canvases use. The data flow and SSE subscription stay with FEAT-ktasef, the toggles with FEAT-ez6xtm, and pinning with FEAT-70j6dn. Each state is carried by shape plus colour, per audit §6: running is an info ring with a marching edge; success a check badge; warning a tangerine (h 60) triangle badge; error a danger border plus × badge; waiting a dashed info ring with a clock; disabled 45% opacity, dashed and struck through; pinned a pin badge in --kf-accent-text (turmeric on dark, ochre on light).

# Acceptance Criteria
- [ ] A single <RunStateDecoration state=…> (or CSS on data-run-status) is used by both canvas-node.svelte and execution-canvas-node.svelte; replay no longer recolours ports or edges with the brand accent.
- [ ] With a greyscale filter applied (test harness), each of the 7 states is still distinguishable in a snapshot of a fixture canvas showing one node per state.
- [ ] Badges are ≥14 px with ≥3:1 badge-to-canvas contrast, and any text in them (item counts) is ≥11 px at ≥4.5:1.
- [ ] Run animations use only the motion tokens (--kf-march for running, --kf-dur-emphasis for the settle) and fall back to static rings and dashed edges under prefers-reduced-motion.
- [ ] The succeeded replay screenshot (execution 'a HTTP 500 chain') shows the success, error and not-reached nodes in three distinct treatments, none of them in the turmeric accent hue (h 90–100).

# Implementation Plan

Ship the decorations with a story/fixture page first so FEAT-ktasef can wire live state into them. Item-count edge labels reuse the VR-06 pill.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
