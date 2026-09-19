---
id: BUG-57n76x
title: Editor shows every imported node as Unknown node type (exact type@version lookup)
status: testing
priority: critical
labels:
    - editor
    - importer
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T13:19:59Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Note: Single root cause across 5 dims. Server Registry.Resolve rounds down; SPA requires exact match (document.ts, workflow-editor.svelte, ports.ts, execution.ts).

Consolidates 5 finding(s) from dims: find:expression-parity, find:ui-canvas, find:ui-ndv, find:web-frontend-code, find:webhook-trigger-parity.

---
### Imported nodes keep n8n typeVersion (e.g. set 3.4, webhook 2) that the editor catalog lacks, so every imported node shows 'Unknown node type' and cannot be edited [find:expression-parity] (high/ui) · area: editor / importer typeVersion · confidence: medium

After import, nodes are stored with the n8n typeVersion. /api/v1/node-types advertises only version 1 for kilasflow.set and kilasflow.webhook, so the editor renders generic icons and a panel reading 'Unknown node type — This stored node version is not available in the current registry'. The server still executes the workflow. This blocks all in-editor expression editing of imported workflows. It may also be reported by the importer and UI dimensions.

Evidence: Imported wf_01a0b8d1-bba4-757a-9ef7-e1b750800757 has document nodes 'Seed kilasflow.set 3.4', 'Webhook kilasflow.webhook 2'. The catalog lists kilasflow.set {version: 1} and kilasflow.webhook {version: 1}. The run succeeded. Opening it in the editor shows generic cube icons, and clicking Edit Fields shows 'Unknown node type' (work/expression-parity/set-expr-rows.png, editor-state.png). The same document with typeVersion rewritten to 1 opens normally (set-native-rows.png).

n8n behavior: Each typeVersion stored in a workflow has its own node description, so imported nodes always open in the NDV.

Impact: Every imported n8n workflow. Users cannot inspect or fix imported parameters or expressions in the editor.

Suggested fix: Have the catalog advertise every version the server accepts (or version ranges), or have the editor fall back to the closest registered version of the same type. Keep the importer's typeVersion consistent with what the editor can render.

Files: internal/interop/n8n/n8n.go, internal/api/handlers/nodes.go, web/src/lib/components/workflow-editor/properties-panel.svelte

Existing tickets: FEAT-wdnc03

---
### Imported nodes that keep n8n's typeVersion open as "Unknown node type": no inspector, and ports exist only where wires already are [find:ui-canvas] (critical/bug) · area: editor canvas / inspector for imported n8n workflows · confidence: high

The importer keeps n8n's typeVersion (for example httpRequest@4.2, set@3.4, switch@3.2, agent@1.6). The server resolves those downward to v1 (Registry.Resolve), but the SPA looks up definitions by exact type@version. Every such node is therefore drawn from `unavailableDefinition`. It shows the "Unknown node type" panel with no parameters, a generic icon, and ports built only from existing connections.

Evidence: Template 2465 imported as wf_01a0af1e-8151-735f-8a75-7ba1e7ed5680. Selecting 'get Product Brochure' (kilasflow.httpRequest@4.2) shows 'Unknown node type — This stored node version is not available in the current registry. Its configuration will be preserved.' and no fields (screenshot /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-canvas/05-http-selected.png). Code: web/src/lib/workflow-editor/document.ts definitionKey/documentFromCanvas falls back to unavailableDefinition → portsFromConnections; workflow-editor.svelte:195 selectedDefinition uses an exact type+version match; internal/node/registry.go:364 Resolve picks the highest version <= the requested one. Side effects seen on template 1954 (t13): the handles read 'AI Agent model attachment' (lowercase port name) and the agent has no main output. After deleting the OpenAI

n8n behavior: Any node opens its editing panel (NDV) with every parameter, whatever its typeVersion. Ports come from the node type.

Impact: 93/100 imported templates contain nodes that cannot be configured in the editor. Users can't fix the credentials, prompts or URLs the importer asks them to fix. Deleting a wire on such a node is permanent.

Suggested fix: Resolve definitions client-side with the same rule as Registry.Resolve (highest registered version <= requested). Alternatively, have GET /workflows/{id} or /node-types return a resolved definition per node. Use it in documentFromCanvas, selectedDefinition and loadOptions. Add a test using an imported document (for example Set 3.4 and HTTP 4.2).

Files: /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/document.ts, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/internal/node/registry.go

Existing tickets: FEAT-k3fmj1, FEAT-chxkvq

