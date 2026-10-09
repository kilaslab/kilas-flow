---
id: FEAT-zm3wh2
title: Vector store retrieve mode, Vector Store Tool, Qdrant and In-Memory stores
status: doing
priority: medium
labels:
    - n8n
    - parity
    - ai
    - rag
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-10-09T11:03:29Z"
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
- [x] The vector store has a `retrieve` mode with an `ai_vectorStore` output (both `kilasflow.vectorStore` and `kilasflow.vectorStorePGVector`; `nodes/vectorstore_retrieve_test.go`: `TestRetrieveModeDeclaresAnEmbeddingInputAndAVectorStoreOutput`, `TestInternalVectorStoreRetrieveEmitsAStoreDescriptor`, `TestPGVectorRetrieveEmitsAStoreDescriptorWithoutOpeningTheDatabase`)
- [x] `toolVectorStore` imports and runs over it (runs: `nodes/vectorstore_tool_test.go` `TestAgentAnswersThroughAVectorStoreToolOverARetrieveStore`, a compiled Manual → Agent ← Vector Store Tool ← retrieve store ← embeddings graph on `engine.NewRunner`; imports: `internal/interop/n8n/vectorstore_import_test.go` and the real template 16706 under `TestRealRAGTemplatesKeepTheirIngestPath`, now strict)
- ~~An in-memory store (per tenant and execution) and Qdrant over REST~~ moved (2026-10-09): the in-memory store to FEAT-v8md2e, scoped per tenant and memoryKey rather than per execution, and Qdrant to FEAT-bvyc0b
- ~~The token splitter~~ moved (2026-10-09) to FEAT-gcq50s, onto its existing "Additional text splitters (character, token)" criterion
- ~~RAG works on the default SQLite install: an in-process or SQLite-backed vector store per tenant, so insert and retrieve-as-tool run without PostgreSQL~~ moved (2026-10-09) to FEAT-v8md2e

# Implementation Plan

Add a `retrieve` mode to the vector store (an `ai_vectorStore` output) and a Vector Store Tool node (`kilasflow.vectorStoreTool`) over it, and map n8n's `toolVectorStore` onto that node.

Split on 2026-10-09: the in-memory/SQLite store went to FEAT-v8md2e, Qdrant to FEAT-bvyc0b and the token splitter to FEAT-gcq50s. This ticket closes on criteria 1 and 2. The canvas not drawing mode-dependent store ports is BUG-pc284d.

# Notes

Related tickets: FEAT-gcq50s

FEAT-gcq50s covers the splitters, in-memory, Supabase and retrieve-for-chain. Coordinate so the two are not built twice; this ticket adds Qdrant and the Vector Store Tool, which that ticket omits.

Related (from the audit): FEAT-gcq50s (todo) covers splitters, in-memory, Supabase and retrieve-for-chain. It does not list Qdrant, although Qdrant is the most-used store after PGVector, and it lists Pinecone as out of scope.

# Related Files

`results-ai.json` (Qdrant, Pinecone, Supabase, In-Memory Vector Store, Vector Store Tool, Token Splitter, Character Text Splitter), `probes/Vector_Store_Tool.json`

# Attachments

## Progress (2026-10-09)

Criteria 1 and 2 are met. Criteria 3–5 moved out: FEAT-v8md2e (SQLite-backed store per tenant and memoryKey, n8n `vectorStoreInMemory`, `embeddingsOllama`), FEAT-bvyc0b (Qdrant over REST) and FEAT-gcq50s (token splitter, beside its character splitter). The canvas gap is BUG-pc284d.

**Retrieve mode** (`nodes/rag.go`, `nodes/vectorstore_retrieve.go`):
- `mode: retrieve` declares one input, `embedding` (required, max 1), and one output, `vectorStore` (`ai_vectorStore`). It has no main port, so the store is a sub-node of whatever reads it.
- Only the `mode` key means retrieve. A native `operation: retrieve` still folds into a search, as before.
- The internal store emits a `vectorStoreSource` descriptor `{nodeName, backend: internal, collection, distance, metadataFilter?, embeddings}` after the usual availability and tenant checks, so it still refuses on SQLite.
- The customer PGVector store emits `{backend: postgres, tableName, idColumn, contentColumn, metadataColumn, embeddingColumn, credentialId, collection, embeddings}` and never opens the database.
- Both refuse a store with no Embeddings sub-node, naming the port.
- Retrieve-as-tool now builds its descriptor from the same store fields. `TestRetrieveAsToolDescriptorsKeepTheirFields` pins that no key changed.

