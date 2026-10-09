---
id: BUG-pc284d
title: The canvas draws static ports for vector store and embeddings nodes, never the ports their mode resolves
status: done
priority: medium
labels:
    - editor
    - canvas
    - rag
parent: EPIC-8rbys7
created: "2026-10-09T11:26:15Z"
updated: "2026-10-09T13:48:08Z"
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
2. **Go-generated golden shared by both suites.** `nodes/testdata/ports_for.golden.json` holds, for the vector stores and the embeddings node, the static ports of each definition and one entry per `{nodeType, parameters}` case with the exact `inputs`/`outputs` Go's `PortsFor` returns (every mode spelling, `operation`-only, empty/null/unknown `mode`, `delete`, both store types, cluster and sequential embeddings). A Go test (`nodes/ports_for_golden_test.go`) recomputes it from the real registry and fails on any difference (`-update` rewrites it, the way `cmd/nodepackgen` does); a vitest (`ports.test.ts`) feeds each case to `resolvedPorts` and asserts the same JSON. A change to `PortsFor` fails the Go test until the golden is regenerated, then the vitest until `ports.ts` follows; a change to `ports.ts` alone fails the vitest.
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

## Merged (2026-10-09)

The reviewed work (up to a98a3674) is on main; the imported-ports golden was regenerated on the merged tree (one line: the Gmail node's `resource` that the tool-form importer now writes). Two review minors are left as follow-ups, not blockers: derive one golden case per `mode`/`operation` Options value from the definitions (nodes/ports_for_golden_test.go), and fix the stale `ports.test.ts` name in that file and the golden's comment (the reader is `ports.golden.test.ts`). The two editor bugs the review confirmed (the slot "+" picker, and stale wires after a mode change) have their own tickets.

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `6bce9edb` (last commit at or before ticket created 2026-10-09)
- Commits (9):
  - `17360940` — BUG-pc284d: the imported-ports golden carries the Gmail resource the tool-form importer now writes
  - `5ffb3d24` — merge: the canvas draws the ports a vector store's mode and an embeddings node's cluster mode resolve, held to a table Go generates (BUG-pc284d)
  - `a98a3674` — BUG-pc284d: an e2e spec reads the real handles of a store and an embeddings node across mode changes; the ticket records what was done
  - `885cf171` — BUG-pc284d: every edge the n8n importer produces for the RAG fixtures lands on a handle the canvas draws
  - `9dec8884` — BUG-pc284d: a node's tile is cut from the ports its configuration resolves, so a retrieve-mode store draws as a hub
  - `767dc1d5` — BUG-pc284d: the canvas resolves vector store and embeddings ports from their mode, held to the table Go generates
  - `ff22b955` — BUG-pc284d: a Go test pins the ports PortsFor resolves for vector stores and embeddings, and fails when a PortsFor node has no editor mirror
  - `11fdc54c` — chore(pine): BUG-pc284d plans a Go-generated ports golden both suites read, a PortsFor census, and resolved-port tile shapes
  - `8deba40d` — chore(pine): FEAT-zm3wh2 closes on retrieve mode and the Vector Store Tool; its SQLite store, Qdrant and token splitter move to FEAT-v8md2e, FEAT-bvyc0b and FEAT-gcq50s, and the canvas port gap is BUG-pc284d
- Files changed (base → working tree):

```
 .env.example                                       |    8 +-
 .github/workflows/ci.yml                           |   53 +-
 .pine/memory/docs.md                               |    5 +-
 .pine/memory/e2e.md                                |    3 +
 .pine/memory/n8n-reference.md                      |    5 +-
 .pine/memory/persistence.md                        |    5 +-
 .pine/memory/web-editor.md                         |    3 +
 .pine/tickets/BUG-2d1pdx.md                        |   49 +
 .pine/tickets/BUG-6gkd12.md                        | 1053 ++++++++++-
 .pine/tickets/BUG-8xz9t1.md                        |  511 +++++
 .pine/tickets/BUG-9pmv8y.md                        | 1074 ++++++++++-
 .pine/tickets/BUG-b4cb1c.md                        | 1117 ++++++++++-
 .pine/tickets/BUG-b8bwhw.md                        | 1279 ++++++++++++-
 .pine/tickets/BUG-f85r5h.md                        |  412 +++++
 .pine/tickets/BUG-f8b8gh.md                        |   47 +
 .pine/tickets/BUG-jx94q9.md                        |  417 +++++
 .pine/tickets/BUG-k4tzc8.md                        |   46 +
 .pine/tickets/BUG-kkkrmx.md                        |  158 ++
 .pine/tickets/BUG-n6p7qy.md                        | 1177 +++++++++++-
 .pine/tickets/BUG-pc284d.md                        |  113 ++
 .pine/tickets/BUG-pdmkn7.md                        |  419 +++++
 .pine/tickets/BUG-prhxdy.md                        |  479 +++++
 .pine/tickets/BUG-vbw239.md                        |  197 ++
 .pine/tickets/BUG-vqghpm.md                        |  329 ++++
 .pine/tickets/BUG-x28fsx.md                        | 1217 +++++++++++-
 .pine/tickets/BUG-ywbvfa.md                        | 1290 ++++++++++++-
 .pine/tickets/EPIC-brpz48.md                       |    7 +-
 .pine/tickets/FEAT-8752vx.md                       |   10 +-
 .pine/tickets/FEAT-bvyc0b.md                       |   52 +
 .pine/tickets/FEAT-cf5n4j.md                       |  549 +++++-
 .pine/tickets/FEAT-e898nz.md                       |  195 +-
 .pine/tickets/FEAT-egm8bf.md                       |    2 +
 .pine/tickets/FEAT-gcq50s.md                       |   11 +-
 .pine/tickets/FEAT-kfmq1z.md                       |    2 +
 .pine/tickets/FEAT-p75n7j.md                       | 1297 ++++++++++++-
 .pine/tickets/FEAT-qae4sh.md                       |  956 +++++++++-
 .pine/tickets/FEAT-r87gtj.md                       |  956 +++++++++-
 .pine/tickets/FEAT-r8ph93.md                       |    3 +
 .pine/tickets/FEAT-v8md2e.md                       |   66 +
 .pine/tickets/FEAT-vntngh.md                       |  129 +-
 .pine/tickets/FEAT-xj5tv6.md                       |    5 +
 .pine/tickets/FEAT-zm3wh2.md                       | 1126 ++++++++++-
 CHANGELOG.md                                       |  140 ++
 CONTRIBUTING.md                                    |   13 +
 Makefile                                           |   44 +-
 cmd/kilasflow/main.go                              |  236 ++-
 cmd/kilasflow/reseal_test.go                       |  219 +++
 cmd/kilasflow/secrets_boot_test.go                 |  102 +-
 cmd/kilasflow/shipped_catalogue_drift_test.go      |  228 +++
 config.example.yaml                                |   24 +-
 docs/src/content/docs/concepts/credentials.md      |   92 +-
 docs/src/content/docs/concepts/node-registry.md    |    2 +-
 docs/src/content/docs/guides/embedding.md          |   19 +-
 docs/src/content/docs/guides/n8n-migration.md      |   97 +-
 .../src/content/docs/guides/tenant-scoped-nodes.md |    6 +
 .../docs/operate/configuration-reference.md        |   36 +-
 docs/src/content/docs/operate/configuration.md     |   14 +-
 docs/src/content/docs/operate/key-rotation.md      |  158 ++
 docs/src/content/docs/operate/security.md          |   19 +-
 docs/src/content/docs/operate/tenant-deletion.md   |    4 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +
 docs/src/content/docs/reference/api/credentials.md |    3 +-
 docs/src/content/docs/reference/api/workflows.md   |    1 +
 docs/src/content/docs/reference/cli.md             |  225 ++-
 docs/src/content/docs/start/install.md             |    5 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |    2 +-
 docs/src/styles/kilasflow.css                      |    2 +-
 e2e/.gitignore                                     |    4 +
 e2e/fixtures/error-form-nodes.ts                   |    7 +-
 e2e/fixtures/n8n-live.ts                           |    2 +
 e2e/global-setup.ts                                |   26 +-
 e2e/helpers/focus-ring.ts                          |  125 ++
 e2e/helpers/stub.ts                                |    6 +-
 e2e/package.json                                   |    3 +-
 e2e/playwright.visual.config.ts                    |   60 +
 e2e/tests/embed-branding.spec.ts                   |  163 ++
 e2e/tests/focus-tab-order.spec.ts                  |  155 ++
 e2e/tests/n8n-compare.spec.ts                      |   17 +-
 e2e/tests/node-coverage.spec.ts                    |   47 +-
 e2e/tests/vector-store-ports.spec.ts               |   98 +
 e2e/tests/workflow-tool-inputs.spec.ts             |  406 ++++
 e2e/visual/fixtures.ts                             |   71 +
 e2e/visual/focus-ring.spec.ts                      |   50 +
 e2e/visual/force-state.ts                          |   34 +
 e2e/visual/state-matrix.spec.ts                    |   25 +
 e2e/visual/theme-subtree.spec.ts                   |   47 +
 internal/ai/agent.go                               |    4 +-
 internal/ai/ai_test.go                             |   32 +
 internal/ai/fromai.go                              |   28 +
 internal/ai/fromai_whole_test.go                   |   47 +
 internal/ai/memory.go                              |   26 +-
 internal/ai/toolusage.go                           |   46 +
 internal/ai/toolusage_test.go                      |   60 +
 internal/api/auth_test.go                          |   25 +-
 internal/api/credential_audit_test.go              |  271 +++
 internal/api/credential_usage_test.go              |  169 ++
 internal/api/handlers/actor.go                     |   33 +
 internal/api/handlers/credentials.go               |  182 +-
 internal/api/handlers/nodes.go                     |   76 +-
 internal/api/handlers/oauth.go                     |   37 +-
 internal/api/handlers/workflows.go                 |   55 +-
 internal/api/node_types_lookup_test.go             |  158 ++
 internal/api/node_types_nested_test.go             |  114 ++
 internal/api/node_types_schema_expression_test.go  |   81 +
 internal/api/oauth_external_test.go                |   69 +
 internal/api/oauth_sheets_test.go                  |   48 +
 internal/api/subworkflow_activation_test.go        |  219 +++
 internal/api/workflow_tags_test.go                 |  342 ++++
 internal/cli/docs_examples_test.go                 |   70 +
 internal/cli/exit.go                               |  136 ++
 internal/cli/exit_reference_test.go                |  286 +++
 internal/cli/skills_not_shipped_test.go            |  307 ++-
 internal/cli/verbs_credential.go                   |   35 +-
 internal/cli/verbs_credential_test.go              |  197 +-
 internal/cli/verbs_node.go                         |   91 +-
 internal/cli/verbs_node_options_test.go            |  333 ++++
 internal/cli/verbs_workflow.go                     |   38 +-
 internal/cli/verbs_workflow_subworkflow_test.go    |  144 ++
 internal/cli/verbs_workflow_tag_test.go            |   84 +
 internal/config/boot_strictness_test.go            |   76 +
 internal/config/config.go                          |   51 +-
 internal/credentials/binding_guard_test.go         |   79 +
 internal/credentials/builtin.go                    |   14 +
 internal/credentials/cipher.go                     |  330 ++++
 internal/credentials/cipher_internal_test.go       |   35 +
 internal/credentials/cipher_test.go                |  388 ++++
 internal/credentials/credentials.go                |   78 +-
 internal/credentials/credentials_test.go           |   30 +-
 internal/credentials/external.go                   |   41 +-
 internal/credentials/google_sheets_probe_test.go   |   68 +
 internal/credentials/google_sheets_test.go         |  168 ++
 internal/credentials/keysource.go                  |   63 +-
 internal/credentials/keysource_test.go             |  103 ++
 internal/credentials/oauth.go                      |   59 +-
 internal/credentials/registry.go                   |    8 +
 internal/credentials/scope_test.go                 |    2 +
 .../database/credential_events_migration_test.go   |   61 +
 internal/database/migrate_test.go                  |    2 +
 internal/docexamples/docexamples.go                |  318 ++++
 internal/docexamples/docexamples_test.go           |  209 +++
 internal/engine/authenticate.go                    |    3 +
 internal/engine/expression_context_test.go         |   24 +
 internal/engine/missing_credential_test.go         |   49 +
 internal/engine/runner.go                          |    8 +
 internal/engine/service.go                         |   19 +
 internal/interop/n8n/chatmodel_import_test.go      |    3 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |    6 +-
 internal/interop/n8n/export_expression_test.go     |   79 +
 internal/interop/n8n/googlesheets.go               |  243 +++
 internal/interop/n8n/googlesheets_import_test.go   |  415 +++++
 internal/interop/n8n/imported_ports_golden_test.go |  133 ++
 internal/interop/n8n/importer_tail_test.go         |   15 +-
 internal/interop/n8n/n8n.go                        |  133 +-
 internal/interop/n8n/n8n_test.go                   |  121 +-
 internal/interop/n8n/parameters.go                 |  192 +-
 internal/interop/n8n/rag_import_test.go            |   33 +-
 internal/interop/n8n/roundtrip_fidelity_test.go    |   26 +
 internal/interop/n8n/sheets_fixture_test.go        |   98 +
 internal/interop/n8n/sheets_template_test.go       |   72 +
 internal/interop/n8n/tags.go                       |  182 ++
 internal/interop/n8n/tags_test.go                  |  312 ++++
 internal/interop/n8n/template6270_shape_test.go    |  305 +++
 .../n8n/testdata/imported_ports.golden.json        | 1945 ++++++++++++++++++++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   24 +
 internal/interop/n8n/testdata/n8n_rag_16706.json   |   10 +-
 .../interop/n8n/testdata/n8n_sheets_19200.json     |  185 ++
 internal/interop/n8n/tool_form_import_test.go      |  559 ++++++
 internal/interop/n8n/tool_forms.go                 |  398 ++++
 internal/interop/n8n/tool_variant_import_test.go   |   16 +-
 internal/interop/n8n/vectorstore.go                |  153 ++
 internal/interop/n8n/vectorstore_import_test.go    |  281 +++
 internal/interop/n8n/workflow_tool_inputs.go       |  241 +++
 internal/interop/n8n/workflow_tool_inputs_test.go  |  319 ++++
 internal/loadoptions/googlesheets.go               |  463 +++++
 internal/loadoptions/googlesheets_export_test.go   |   15 +
 internal/loadoptions/googlesheets_headers_test.go  |  240 +++
 internal/loadoptions/googlesheets_test.go          |  315 ++++
 internal/loadoptions/workflows.go                  |   79 +
 internal/loadoptions/workflows_test.go             |  158 ++
 internal/node/registry.go                          |   15 +
 internal/node/tool_form_scope_test.go              |   99 +
 internal/node/visibility.go                        |   75 +-
 internal/property/mapper.go                        |    5 +
 internal/property/mapper_test.go                   |   22 +
 internal/repository/credential_events.go           |  130 ++
 internal/repository/credential_events_test.go      |  122 ++
 internal/repository/credential_reseal_test.go      |  218 +++
 internal/repository/credential_seal_test.go        |  133 ++
 internal/repository/credential_usage.go            |  204 ++
 internal/repository/credential_usage_test.go       |  194 ++
 internal/repository/credentials.go                 |   30 +-
 internal/repository/credentials_external_test.go   |    8 +-
 internal/repository/export_reseal_test.go          |    7 +
 internal/repository/models.go                      |   27 +
 internal/repository/polls_test.go                  |    8 +
 internal/repository/postgres_execution_test.go     |    2 +
 internal/repository/prefix_test.go                 |   40 +
 internal/repository/refresh.go                     |    6 +-
 internal/repository/refresh_external_test.go       |   86 +
 internal/repository/refresh_sheets_test.go         |   86 +
 internal/repository/reseal.go                      |  153 ++
 internal/repository/subworkflow_activation_test.go |  116 ++
 internal/repository/table_names_test.go            |   12 +
 internal/repository/tenant_rows.go                 |    6 +-
 internal/repository/webhook_state.go               |    7 +-
 internal/repository/workflow_history.go            |    4 +
 internal/repository/workflow_tags.go               |   87 +
 internal/repository/workflow_tags_test.go          |  283 +++
 internal/repository/workflows.go                   |  132 +-
 internal/sheetsa1/a1.go                            |   83 +
 internal/sheetsa1/a1_test.go                       |   79 +
 internal/shippedcatalogue/shippedcatalogue.go      |   10 +
 internal/skills/check.go                           |  146 ++
 internal/skills/check_test.go                      |   59 +
 internal/skills/examples.go                        |  287 +++
 internal/skills/examples_coverage_test.go          |   49 +
 internal/skills/examples_shape_test.go             |  121 ++
 internal/skills/examples_test.go                   |  642 +++++++
 internal/skills/skill.go                           |   11 +
 internal/skills/skills_test.go                     |  254 ++-
 .../skills/kilasflow-fixture/SKILL.md              |   32 +
 internal/tenantpurge/harness_test.go               |   34 +-
 internal/tenantpurge/purge.go                      |    3 +-
 internal/tenantpurge/purge_test.go                 |    4 +-
 internal/workflow/document.go                      |    9 +
 internal/workflow/tags.go                          |  113 ++
 internal/workflow/tags_test.go                     |  188 ++
 migrations/postgres/000025_workflow_tags.down.sql  |    5 +
 migrations/postgres/000025_workflow_tags.up.sql    |   36 +
 .../postgres/000026_credential_events.down.sql     |    4 +
 .../postgres/000026_credential_events.up.sql       |   36 +
 migrations/sqlite/000025_workflow_tags.down.sql    |    5 +
 migrations/sqlite/000025_workflow_tags.up.sql      |   35 +
 .../sqlite/000026_credential_events.down.sql       |    4 +
 migrations/sqlite/000026_credential_events.up.sql  |   35 +
 nodes/ai.go                                        |  170 +-
 nodes/batch_shape_test.go                          |    1 +
 nodes/core.go                                      |    7 +-
 nodes/error_workflow.go                            |   81 +-
 nodes/executors.go                                 |   17 +-
 nodes/glyphs_test.go                               |    6 +
 nodes/gmail.go                                     |   40 +-
 nodes/google.go                                    |   54 +-
 nodes/google_sheets.go                             |  523 ++++++
 nodes/google_sheets_def_test.go                    |  233 +++
 nodes/google_sheets_exec.go                        |  638 +++++++
 nodes/google_sheets_export_test.go                 |   14 +
 nodes/google_sheets_fake_test.go                   |  534 ++++++
 nodes/google_sheets_review_test.go                 |  258 +++
 nodes/google_sheets_run_test.go                    |  203 ++
 nodes/google_sheets_template_run_test.go           |   96 +
 nodes/google_sheets_test.go                        |  526 ++++++
 nodes/google_sheets_trigger.go                     |  386 ++++
 nodes/google_sheets_trigger_review_test.go         |  192 ++
 nodes/google_sheets_trigger_test.go                |  386 ++++
 nodes/google_sheets_write.go                       |  598 ++++++
 nodes/google_sheets_write_test.go                  |  523 ++++++
 nodes/google_template_test.go                      |  191 ++
 nodes/openai_credential_url_test.go                |   18 +-
 nodes/pgvector.go                                  |   36 +-
 nodes/pgvector_customer.go                         |   26 +-
 nodes/pgvector_test.go                             |    9 +
 nodes/poll.go                                      |   20 +-
 nodes/ports_for_golden_test.go                     |  276 +++
 nodes/postgres_v2.go                               |  160 +-
 nodes/rag.go                                       |   13 +
 nodes/stop_and_error_leak_test.go                  |  115 ++
 nodes/subworkflow.go                               |   55 +
 nodes/subworkflow_inputs_test.go                   |   54 +
 nodes/testdata/ports_for.golden.json               | 1826 ++++++++++++++++++
 nodes/tool_form.go                                 |  486 +++++
 nodes/tool_form_postgres_live_test.go              |   96 +
 nodes/tool_form_run_test.go                        |  977 ++++++++++
 nodes/tool_form_test.go                            |  236 +++
 nodes/vectorstore_retrieve.go                      |   84 +
 nodes/vectorstore_retrieve_test.go                 |  250 +++
 nodes/vectorstore_tool.go                          |  325 ++++
 nodes/vectorstore_tool_export_test.go              |   20 +
 nodes/vectorstore_tool_security_test.go            |  208 +++
 nodes/vectorstore_tool_test.go                     |  565 ++++++
 nodes/workflow_tool_inputs_test.go                 |  263 +++
 schemas/workflow-v1.schema.json                    |    8 +-
 scripts/cli-examples/main.go                       |  583 ++++++
 scripts/cli-examples/main_test.go                  |  215 +++
 scripts/cli-examples/process_other.go              |   14 +
 scripts/cli-examples/process_test.go               |  110 ++
 scripts/cli-examples/process_unix.go               |   35 +
 scripts/cli-examples/seed.go                       |  330 ++++
 scripts/cli-examples/server.go                     |  183 ++
 scripts/cli-examples/testdata/credential.json      |    5 +
 scripts/cli-examples/testdata/patch.json           |   19 +
 scripts/cli-examples/testdata/payload.json         |    1 +
 scripts/cli-examples/testdata/wf.json              |    9 +
 scripts/cli-examples/testdata/workflow.json        |   28 +
 scripts/docs-facts/main.go                         |   20 +-
 scripts/docs-facts/main_test.go                    |  147 ++
 scripts/skills-command-reference/main.go           |   18 +-
 sdk/CHANGELOG.md                                   |   27 +
 sdk/README.md                                      |   20 +-
 sdk/examples/reference-host/server.mjs             |   12 +-
 sdk/src/generated/models.ts                        |   61 +-
 sdk/src/server.ts                                  |   17 +
 sdk/test/operation-coverage.test.mjs               |   21 +-
 sdk/test/operations.test.ts                        |   19 +
 skills/index.json                                  |  158 +-
 skills/kilasflow-ai-agents/SKILL.md                |  114 ++
 skills/kilasflow-ai-agents/references/EXAMPLE.md   |  146 ++
 skills/kilasflow-ai-agents/references/MODELS.md    |  105 ++
 skills/kilasflow-ai-agents/references/RAG.md       |  188 ++
 skills/kilasflow-credentials/SKILL.md              |   47 +-
 .../references/CREDENTIAL_TYPES.md                 |   28 +-
 skills/kilasflow-credentials/references/EXAMPLE.md |   97 +
 skills/kilasflow-datastore/SKILL.md                |   52 +-
 skills/kilasflow-datastore/references/EXAMPLE.md   |  130 ++
 skills/kilasflow-datastore/references/FILTERS.md   |   40 +-
 skills/kilasflow-debugging/SKILL.md                |   34 +-
 skills/kilasflow-debugging/references/EXAMPLE.md   |   70 +
 .../references/TRACE_READING.md                    |   43 +-
 skills/kilasflow-embedding/SKILL.md                |   57 +-
 .../references/EMBED_HANDSHAKE.md                  |   15 +-
 skills/kilasflow-embedding/references/EXAMPLE.md   |   82 +
 .../references/SESSION_AUTHORITY.md                |    4 +-
 skills/kilasflow-error-handling/SKILL.md           |   39 +-
 .../kilasflow-error-handling/references/EXAMPLE.md |  117 ++
 skills/kilasflow-expressions/SKILL.md              |   40 +-
 skills/kilasflow-expressions/references/EXAMPLE.md |  134 ++
 .../references/EXPRESSION_ROOTS.md                 |   61 +-
 skills/kilasflow-flow-patterns/SKILL.md            |  105 ++
 .../references/BINARY_DATA.md                      |  116 ++
 .../kilasflow-flow-patterns/references/EXAMPLE.md  |  134 ++
 .../references/SUB_WORKFLOWS.md                    |  173 ++
 skills/kilasflow-import-export/SKILL.md            |   34 +-
 .../kilasflow-import-export/references/EXAMPLE.md  |  108 ++
 .../references/MAPPING_LIMITS.md                   |   55 +-
 skills/kilasflow-node-configuration/SKILL.md       |   44 +-
 .../references/EXAMPLE.md                          |  141 ++
 .../references/LOAD_OPTIONS.md                     |   65 +-
 .../references/PROPERTY_KINDS.md                   |   67 +-
 skills/kilasflow-node-packs/SKILL.md               |   38 +-
 skills/kilasflow-node-packs/references/EXAMPLE.md  |   75 +
 .../references/MODULE_AND_SIDECAR.md               |   32 +-
 .../kilasflow-node-packs/references/PACK_FORMAT.md |   28 +-
 skills/kilasflow-operations/SKILL.md               |   17 +-
 skills/kilasflow-operations/references/EXAMPLE.md  |   86 +
 .../references/UPGRADE_ORDER.md                    |   15 +-
 skills/kilasflow-triggers/SKILL.md                 |   48 +-
 skills/kilasflow-triggers/references/EXAMPLE.md    |   80 +
 .../references/WEBHOOK_DELIVERY.md                 |   33 +-
 skills/kilasflow-workflow-lifecycle/SKILL.md       |   36 +-
 .../references/EXAMPLE.md                          |  111 ++
 .../references/NAMING_AND_DESCRIPTIONS.md          |   25 +-
 .../references/VALIDATION_CHECKLIST.md             |   29 +-
 skills/using-kilasflow-skills/SKILL.md             |   43 +-
 .../using-kilasflow-skills/references/EXAMPLE.md   |   65 +
 web/messages/en/credentials.json                   |    5 +
 web/messages/en/properties.json                    |    8 +
 web/messages/en/workflows.json                     |   15 +-
 web/messages/id/credentials.json                   |    5 +
 web/messages/id/properties.json                    |    8 +
 web/messages/id/workflows.json                     |   15 +-
 web/package.json                                   |    1 +
 web/src/app.css                                    |  688 +++++--
 web/src/app.d.ts                                   |    3 +
 .../lib/api/generated/credentials/credentials.ts   |   26 +-
 .../lib/api/generated/models/credentialResource.ts |    6 +
 .../generated/models/credentialUsageResource.ts    |   19 +
 .../api/generated/models/deleteCredentialParams.ts |   14 +
 web/src/lib/api/generated/models/document.ts       |    2 +
 web/src/lib/api/generated/models/index.ts          |    2 +
 .../api/generated/models/listWorkflowsParams.ts    |    5 +
 .../api/generated/models/loadOptionsInputBody.ts   |    2 +-
 .../generated/models/resourceMapperDeclaration.ts  |    1 +
 .../api/generated/models/workflowDocumentInput.ts  |    6 +
 .../lib/api/generated/models/workflowSummary.ts    |    2 +
 .../lib/components/dashboard/dashboard-nav.svelte  |   10 +-
 .../components/dashboard/workflow-tag-chips.svelte |   27 +
 web/src/lib/components/ui/button/button.svelte     |   14 +-
 web/src/lib/components/ui/checkbox/checkbox.svelte |    2 +-
 .../lib/components/ui/dialog/dialog-content.svelte |    2 +-
 .../dropdown-menu-checkbox-item.svelte             |    2 +-
 .../ui/dropdown-menu/dropdown-menu-content.svelte  |    2 +-
 .../ui/dropdown-menu/dropdown-menu-item.svelte     |    2 +-
 .../dropdown-menu/dropdown-menu-radio-item.svelte  |    2 +-
 .../dropdown-menu/dropdown-menu-sub-content.svelte |    2 +-
 .../dropdown-menu/dropdown-menu-sub-trigger.svelte |    2 +-
 web/src/lib/components/ui/input/input.svelte       |    4 +-
 web/src/lib/components/ui/list-row/index.ts        |    8 +
 web/src/lib/components/ui/list-row/list-row.svelte |   29 +
 .../components/ui/popover/popover-content.svelte   |    2 +-
 .../components/ui/scroll-area/scroll-area.svelte   |    2 +-
 .../lib/components/ui/select/select-content.svelte |    2 +-
 .../lib/components/ui/select/select-item.svelte    |    2 +-
 .../lib/components/ui/select/select-trigger.svelte |    2 +-
 .../lib/components/ui/sheet/sheet-content.svelte   |    2 +-
 web/src/lib/components/ui/sonner/sonner.svelte     |    2 +-
 web/src/lib/components/ui/switch/switch.svelte     |    4 +-
 web/src/lib/components/ui/tabs/tabs-content.svelte |    2 +-
 web/src/lib/components/ui/tabs/tabs-trigger.svelte |    2 +-
 web/src/lib/components/ui/textarea/textarea.svelte |    2 +-
 .../workflow-editor/canvas-chat-panel.svelte       |    4 +-
 .../components/workflow-editor/canvas-node.svelte  |   19 +-
 .../components/workflow-editor/code-editor.svelte  |    2 +-
 .../workflow-editor/execution-canvas-node.svelte   |    3 +-
 .../components/workflow-editor/node-picker.svelte  |    6 +-
 .../workflow-editor/properties-panel.svelte        |    2 +-
 .../workflow-editor/property-field.svelte          |  247 ++-
 .../workflow-editor/property-field.test.ts         |  201 ++
 .../workflow-editor/version-panel.svelte           |    2 +-
 .../workflow-editor/workflow-editor.svelte         |   16 +-
 web/src/lib/dashboard/credential-usage.test.ts     |   46 +
 web/src/lib/dashboard/credential-usage.ts          |   38 +
 web/src/lib/dashboard/workflow-list.test.ts        |  138 +-
 web/src/lib/dashboard/workflow-list.ts             |   99 +-
 web/src/lib/design-system/app-css.ts               |   15 +
 web/src/lib/design-system/class-scan.ts            |  245 +++
 web/src/lib/design-system/color.test.ts            |  120 ++
 web/src/lib/design-system/color.ts                 |  513 ++++++
 web/src/lib/design-system/contrast-pairs.ts        |   84 +
 web/src/lib/design-system/css-tokens.test.ts       |  110 ++
 web/src/lib/design-system/css-tokens.ts            |  236 +++
 web/src/lib/design-system/focus-lint.test.ts       |  143 ++
 web/src/lib/design-system/focus-lint.ts            |  236 +++
 web/src/lib/design-system/state-matrix.svelte      |  335 ++++
 web/src/lib/design-system/theme-color.test.ts      |   23 +
 web/src/lib/design-system/theme-color.ts           |   34 +
 web/src/lib/design-system/tokens.contrast.test.ts  |  116 ++
 web/src/lib/design-system/tokens.groups.test.ts    |  129 ++
 web/src/lib/design-system/tokens.hue.test.ts       |  177 ++
 web/src/lib/embed/brand-root.test.ts               |   44 +
 web/src/lib/embed/brand-root.ts                    |   24 +
 web/src/lib/workflow-editor/document.test.ts       |   18 +-
 web/src/lib/workflow-editor/document.ts            |   14 +-
 web/src/lib/workflow-editor/glyphs.json            |    1 +
 web/src/lib/workflow-editor/node-visual.test.ts    |   43 +-
 web/src/lib/workflow-editor/node-visual.ts         |   28 +-
 web/src/lib/workflow-editor/ports.golden.test.ts   |   77 +
 web/src/lib/workflow-editor/ports.imported.test.ts |  130 ++
 web/src/lib/workflow-editor/ports.modes.test.ts    |  201 ++
 web/src/lib/workflow-editor/ports.ts               |  120 +-
 .../lib/workflow-editor/resource-mapper.test.ts    |  262 ++-
 web/src/lib/workflow-editor/resource-mapper.ts     |  172 +-
 web/src/lib/workflow-editor/shortcuts.test.ts      |   37 +
 web/src/lib/workflow-editor/shortcuts.ts           |   23 +
 web/src/lib/workflow-editor/validation.test.ts     |   24 +
 web/src/lib/workflow-editor/validation.ts          |   14 +-
 web/src/lib/workflow-editor/workflow-input.ts      |   27 +
 web/src/routes/(dashboard)/+layout.svelte          |   11 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |   81 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   50 +-
 web/src/routes/+page.svelte                        |   34 +-
 web/src/routes/design/states/+page.svelte          |    5 +
 web/src/routes/design/states/+page.ts              |   16 +
 web/src/routes/embed/[id]/+page.svelte             |   13 +-
 web/vite.config.ts                                 |   10 +
 454 files changed, 59648 insertions(+), 1821 deletions(-)
```