---
### Imported n8n nodes open as "Unknown node type" with no parameters, because the editor needs an exact typeVersion match [find:ui-ndv] (critical/bug) · area: NDV / node definition lookup · confidence: high

The importer keeps each n8n node's own typeVersion (HTTP Request 4.2, Set 3.4, If 2.2 and so on). The server picks the closest registered version at or below that number (Resolve), but the editor looks for an exact `definition.version === node.typeVersion`. Almost every imported node therefore opens as 'Unknown node type - This stored node version is not available in the current registry'. No parameters or settings are shown and the canvas icon is a generic cube, even though the server runs the node.

Evidence: web/src/lib/components/workflow-editor/workflow-editor.svelte:195-197 does the exact match. internal/node/registry.go:364-390 Resolve() picks the highest registered version <= requested. Live: open /app/workflows/wf_01a0af1f-80ae-7bee-bef0-1b8c5b6bc688 ([ui-ndv] core nodes, imported from n8n) and click 'Edit Fields'. The panel shows 'Unknown node type' (screenshots work/ui-ndv/31-imported-set-unknown.png, 02-http-panel.png). work/ui-ndv/versions.py over import-baseline.json: 984 of 1166 mapped (non-placeholder, non-sticky) nodes in 93/100 imported templates have a version the catalog does not list. Top ones: httpRequest 4.2 x180, set 3.4 x176, foreignCode 2 x70, if 2.2 x50, lmChatOpenAi 1.2 x47, telegram 1.2 x38, wait 1.1 x33, merge 3 x29.

n8n behavior: n8n opens every node version it ships with its own parameter UI, and the NDV footer names the version (e.g. 'AI Agent node version 3.1 (Latest)').

Impact: 93/100 imported templates cannot be edited in the NDV. The panel text is also wrong: it says the version is unavailable while the workflow runs. This blocks the core V2 'import then adjust' journey.

Suggested fix: Resolve definitions in the editor with the same rule the server uses (highest registered version <= requested, or the highest when none is asked for), or have GET /node-types or the workflow response return the resolved definition per node. Show the stored and resolved versions in a footer, as n8n does.

Files: web/src/lib/components/workflow-editor/workflow-editor.svelte, web/src/lib/components/workflow-editor/canvas-node.svelte, internal/node/registry.go

Existing tickets: FEAT-k3fmj1

---
### Editor shows imported trigger nodes (and any other versioned node) as "Unknown node type" because it looks up the exact typeVersion, so imported webhooks can't be configured [find:webhook-trigger-parity] (critical/bug) · area: editor node panel / node definition resolution · confidence: high

The importer keeps n8n's typeVersion (webhook 2, respondToWebhook 1.1/1.4, schedule 1.2, wait 1.1, executeWorkflowTrigger 1.1, set 3.4, httpRequest 4.1). The server picks the highest registered version at or below the requested one. The editor instead keys definitions by the exact `${type}@${version}`, so every such node renders as "Unknown node type" with no parameters and a generic icon.

Evidence: Imported a Webhook v2 workflow (echo, wf_01a0b8e3-4276-...) and opened it at /app/workflows/<id>. The node panel reads "Unknown node type — This stored node version is not available in the current registry. Its configuration will be preserved." Baseline template 5171 (wf_01a0af16-3806-7af8-85d1-25933597dd31): '1. The Kitchen (GET /menu)' shows the same, and every node except the v1 Manual Trigger shows a generic cube icon. Code: web/src/lib/workflow-editor/document.ts:78-83 and 209-211 (definitionKey exact match), ports.ts:86, execution.ts:132. The server uses Catalog.Resolve (internal/webhook/webhook.go:668-673). Screenshots: /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/webhook-trigger-parity/ui-imported-webhook-unknown.png and ui-5171-canvas.png

n8n behavior: The n8n editor renders each node with the parameter set of its own typeVersion.

Impact: After import the user cannot view or change the webhook path, method, auth or response mode. They also cannot attach the credentials the import report asks for, or fix any blocking issue in the UI. The import → fix → activate loop is broken for essentially every imported n8n workflow.

Suggested fix: Resolve definitions in the editor with the server's rule (highest version <= requested, falling back to latest), or have /node-types return a resolution map. Apply it in document.ts, ports.ts and execution.ts. Add an e2e test that opens nodes of an imported n8n workflow.

Files: /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/document.ts, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/ports.ts, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/execution.ts, /Users/izzadev/projects/k-flow/internal/interop/n8n/n8n.go

Existing tickets: FEAT-k3fmj1

