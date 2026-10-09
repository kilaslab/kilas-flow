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
- [x] `resolvedPorts` mirrors `vectorStorePortsFor` for `kilasflow.vectorStore` and `kilasflow.vectorStorePGVector` across insert, getMany, retrieve, retrieve-as-tool and delete, and for native graphs with only `operation` set — evidence: `web/src/lib/workflow-editor/ports.ts` (`vectorStorePorts`, `vectorStoreModeOf`); `ports.modes.test.ts` "vector store ports follow the mode" (both store types x six modes, plus operation-only graphs) and all 35 store cases per type in `ports.golden.test.ts`
- [x] `resolvedPorts` mirrors `embeddingsPortsFor` for `kilasflow.embeddings` (cluster vs sequential) — evidence: `embeddingsPorts` in ports.ts; `ports.modes.test.ts` "embeddings node ports follow the mode" and the 11 embeddings cases in `ports.golden.test.ts`
- [x] A vitest table pins each mode's inputs and outputs, written from the Go `PortsFor` cases so a drift on either side fails one of the two test suites — evidence: `nodes/testdata/ports_for.golden.json` is generated from the registry by `TestPortsForGolden` (Go fails on any PortsFor change until regenerated) and read by `ports.golden.test.ts` (vitest fails until ports.ts follows); the old `ports.ts` fails 41 of its 81 cases; `TestEveryPortsForNodeIsMirroredByTheEditor` fails (mutation-checked) when a node resolves ports in Go with no editor case
- [x] Switching Mode in the parameter panel redraws the ports, and wires that no longer fit are handled the way the Switch rule count already handles them — evidence: e2e `e2e/tests/vector-store-ports.spec.ts` (changes Mode in the inspector and reads the DOM handles; the stale `vectorStore` wire stays in the saved document and Execute reports "connection must reference declared source and target ports", as for a Switch that lost a rule); vitest `ports.imported.test.ts` "keeps the connection and redraws the handles when the mode of an imported store changes"; `ports.modes.test.ts` "refuses to re-draw a wire the new mode has no handle for"

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
2. **Go-generated golden shared by both suites.** `nodes/testdata/ports_for.golden.json` holds, for the vector stores and the embeddings node, the static ports of each definition and one entry per `{nodeType, parameters}` case with the exact `inputs`/`outputs` Go's `PortsFor` returns (every mode spelling, `operation`-only, empty/null/unknown `mode`, `delete`, both store types, cluster and sequential embeddings). A Go test (`nodes/ports_for_golden_test.go`) recomputes it from the real registry and fails on any difference (`-update` rewrites it, the way `cmd/nodepackgen` does); a vitest (`ports.golden.test.ts`) feeds each case to `resolvedPorts` and asserts the same JSON. The Go test also derives one case per `mode` / `operation` option each definition offers, so an option added later changes the golden. A change to `PortsFor` fails the Go test until the golden is regenerated, then the vitest until `ports.ts` follows; a change to `ports.ts` alone fails the vitest.
3. **Census guard.** The same Go test walks the registry and fails for any definition with `PortsFor != nil` that is neither in the golden nor on an explicit list of types mirrored with their own tests (switch, merge, datastore). The next mode-dependent node cannot silently be drawn static, which is how this ticket arose.
4. **`ports.ts`**: add `VECTOR_STORE_NODE_TYPES` (`kilasflow.vectorStore`, `kilasflow.vectorStorePGVector`) and `EMBEDDINGS_NODE_TYPE` cases; `vectorStoreModeOf` and the typed-ports-only-when-`mode`-is-a-key rule are ported line for line (the Go `hasMode` is key presence, so `mode: ""` with `operation: insert` is typed insert ports; the golden pins it).
5. **`node-visual.ts`**: `nodeShape` / `nodeVisual` take the resolved ports; both Svelte node components compute `ports` first and pass them. Shapes of the three already-mirrored nodes do not change (their resolved ports keep the static kinds).
6. **Mode change and now-invalid edges: same as Switch / Merge.** `updateNodeProperty` only replaces the node; the document keeps its connections, the node object is rebuilt so Flow re-measures it and draws the new handles, an edge whose handle no longer exists is not drawn, and the compiler reports it as `unknown_port` on that connection id when the workflow is activated or run (a save does not compile), which the editor already lists above the canvas. (Corrected while implementing: the first draft of this plan said "on save"; the e2e run showed a save accepts the dangling wire and Execute is what names it.) No pruning is added; a test pins that the connection survives the mode change and that `canConnect` refuses to re-create it.
7. **Imported edges.** The importer already resolves each n8n edge through `PortsFor` (`internal/interop/n8n/n8n.go:1615`), so an insert-mode store's `main` items arrive as `Loader.main -> Store.document` (`rag_import_test.go`), a retrieve store's `ai_vectorStore` edge lands on the Vector Store Tool's `vectorStore` input, and a cluster embeddings node wires `embedding` out only. A projection test builds that 16706-shaped graph with the real static ports from the golden and asserts every projected edge's `sourceHandle` / `targetHandle` is a handle `resolvedPorts` draws, before and after a mode change.

### Proof

- `go test ./nodes -run TestPortsForGolden` (red first: golden absent / census fails with no golden), `go test ./nodes/... -count=1`.
- `web/` `pnpm test` (ports golden table red first against the current `resolvedPorts`, plus the per-mode table, `canConnect` through retrieve ports, `nodeShape` with resolved ports, the 16706-shaped projection test), `pnpm check`.
- Not done here, recorded for a follow-up: the node picker (`catalog.ts:49`) still filters on static ports, so a store is not offered as a provider of `ai_tool` (retrieve-as-tool) from an agent's tool slot; and the Embeddings node has no `mode` parameter, so cluster mode only arrives through an n8n import.

