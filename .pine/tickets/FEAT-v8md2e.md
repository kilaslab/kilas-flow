---
id: FEAT-v8md2e
title: 'RAG on the default SQLite install: a SQLite-backed vector store per tenant and memoryKey, n8n vectorStoreInMemory mapped onto it'
status: todo
priority: medium
labels:
    - n8n
    - parity
    - ai
    - rag
    - sqlite
parent: EPIC-8rbys7
created: "2026-10-09T11:26:08Z"
updated: "2026-10-09T11:26:08Z"
---

# Description

RAG does not run on the default SQLite install. The internal vector store (`kilasflow.vectorStore`) and everything that needs it refuse there with the pgvector install message (`nodes.VectorUnavailableReason`), so a fresh install cannot insert or retrieve documents at all. n8n's Simple Vector Store (`@n8n/n8n-nodes-langchain.vectorStoreInMemory`) works on any n8n install and appears in 6 templates. Here it imports as an unsupported placeholder.

Split out of FEAT-zm3wh2 on 2026-10-09 (its criteria "an in-memory store" and "RAG works on the default SQLite install"). The retrieve mode and the Vector Store Tool shipped there; this ticket is the store they need on SQLite. It also carries the `embeddingsOllama` mapping from audit finding AI-12, because local RAG on SQLite is that finding's scenario.

**Recommended shape: SQLite-backed, not process memory.** Rows in a SQLite table survive a restart and are covered by the tenant purge. A process-memory map loses every ingested document on restart and is invisible to `internal/tenantpurge`. Brute-force cosine over one tenant's rows is enough at the sizes this tier serves.

**Scope: per tenant plus memoryKey, not per execution.** The original FEAT-zm3wh2 text said "per tenant and execution". That contradicts how n8n uses the node: documents are ingested in one run and queried in a later one under the same memory key, so a store scoped to one execution would always be empty when queried.

**n8n wire-format facts (format only, read 2026-10-09; no n8n code copied):**
- `vectorStoreInMemory` shares the vector-store `mode` parameter: `load` (Get Many), `insert`, `retrieve` (the default when absent), `retrieve-as-tool`, `update`.
- `memoryKey` is a plain string up to typeVersion 1.1 and a resource locator `{__rl: true, mode: 'list', value}` from 1.2.
- `clearStore` (insert mode) defaults to false; true empties the key before inserting.
- Ports follow the shared vector-store rules: `ai_embedding` in always; `main` + `ai_document` in on insert; `ai_vectorStore` out on retrieve; `ai_tool` out on retrieve-as-tool.
- `embeddingsOllama`: a model name plus an `ollamaApi` credential carrying the base URL. Ollama serves an OpenAI-compatible `/v1/embeddings`.

**Why it is not small (each item is in scope):**
- `cmd/kilasflow/main.go` wires `VectorUnavailableReason`. On SQLite it must hand the nodes the SQLite store instead of a refusal.
- A migration that quotes identifiers and indents with spaces (`.pine/memory/persistence.md`), with `internal/tenantpurge` coverage in the same change (`TestEveryTenantTableIsPurgedOrExplicitlyExempt`). `migrations/sqlite` and `migrations/postgres` are numbered in step, so the PostgreSQL side needs a matching (possibly empty) file.
- e2e expectations flip: `e2e/tests/node-coverage.spec.ts` pins `kilasflow.vectorStore@1` (and "the internal vector store still needs pgvector") as refusals on SQLite.
- `internal/interop/n8n/n8n_test.go` `TestUnknownConnectionKindNamesBothEndpoints` uses `vectorStoreInMemory` as its unmapped type and must pick another.
- Install docs: the RAG section says PostgreSQL + pgvector is required.

# Acceptance Criteria
- [ ] A SQLite-backed implementation of the `nodes.VectorStore` interface (tenant + collection scoped, brute-force cosine search, metadata filter), used by the internal vector store on a SQLite install instead of the pgvector refusal
- [ ] Insert, Get Many, retrieve and retrieve-as-tool run end to end on SQLite with an embeddings sub-node, and documents ingested in one execution are found by a later one
- [ ] Its table is tenant-scoped and covered by `internal/tenantpurge`; the migration quotes identifiers and indents with spaces
- [ ] n8n `vectorStoreInMemory` imports onto it with `memoryKey` (string or v1.2 locator) as the collection and every mode mapped; `clearStore` is honoured or named in a lossy note; export writes it back
- [ ] n8n `embeddingsOllama` imports onto the Embeddings node with the Ollama base URL
- [ ] e2e node-coverage and the install docs say RAG runs on SQLite, with PostgreSQL + pgvector as the scale path

# Implementation Plan

1. A new `nodes/vectorstore_sqlite.go` implementing `VectorStore` over the install's SQLite handle. Embeddings are stored as JSON or packed float32, and cosine is computed in Go.
2. Wire it in `cmd/kilasflow/main.go` when the driver is sqlite; keep the pgvector path for PostgreSQL.
3. The migration and its tenantpurge entry land in the same commit.
4. Importer: `vectorStoreInMemory` → `kilasflow.vectorStore` with `collection` = memoryKey; `embeddingsOllama` → `kilasflow.embeddings`.
5. Flip the e2e and doc expectations.

# Notes

Related tickets: FEAT-zm3wh2 (retrieve mode and Vector Store Tool, done there) and FEAT-gcq50s (it listed the in-memory store; this ticket now owns it).

# Related Files
- nodes/pgvector.go — VectorStore interface, VectorUnavailableReason
- cmd/kilasflow/main.go — vector wiring
- internal/tenantpurge/
- internal/interop/n8n/n8n.go — mappings
- e2e/tests/node-coverage.spec.ts
