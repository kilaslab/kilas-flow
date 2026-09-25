---
id: BUG-x94b3d
title: Three e2e specs expect a run refused at validation (422) and get it queued (202)
status: todo
priority: medium
labels:
    - e2e
    - validation
    - regression
created: "2026-09-25T13:26:40Z"
updated: "2026-09-25T13:26:40Z"
---

# Description

Three e2e tests expect a run to be refused at validation (422), and the server queues it instead (202). They fail on main as of 2026-09-25: they failed at 506d1d8 and again after the day's merges.

- `e2e/tests/node-coverage.spec.ts:917` "embeddings refuse without text; the internal vector store still needs pgvector". An Embeddings node with only `model`, and a Vector Store insert without pgvector, are expected to be refused with 422.
- `e2e/tests/node-coverage.spec.ts:959` "extract from file, customer pgvector and google nodes fail closed without their inputs".
- `e2e/tests/n8n-compare.spec.ts:687` "the editor-validated tier refuses with user-facing diagnostics".

# Steps to Reproduce

`cd e2e && npx playwright test tests/node-coverage.spec.ts tests/n8n-compare.spec.ts -g "embeddings refuse|extract from file|editor-validated tier"` gives `Expected: 422, Received: 202` for all three.

# Expected

Either the run-time validation refuses these documents again, or, if queuing them became the intended behaviour, the specs and the docs say so.

# Acceptance Criteria
- [ ] Find the commit that changed the verdict (git bisect over e2e with these three tests).
- [ ] Restore the refusal, or update the specs with the reason.
- [ ] All three pass.
