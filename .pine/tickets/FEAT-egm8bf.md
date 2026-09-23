---
id: FEAT-egm8bf
title: "Visual regression harness (Playwright screenshots, both themes, two viewports) and a token lint with value budgets"
status: todo
priority: high
labels:
    - frontend
    - testing
    - ci
    - design-system
    - visual-revamp
parent: EPIC-3en6xr
created: "2026-09-23T03:05:15Z"
updated: "2026-09-23T03:05:15Z"
---

# Description

Nothing currently stops a hard-coded colour, an arbitrary text size or a new radius from landing. The audit found 15 hex literals, 10 oklch/color-mix literals in TS/Svelte, 18 raw palette classes, 142 arbitrary text sizes and at least 6 arbitrary radii. A revamp of this size also needs before/after evidence. This ticket lands first, so the baseline is captured before VR-01 swaps the tokens.

# Acceptance Criteria
- [ ] `pnpm test:visual` captures these 12 surfaces × 2 themes × 2 viewports (1440×900, 1280×800) against a seeded fixture workspace: workflows list, editor with a 9-node agent workflow, editor with an imported sticky-heavy template, inspector open, node picker, version panel, chat panel, executions list, failed execution replay, credentials with dialog open, datastore detail, settings. It fails on a pixel diff above threshold and uploads diffs as CI artefacts.
- [ ] `pnpm lint:tokens` fails on hex, rgb(), hsl() or oklch() literals outside app.css, on raw Tailwind palette classes, on arbitrary text-[…], rounded-[…] and duration-[…] classes, and on duration-N values outside the motion token set. It starts in report mode with the audited baseline counts and is flipped to blocking once VR-01/02/04 land.
- [ ] `pnpm test:design-budget` runs measure.js (from the audit) on the same surfaces and asserts per page: ≤6 font sizes, ≤3 font weights, ≤4 radii, ≤3 control heights among {24, 28, 32}, 0 text contrast failures, and ≤12 distinct background colours on editor pages.
- [ ] The harness disables animations and waits for fonts and network idle, so reruns on an unchanged build produce 0 diffs (flake check: 3 consecutive runs).

# Implementation Plan

Reuse the existing browser e2e harness (FEAT-cx3hq1). Seed a deterministic workspace via the API (no reliance on other agents' data). Store baselines per theme and viewport.

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments
