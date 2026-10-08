---
id: BUG-9pmv8y
title: Real RAG templates lose the main edge into the insert-mode vector store
status: todo
priority: high
labels:
    - n8n
    - importer
    - rag
deps:
    - FEAT-zm3wh2
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-10-08T16:37:18Z"
---

# Description

This affects the very templates EPIC-hkypt5 targets (3647, 16706, 5908). Imported from their real JSON, each one's insert-mode PGVector store is cut off from its data. The hand-authored fixtures omit that edge, so the tests pass anyway.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-6). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** An insert-mode vector store has a **main** input: the items to embed arrive on it, and the Default Data Loader hangs off it as an `ai_document` sub-node. Templates 3647, 16706 and 5908, the ones EPIC-hkypt5 targets, all wire `Extract from … → Postgres PGVector Store` on main.

# Steps to Reproduce

1. Fetch the real template JSON from `api.n8n.io/api/templates/workflows/3647` (and 16706, 5908, 18957) and POST it to /api/v1/workflows/import.
2. Also import the minimal graph Manual → PGVector(`mode: insert`) ← Default Data Loader, Embeddings.

# Expected

The importer re-routes `X → store(insert)` main onto the attached Document Loader's main input (KilasFlow's shape), or the store keeps a main input in insert mode. Real templates should be the regression fixtures.

# Actual

- The lossy issue reads: "the \"main\" connection from \"Extract from PDF\" to \"Postgres PGVector Store\" was held back because \"Postgres PGVector Store\" declares no main port for it". It appears three times for 3647, once for 16706 ("Extract from File" → "Create HR Policies"), once for 5908 and once for 18957.
- The ingest store is left disconnected from its data. In 16706, the `ai_vectorStore` edge into the Vector Store Tool and its `ai_embedding` edge are held back too.
- The hand-authored fixtures in `internal/interop/n8n/testdata/n8n_rag_*.json` omit that main edge, so `TestHandAuthoredRAGFixturesImportWithoutPlaceholders` passes anyway.

# Acceptance Criteria
- [x] An n8n main edge into an insert-mode vector store is re-routed onto the attached Default Data Loader (or the store keeps a main input), with an info note
- [ ] Templates 3647, 16706 and 5908, from their real JSON, import with the ingest path connected and no held-back edges (ingest path: met for all three and 18957. 3647, 5908 and 18957 hold nothing back. 16706 still holds back its retrieval side, see Progress)
- [x] The hand-authored RAG fixtures are replaced or complemented by the real template JSON as regression fixtures (complemented: the real JSON cannot be committed, see Fix)

# Implementation Plan

In `importConnections`, rewrite main edges that target an insert-mode vector store onto the loader attached to it via `ai_document`, and report it as a `dropped`/info note. Replace the hand-authored RAG fixtures with the real template JSON.

# Notes

Related tickets: EPIC-hkypt5, FEAT-wwxwvq, FEAT-yxpj0d

Blocks the honest completion of EPIC-hkypt5 (8/9 done). Link it there when triaging.

Related (from the audit): EPIC-hkypt5 (doing). FEAT-wwxwvq and FEAT-yxpj0d are done and claim these template shapes import; with the real templates they do not.

# Related Files

- Import output recorded in this session. Workflows: "[node-gap] real RAG template 3647/16706/4799/5908", "[node-gap] real PGVector insert template 18957.json/2752".
- `nodes/rag.go:344-352`: insert mode declares only `embedding` + `document` inputs.
- `nodes/rag.go:333`: n8n's `retrieve` mode is folded into getMany, so no `ai_vectorStore` output exists.

# Attachments

## Fix (2026-10-08)

`importConnections` (`internal/interop/n8n/n8n.go`) now re-routes the edge. When a main edge cannot land on its target, the target declares a `document` input, and an `ai_document` edge names a loader for that target, the edge goes to the loader's main input instead. Each re-routed edge adds a `lossy` issue on the store that names the source and the loader. A store with no loader attached still holds the edge back, because the items have nowhere honest to go. Export does the inverse: a main edge into a loader that serves a store is written back onto that store, so the round trip opens in n8n with the items on the store's main input.

Fixtures: the real template JSON is user content, so it is never committed (`.pine/memory/licensing.md`). Two changes stand in for it instead:
- The four `testdata/n8n_rag_*.json` fixtures are rewired to n8n's real shape, with the items on the store's main input rather than on the loader. `TestHandAuthoredRAGFixturesImportWithoutPlaceholders` also fails now on any held-back edge.
- `TestRealRAGTemplatesKeepTheirIngestPath` imports 3647, 16706, 5908 and 18957 from their real JSON, read from `KILASFLOW_N8N_TEMPLATES_DIR` (`<id>.json`, exactly as api.n8n.io returns it). It skips when the variable is unset. Run against freshly fetched copies, all four pass, and the test logs whatever non-ingest edges are still held back.

Tests:
- `TestMainEdgeIntoAnInsertStoreLandsOnItsDataLoader`: two sources go to the loader, the issue is lossy, and the graph compiles.
- `TestMainEdgeIntoAnInsertStoreWithoutALoaderIsStillHeldBack`
- `TestReRoutedIngestEdgeExportsBackOntoTheStore`

## Progress

What is left is outside the ingest path. In 16706, the second "Postgres PGVector Store" carries no `mode`, so n8n's default `retrieve` applies. It feeds "Answer questions with a vector store" (toolVectorStore) over `ai_vectorStore`. That edge and the store's `ai_embedding` edge are still held back, because KilasFlow has no retrieve mode and no `ai_vectorStore` output (`nodes/rag.go`). FEAT-zm3wh2 (vector store retrieve mode and Vector Store Tool) owns that. Close this ticket once FEAT-zm3wh2 lands and `TestRealRAGTemplatesKeepTheirIngestPath/16706` logs nothing held back.
