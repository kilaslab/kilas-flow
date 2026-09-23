---
id: FEAT-kpn0m3
title: "Inspector (NDV) visual pass: field anatomy, switches, segmented expr/fixed toggle, one tab style, max two nesting borders"
status: todo
priority: medium
labels:
    - frontend
    - editor
    - inspector
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-09-23T03:05:16Z"
---

# Description

The inspector works but is noisy. Booleans render as full-width bordered boxes holding a checkbox plus 'Enabled'/'Disabled'. The IF condition builder nests three bordered boxes. Mode toggles are tiny mono chips ('expr', 'fixed'). Tabs look different from the version panel's tabs. Labels, help text and controls vary between 11, 12 and 13 px. This pass defines one field anatomy (12/500 label, 28 px control, 12 px subtle help, 16 px gap between fields) and applies it to property-field.svelte and properties-panel.svelte. Data panes stay with FEAT-eqzpzq, parameter logic with FEAT-mxmjt7, and credential UX with FEAT-kcdrcy.

# Acceptance Criteria
- [ ] Boolean properties render as a 28 px row with a switch (not a bordered checkbox box); screenshot 20-inspector-agent is re-captured and shows switches.
- [ ] Expression and fixed mode is a 24 px segmented control using tokens, not mono text chips; the expression input uses the mono family and every other input uses sans.
- [ ] No inspector region has more than 2 nested bordered containers (a DOM check in the design-budget test on the IF, Switch and Set inspectors).
- [ ] One Tabs style (13 px/500, 2 px --kf-selection underline, 28 px tall) is used by the inspector, version panel and execution node panel; measure.js reports a single tab height and size across those three, and the underline is ≥3:1 against the panel in both themes.
- [ ] Inspector pages pass the VR-03 budgets (≤6 font sizes, ≤3 control heights, 0 contrast failures) in both themes.

# Implementation Plan

Extract a Field primitive (label, control, help, error) and a SegmentedControl. Migrate property-field kinds incrementally, booleans and modes first.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
