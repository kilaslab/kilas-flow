---
id: FEAT-38je8w
title: "Iconography: lucide at 14/16/20 px with a normalised 1.5 stroke, harmonised node glyph chips, labelled toolbar"
status: todo
priority: low
labels:
    - frontend
    - iconography
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-09-23T03:05:16Z"
---

# Description

The icons all come from lucide (80 icons), which is good, but they appear at 12, 14, 16, 20, 24 and 28 px, all at stroke 2. At 12 px (size-3 ×83) the stroke reads bold next to 11–12 px text, and canvas glyphs are 24 px inside a 30 px chip. Server iconURL brand SVGs (some filled) sit beside outline glyphs without normalisation. The editor toolbar has 9 unlabelled icon buttons next to two filled text buttons. Fallback icon selection stays with BUG-15st2k.

# Acceptance Criteria
- [ ] Icons render at 14, 16 or 20 px only (canvas glyph chips 20 px), with absoluteStrokeWidth 1.5. A DOM check finds no lucide svg outside {14, 16, 20} px on the VR-03 surfaces.
- [ ] Brand SVGs from iconURL render inside the same chip at 20 px, with the chip background from the category token, so filled and outline marks sit at the same optical size (visual diff on the RAG and Telegram fixtures).
- [ ] The editor toolbar has exactly one filled button (the primary action). Every icon-only button has a tooltip showing its shortcut and an aria-label (Playwright check).
- [ ] The icon size rule is part of lint:tokens (no size-3 or size-7 on lucide icons).

# Implementation Plan

Wrap lucide in an <Icon size='sm|md|lg'> that sets the size and absoluteStrokeWidth, then codemod.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
