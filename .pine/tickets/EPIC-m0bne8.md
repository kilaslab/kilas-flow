---
id: EPIC-m0bne8
title: JWT webhook auth, generic credential family, and backend API gaps
status: done
priority: medium
created: "2026-09-20T05:07:08Z"
updated: "2026-09-20T05:55:13Z"
---

# Description

# Goals

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `794adbb3` (last commit at or before ticket created 2026-09-20)
- Files changed (base → working tree):

```
 .pine/MEMORY.md                                    |   1 +
 .pine/tickets/FEAT-3taswf.md                       |  14 +-
 .pine/tickets/FEAT-48hreg.md                       |  20 +-
 .pine/tickets/FEAT-7cg0cd.md                       |  20 +-
 .pine/tickets/FEAT-cwmw90.md                       | 513 ++++++++++++++++++++-
 docs/src/content/docs/reference/api-contract.md    |   5 +-
 docs/src/content/docs/reference/api.md             |   4 +-
 docs/src/content/docs/reference/api/workflows.md   |  21 +
 e2e/fixtures/live-backend.ts                       |  12 +-
 e2e/helpers/stub.ts                                |   8 +
 e2e/tests/live-backend-api.spec.ts                 |  52 +--
 e2e/tests/live-backend-webhook.spec.ts             | 117 ++++-
 go.mod                                             |   1 +
 go.sum                                             |   2 +
 internal/api/handlers/workflows.go                 |  70 ++-
 internal/api/workflows_test.go                     |  71 ++-
 internal/credentials/builtin.go                    |  68 +++
 internal/credentials/credentials_test.go           |  42 ++
 internal/credentials/registry.go                   |  54 +++
 internal/interop/n8n/parameters.go                 |   3 +-
 internal/webhook/shape.go                          |  15 +-
 internal/webhook/webhook.go                        |  21 +-
 internal/webhook/webhook_test.go                   | 124 ++++-
 nodes/core.go                                      |   5 +
 nodes/error_workflow.go                            |   4 +
 nodes/http.go                                      |   2 +
 nodes/presentation_test.go                         |  35 ++
 nodes/webhook.go                                   |  18 +-
 scripts/generate-api-reference.mjs                 |   2 +-
 sdk/src/generated/models.ts                        |  52 +++
 sdk/src/server.ts                                  |  10 +
 sdk/test/operation-coverage.test.mjs               |   1 +
 .../generated/models/executionNodeRunResource.ts   |   1 +
 web/src/lib/api/generated/workflows/workflows.ts   |  97 ++++
 34 files changed, 1396 insertions(+), 89 deletions(-)
```