**Vector Store Tool** (`kilasflow.vectorStoreTool`, `nodes/vectorstore_tool.go`):
- Ports: `vectorStore` (`ai_vectorStore`) and `model` (`ai_languageModel`) in, both required and capped at one; `tool` out.
- Parameters: `toolName`, `toolDescription` (required) and `topK` (default 4, whole number 1..100, refused rather than clamped).
- The executor carries the store and model descriptors whole into a `vectorStoreAnswer` tool descriptor. It refuses anything on the vectorStore port that is not a retrieve-mode store.
- In the agent, `toolFrom` gained one case. `vectorStoreAnswerToolFrom` builds the retrieval through the existing `vectorStoreToolFrom`, so it gets the same install refusal, the execution's tenant and the same customer-PG guard. It resolves the tool's model with the agent's own `resolveModel`: credential re-read, outbound pre-flight, credential host scope and timeout ceiling.
- A call embeds the question, takes the top K documents and asks the tool's model to answer from them with a system prompt of our own, and the answer is the tool result. With no match it says so and makes no model call.

**Importer** (`internal/interop/n8n/vectorstore.go`):
- A `vectorStorePGVector` with no `mode` imports as `retrieve`. One with no or an empty `tableName` imports as `n8n_vectors`; that was the one-line default fix the orchestrator asked for.
- `toolVectorStore` maps to `kilasflow.vectorStoreTool`. At v1 the tool name is `name`; at v1.1, or when `name` is empty, it is the node name. An empty description gets a stand-in, and a non-numeric topK becomes a lossy note.
- Export writes `toolVectorStore` at v1 with `name`, `description` and `topK`.
- The 16706 fixture's retrieval half is rewired to the real shape. 4799 still covers retrieve-as-tool.

**Deviations from the plan:**
- ai.go changed by the two-line `toolFrom` case only, per the orchestrator's shared-file rule. The plan's two extractions (`vectorRetrieverFrom`, `vectorStoreTool.search`) were not made. Instead the answer tool builds a retrieve-as-tool tool from the store descriptor and calls its `Invoke`, reading the matches back from its JSON. That reuses retrieval with no copy and no edit to ai.go.
- The store's retrieve descriptor carries no topK; the tool supplies it, as n8n's does.
- `options.columnNames` is not lifted on import. It needs the export inverse to be honest, which made it more than the one-line fix, so it is noted on FEAT-gcq50s with the other PGVector fidelity gaps.
- The documented catalogue size (`node-registry.md`, `what-kilasflow-is.md`) moved to 63 types / 70 pairs, because `TestTheDocumentedCatalogueSizeIsTheShippedOne` counts it. Other tracks adding nodes will collide on those two sentences.

**Not done:** the optional live-PostgreSQL subtest. No pgvector database was available to this track, and the only PostgreSQL container running belonged to another track.

### Review fixes (2026-10-09)
- **Security regression tests** (`nodes/vectorstore_tool_security_test.go`):
  - A question carrying `={{ $env.X }} {{ 1+1 }}` reaches the embeddings and chat requests verbatim.
  - A passage telling the model to call a tool adds no `tools` key and no second call, and stays in the user message.
  - A model credential scoped to another host is refused ("is not allowed for host").
  - An install without an internal store refuses the tool ("not available on this install").
- **Prompt and cap:** the system prompt now says the passages are reference text, not instructions. Passages are capped at 64 KiB per call: the one that crosses the cap is cut on a character boundary and marked, and later ones are left out with a note.
- **Export symmetry:** `vectorStorePGVectorToN8N` writes `mode: insert` for a native store with no mode, with a lossy note on attaching a loader and embeddings in n8n. It maps Get Many to n8n's `load` and always names the table (`documents` by default). A native insert store survives export and re-import as an insert.
- **Import reporting:** a toolVectorStore `topK` that is not a whole number from 1 to 100 falls back to 4 with a lossy note. A description written in place of a missing one is reported as lossy.
- **Back-compat pin:** `operation: retrieve` and `operation: search` keep main → main ports; only `mode: retrieve` gives the retrieve ports.
- **Token usage:** `internal/ai.RecordToolUsage` plus a per-call tally in the loop (3 lines in `agent.go`) add the tool's own model call to the agent's usage output and to `agent.completed`.
