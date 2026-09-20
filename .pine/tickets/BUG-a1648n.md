---
id: BUG-a1648n
title: Editor coerces imported n8n-v2 gt/gte/lt/lte conditions to equals on read, silently rewriting comparisons
status: done
priority: medium
created: "2026-09-20T01:17:16Z"
updated: "2026-09-20T02:03:59Z"
---

# Description

# Steps to Reproduce

# Expected

# Actual

# Acceptance Criteria
- [ ] Define acceptance criteria

# Related Files

# Attachments

## Work (WebFormsOps 2026-09-20)
- Root cause: KNOWN_OPERATIONS derived only from the editor's own ConditionOperator union (larger/smaller family) — n8n-v2 gt/gte/lt/lte fell through to the 'equals' default in operatorOf, so read+save rewrote the comparison.
- Fix: canonical ConditionOperator is now gt/gte/lt/lte (matches Go evaluator's numberOperation output vocabulary); legacy larger/largerEqual/smaller/smallerEqual folded via OPERATOR_ALIASES at the read boundary; dropdown offers canonical names with the same labels; typeForOperation coerces the type family when the operator changes (gt→number, after→dateTime, true→boolean).
- Tests: 2 new round-trip tests (v2 names survive read+write; legacy names fold to canonical). Pre-fix: 2 failed / 15 passed. Post-fix: 17 passed.
- Scope: web only. Flat-shape server path (readFilter in nodes/conditions.go) untouched — Go executor already folds spellings at comparison time.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `606355b1` (last commit at or before ticket created 2026-09-20)
- Commits (1):
  - `7bf4b552` — BUG-a1648n: recognise n8n-v2 gt/gte/lt/lte, fold legacy larger/smaller — conditions
- Files changed (base → working tree):

```
 .pine/tickets/BUG-1tj5wy.md                        | 454 +++++++++++++++++++-
 .pine/tickets/BUG-277a2m.md                        | 454 +++++++++++++++++++-
 .pine/tickets/BUG-341sxn.md                        |  40 ++
 .pine/tickets/BUG-4053h6.md                        | 464 ++++++++++++++++++++-
 .pine/tickets/BUG-57n76x.md                        | 455 +++++++++++++++++++-
 .pine/tickets/BUG-66es9z.md                        | 248 +++++++++++
 .pine/tickets/BUG-6as5y7.md                        | 453 +++++++++++++++++++-
 .pine/tickets/BUG-6bqh51.md                        | 455 +++++++++++++++++++-
 .pine/tickets/BUG-6jvcs5.md                        | 455 +++++++++++++++++++-
 .pine/tickets/BUG-8dmp5y.md                        | 460 +++++++++++++++++++-
 .pine/tickets/BUG-8h4yy1.md                        | 454 +++++++++++++++++++-
 .pine/tickets/BUG-8sb0jw.md                        | 460 +++++++++++++++++++-
 .pine/tickets/BUG-8t94wn.md                        | 453 +++++++++++++++++++-
 .pine/tickets/BUG-9853ay.md                        | 454 +++++++++++++++++++-
 .pine/tickets/BUG-a1648n.md                        |  29 ++
 .pine/tickets/BUG-aede06.md                        |  95 ++++-
 .pine/tickets/BUG-f9frth.md                        | 111 ++++-
 .pine/tickets/BUG-hm76dq.md                        |  27 +-
 .pine/tickets/BUG-j7rtv3.md                        |  90 ++++
 .pine/tickets/BUG-ysvmaa.md                        |  78 +++-
 .pine/tickets/FEAT-15k49d.md                       |  37 ++
 .pine/tickets/FEAT-cwmw90.md                       |  21 +
 .pine/tickets/FEAT-jvembs.md                       |   4 +-
 internal/api/handlers/interop.go                   | 130 +++++-
 internal/api/import_diagnostics_test.go            | 142 +++++++
 internal/conditions/conditions.go                  | 455 +++++++++++++++++---
 internal/conditions/conditions_test.go             | 195 +++++++++
 internal/conditions/doc.go                         |   8 +
 internal/engine/loopstate_test.go                  | 183 ++++++++
 internal/engine/runner_test.go                     | 136 ++++++
 internal/engine/wait_service_test.go               | 141 +++++++
 internal/interop/n8n/corpus/BASELINE.md            |  16 +-
 internal/interop/n8n/corpus/baseline.json          |  43 +-
 internal/interop/n8n/n8n_test.go                   |  56 +++
 internal/interop/n8n/parameters.go                 |  17 +-
 internal/interop/n8n/waitsubworkflow_test.go       |  44 ++
 internal/repository/import_diagnostics.go          |  83 ++++
 internal/repository/import_diagnostics_test.go     | 199 +++++++++
 internal/repository/models.go                      |   6 +
 internal/repository/workflow_history.go            |  14 +-
 internal/repository/workflows.go                   |  11 +-
 .../000012_workflow_import_diagnostics.down.sql    |   9 +
 .../000012_workflow_import_diagnostics.up.sql      |  29 ++
 .../000012_workflow_import_diagnostics.down.sql    |   9 +
 .../000012_workflow_import_diagnostics.up.sql      |  24 ++
 nodes/assignments.go                               |  56 ++-
 nodes/executors_test.go                            |  66 +++
 web/src/lib/api/generated/interop/interop.ts       | 115 ++++-
 .../generated/models/executionRequestResource.ts   |   1 +
 web/src/lib/api/generated/models/index.ts          |   2 +
 .../api/generated/models/runWorkflowInputBody.ts   |   2 +
 .../generated/models/workflowDiagnosticsParams.ts  |  14 +
 .../models/workflowDiagnosticsResource.ts          |  20 +
 .../workflow-lifecycle/workflow-lifecycle.ts       |   2 +-
 .../components/workflow-editor/canvas-node.svelte  |  45 ++
 .../workflow-editor/property-field.svelte          |  17 +-
 .../workflow-editor/workflow-editor.svelte         |  66 ++-
 web/src/lib/embed/embed-editor.svelte              |  10 +-
 web/src/lib/workflow-editor/conditions.test.ts     |  35 +-
 web/src/lib/workflow-editor/conditions.ts          |  59 ++-
 .../lib/workflow-editor/import-diagnostics.test.ts |  41 ++
 web/src/lib/workflow-editor/import-diagnostics.ts  |  92 ++++
 web/src/lib/workflow-editor/run-trigger.test.ts    |  75 ++++
 web/src/lib/workflow-editor/run-trigger.ts         |  39 ++
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  99 ++++-
 .../(dashboard)/app/workflows/import-dialog.svelte |  15 +-
 .../app/workflows/import-report-drawer.svelte      |  60 +++
 .../(dashboard)/app/workflows/import-report.svelte | 122 +++---
 68 files changed, 8983 insertions(+), 271 deletions(-)
```
