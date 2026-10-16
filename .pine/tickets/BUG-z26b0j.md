---
id: BUG-z26b0j
title: CI's visual regression step would fail the e2e job on the first push, and its continue-on-error hid functional failures
status: todo
priority: medium
parent: EPIC-3en6xr
created: "2026-10-16T15:15:12Z"
updated: "2026-10-16T15:15:12Z"
---

# Description

The CI "Visual regression" step added with BUG-ywbvfa would turn the `e2e` job red on the first push. With no `e2e/visual/__screenshots__/linux/` baselines, the config used `updateSnapshots: 'missing'`, which in Playwright 1.63 writes the baseline AND records a soft error, so the step exits 1. Its `continue-on-error: ${{ hashFiles('e2e/visual/__screenshots__/linux/**') == '' }}` is evaluated after the step ran, when the step has just written those baselines, so it is false and the job fails. While it did apply, it also hid every functional assertion in the visual specs.

Found by the pre-push CI-readiness review on 2026-10-16.

# Steps to Reproduce

1. A clone with no linux baselines; `cd e2e && CI=1 pnpm exec playwright test -c playwright.visual.config.ts`.

# Expected

The first CI run writes the missing baselines and passes; plain `expect()` assertions keep gating.

# Actual

Every screenshot test fails with "A snapshot doesn't exist ..., writing actual."

# Acceptance Criteria
- [x] CI uses `updateSnapshots: 'changed'` while no linux baselines exist (writes missing snapshots without failing) and `'none'` once they do; local runs keep `'missing'`. `continue-on-error` is removed, so functional assertions gate. Evidence: a fresh clone with no baselines, `CI=1 pnpm exec playwright test -c playwright.visual.config.ts --workers=4` → 23 passed, baselines written, exit 0; Playwright 1.63's `handleMissing` returns pass for 'changed' and a soft error for 'missing'.
- [x] The `e2e` job has `timeout-minutes: 45`.
