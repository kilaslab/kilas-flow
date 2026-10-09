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

## Plan (2026-10-09)

### Root cause

- `web/src/lib/workflow-editor/ports.ts:140` `resolvedPorts` switches on three node types (switch, merge, datastore) and hands every other node its definition's static `inputs`/`outputs`. The server resolves the rest per configuration in Go: `nodes/rag.go:302` `embeddingsPortsFor` (cluster vs sequential), `nodes/rag.go:349` `vectorStorePortsFor` over `nodes/rag.go:325` `vectorStoreModeOf`, plus the retrieve ports added by FEAT-zm3wh2. `node.Definition.PortsFor` is `json:"-"` (`internal/node/registry.go:118`), so nothing reaches the browser but the unconfigured main-in / main-out pair.
- A second, hidden half: `web/src/lib/workflow-editor/node-visual.ts:326` `nodeShape` decides the tile (trigger / step / hub / attachment) from the STATIC ports, and `canvas-node.svelte:72` and `execution-canvas-node.svelte:54` call it before `resolvedPorts`. A retrieve-mode store would get the right handles but a plain step tile, whose attachment labels print over the node-name band (a hub carries its name inside the tile). The shape has to follow the resolved ports too.

### Approach

1. **Not served by the API (yet).** `PortsFor` is a Go callback, not data. Serving it means either (a) a declarative description, which `vectorStoreModeOf` is not (aliases, `mode` key presence vs value, the `operation` fallback are code and would have to be re-expressed and mirrored all the same), or (b) a resolve-ports endpoint, which is asynchronous while `resolvedPorts` is a synchronous pure function called from `canConnect`, the projection, `hasLabelledOutputs`, the replay and every render. Neither is cheap, so `ports.ts` mirrors the Go rules, and the drift is made impossible to miss instead:
2. **Go-generated golden shared by both suites.** `nodes/testdata/ports_for.golden.json` holds, for the vector stores and the embeddings node, the static ports of each definition and one entry per `{nodeType, parameters}` case with the exact `inputs`/`outputs` Go's `PortsFor` returns (every mode spelling, `operation`-only, empty/null/unknown `mode`, `delete`, both store types, cluster and sequential embeddings). A Go test (`nodes/ports_for_golden_test.go`) recomputes it from the real registry and fails on any difference (`-update` rewrites it, the way `cmd/nodepackgen` does); a vitest (`ports.test.ts`) feeds each case to `resolvedPorts` and asserts the same JSON. A change to `PortsFor` fails the Go test until the golden is regenerated, then the vitest until `ports.ts` follows; a change to `ports.ts` alone fails the vitest.
3. **Census guard.** The same Go test walks the registry and fails for any definition with `PortsFor != nil` that is neither in the golden nor on an explicit list of types mirrored with their own tests (switch, merge, datastore). The next mode-dependent node cannot silently be drawn static, which is how this ticket arose.
4. **`ports.ts`**: add `VECTOR_STORE_NODE_TYPES` (`kilasflow.vectorStore`, `kilasflow.vectorStorePGVector`) and `EMBEDDINGS_NODE_TYPE` cases; `vectorStoreModeOf` and the typed-ports-only-when-`mode`-is-a-key rule are ported line for line (the Go `hasMode` is key presence, so `mode: ""` with `operation: insert` is typed insert ports; the golden pins it).
5. **`node-visual.ts`**: `nodeShape` / `nodeVisual` take the resolved ports; both Svelte node components compute `ports` first and pass them. Shapes of the three already-mirrored nodes do not change (their resolved ports keep the static kinds).
6. **Mode change and now-invalid edges: same as Switch / Merge.** `updateNodeProperty` only replaces the node; the document keeps its connections, the node object is rebuilt so Flow re-measures it and draws the new handles, an edge whose handle no longer exists is not drawn, and the compiler reports it on save as `unknown_port` on that connection id, which the editor already lists above the canvas. No pruning is added; a test pins that the connection survives the mode change and that `canConnect` refuses to re-create it.
7. **Imported edges.** The importer already resolves each n8n edge through `PortsFor` (`internal/interop/n8n/n8n.go:1615`), so an insert-mode store's `main` items arrive as `Loader.main -> Store.document` (`rag_import_test.go`), a retrieve store's `ai_vectorStore` edge lands on the Vector Store Tool's `vectorStore` input, and a cluster embeddings node wires `embedding` out only. A projection test builds that 16706-shaped graph with the real static ports from the golden and asserts every projected edge's `sourceHandle` / `targetHandle` is a handle `resolvedPorts` draws, before and after a mode change.

### Proof

- `go test ./nodes -run TestPortsForGolden` (red first: golden absent / census fails with no golden), `go test ./nodes/... -count=1`.
- `web/` `pnpm test` (ports golden table red first against the current `resolvedPorts`, plus the per-mode table, `canConnect` through retrieve ports, `nodeShape` with resolved ports, the 16706-shaped projection test), `pnpm check`.
- Not done here, recorded for a follow-up: the node picker (`catalog.ts:49`) still filters on static ports, so a store is not offered as a provider of `ai_tool` (retrieve-as-tool) from an agent's tool slot; and the Embeddings node has no `mode` parameter, so cluster mode only arrives through an n8n import.
