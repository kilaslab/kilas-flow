---
id: BUG-pc284d
title: The canvas draws static ports for vector store and embeddings nodes, never the ports their mode resolves
status: todo
priority: medium
labels:
    - editor
    - canvas
    - rag
parent: EPIC-8rbys7
created: "2026-10-09T11:26:15Z"
updated: "2026-10-09T11:26:15Z"
---

# Description

The canvas draws a vector store's and an embeddings node's static ports, never the ports their mode gives them. The server resolves those ports per configuration through `node.Definition.PortsFor` (`nodes/rag.go` `vectorStorePortsFor` and `embeddingsPortsFor`), but `PortsFor` is a Go function serialised as `json:"-"`. The web side mirrors it by hand in `web/src/lib/workflow-editor/ports.ts` `resolvedPorts`, and that function only knows Switch, Merge and Datastore. Every other node falls through to `definition.inputs`/`definition.outputs`.

So the editor disagrees with the compiler for every mode-dependent store and embeddings node:
- `kilasflow.vectorStore` and `kilasflow.vectorStorePGVector` with `mode: insert` take `embedding` + `document` in, not `main`.
- With `mode: getMany` they take `main` + `embedding`.
- With `mode: retrieve` they take `embedding` in and give `vectorStore` (`ai_vectorStore`) out. FEAT-zm3wh2 added this mode on 2026-10-09.
- With `mode: retrieve-as-tool` they take `embedding` in and give `tool` (`ai_tool`) out.
- `kilasflow.embeddings` with `mode: cluster` has no inputs and only the `embedding` output.

The canvas draws all of them with main in and main out. That predates FEAT-zm3wh2, which made it more visible: every imported n8n RAG workflow (16706, 4799, 3647, 5908) renders with wires that land on ports the canvas does not show.

Found while planning FEAT-zm3wh2 (2026-10-09). Deliberately not fixed there, because the web editor was being changed by other tracks in the same wave.

# Steps to Reproduce

1. Import n8n template 16706 (or the fixture `internal/interop/n8n/testdata/n8n_rag_16706.json`) and open it in the editor.
2. Look at "Postgres PGVector Store" (retrieve mode) and the insert-mode store, and at both "Embeddings OpenAI" nodes (cluster mode).
3. Alternatively, add a Vector Store node on the canvas and switch its Mode to "Retrieve as Tool" or "Retrieve (for Vector Store Tool)".

# Expected

The node shows the ports the server resolves for its mode: an `embedding` input and a `vectorStore` output in retrieve mode, a `tool` output in retrieve-as-tool mode, `embedding` + `document` inputs in insert mode, and only an `embedding` output for a cluster-mode embeddings node. Connecting from those ports in the editor works and saves.

# Actual

The node shows a main input and a main output whatever its mode. Imported AI edges attach to ports that are not drawn, and an author cannot draw the `ai_vectorStore` or `ai_embedding` wire from the canvas.

# Acceptance Criteria
- [ ] `resolvedPorts` mirrors `vectorStorePortsFor` for `kilasflow.vectorStore` and `kilasflow.vectorStorePGVector` across insert, getMany, retrieve, retrieve-as-tool and delete, and for native graphs with only `operation` set
- [ ] `resolvedPorts` mirrors `embeddingsPortsFor` for `kilasflow.embeddings` (cluster vs sequential)
- [ ] A vitest table pins each mode's inputs and outputs, written from the Go `PortsFor` cases so a drift on either side fails one of the two test suites
- [ ] Switching Mode in the parameter panel redraws the ports, and wires that no longer fit are handled the way the Switch rule count already handles them

# Implementation Plan

Add `VECTOR_STORE_NODE_TYPES` and `EMBEDDINGS_NODE_TYPE` cases to `resolvedPorts`, with helpers that read `mode` (falling back to `operation`) the way `vectorStoreModeOf` does. Better long-term: have the definitions API serialise the resolved ports for a given configuration, so the client stops hand-mirroring Go functions. That would be a separate ticket.

# Notes

Related tickets: FEAT-zm3wh2 (added retrieve mode and the Vector Store Tool), BUG-9pmv8y (real RAG templates).

# Related Files
- web/src/lib/workflow-editor/ports.ts — resolvedPorts
- nodes/rag.go — vectorStorePortsFor, embeddingsPortsFor, vectorStoreModeOf
