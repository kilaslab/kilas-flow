---
id: FEAT-r8ph93
title: "Colour system: separate the brand accent from semantic states everywhere (badges, banners, links, run results)"
status: todo
priority: high
labels:
    - frontend
    - design-system
    - color
    - accessibility
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:15Z"
updated: "2026-09-23T03:05:15Z"
---

# Description

Brand teal currently means "primary action", "link", "selected", "focused" and "succeeded" all at once. On /executions, 29 of 138 text elements plus 12 pill fills are brand teal, and in execution replay every succeeded node, port and edge turns brand-coloured. With tokens v2 in place, every usage has to move onto the right role: accent only for the primary action, selection, focus and links; success, warning, danger and info for state, always paired with an icon or shape. This ticket also fixes the colour pairs that fail today: the canvas count badge (white 8 px on #ec5c52, 3.24:1), the Failed badge (4.36:1 dark, 4.37:1 light) and white on --destructive (3.38:1). It overlaps with BUG-namghh's list; the failing pairs are fixed here structurally, and BUG-namghh can be closed against this ticket's scan.

# Acceptance Criteria
- [ ] On /executions (dark and light) the accent colour appears only on links and the primary button: measure.js counts 0 status badges using --kf-accent or --kf-accent-text, 0 links using --kf-success, and 0 elements using turmeric (h 90–100) for a status.
- [ ] Every status badge (Succeeded, Failed, Running, Waiting, Cancelled, Draft/Active) renders an icon or dot plus a label and passes ≥4.5:1 on its subtle background in both themes (measure.js contrastFailCount = 0 on /executions, /executions/<id> and /app/workflows).
- [ ] The canvas import/validation badges pass ≥4.5:1 and are at least 11 px (no text-[0.5rem] remains).
- [ ] 0 raw Tailwind palette classes remain (baseline: 18, e.g. text-red-600, text-emerald-400, border-violet-500/30, text-neutral-800); `rg '(bg|text|border|ring)-(red|green|emerald|violet|amber|neutral)-[0-9]'` in web/src returns nothing.
- [ ] Links use --kf-accent-text and never shadcn's --accent (the hover surface); root-page links pass ≥4.5:1 in both themes (BUG-namghh case).

# Implementation Plan

Introduce a <StatusBadge status=…> primitive fed by a single status→token map shared with execution.ts, and migrate lists, replay and banners to it. Audit every text-primary/text-success usage and re-map it by role.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