## Progress (2026-10-09)

What changed:
- `web/src/lib/workflow-editor/ports.ts`: `resolvedPorts` now has cases for `kilasflow.vectorStore`, `kilasflow.vectorStorePGVector` (`vectorStorePorts`, a line-for-line port of `vectorStoreModeOf` and `vectorStorePortsFor`) and `kilasflow.embeddings` (`embeddingsPorts`). The error output is added after them like for every node.
- `web/src/lib/workflow-editor/node-visual.ts` + `canvas-node.svelte` + `execution-canvas-node.svelte`: `nodeShape`/`nodeVisual` take the resolved ports, and both node components resolve `ports` before cutting the tile. Without this a retrieve-mode store got its handles but stayed a plain step tile whose attachment labels printed over its name (the screenshot of the e2e failure showed the insert store as a hub with its two handles and labels clear of the name). Shapes of switch, merge, datastore and every other node are unchanged.
- Drift protection, which is the real fix for "the canvas disagrees with the compiler":
  - `nodes/ports_for_golden_test.go` (`TestPortsForGolden`, `-update-ports-golden`) writes `nodes/testdata/ports_for.golden.json` from the real registry: 81 `{nodeType, parameters}` cases (every mode spelling, operation-only, empty/null/blank/numeric/object `mode`, both stores, embeddings) with the exact Go ports, plus the static ports of the stores' RAG neighbours. `web/.../ports.golden.test.ts` feeds each case to `resolvedPorts`.
  - `TestEveryPortsForNodeIsMirroredByTheEditor` walks the registry and fails when a node resolves ports in Go but is neither in the golden nor named in `portsGoldenMirroredByHand` (switch, merge, datastore); mutation-checked by removing datastore from that list. A comment on `node.Definition.PortsFor` (`internal/node/registry.go`) points the next author at both.
  - `internal/interop/n8n/imported_ports_golden_test.go` (`-update-imported-ports-golden`) writes the documents the importer really produces for the RAG fixtures (16706, 3647, 4799, 5908, the insert-store and retrieve-store shapes) with the static ports of every definition they use; `ports.imported.test.ts` projects each through `documentFromCanvas` and asserts every edge's source and target handle is one `resolvedPorts` draws (the old `ports.ts` fails all 8 of those tests), checks the 16706 retrieval/ingest handles by name, and walks a mode change through `updateNodeProperty` + `reconcileCanvas`.
- `e2e/tests/vector-store-ports.spec.ts`: seeds an imported-shaped retrieval graph, reads the real DOM handles of the store and the cluster embeddings node, changes Mode in the inspector (retrieve-as-tool, insert, retrieve) and checks the handles and the edges Flow draws, then saves and executes and checks the stale wire is reported. Passes (1 test, run against the binary `make build-all` built from this branch).

Deviations from the plan:
- The plan said a save reports a wire a mode change orphaned; it does not. A save never compiles (`workflow_validate.go`), the stale connection is stored, and the compiler's `unknown_port` on that connection id appears when the workflow is activated or run, and in `POST /workflows/validate`. That is what a Switch that lost a rule does today, so nothing was added; the plan text is corrected in place.
- The ports golden is Go-generated rather than hand-written, and a second golden (imported documents) was added; the plan only had the first.
- The tests are split over `ports.golden.test.ts`, `ports.modes.test.ts` and `ports.imported.test.ts` beside the existing `ports.test.ts` rather than growing it.

Go quirks pinned rather than fixed (they are in the golden, and ports.ts reproduces them): `mode: "GETMANY"` (any casing other than exactly `getMany`) resolves to plain main/main on the server because `vectorStoreModeOf` lower-cases it before comparing against the mixed-case `getMany`; and the typed insert/Get Many ports apply whenever the `mode` KEY exists, even as `""` or `null`, so a document with `mode: ""` and `operation: insert` expects `embedding` + `document` inputs. Neither is reachable from the inspector's Mode select (it has no empty choice), and changing them is a compiler change this ticket does not need.

Left / follow-ups (none blocks the criteria):
- The picker filters on static ports (`web/src/lib/workflow-editor/catalog.ts:49`), so a vector store is not offered as a provider of `ai_tool` from an agent's tools slot; and the Embeddings node has no `mode` parameter, so cluster mode reaches the canvas only through an n8n import.
- Serving resolved ports from the node-types API stays the better long-term answer (the ticket's own note); the golden makes the interim mirror safe, not unnecessary.
- Observed, not touched, not verified in a browser, unrelated to the modes: the "+" under an empty attachment handle opens the picker with `acceptsMain` (because `connecting` is set) as well as `providesKind`, and `addNode` in workflow-editor.svelte looks for an input of the slot's kind on the picked node, which a sub-node (model, embeddings) does not have. It may therefore offer nothing or add the node unwired.

Verification (all in this worktree): `pnpm check` 0 errors 0 warnings; `pnpm test` 77 files / 1150 tests pass (before the last two comment edits; re-run below in the final report); `go test ./nodes/... ./internal/interop/... ./internal/node/... ./internal/workflow/... -count=1` pass; `e2e/tests/vector-store-ports.spec.ts` pass. Generated artefacts: none touched (no API shape, CLI, skill or config change), so no `generate-*` target needed a rerun.
