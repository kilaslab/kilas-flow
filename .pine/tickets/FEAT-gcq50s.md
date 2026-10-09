---
id: FEAT-gcq50s
title: Drive/Gmail full parity and remaining RAG cluster modes
status: todo
priority: low
labels:
    - parity
parent: EPIC-8rbys7
created: "2026-09-21T12:03:36Z"
updated: "2026-10-09T11:01:23Z"
---

# Description

Work deliberately left out of the first RAG/Google slice. The shipped subset is enough for the four template *shapes* (Search/Download/Move, Gmail Trigger/Get, cluster insert + retrieve-as-tool). This ticket tracks the rest of n8n's Drive/Gmail/RAG surface.

# Acceptance Criteria
- [ ] Google Shared Drives (corpora, driveId) on Drive nodes and trigger
- [ ] Remaining Drive trigger events beyond file created/updated in a folder
- [ ] Gmail drafts, labels, reply, threads, attachment download/upload
- [ ] Additional text splitters (character, token)
- [ ] Vector Store rerank / retrieve-for-chain; in-memory and Supabase stores
- [ ] n8n embeddings types that are not OpenAI-compatible `/v1/embeddings` (Cohere, Gemini, Azure, HuggingFace, Bedrock, Vertex) except behind an OpenAI-compatible gateway

# Implementation Plan
Separate tickets per area when someone needs them. Do not block 16706/3647/4799/5908 shapes on this list.

# Notes
OCR, Pinecone, Q&A Chain retriever and tenant `CREATE EXTENSION` stay out of scope. Google Sheets is no longer out of scope: FEAT-vntngh owns it. The vector-store retrieve mode and the Vector Store Tool are owned by FEAT-zm3wh2. The in-memory store moved to FEAT-v8md2e (2026-10-09), which builds it as a SQLite-backed store per tenant and memoryKey; the "in-memory" half of the rerank criterion above is tracked there, not here.

**Token splitter (2026-10-09):** FEAT-zm3wh2's criterion "The token splitter" moved here, onto the "Additional text splitters (character, token)" criterion above, so the two splitters are built once. The facts carried over:
- n8n `@n8n/n8n-nodes-langchain.textSplitterTokenSplitter` v1 has `chunkSize` (default 1000) and `chunkOverlap` (default 0) and counts tokens with the cl100k_base encoding. It appears in 16 templates; the character splitter in 1.
- It needs a tokenizer decision first. The cl100k_base merge ranks are third-party bytes, so they either join the closed vendoring list with their LICENSE and a PROVENANCE.md (`TestVendoredThirdPartyCarriesItsLicence`), or come from a Go module that ships offline ranks, checked against `.pine/memory/licensing.md` before it is added. A character-count approximation is not parity: chunk boundaries would differ from n8n's on every document.

**PGVector import fidelity left over from FEAT-zm3wh2 (2026-10-09):** an absent `tableName` now imports as n8n's default `n8n_vectors`. These remain:
- `options.columnNames.values` (`idColumnName`, `vectorColumnName`, `contentColumnName`, `metadataColumnName`) is not lifted onto the node's `idColumn`/`embeddingColumn`/`contentColumn`/`metadataColumn`. A template with custom column names runs against the default ones. Doing it right needs the export inverse too (write the columns back into `options.columnNames` and drop the top-level keys), which is why it was not folded into the one-line tableName fix.
- `options.collection` (n8n's separate collection table) and `options.distanceStrategy` (cosine, innerProduct, euclidean) are carried but not honoured: the customer-PG path filters on `metadata.collection` and always orders by cosine.
- A natively authored Get Many exports `mode: getMany`, which n8n does not know (its value is `load`); and `load`'s `prompt` is not read as `query`.

Re-parented (2026-10-09) from EPIC-hkypt5 to the parity backlog EPIC-8rbys7: it is a low-priority list of remaining Drive/Gmail/RAG surface, to be split into its own tickets when someone needs an area, and it no longer holds the RAG-templates epic open.

# Related Files

# Attachments