---
### Editor looks up node definitions by exact type and version while the server rounds down, so 984 imported nodes in 93/100 templates show as 'Unknown node type' with no inspector and no ports [find:web-frontend-code] (critical/parity-gap) · area: editor / n8n import · confidence: high

The importer keeps n8n's typeVersion (FEAT-k3fmj1): httpRequest 4.2, set 3.4, if 2.2, merge 3, telegram 1.2, and so on. The server's registry resolves 'highest registered version <= requested' (internal/node/registry.go:364-386), so these nodes compile and run. The editor instead needs an exact type@version match (document.ts:78-82, workflow-editor.svelte:195-197, and the same projection in execution-canvas). Every such node therefore gets a fallback definition: grey tile, 'Unknown node type ... not available in the current registry', no properties panel, and ports inferred only from existing connections, so an unconnected imported node cannot be wired at all.

Evidence: Imported templates/2777.json as wf_01a0b8e1-2539-7d07-877b-0c4f9cbc3e88 and clicked 'DeepSeek JSON Body' (kilasflow.httpRequest 4.2). The side panel shows 'Unknown node type / This stored node version is not available in the current registry' (screenshot /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/web-frontend-code/sel1.png). GET /api/v1/node-types lists kilasflow.httpRequest only at version 1. The script work/web-frontend-code/vermismatch.py over all 100 baseline imports found 984 nodes with a catalogued type but a non-exact version, in 93 templates. Top cases: httpRequest@4.2 x180, set@3.4 x176, foreignCode@2 x70, if@2.2 x50, lmChatOpenAi@1.2 x47, pack.telegram@1.2 x38, wait@1.1 x33, merge@3 x29.

n8n behavior: n8n opens every node of a supported type and version in the NDV, and the footer states the resolved version.

Impact: After import, users cannot view or fix the configuration of most mapped nodes (HTTP, Set, IF, Merge, Telegram, AI model). This blocks the n8n-first workflow of 'import, then fix the placeholders in the editor' for 93/100 templates. The execution replay canvas also mislabels these nodes.

Suggested fix: Add one shared resolveDefinition(type, version, definitions) that applies the same rounding-down rule as Registry.Resolve, and use it in document.ts, workflow-editor.svelte, execution-canvas and the version diff. Alternatively have the API return the resolved version with each node. Show 'n8n v4.2 → KilasFlow v1' in the inspector header.

Files: /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/document.ts, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/execution-canvas.svelte, /Users/izzadev/projects/k-flow/internal/node/registry.go

Existing tickets: FEAT-k3fmj1

## Acceptance criteria

- [ ] Imported nodes keep n8n typeVersion (e.g. set 3.4, webhook 2) that the editor catalog lacks, so every imported
- [ ] Imported nodes that keep n8n's typeVersion open as "Unknown node type": no inspector, and ports exist only whe
- [ ] Imported n8n nodes open as "Unknown node type" with no parameters, because the editor needs an exact typeVersi
- [ ] Editor shows imported trigger nodes (and any other versioned node) as "Unknown node type" because it looks up 
- [ ] Editor looks up node definitions by exact type and version while the server rounds down, so 984 imported nodes
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)

## WebEditorCore plan (2026-09-19)
Scope: client-side round-down only. No importer/registry changes.
1. `document.ts`: add exported `resolveDefinition(type, ver, defs)` = highest registered `<=` requested, else latest, else null; use in `documentFromCanvas`; drop exact `definitionKey` map.
2. `ports.ts:86`, `execution.ts:132`, `workflow-editor.svelte:195`: same `resolveDefinition` (import from `document.ts`).
3. Footer stored→resolved in `workflow-editor.svelte` inspector (own file; `properties-panel.svelte` untouched — Web-Forms-Ops).
5. Scoped `vitest run` on those files, commit, mark testing.

## Fix & test evidence (WebEditorCore, 2026-09-19)
Commit `8766843` — `resolveDefinition(type, ver, defs)` = highest registered `<=` requested, else latest, else null; used in `documentFromCanvas` (`document.ts`), `lookupPort` (`ports.ts:86`), `edgeItemCounts` (`execution.ts:132`), `selectedDefinition` (`workflow-editor.svelte:195`); stored→resolved footer in inspector (`Stored vX · resolved to vY`, wide + narrow).
Tests: `npx vitest run document.test.ts ports.test.ts execution.test.ts` → 3 files, 36 tests passed (new: round-down set@3.4/http@4.2/webhook path, latest-fallback, projection/ports/counts version-tolerant).
Residual: properties-panel footer untouched (Web-Forms-Ops file); execution-canvas needs no change (shares `documentFromCanvas`).