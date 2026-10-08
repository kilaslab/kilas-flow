---
id: BUG-x94b3d
title: Three e2e specs expect a run refused at validation (422) and get it queued (202)
status: done
priority: medium
labels:
    - e2e
    - validation
    - regression
created: "2026-09-25T13:26:40Z"
updated: "2026-10-08T15:59:02Z"
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
- [x] Find the commit that changed the verdict (git bisect over e2e with these three tests). The culprit is 018af94 (EPIC-hkypt5). I bisected at the Go level, compiling against the SQLite catalogue. At 018af94^, an Embeddings node with only `model` is refused with the pgvector install message. At 018af94 it compiles. The same commit rewrote the specs to expect a 422 naming "no text to embed". It also added Extract From File with a spec that expects a 422 for a missing binary. No validator at any commit gives either answer: both are per-item errors the executor raises. The specs have not passed since 018af94.
- [x] Restore the refusal, or update the specs with the reason. The specs are updated. Validation cannot restore the refusal: a manual run's input, or the node upstream, can carry the text or the file.
- [x] All three pass.

## Fix

Queuing is the intended behaviour. 018af94 decoupled Embeddings from pgvector on purpose: they call an OpenAI-compatible API. The unit tests `TestVectorNodesRefuseWithoutTheTier` and `TestEmbeddingsValidateRequiresAModel` already hold that contract. Whether an item carries `text`, or a file, is only known at run time. The Manual Trigger emits the run's `input`, so `POST /run {"input":{"text":"…"}}` feeds real text to the same document. A refusal at compile time would refuse runs that work.

- `e2e/tests/node-coverage.spec.ts` has a new `run-failed` tier, `RUN_FAILURE_COVERAGE`, which the coverage report counts. It holds `kilasflow.embeddings@1` and `kilasflow.extractFromFile@1`. Their two tests now expect a 202, a failed run, and a node error naming "no text to embed" or "binary storage|binary property". The internal Vector Store is still refused with 422 (pgvector).
- `e2e/tests/n8n-compare.spec.ts`: the Embeddings case in the editor-validated tier now expects the same run, accepted and then failed.
- `docs/src/content/docs/concepts/execution-model.md`: compilation judges the document, not the items. The section uses these two nodes as its examples.
- Go guard `nodes/item_inputs_test.go` `TestNodesThatNeedTheirItemsAreQueuedAndFailTheRunByName` runs on the SQLite catalogue. Both documents compile, and the runner fails the node by name. The same Embeddings document with text in its input gets past the text check. The vector store is still refused with pgvector. The guard pins the intended contract and does not reproduce an engine bug: the engine was right and the specs were wrong. The guard would fail against the compile verdict at 018af94^.

Verified:
- `go vet ./nodes/` and `go test ./nodes/` pass.
- `npx playwright test tests/node-coverage.spec.ts tests/n8n-compare.spec.ts -g "embeddings refuse|extract from file|editor-validated tier|coverage report"`: 4 passed.
