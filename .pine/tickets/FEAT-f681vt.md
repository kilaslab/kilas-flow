---
id: FEAT-f681vt
title: Ship the generic canvas editor and first runnable workflow slice
status: done
priority: critical
labels:
    - ui
    - canvas
    - workflow-editor
deps:
    - FEAT-3mady6
    - FEAT-7j7c84
    - FEAT-19f1ny
parent: EPIC-c7gbdp
phase: p1
created: "2026-08-29T15:40:40Z"
updated: "2026-09-05T01:12:00Z"
---

## Scope

Build `/app/workflows/:id` as a generic server-backed workflow editor using the node registry. The first demonstrable workflow is Manual Trigger → Set, saved through the workflow API and executed by the Go runtime. HTTP Request extends this exact slice later.

## Acceptance criteria

- The editor loads/saves canonical workflow JSON through REST, reconciles save failures visibly, and never treats browser canvas state as authoritative after refresh.
- Empty canvas, trigger picker, node-category/search picker, node creation/removal/positioning, edge creation/removal, and selection all work for registered core nodes.
- Node handles and connection validation derive from registry metadata; incompatible ports are blocked in the canvas and rejected again by the server.
- The property panel is generated from node metadata, keeps shared Settings separate from node-specific Parameters, and supports Manual Trigger, Set, IF, and Merge without separate hard-coded full-panel implementations.
- A user can create Manual Trigger → Set, save, run, and see success/failure feedback. Browser coverage proves this flow against a running API.
- Editor chrome is accessible and fits desktop and narrow mobile viewports without an accidental nested page scroller.

## References

- PRD: §§3.1, 23, 38, 43–44, 65 first vertical slice.
- Design reference: `06-canvas-empty-add-first-step.png`, `07-node-picker-triggers.png`, `08-node-picker-categories.png`, `09-node-picker-search-results.png`, `10-ndv-set-edit-fields.png`, `12-canvas-wired-manual-set-http.png`, `14-canvas-if-branching.png`.

## Relevant documentation

- Official Svelte Flow connection validation: https://svelteflow.dev/examples/interaction/validation
- Official Svelte Flow custom handles: https://svelteflow.dev/learn/customization/handles
- Use `find-docs` to refresh SvelteKit/Svelte Flow APIs for the installed versions before implementation; record exact links/versions.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `verification-before-completion`.
- `impeccable`, `web-design-guidelines`, `mobile-responsive-audit`, `playwright-cli` — use when their triggers apply.

## Implementation Plan

### Boundary and contracts

- Treat `GET /api/v1/node-types`, `GET/PUT /api/v1/workflows/{id}`, `POST /api/v1/workflows/{id}/run`, and `GET /api/v1/executions/{id}` as the only editor/runtime boundary. First confirm the final dependency commits expose those operations and regenerate/check the Orval client; do not add an editor-specific workflow shape, node catalogue, save endpoint, or execution path.
- Keep three client state classes explicit: the last server-confirmed `WorkflowResource.latestVersion.document`, a local editable draft derived from it, and ephemeral canvas selection/picker/panel state. A successful GET or PUT replaces the confirmed document and draft with the response; a failed PUT retains the draft, marks it unsaved, and exposes the RFC 9457 problem plus a retry action. Refresh always starts from GET.
- Run only a clean, saved draft. POST `/run` uses the latest saved revision, then the UI polls its returned execution ID only until `succeeded`, `failed`, or `cancelled`; detailed trace inspection stays with FEAT-0j7r5s.

### File map

- Create `web/src/lib/workflow-editor/document.ts` and `document.test.ts` for lossless conversion between canonical `Document`/`WorkflowDocumentInput` and Svelte Flow nodes/edges, UUID allocation, metadata-default initialization, dirty-state comparisons, and API-validation issue targeting.
- Create `web/src/lib/workflow-editor/ports.ts` and `ports.test.ts` for registry-derived handle lookup and connection validation. It must accept only declared output-to-input ports of the same `ConnectionKind`, reject unknown/wrong-direction/duplicate/self edges, and create canonical `Connection` records with the server vocabulary.
- Create `web/src/lib/components/workflow-editor/{workflow-editor,canvas-node,node-picker,properties-panel,property-field}.svelte`. These are the generic UI boundary: the picker and custom node read API `Definition` data, while `property-field` switches only on `PropertyDefinition.kind` (`string`, `number`, `boolean`, `select`, `keyValue`, `conditions`) and `visibleWhen`—never on a node type.
- Replace the placeholder in `web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte` with the query/loading/error/retry route shell and the generic editor. Modify `web/src/routes/(dashboard)/+layout.svelte` so this route alone receives a full-height, padding-free editor slot under the existing header; all other dashboard pages keep their current scrolling layout.

### Delivery sequence

