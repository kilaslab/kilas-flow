---
id: FEAT-zm3wh2
title: Vector store retrieve mode, Vector Store Tool, Qdrant and In-Memory stores
status: todo
priority: medium
labels:
    - n8n
    - parity
    - ai
    - rag
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-09-23T01:17:54Z"
---

# Description

Qdrant (22 templates) is the most-used store after PGVector, and the Vector Store Tool appears in 20. n8n's `retrieve` mode is folded into getMany, so no `ai_vectorStore` edge can exist.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-13). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Qdrant appears in 22 templates, Vector Store Tool in 20, Token Splitter in 16, Pinecone in 13, Supabase in 12, Vector Store Retriever in 8, In-Memory in 6 and Character Splitter in 1. The cluster appears in 6.3% of templates.

# Steps to Reproduce

1. Import each store in retrieve-as-tool mode on an agent with OpenAI embeddings, using real instances: qdrant 2840, pinecone 19501, supabase 5449, inMemory 6137.
2. Import toolVectorStore 3151 wired to a PGVector store.
3. Import the token and character splitters under a Default Data Loader.

# Expected

At least In-Memory and Qdrant (both REST, no SQL), `retrieve` mode with an `ai_vectorStore` output, the Vector Store Tool, and the token splitter import and run.

# Actual

- Every one is a blocking placeholder.
- With Vector Store Tool, the PGVector edge is also held back: "the \"ai_vectorStore\" connection from \"Postgres PGVector Store\" to \"Vector Store Tool\" was held back because \"Postgres PGVector Store\" declares no ai_vectorStore port for it". n8n's `retrieve` mode is folded into getMany (`nodes/rag.go:333`).

# Also found by the audit

## AI-12: RAG cannot run at all on the default SQLite install, and there is no Ollama embeddings path

*gap · medium · ai-agent*

**n8n:** The Simple Vector Store (in-memory) works on any install, including in retrieve-as-tool mode, and Embeddings Ollama embeds with a local model.

**Steps to reproduce:**

1. Build Manual → Agent ← Vector Store (`mode: retrieve-as-tool`, collection `ai_ollama_kb`) ← Embeddings (`mode: cluster`, Ollama base URL).
2. Validate and run.
3. Import an n8n workflow using `vectorStoreInMemory` + `embeddingsOllama`.

**Actual:**

- Validate: blocking `the embeddings and vector store nodes need PostgreSQL with the pgvector extension installed; this install runs on sqlite, where they are unavailable`, and run returns 422. The workflow saves anyway.
- Import: both nodes become unsupported placeholders.
- Separately, gemma4 cannot embed: Ollama `/v1/embeddings` answers `input after truncation exceeds maximum context length`. An embedding model (e.g. nomic-embed-text) would be needed, and I did not pull one, per the rules.

**Expected:**

An in-process or SQLite vector store (per tenant) so RAG and retrieve-as-tool work out of the box. `embeddingsOllama` maps onto the Embeddings node with the Ollama base URL.

**Suggested fix:**

Add a SQLite or in-memory vector store behind the `VectorStore` interface (brute-force cosine is fine at small scale), and map `vectorStoreInMemory` and `embeddingsOllama` in the importer.

**Evidence:**

`case-12.workflow.json`, `case-12-run-refused.response.json`, `case-12c.execution.json`, `case-import.response.json`. Code `nodes/pgvector.go:161-170`.

**Related:**

FEAT-gcq50s (todo, in-memory store listed), NG-13, NG-11


# Acceptance Criteria
- [ ] The vector store has a `retrieve` mode with an `ai_vectorStore` output
- [ ] `toolVectorStore` imports and runs over it
- [ ] An in-memory store (per tenant and execution) and Qdrant over REST
- [ ] The token splitter
- [ ] RAG works on the default SQLite install: an in-process or SQLite-backed vector store per tenant, so insert and retrieve-as-tool run without PostgreSQL

# Implementation Plan

Add a `retrieve` mode to the vector store (an `ai_vectorStore` output) and a `kilasflow.toolVectorStore` over it. Add an in-memory store per tenant and execution, then Qdrant over REST.

# Notes

Related tickets: FEAT-gcq50s

FEAT-gcq50s covers the splitters, in-memory, Supabase and retrieve-for-chain. Coordinate so the two are not built twice; this ticket adds Qdrant and the Vector Store Tool, which that ticket omits.

Related (from the audit): FEAT-gcq50s (todo) covers splitters, in-memory, Supabase and retrieve-for-chain. It does not list Qdrant, although Qdrant is the most-used store after PGVector, and it lists Pinecone as out of scope.

# Related Files

`results-ai.json` (Qdrant, Pinecone, Supabase, In-Memory Vector Store, Vector Store Tool, Token Splitter, Character Text Splitter), `probes/Vector_Store_Tool.json`

# Attachments
