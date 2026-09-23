---
id: FEAT-f3hx3a
title: "Dashboard lists and tables: one row and actions pattern, styled selects and file inputs, tabular numbers, consistent empty and loading states"
status: todo
priority: medium
labels:
    - frontend
    - dashboard
    - tables
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-09-23T03:05:16Z"
---

# Description

Four row-action patterns do the same job. Credentials have an 'Edit' button plus an always-red trash icon, datastores have grey pencil and trash icons, workflows repeat an 'Activate' outline button on all 239 rows plus '⋯', and settings uses a red 'Revoke' text button. Filters use native <select> (in 8 files) and native file inputs with OS chrome in dark mode. The list skeleton exists but is nearly invisible (about 1.1:1). This ticket unifies them on the VR-05 ListRow and Table primitives. Feature work stays with FEAT-zn5rqy (executions actions), BUG-605n21 (data table content) and FEAT-0895qc.

# Acceptance Criteria
- [ ] Every dashboard list uses one actions pattern: a primary inline action (optional) plus an overflow menu, with icon buttons at 24 px that have tooltips; destructive actions are neutral until hover or focus and turn --kf-danger only inside the menu or confirm. A visual diff covers the 5 lists.
- [ ] 0 native <select> and 0 unstyled native file inputs remain in dashboard routes (baseline: native selects in 8 files, file inputs in 2), and all replacements are keyboard-operable (Playwright keyboard test on each filter).
- [ ] The workflow active state is a switch or badge in the row, not a repeated 'Activate' button; measure.js counts ≤1 filled or outline button per row.
- [ ] Skeleton rows use --kf-skeleton at ≥1.3:1 against the list background, and every empty state uses the shared EmptyState (icon, one line, primary action).
- [ ] Numeric columns (duration, rows, columns, revisions) use tabular-nums and right alignment.

# Implementation Plan

Build on FEAT-ptyh9w's list shell. Replace native selects with the existing bits-ui Select. Style the file input via a Dropzone button.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
