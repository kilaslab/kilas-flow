---
id: FEAT-ecv13e
title: Gmail Trigger and Get for inbox RAG
status: done
priority: high
labels:
    - google
    - gmail
deps:
    - FEAT-mc6s65
    - FEAT-wcmk3r
parent: EPIC-hkypt5
created: "2026-09-21T12:03:36Z"
updated: "2026-09-21T13:39:52Z"
---

# Description

Template 5908 is Gmail Trigger → Gmail Get → insert, plus Chat/Agent retrieve-as-tool. Send is implemented but not blocking ingest.

# Acceptance Criteria
- [x] `kilasflow.gmailTrigger` polls `users.messages` with a leased cursor
- [x] First tick records listed ids and emits nothing
- [x] Later ticks skip already-seen message ids
- [x] `kilasflow.gmail` Get / Get Many / Send
- [x] Credential type `gmailOAuth2`

# Implementation Plan
Poller uses the shared Google client. Drafts, labels, reply, threads, attachments are the low-parity ticket.

# Notes
Hosted/public Chat Trigger remains out of scope; 5908 uses the editor chat trigger already on this branch.

# Related Files
- `nodes/gmail.go`
- `nodes/google_test.go`

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `c2e302d7` (last commit at or before ticket created 2026-09-21)
- Files changed (base → working tree):

```
 cmd/kilasflow/main.go                              |  45 +++-
 config.example.yaml                                |  11 +
 docs/src/content/docs/concepts/credentials.md      |   9 +-
 .../docs/operate/configuration-reference.md        |  27 ++
 docs/src/content/docs/operate/tenant-deletion.md   |  10 +-
 docs/src/content/docs/reference/api-contract.md    |   5 +-
 docs/src/content/docs/reference/api.md             |   4 +-
 docs/src/content/docs/reference/api/credentials.md |  25 ++
 e2e/fixtures/n8n-live.ts                           |   9 +
 e2e/tests/n8n-compare.spec.ts                      |  53 ++--
 e2e/tests/node-coverage.spec.ts                    |  79 +++++-
 go.mod                                             |   1 +
 go.sum                                             |   2 +
 internal/api/handlers/credentials.go               |  43 +++-
 internal/api/handlers/executions.go                |   2 +-
 internal/api/middleware/embed_sentences_test.go    |   1 +
 internal/api/middleware/scope_test.go              |   1 +
 internal/api/routes.go                             |  21 +-
 internal/api/server.go                             |   9 +
 internal/cli/openapi_contract_test.go              |   2 +-
 internal/config/config.go                          |  20 ++
 internal/credentials/builtin.go                    |   6 +
 internal/credentials/credentials_test.go           |   2 +
 internal/database/migrate_test.go                  |   1 +
 internal/database/workflow_actor_migration_test.go |  10 +-
 internal/engine/service.go                         |  12 +
 internal/execution/records.go                      |   2 +
 internal/interop/n8n/n8n.go                        |  48 ++++
 internal/interop/n8n/n8n_test.go                   |  18 ++
 internal/interop/n8n/parameters.go                 |  35 +++
 internal/repository/postgres_execution_test.go     |   1 +
 internal/repository/table_names_test.go            |  11 +
 internal/repository/tenant_rows.go                 |   9 +-
 internal/repository/workflows.go                   |  20 ++
 internal/tenantpurge/harness_test.go               |  25 +-
 internal/tenantpurge/purge.go                      |   2 +-
 nodes/ai.go                                        | 139 +++++++++++
 nodes/core.go                                      |  13 +
 nodes/executors.go                                 |  12 +-
 nodes/pgvector.go                                  | 271 +++++++++++++++------
 nodes/pgvector_test.go                             |  19 +-
 scripts/generate-api-reference.mjs                 |   2 +-
 sdk/src/generated/models.ts                        |  58 +++++
 web/messages/en/credentials.json                   |   7 +
 web/messages/id/credentials.json                   |   7 +
 .../lib/api/generated/credentials/credentials.ts   |  94 +++++++
 web/src/lib/api/generated/models/index.ts          |   1 +
 .../routes/(dashboard)/credentials/+page.svelte    |  92 ++++++-
 web/vite.config.ts                                 |   2 +-
 49 files changed, 1139 insertions(+), 159 deletions(-)
```
