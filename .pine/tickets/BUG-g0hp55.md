---
id: BUG-g0hp55
title: Four e2e specs were stale after the tool forms, the vector-store tool and the executionOrder fix
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-16T15:19:52Z"
updated: "2026-10-16T15:19:52Z"
---

# Description

The pre-push e2e run on 2026-10-16 found four functional specs red on main, each a stale expectation rather than a product bug:

1. `n8n-compare.spec.ts` "the executable-node matrix names every live catalogue entry in exactly one tier": `e2e/fixtures/n8n-live.ts` lacked `kilasflow.gmailTool@1`, `kilasflow.postgresTool@2`, `pack.telegramTool@1` and `kilasflow.vectorStoreTool@1`, added by FEAT-cf5n4j and FEAT-zm3wh2. This one runs in CI.
2–3. `waha-migration.spec.ts` (two tests) still expected a `dropped` `settings` issue that BUG-jx94q9 correctly removed (the template's only setting is `executionOrder: "v1"`). Gated on the local WAHA corpus, so CI never ran them.
4. `waha-migration.spec.ts` "an unmapped node arrives as a capsule…" clicked the tile's name label (`pointer-events-none`), and then looked for the original n8n type as free text although it is a field value.

# Acceptance Criteria
- [x] The four tool forms are in the matrix's `EDITOR_VALIDATED` tier.
- [x] The WAHA diagnostics expect three dropped entries, with the reason in the comment.
- [x] The capsule test clicks the node wrapper and asserts the "Original node type" field's value.
- [x] `KILASFLOW_E2E_PREBUILT=1 pnpm exec playwright test tests/n8n-compare.spec.ts tests/waha-migration.spec.ts` → 19 passed, 5 skipped (live-n8n gated), 0 failed.
