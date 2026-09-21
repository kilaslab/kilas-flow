---
id: FEAT-yxpj0d
title: 'Cluster RAG: embeddings descriptors, loader, splitter, retrieve-as-tool'
status: done
priority: high
labels:
    - rag
    - ai
parent: EPIC-hkypt5
created: "2026-09-21T12:03:36Z"
updated: "2026-09-21T13:39:47Z"
---

# Description

Imported n8n RAG graphs attach embeddings, a document loader, and a recursive splitter on typed `ai_*` ports. KilasFlow already ran sequential `main` embeddings against the install pgvector store. Cluster mode must emit a `$ai` descriptor (`kind=embeddings`) that the Vector Store parent calls, matching chat-model sub-nodes.

# Acceptance Criteria
- [x] `kilasflow.embeddings` with `mode=cluster` has no main input and emits a single embedding-port `$ai` descriptor without calling the provider
- [x] Native sequential embeddings on `main` still embed each item's `text`
- [x] `kilasflow.documentLoader` and `kilasflow.textSplitter` produce `pageContent` chunks
- [x] Vector Store `mode=insert` / `retrieve-as-tool` use `ai_embedding` and `ai_document` / `ai_tool` ports
- [x] Agent `toolFrom` invokes a vector store tool and returns `matches`

# Implementation Plan
PortsFor on embeddings and vector store by `mode`. Cluster embeddings never POST `/embeddings`; the parent calls `EmbedFromDescriptor`.

# Notes
Compiler requires incoming `main` whenever Inputs include main, so cluster embeddings must use `PortsFor` with `Inputs: nil`.

# Related Files
- `nodes/rag.go`
- `nodes/pgvector.go`
- `nodes/ai.go`
- `nodes/rag_test.go`

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
