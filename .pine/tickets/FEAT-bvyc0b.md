---
id: FEAT-bvyc0b
title: Qdrant vector store over REST
status: todo
priority: medium
labels:
    - n8n
    - parity
    - ai
    - rag
parent: EPIC-8rbys7
created: "2026-10-09T11:26:15Z"
updated: "2026-10-09T11:26:15Z"
---

# Description

Qdrant is the most-used vector store in n8n templates after PGVector: 22 templates (audit finding NG-13). `@n8n/n8n-nodes-langchain.vectorStoreQdrant` imports here as an unsupported placeholder, so every one of those templates is blocked.

Split out of FEAT-zm3wh2 on 2026-10-09 (its criterion "Qdrant over REST"). The vector-store `retrieve` mode and the Vector Store Tool shipped there, so a Qdrant store only has to implement the store itself. The ports, the retrieve descriptor and the agent tools already exist.

Qdrant speaks plain REST, so no driver or SQL is needed. Collections are created with a vector size and distance, points are upserted with a payload, and a similarity query returns scored points.

**n8n wire-format facts (format only, read 2026-10-09; no n8n code copied):**
- Credential type `qdrantApi`: `qdrantUrl` and `apiKey`.
- `qdrantCollection` is a resource locator (`{__rl: true, mode: 'list'|'id', value}`).
- `options.contentPayloadKey` defaults to `content`; `options.metadataPayloadKey` defaults to `metadata`.
- `options.collectionConfig` is JSON for collection creation (vector size and distance).
- `options.searchFilterJson` is a Qdrant filter object applied to retrieval.
- It shares the vector-store `mode` parameter: `load` (Get Many), `insert`, `retrieve` (the default when absent), `retrieve-as-tool`, `update`. Ports follow the shared vector-store rules.

**Sequencing:** it needs a `qdrantApi` credential type with a domain scope. The URL is tenant-supplied, so the credential's allowed domains and the outbound policy must bind it exactly as they bind an HTTP Request. Land it after the credential-type work in the 2026-10-09 wave.

# Acceptance Criteria
- [ ] A `qdrantApi` credential type (URL + API key) with a domain scope
- [ ] A Qdrant vector store node over REST through `safehttp`: insert (creating the collection when absent), Get Many, retrieve (`ai_vectorStore` out) and retrieve-as-tool, with the payload keys `content`/`metadata` by default
- [ ] The Vector Store Tool and the agent's retrieve-as-tool run over it
- [ ] n8n `vectorStoreQdrant` imports with its collection locator, payload keys and search filter, and exports back
- [ ] Tests reach an httptest Qdrant stub through `outbound.allowed_private_endpoints` (never `allow_private_networks`), and one test proves a different loopback port is still refused

# Implementation Plan

A new `nodes/vectorstore_qdrant.go` with the node definition and executor. On retrieve it emits the same `vectorStoreSource` descriptor the other stores emit, and the agent's retriever gains a `qdrant` backend. The importer mapping goes in a new file under `internal/interop/n8n/`.

# Notes

Related tickets: FEAT-zm3wh2 (retrieve mode and Vector Store Tool) and FEAT-gcq50s (remaining RAG surface).

# Related Files
- nodes/vectorstore_tool.go — the retrieve descriptor and answer tool this store plugs into
- internal/credentials/builtin.go — credential types
- internal/interop/n8n/n8n.go — mappings
