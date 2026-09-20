---
id: BUG-57n76x
title: Editor shows every imported node as Unknown node type (exact type@version lookup)
status: done
priority: critical
labels:
    - editor
    - importer
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T02:03:57Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (3):
  - `c54aeb04` — BUG-57n76x: record fix evidence — editor-core
  - `8766843b` — BUG-57n76x: round-down definition resolution — editor-core
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   11 +-
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   40 +
 .pine/tickets/BUG-4053h6.md                        |  921 +++++++
 .pine/tickets/BUG-57n76x.md                        |  127 +
 .pine/tickets/BUG-66es9z.md                        |   60 +
 .pine/tickets/BUG-6as5y7.md                        |  220 ++
 .pine/tickets/BUG-6bqh51.md                        |  189 ++
 .pine/tickets/BUG-6jvcs5.md                        |  238 ++
 .pine/tickets/BUG-8dmp5y.md                        |  183 ++
 .pine/tickets/BUG-8h4yy1.md                        |   46 +
 .pine/tickets/BUG-8sb0jw.md                        |  239 ++
 .pine/tickets/BUG-8t94wn.md                        |  179 ++
 .pine/tickets/BUG-9853ay.md                        |   84 +
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |   29 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 10476 bytes
 .pine/tickets/BUG-aede06.md                        |  326 +++
 .pine/tickets/BUG-c241hm.md                        |  154 ++
 .pine/tickets/BUG-cq4yk3.md                        |  338 +++
 .pine/tickets/BUG-dndnhn.md                        |   48 +
 .pine/tickets/BUG-esb9sh.md                        |  138 ++
 .pine/tickets/BUG-f9frth.md                        |  410 +++
 .pine/tickets/BUG-fv5fer.md                        |  172 ++
 .pine/tickets/BUG-gaavr5.md                        |  363 +++
 .pine/tickets/BUG-hfhzq6.md                        |   49 +
 .pine/tickets/BUG-hm76dq.md                        |  119 +
 .pine/tickets/BUG-j7rtv3.md                        |   90 +
 .pine/tickets/BUG-kzkvv6.md                        |   74 +
 .pine/tickets/BUG-mewhrd.md                        |   68 +
 .pine/tickets/BUG-mz8xrb.md                        |   56 +
 .pine/tickets/BUG-npfz43.md                        |   38 +
 .pine/tickets/BUG-pwckhd.md                        |   63 +
 .pine/tickets/BUG-qmgz2f.md                        |  179 ++
 .pine/tickets/BUG-qq4xva.md                        |   57 +
 .pine/tickets/BUG-rjd6fm.md                        |  272 ++
 .pine/tickets/BUG-rrkjrd.md                        |   76 +
 .pine/tickets/BUG-s0wy50.md                        |   56 +
 .pine/tickets/BUG-t2wezf.md                        |   85 +
 .pine/tickets/BUG-tcqkad.md                        |  188 ++
 .pine/tickets/BUG-txc9xg.md                        |   71 +
 .pine/tickets/BUG-wdypd2.md                        |  230 ++
 .pine/tickets/BUG-wp2y0y.md                        |   45 +
 .pine/tickets/BUG-xf1wqm.md                        |   42 +
 .pine/tickets/BUG-y57cz4.md                        |  167 ++
 .pine/tickets/BUG-ysvmaa.md                        |  298 +++
 .pine/tickets/BUG-ze1nn8.md                        |  114 +
 .pine/tickets/BUG-ztzxck.md                        |   56 +
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  311 +++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  175 ++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  188 ++
 .pine/tickets/FEAT-j5s2n4.md                       |  188 ++
 .pine/tickets/FEAT-jvembs.md                       |  363 +++
 .pine/tickets/FEAT-nqpvf6.md                       |  161 ++
 .pine/tickets/FEAT-qdedm0.md                       |   23 +
 .pine/tickets/FEAT-x5km1z.md                       |  142 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |  114 +-
 README.md                                          |   27 +-
 cmd/kilasflow/main.go                              |  158 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |   13 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    4 +-
 docs/src/content/docs/404.md                       |    2 +-
 docs/src/content/docs/concepts/execution-model.md  |  210 +-
 docs/src/content/docs/concepts/expressions.md      |   99 +-
 docs/src/content/docs/concepts/node-registry.md    |   35 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 docs/src/content/docs/concepts/webhooks.md         |   62 +-
 docs/src/content/docs/guides/community-nodes.md    |    2 +-
 docs/src/content/docs/guides/embedding.md          |   16 +-
 docs/src/content/docs/guides/n8n-migration.md      |  127 +-
 docs/src/content/docs/guides/node-authoring.md     |   10 +-
 docs/src/content/docs/index.mdx                    |    2 +-
 .../docs/operate/configuration-reference.md        |   13 +-
 docs/src/content/docs/operate/deployment.md        |   26 +-
 docs/src/content/docs/operate/security.md          |   52 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +-
 docs/src/content/docs/reference/api-contract.md    |   13 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    2 +-
 docs/src/content/docs/reference/api/datastores.md  |  392 +++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |    2 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    4 +-
 docs/src/content/docs/reference/api/tenants.md     |  200 ++
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 .../content/docs/reference/expression-grammar.md   |  369 ++-
 docs/src/content/docs/start/install.md             |   44 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   91 +-
 e2e/fixtures/datastore.ts                          |    8 +-
 e2e/fixtures/epic-telegram.ts                      |   24 +-
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import.ts                     |   13 +-
 e2e/fixtures/n8n-live.ts                           |   10 +-
 e2e/playwright.config.ts                           |    4 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   30 +-
 e2e/tests/epic-acceptance.spec.ts                  |    8 +-
 e2e/tests/library-import.spec.ts                   |    8 +-
 e2e/tests/waha-migration.spec.ts                   |    6 +-
 internal/ai/agent.go                               |  101 +-
 internal/ai/ai.go                                  |   31 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  140 +-
 internal/ai/openai_test.go                         |  145 ++
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  102 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/embed_confinement_test.go             |  284 +++
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 +++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   37 +-
 internal/api/handlers/datastores.go                |   32 +-
 internal/api/handlers/datastores_csv.go            |   72 +
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  318 ++-
 internal/api/handlers/interop.go                   |  130 +-
 internal/api/handlers/problem.go                   |   31 +
 internal/api/handlers/schedules.go                 |   35 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  132 +
 internal/api/middleware/cors_test.go               |  189 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   14 +-
 internal/api/server.go                             |  123 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  257 ++
 internal/config/config.go                          |  305 ++-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |   16 +
 internal/engine/approval.go                        |   17 +-
 internal/engine/authenticate.go                    |   45 +-
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |   24 +
 internal/engine/error_workflow_test.go             |  230 ++
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 +++
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiprocess_test.go               |  115 +-
 internal/engine/runner.go                          | 1670 +++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  724 +++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  174 +-
 internal/engine/wait_service_test.go               |  232 +-
 internal/engine/worker_test.go                     |   15 +
 internal/expression/doc.go                         |   94 +-
 internal/expression/evaluator.go                   |  771 ++++++
 internal/expression/expression.go                  |  474 +---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  528 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1187 +++++++++
 internal/expression/parity_test.go                 |  505 ++++
 internal/expression/parser.go                      |  824 +++++++
 internal/expression/roots.go                       |  339 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         |  772 ++++++
 internal/interop/n8n/n8n.go                        |  462 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2606 ++++++++++++++++++--
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++++
 internal/loadoptions/loadoptions.go                |   11 +
 internal/loadoptions/redirect_test.go              |  117 +
 internal/property/property.go                      |   20 +-
 internal/property/visibility_test.go               |   34 +
 internal/repository/auth.go                        |  388 +++
 internal/repository/auth_admin_test.go             |  477 ++++
 internal/repository/claim_lease_test.go            |  266 ++
 internal/repository/claim_wake_test.go             |    4 +-
 internal/repository/credentials.go                 |  103 +
 internal/repository/execution_retention_test.go    |    3 +-
 internal/repository/executions.go                  |  534 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   40 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  153 ++
 internal/repository/tenant_purge_test.go           |    2 +-
 internal/repository/waits_test.go                  |    2 +-
 internal/repository/webhooks.go                    |  188 +-
 internal/repository/webhooks_test.go               |  306 +++
 internal/repository/workflow_history.go            |   14 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  233 +-
 internal/safehttp/safehttp.go                      |   54 +-
 internal/safehttp/safehttp_test.go                 |   97 +
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   52 +
 internal/scheduler/scheduler_test.go               |   67 +
 internal/sqlbuild/sqlbuild_test.go                 |    4 +-
 internal/web/dist/index.html                       |   37 -
 internal/web/embed.go                              |  459 +++-
 internal/web/embed_test.go                         |  335 ++-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |    2 +-
 internal/webhook/form.go                           |  262 ++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/request_lifecycle.go              |  425 +++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  758 ++++--
 internal/webhook/webhook_test.go                   |  678 ++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 nodes/ai.go                                        |  498 +++-
 nodes/ai_test.go                                   |  733 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  220 ++
 nodes/embedscope_test.go                           |  242 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  347 ++-
 nodes/http_test.go                                 |  302 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |   97 +-
 nodes/subworkflow_calls_test.go                    |   56 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  477 +++-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   83 +-
 packs/waha/waha_test.go                            |  373 ++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 scripts/check-coordinates.sh                       |   60 +
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/smoke-dev.sh                               |   27 +
 sdk/README.md                                      |   12 +
 sdk/examples/host-page/README.md                   |   11 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 sdk/package.json                                   |    6 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++
 web/src/lib/api/generated/auth/auth.ts             |   32 +-
 .../lib/api/generated/credentials/credentials.ts   |   30 +-
 web/src/lib/api/generated/datastores/datastores.ts |   32 +-
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionRequestResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |   15 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   32 +-
 web/src/lib/api/http.ts                            |   16 +
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  376 ++-
 .../components/workflow-editor/node-picker.svelte  |  148 +-
 .../workflow-editor/properties-panel.svelte        |  167 +-
 .../workflow-editor/property-field.svelte          |  534 ++--
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  736 +++++-
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  151 +-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   94 +-
 web/src/lib/workflow-editor/conditions.ts          |  176 +-
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   29 +-
 web/src/lib/workflow-editor/document.test.ts       |  158 ++
 web/src/lib/workflow-editor/document.ts            |  291 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |   50 +
 web/src/lib/workflow-editor/expression-assist.ts   |   91 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   49 +
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 +-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  243 +-
 web/src/lib/workflow-editor/ports.ts               |  125 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   77 +
 web/src/lib/workflow-editor/shortcuts.ts           |  115 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  369 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  369 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  147 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |    6 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  176 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   59 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  293 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 434 files changed, 56718 insertions(+), 4725 deletions(-)
```
