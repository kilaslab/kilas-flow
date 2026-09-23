---
id: FEAT-npc3ge
title: "Density and layout rhythm: 24/28/32 controls, 40 px rows, one content width, one page header, editor chrome sizes"
status: todo
priority: high
labels:
    - frontend
    - layout
    - density
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-09-23T03:05:16Z"
---

# Description

KilasFlow is already denser than n8n, but it is irregular. Five lists use five row heights (39, 44, 45, 49 and 50 px), and the editor has up to 17 distinct control heights on one screen (20–40 px). Pages use three content widths (896 px, 1024 px and full), every dashboard page repeats its title in the top bar and the H1, dialog inputs are 32 px while list inputs are 28 px, and the inspector is a fixed 320 px. This pass applies the v2 density spec (audit §9) without loosening the density.

# Acceptance Criteria
- [ ] measure.js reports ≤3 distinct interactive control heights per page, all in {24, 28, 32}, with 32 only under pointer: coarse (baseline: up to 17 on the editor).
- [ ] All five dashboard lists (workflows, executions, credentials, datastores, API keys) use 40 px single-line or 52 px two-line rows, and dense tables (executions, datastore grid) use 36 px. Verified by a row-height assertion in test:design-budget.
- [ ] Every dashboard list page uses one max content width (70rem) and one page-header pattern, with the H1 in the content and the top bar showing only the breadcrumb (no duplicated title text).
- [ ] The inspector opens at 360 px, can be resized between 320 and 560 px, and remembers its width; the version panel and node picker use the same right rail width. Chrome collisions stay with BUG-bcahaj.
- [ ] The spacing lint (part of lint:tokens) reports 0 arbitrary px spacing classes; all gaps and paddings are on the 4 px grid.

# Implementation Plan

Create ListRow and PageHeader primitives (extending the FEAT-ptyh9w list shell). Set the default button size to 28 and remove the size=sm 12.8 px variant. Coordinate the sidebar auto-collapse and inspector overlap with BUG-bcahaj.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
