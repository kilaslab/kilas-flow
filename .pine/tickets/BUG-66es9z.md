---
id: BUG-66es9z
title: Datastore ifExists/ifNotExists double-output port defect (canvas + executor)
status: done
priority: medium
created: "2026-09-20T00:50:48Z"
updated: "2026-09-20T02:03:57Z"
---

# Description

# Steps to Reproduce

# Expected

# Actual

# Acceptance Criteria
- [ ] Define acceptance criteria

# Related Files

# Attachments

## Work (PortsDiagnostics 2026-09-20, nodes/datastore.go + canvas port mirror)

Commit `db8831e` — nodes/datastore.go, nodes/datastore_test.go, web/src/lib/workflow-editor/ports.ts, web/src/lib/workflow-editor/ports.test.ts.

**The defect.** `datastorePortsFor` returned two outputs both named `main`. A connection's port is resolved *by name* (`internal/workflow/compiler.go` `outputPort`, first match wins; the n8n importer's `resolvePort`/`outputIndexesFor` do the same), so every wire — drawn in the canvas or imported — landed on output index 0 and the second output could not be addressed at all: a connection naming it was refused by the compiler (`port.unknown`, "connection must reference declared source and target ports"), and the canvas's two handles shared one id.

**Fix.**
- The two ports are now the two outcomes of the operation's own test, named the way the IF node names its branches (`nodes/core.go`): `true` for the outcome tested for, `false` for the one it does not. Identity holds while the label follows the configuration — If Exists: `Row found` / `No row`; If Not Exists: `No row` / `Row found`. This is the Switch rule (the name is the identity, the label moves) applied to a fixed fork; per-operation names (`rowFound`) would have swapped the ports' identity between the two operations.
- `web/src/lib/workflow-editor/ports.ts` `datastoreOutputs` mirrors the same two names and labels, so the canvas handle id, `canConnect` and the saved connection agree with the server.
- **Behaviour change in the executor (deliberate, flagged):** `runBranch` sent the tested item to the *second* port for If Not Exists whichever way the test went, so its first port could never carry anything (`emit([])` — a fork with a permanently dead arm). The item now leaves on the first port when the table holds no match, mirroring If Exists' "test held → first port". The old test pinned the dead arm; it was rewritten to the mirror rule with both outcomes asserted.

**Evidence** (scoped, HEAD = `db8831e`):
```
go test ./nodes/ -run 'Datastore|IfExists' -count=1            # ok
web: vitest run src/lib/workflow-editor/ports.test.ts          # 17 passed
```
Pre-fix, against the reverted file, the new tests fail for the right reasons:
- `ifExists outputs are both named "main", so one of them can never be addressed`
- `Compile() error = connection must reference declared source and target ports` (the second port was unreachable)
- `if-not-exists on an absent row = [0 1] items per port, want 1 and 0` (the dead first port)

What the tests prove: (1) both branch operations declare two outputs with distinct names and labels; (2) a document wiring `false` compiles with `SourceOutputIndex == 1`, and the retired `main` name is refused with the port named instead of being silently rewired onto the first branch; (3) in a compiled run, an absent row puts the tested item on the node wired to the second port while the first port's node is recorded skipped, and a matching row does the reverse.

**Not verified:** the canvas was not rendered in a browser for this change (no server session was used; other agents hold uncommitted server/frontend work). The derivation the canvas consumes (`resolvedPorts` → handle id `port.name`, label `portLabel`) is pinned by the vitest file.

**For anyone holding a document written against the old shape:** a connection whose source port is `main` on a branch node is now refused with `port.unknown` (previously it silently ran the first branch). No migration exists — `schemaVersion` is a gate, not a migration — and none is needed for this repository's data (the local store holds no workflows); it is called out here because the refusal is user-visible.

### Documented bound (export to n8n)

n8n's Data Table node answers `rowExists`/`rowNotExists` with a **single** pass-through output (its docs; the KilasFlow node deliberately forks instead). A KilasFlow-authored wire on the second branch therefore exports to `main[1]` — `internal/interop/n8n/n8n.go` `Export` writes the slot the source node's own port index maps to (`outputIndexesFor`, "false" → 1) — and n8n, which walks only the outputs its node declares, will not run it. The wire is not *rewired* (index 1 ≠ 0) and nothing else in the file is affected, but the second branch does not survive an n8n round trip.

Not fixed here: an honest diagnostic needs "how many item outputs does n8n's equivalent declare" as data on the mapping table (`mapping` in `internal/interop/n8n/n8n.go`), which is a wider change than this ticket. Worth a ticket of its own if the n8n round trip is to stay lossless-by-report.

### Canvas note

`canvas-node.svelte` renders outputs as `{#each mainOutputs as port, index (port.name)}` — **keyed by the port name**. Two ports named `main` were therefore a duplicate key as well as a collapse, so the fix is what makes the second handle exist at all; the add-step button is wired to `port.name`, which is the same string the connection and the compiler use.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `677010c9` (last commit at or before ticket created 2026-09-20)
- Commits (2):
  - `31c424f8` — chore(pine): file editor-loop, conditions-coercion, paging and follow-up tickets
  - `db8831e7` — BUG-66es9z: name the datastore branch ports apart, and route If Not Exists to the first one — nodes/canvas
- Files changed (base → working tree):

```
 .pine/tickets/BUG-1tj5wy.md                        |  454 +++++++-
 .pine/tickets/BUG-277a2m.md                        |  456 +++++++-
 .pine/tickets/BUG-341sxn.md                        |   40 +
 .pine/tickets/BUG-4053h6.md                        |  478 ++++++++-
 .pine/tickets/BUG-57n76x.md                        |  455 +++++++-
 .pine/tickets/BUG-66es9z.md                        |   60 ++
 .pine/tickets/BUG-6jvcs5.md                        |    2 +
 .pine/tickets/BUG-8dmp5y.md                        |   30 +
 .pine/tickets/BUG-8h4yy1.md                        |   12 +-
 .pine/tickets/BUG-a1648n.md                        |   29 +
 .pine/tickets/BUG-aede06.md                        |  101 +-
 .pine/tickets/BUG-c241hm.md                        |   22 +-
 .pine/tickets/BUG-cq4yk3.md                        |    4 +-
 .pine/tickets/BUG-f9frth.md                        |  144 ++-
 .pine/tickets/BUG-fv5fer.md                        |    4 +-
 .pine/tickets/BUG-hm76dq.md                        |   27 +-
 .pine/tickets/BUG-j7rtv3.md                        |   90 ++
 .pine/tickets/BUG-kzkvv6.md                        |    4 +-
 .pine/tickets/BUG-mz8xrb.md                        |    2 +
 .pine/tickets/BUG-pwckhd.md                        |    4 +-
 .pine/tickets/BUG-qmgz2f.md                        |    9 +-
 .pine/tickets/BUG-rrkjrd.md                        |   11 +-
 .pine/tickets/BUG-t2wezf.md                        |   17 +-
 .pine/tickets/BUG-tcqkad.md                        |    2 +
 .pine/tickets/BUG-ysvmaa.md                        |   78 +-
 .pine/tickets/BUG-ztzxck.md                        |    2 +
 .pine/tickets/FEAT-0895qc.md                       |    3 +-
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-jvembs.md                       |    4 +-
 .pine/tickets/FEAT-nqpvf6.md                       |   13 +-
 .pine/tickets/FEAT-qdedm0.md                       |   23 +
 cmd/kilasflow/main.go                              |   15 +-
 docs/src/content/docs/concepts/execution-model.md  |   16 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 ++-
 docs/src/content/docs/concepts/webhooks.md         |   62 +-
 docs/src/content/docs/guides/embedding.md          |   11 +-
 docs/src/content/docs/guides/n8n-migration.md      |  103 +-
 docs/src/content/docs/operate/security.md          |   28 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |    2 +-
 internal/api/credentials_pagination_test.go        |   76 ++
 internal/api/handlers/credentials.go               |   28 +-
 internal/api/handlers/interop.go                   |  130 ++-
 internal/api/handlers/workflows.go                 |   30 +-
 internal/api/import_diagnostics_test.go            |  142 +++
 internal/api/workflows_test.go                     |   74 +-
 internal/conditions/conditions.go                  |  455 ++++++--
 internal/conditions/conditions_test.go             |  195 ++++
 internal/conditions/doc.go                         |    8 +
 internal/engine/authenticate.go                    |   17 +
 internal/engine/authenticate_test.go               |  135 +++
 internal/engine/error_workflow_test.go             |  230 ++++
 internal/engine/lease_test.go                      |    2 +-
 internal/engine/live_progress_test.go              |  187 ++++
 internal/engine/loopstate_test.go                  |  183 ++++
 internal/engine/multiprocess_test.go               |   16 +-
 internal/engine/runner.go                          |  124 ++-
 internal/engine/runner_test.go                     | 1094 +++++++++++++++++++-
 internal/engine/service.go                         |  268 ++++-
 internal/engine/service_test.go                    |  134 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |   29 +-
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |   24 +-
 internal/engine/wait_service_test.go               |  156 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/importer_tail_test.go         |  151 +++
 internal/interop/n8n/n8n.go                        |   72 +-
 internal/interop/n8n/n8n_test.go                   |   68 +-
 internal/interop/n8n/parameters.go                 |  308 +++++-
 internal/interop/n8n/waitsubworkflow_test.go       |   44 +
 internal/repository/claim_lease_test.go            |    2 +-
 internal/repository/claim_wake_test.go             |    4 +-
 internal/repository/credentials.go                 |  103 ++
 internal/repository/execution_retention_test.go    |    3 +-
 internal/repository/executions.go                  |   58 +-
 internal/repository/import_diagnostics.go          |   83 ++
 internal/repository/import_diagnostics_test.go     |  199 ++++
 internal/repository/models.go                      |    6 +
 internal/repository/models_test.go                 |  103 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/subworkflow_activation_test.go |  153 +++
 internal/repository/tenant_purge_test.go           |    2 +-
 internal/repository/waits_test.go                  |    2 +-
 internal/repository/workflow_history.go            |   14 +-
 internal/repository/workflows.go                   |   91 +-
 internal/scheduler/extract.go                      |    6 +
 internal/webhook/form.go                           |  262 +++++
 internal/webhook/form_test.go                      |  169 +++
 internal/webhook/shape.go                          |   40 +
 internal/webhook/webhook.go                        |   96 +-
 internal/webhook/webhook_test.go                   |   96 ++
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 nodes/assignments.go                               |   56 +-
 nodes/core.go                                      |    3 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +++-
 nodes/error_workflow.go                            |  223 ++++
 nodes/error_workflow_test.go                       |  118 +++
 nodes/executors.go                                 |    2 +
 nodes/executors_test.go                            |   66 ++
 nodes/http.go                                      |    6 +-
 nodes/subworkflow.go                               |   28 +
 nodes/subworkflow_calls_test.go                    |   56 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    1 +
 nodes/webhook.go                                   |  237 ++++-
 sdk/examples/reference-host/README.md              |   59 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++++++++++++
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
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   32 +-
 .../components/workflow-editor/canvas-node.svelte  |   45 +
 .../workflow-editor/property-field.svelte          |   17 +-
 .../workflow-editor/workflow-editor.svelte         |   66 +-
 web/src/lib/embed/embed-editor.svelte              |  151 ++-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/workflow-editor/conditions.test.ts     |   35 +-
 web/src/lib/workflow-editor/conditions.ts          |   59 +-
 web/src/lib/workflow-editor/credentials.test.ts    |    4 +-
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 ++
 web/src/lib/workflow-editor/ports.test.ts          |  224 +++-
 web/src/lib/workflow-editor/ports.ts               |  122 ++-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 ++
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |    2 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  356 ++++++-
 .../(dashboard)/app/workflows/import-dialog.svelte |   15 +-
 .../app/workflows/import-report-drawer.svelte      |   60 ++
 .../(dashboard)/app/workflows/import-report.svelte |  122 ++-
 .../routes/(dashboard)/credentials/+page.svelte    |    2 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |    2 +-
 web/src/routes/(dashboard)/executions/+page.svelte |    2 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |    4 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |    2 +-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 174 files changed, 12864 insertions(+), 702 deletions(-)
```
