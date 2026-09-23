---
id: BUG-9pmv8y
title: Real RAG templates lose the main edge into the insert-mode vector store
status: todo
priority: high
labels:
    - n8n
    - importer
    - rag
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
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
- [ ] An n8n main edge into an insert-mode vector store is re-routed onto the attached Default Data Loader (or the store keeps a main input), with an info note
- [ ] Templates 3647, 16706 and 5908, from their real JSON, import with the ingest path connected and no held-back edges
- [ ] The hand-authored RAG fixtures are replaced or complemented by the real template JSON as regression fixtures

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