1. Refresh the installed Svelte Flow/SvelteKit API reference before coding and record the exact version/links in the implementation notes. The initial lookup is `@xyflow/svelte` 1.6.5; use `SvelteFlow`, `Handle`, and `isValidConnection` only after checking the installed declarations. The current official references are https://svelteflow.dev/api-reference/components/handle and https://svelteflow.dev/examples/interaction/validation.
2. Write the pure failing tests first. Cover canonical document round-trips (including stored positions, connections, and absent optional maps), unique client node/edge IDs, defaults from registry metadata, dirty/reconciliation semantics, and port validation using both the current Manual Trigger → Set metadata and synthetic non-`main` ports. Then implement those helpers without touching generated API files.
3. Build the custom canvas node and controlled Svelte Flow adapter. Render one source `Handle` per `Definition.outputs` port and one target handle per `Definition.inputs` port, with port name/kind labels and stable handle IDs. Route position, addition, deletion, edge, and selection callbacks through the document helpers; canvas changes update only the local draft until Save.
4. Build the empty-canvas and picker flow from the fetched registry: the first-step action opens Triggers, category navigation is built from `Definition.category`, and search matches display name/description/category. Selecting a definition places one metadata-defaulted node at the requested or viewport-centred position and selects it. The same picker creates Set, IF, and Merge; it does not carry a frontend copy of those definitions.
5. Implement the generic property panel. Keep Parameters and Settings as separate tabs, preserve current values/defaults, evaluate all `visibleWhen` predicates, and write values back to the selected node’s `parameters` or `settings` map. The key/value control must produce a non-empty Set assignment such as `{ "status": "ready" }`; the condition control must produce the current core single-condition shape with `equals`, `notEquals`, `exists`, and `notExists`; select and number controls retain typed values. Manual Trigger therefore has only shared settings without a special panel, while Set/IF/Merge obtain their forms from their registry metadata.
6. Add editor chrome and persistence: workflow name/back navigation, dirty/saving/saved/error status, accessible Save/Run/Add controls, node/edge delete affordances, and inline validation feedback mapped to the affected node/edge. Save sends the canonical input without workflow identity and immediately adopts the response document/revision. Keep Run disabled while dirty and show server compiler failures from the run request instead of attempting local execution.
7. Add run feedback without growing an inspector: submit the manual run, announce queued/running state, poll the execution resource with cancellation/unmount cleanup, and show an accessible success/failure/cancelled result. The success path must confirm the Manual Trigger → Set output; failures retain the saved graph and surface the server’s message.
8. Finish responsive and accessibility behavior. Desktop uses canvas plus a selectable side property panel; narrow screens keep the canvas in the single editor viewport and expose properties/picker in focus-managed overlays. Verify named controls, keyboard node/edge deletion, visible focus, dialog/sheet focus return, and that 375×667 and 393×852 have no document-level or accidental nested editor scroll.
9. Verify with `cd web && pnpm test && pnpm check && pnpm generate:api:check && pnpm build`, then `make test`, `make lint`, `make smoke-dev`, and `make smoke-sqlite`. Use Playwright CLI against a temporary SQLite Go API plus Vite (the existing `scripts/smoke-dev.sh` lifecycle is the model) to prove create workflow → empty canvas → Manual Trigger → Set → connect → set `status=ready` → save → reload reconciliation → run → succeeded. Also exercise picker search/category, move/remove/select behavior, a forced PUT failure followed by retry, generic IF/Merge panels, incompatible-port rejection, keyboard/focus behavior, and the two narrow viewport sizes. Run `pine doctor` before handoff.

### Non-goals and handoff constraints

- No HTTP Request, credentials, expressions, activation UI, execution-history/trace panel, embed route changes, or frontend-only node metadata belongs in this ticket.
- Server-side compiler rejection is already the authoritative second validation pass. If the dependency implementation is not yet committed or its final generated API contract differs, resolve that boundary with FEAT-3mady6/FEAT-7j7c84 rather than introducing a compatibility shim in the canvas.

## Implementation Notes

- `@xyflow/svelte` 1.6.5. `SvelteFlow` (controlled `bind:nodes`/`bind:edges`), `Handle`, and `isValidConnection` per https://svelteflow.dev/api-reference/components/handle and https://svelteflow.dev/examples/interaction/validation.
- Pure helpers landed test-first in `web/src/lib/workflow-editor/{document,ports,validation,key-value}.ts`; generic UI in `web/src/lib/components/workflow-editor/`.
- Only one properties panel is mounted at a time. `mediaQuery('(max-width: 1023.98px)')` picks the desktop aside or the narrow-viewport dialog, instead of rendering both and hiding one with CSS — that duplicated every control id and kept an `aria-modal` dialog in the desktop DOM.
- The workspace is dark-first: the bare `:root` palette in `web/src/app.css` is the dark theme and light is opt-in via `[data-theme='light']`/`.light`. Svelte Flow's light-only `--xy-*` chrome defaults are bound to the KilasFlow palette so the canvas follows the theme.

## Work Evidence

- Browser-verified against a live SQLite API plus Vite: create workflow → empty canvas → Manual Trigger → picker search/category → Set → drag-connect main→main → `status=ready` → Save → reload reconciliation → Run → "Run succeeded."
- The persisted execution reported two succeeded node runs with output `{"status":"ready"}` for the Set node.
- 375×667: no document scroll, no accidental nested editor scroller, exactly one properties panel (the focus-managed dialog), desktop aside unmounted.
- `pnpm test` (21 passing), `pnpm check` (0 errors), `pnpm generate:api:check`, `pnpm build`, `go build ./...`, `go vet ./...`, `go test ./...`, `make smoke-sqlite`.
