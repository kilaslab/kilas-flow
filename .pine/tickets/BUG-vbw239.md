---
id: BUG-vbw239
title: 'make sdk-test is red: start-credential-oauth and convert-workflow-fragment have no SDK method and no exclusion'
status: done
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T12:00:21Z"
updated: "2026-10-09T12:22:03Z"
---

# Description

`make sdk-test` is red on main. `sdk/test/operation-coverage.test.mjs` ("covers every operation the server declares") boots a real binary, reads its OpenAPI document and requires every operation id to have a `KilasFlowClient` method in `OPERATION_COVERAGE` or a justified entry in `EXCLUSIONS`. Two operations the server declares have neither: `start-credential-oauth` (POST /credentials/{id}/oauth/start) and `convert-workflow-fragment` (POST /workflows/convert). Both are listed in docs/src/content/docs/reference/api-contract.md.

Found while reviewing Track E of the 2026-10-09 parallel wave; reproduced on main 54a3ee92 and on the Track E worktree identically.

# Steps to Reproduce

1. `make sdk-test`

# Expected

155/155 pass.

# Actual

154/155: `operations with no client method and no exclusion: start-credential-oauth, convert-workflow-fragment`.

# Acceptance Criteria
- [x] Each of the two operations either gets a client method (named in the SDK README, covered in `OPERATION_COVERAGE`, with an operations test) or a justified `EXCLUSIONS` entry whose reason is a real constraint (for example: the OAuth start is bound to the browser that starts it), and `make sdk-test` passes. Evidence: `convert-workflow-fragment` -> `convertWorkflowFragment` (README Interop row, `OPERATION_COVERAGE`, operations test "converts pasted n8n nodes without saving anything"); `start-credential-oauth` -> `EXCLUSIONS` entry stating the HttpOnly-cookie / Origin / Host browser binding. `make sdk-test`: 156/156 (was 154/155), `make sdk-check` and `make sdk-build` clean.
- [x] sdk/CHANGELOG.md records any added method as Additive. Evidence: Unreleased **Additive** line for `convertWorkflowFragment`.

## Plan (2026-10-09)

Decision per operation, from reading the handlers rather than assuming:

- `convert-workflow-fragment` (`internal/api/handlers/interop.go`, `ConvertFragment`): a pure translator. It runs `n8n.ImportFragment` against the tenant's catalogue, saves nothing, needs only `workflow:read`, and returns nodes, connections and the import report as JSON. A host backend can use it (translate n8n JSON a customer pastes into the host's own UI, then decide whether to save it), exactly as `importWorkflow` and `validateWorkflowDocument` are used. So it gets a client method, `convertWorkflowFragment`, next to `importWorkflow`.
- `start-credential-oauth` (`internal/api/handlers/oauth.go`, `StartOAuth`): the sign-in is bound to the browser that starts it. The response sets an HttpOnly nonce cookie scoped to the callback path, the state carries the nonce hash, and the callback refuses ("this sign-in was not started in this browser") unless that cookie comes back with it. The state also records the starting request's `Origin` as the `postMessage` target and the redirect URI is derived from that request's `Host`. The SDK client is a backend component: its `Transport` reads only the JSON body, so the cookie would land in the host's server process (and be dropped) while the end user's browser, opening the returned `authorizeUrl`, reaches the callback without it. An embed session is refused the whole `/credentials/*` mutation prefix (`internal/api/middleware/scope.go`), so the iframe cannot call it either, and the SDK's browser entry deliberately has no API client. The only caller is the first-party dashboard (`web/src/routes/(dashboard)/credentials/+page.svelte`). So it gets an `EXCLUSIONS` entry stating that constraint.

Changes:
1. `sdk/src/server.ts`: `convertWorkflowFragment(input: ConvertFragmentInputBody, signal?)` -> `POST /workflows/convert`, returning `ConvertedFragmentResource` (types already generated).
2. `sdk/test/operation-coverage.test.mjs`: `'convert-workflow-fragment': 'convertWorkflowFragment'` in `OPERATION_COVERAGE`; `'start-credential-oauth'` in `EXCLUSIONS` with the reason above.
3. `sdk/test/operations.test.ts`: an interop test that the method posts the envelope untouched to `/api/v1/workflows/convert` and resolves the typed result.
4. `sdk/README.md`: name `convertWorkflowFragment` in the Interop row, total 80 -> 81, and a short paragraph saying the Google sign-in start has no method and why.
5. `sdk/CHANGELOG.md`: an **Additive** line under Unreleased.

Proof: the new operations test fails first (no method), then `make sdk-test` goes from 154/155 to green (the new total), plus `make sdk-check` and `make sdk-build`-level typecheck.

## Progress (2026-10-09)

- Baseline reproduced first: `make sdk-test` 154/155 with `operations with no client method and no exclusion: start-credential-oauth, convert-workflow-fragment`.
- `convert-workflow-fragment` got a method. `KilasFlowClient.convertWorkflowFragment(input: ConvertFragmentInputBody, signal?)` posts to `/workflows/convert` and returns the generated `ConvertedFragmentResource`; it sits beside `importWorkflow` in `sdk/src/server.ts`. The operations test was written first and failed with "convertWorkflowFragment is not a function" before the method existed.
- `start-credential-oauth` got no method, verified rather than assumed in `internal/api/handlers/oauth.go`: the response sets an HttpOnly, SameSite=Lax nonce cookie scoped to the callback path, the signed state carries the nonce hash, the callback refuses with "this sign-in was not started in this browser" without that cookie, the state records the starting request's `Origin` as the `postMessage` target, and the redirect URI derives from that request's `Host`. The SDK `Transport` reads only the JSON body, so a host backend would hold the cookie in its own process while the customer's browser reached the callback without it. An embed session is refused every non-GET `/credentials/*` request (`internal/api/middleware/scope.go`), the SDK browser entry has no API client by design, and the only caller in the repo is the dashboard's credentials page. The `EXCLUSIONS` entry says this, and `sdk/README.md` carries a paragraph for readers.
- README total moved 80 -> 81 ("all 81 under `/api/v1` that a backend can drive"); the README test ties that number to `OPERATION_COVERAGE`, and the declared surface is now 81 covered + 1 excluded = 82.
- No deviations from the plan. Not regenerated: nothing under `sdk/src/generated` (the types for both operations already existed).

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `54a3ee92` (last commit at or before ticket created 2026-10-09)
- Commits (4):
  - `e16278e6` — BUG-vbw239: the OAuth exclusion says the redirect URI follows the Host only without server.public_url, and the README says only a browser can complete a Connect
  - `6501dc9d` — merge: make sdk-test and the node-coverage e2e specs are green again (BUG-vbw239, BUG-kkkrmx)
  - `c7c81350` — BUG-vbw239: the SDK covers convert-workflow-fragment with convertWorkflowFragment and excludes start-credential-oauth for its browser-bound sign-in, so make sdk-test is green
  - `9a1e48c2` — chore(pine): BUG-vbw239 records that make sdk-test is red on two operations with no client method
- Files changed (base → working tree):

```
 .github/workflows/ci.yml                           |   36 +-
 .pine/memory/docs.md                               |    4 +-
 .pine/memory/e2e.md                                |    1 +
 .pine/memory/n8n-reference.md                      |    3 +-
 .pine/memory/persistence.md                        |    3 +-
 .pine/tickets/BUG-6gkd12.md                        | 1053 +++++++++++++++++-
 .pine/tickets/BUG-9pmv8y.md                        | 1074 ++++++++++++++++++-
 .pine/tickets/BUG-b4cb1c.md                        | 1117 ++++++++++++++++++-
 .pine/tickets/BUG-kkkrmx.md                        |   51 +
 .pine/tickets/BUG-pc284d.md                        |   60 ++
 .pine/tickets/BUG-vbw239.md                        |   55 +
 .pine/tickets/FEAT-bvyc0b.md                       |   52 +
 .pine/tickets/FEAT-e898nz.md                       |  195 +++-
 .pine/tickets/FEAT-gcq50s.md                       |   11 +-
 .pine/tickets/FEAT-v8md2e.md                       |   66 ++
 .pine/tickets/FEAT-zm3wh2.md                       | 1126 +++++++++++++++++++-
 CHANGELOG.md                                       |   30 +
 CONTRIBUTING.md                                    |    3 +
 Makefile                                           |   30 +
 cmd/kilasflow/catalogue_docs_test.go               |  132 +--
 cmd/kilasflow/shipped_catalogue_drift_test.go      |  227 ++++
 docs/src/content/docs/concepts/credentials.md      |   10 +-
 docs/src/content/docs/concepts/node-registry.md    |   20 +-
 docs/src/content/docs/guides/embedding.md          |   12 +-
 docs/src/content/docs/guides/n8n-migration.md      |   48 +-
 docs/src/content/docs/operate/tenant-deletion.md   |    3 +-
 docs/src/content/docs/reference/api/workflows.md   |    1 +
 docs/src/content/docs/reference/cli.md             |  199 ++--
 docs/src/content/docs/start/what-kilasflow-is.md   |   15 +-
 e2e/fixtures/error-form-nodes.ts                   |    7 +-
 e2e/tests/n8n-compare.spec.ts                      |    3 +-
 e2e/tests/node-coverage.spec.ts                    |   13 +-
 internal/ai/agent.go                               |    4 +-
 internal/ai/toolusage.go                           |   46 +
 internal/ai/toolusage_test.go                      |   60 ++
 internal/api/handlers/workflows.go                 |   17 +-
 internal/api/workflow_tags_test.go                 |  342 ++++++
 internal/cli/docs_examples_test.go                 |   70 ++
 internal/cli/exit.go                               |  136 +++
 internal/cli/exit_reference_test.go                |  286 +++++
 internal/cli/verbs_workflow.go                     |   38 +-
 internal/cli/verbs_workflow_tag_test.go            |   84 ++
 internal/database/migrate_test.go                  |    1 +
 internal/docexamples/docexamples.go                |  318 ++++++
 internal/docexamples/docexamples_test.go           |  209 ++++
 internal/interop/n8n/n8n.go                        |   15 +-
 internal/interop/n8n/n8n_test.go                   |    2 +
 internal/interop/n8n/rag_import_test.go            |   33 +-
 internal/interop/n8n/tags.go                       |  182 ++++
 internal/interop/n8n/tags_test.go                  |  312 ++++++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   24 +
 internal/interop/n8n/testdata/n8n_rag_16706.json   |   10 +-
 internal/interop/n8n/vectorstore.go                |  153 +++
 internal/interop/n8n/vectorstore_import_test.go    |  281 +++++
 internal/repository/postgres_execution_test.go     |    1 +
 internal/repository/prefix_test.go                 |   40 +
 internal/repository/table_names_test.go            |   11 +
 internal/repository/tenant_rows.go                 |    3 +-
 internal/repository/workflow_history.go            |    4 +
 internal/repository/workflow_tags.go               |   87 ++
 internal/repository/workflow_tags_test.go          |  283 +++++
 internal/repository/workflows.go                   |   32 +
 internal/shippedcatalogue/shippedcatalogue.go      |  162 +++
 internal/shippedcatalogue/shippedcatalogue_test.go |  132 +++
 internal/tenantpurge/harness_test.go               |    2 +
 internal/tenantpurge/purge.go                      |    2 +-
 internal/tenantpurge/purge_test.go                 |    2 +-
 internal/workflow/document.go                      |    9 +
 internal/workflow/tags.go                          |  113 ++
 internal/workflow/tags_test.go                     |  188 ++++
 migrations/postgres/000025_workflow_tags.down.sql  |    5 +
 migrations/postgres/000025_workflow_tags.up.sql    |   36 +
 migrations/sqlite/000025_workflow_tags.down.sql    |    5 +
 migrations/sqlite/000025_workflow_tags.up.sql      |   35 +
 nodes/ai.go                                        |    2 +
 nodes/core.go                                      |    1 +
 nodes/executors.go                                 |    1 +
 nodes/glyphs_test.go                               |    1 +
 nodes/pgvector.go                                  |   30 +-
 nodes/pgvector_customer.go                         |   26 +-
 nodes/rag.go                                       |   13 +
 nodes/vectorstore_retrieve.go                      |   84 ++
 nodes/vectorstore_retrieve_test.go                 |  250 +++++
 nodes/vectorstore_tool.go                          |  325 ++++++
 nodes/vectorstore_tool_export_test.go              |   20 +
 nodes/vectorstore_tool_security_test.go            |  208 ++++
 nodes/vectorstore_tool_test.go                     |  565 ++++++++++
 schemas/workflow-v1.schema.json                    |    8 +-
 scripts/cli-examples/main.go                       |  583 ++++++++++
 scripts/cli-examples/main_test.go                  |  187 ++++
 scripts/cli-examples/process_other.go              |   14 +
 scripts/cli-examples/process_test.go               |  110 ++
 scripts/cli-examples/process_unix.go               |   35 +
 scripts/cli-examples/seed.go                       |  275 +++++
 scripts/cli-examples/server.go                     |  183 ++++
 scripts/cli-examples/testdata/credential.json      |    5 +
 scripts/cli-examples/testdata/patch.json           |   19 +
 scripts/cli-examples/testdata/payload.json         |    1 +
 scripts/cli-examples/testdata/wf.json              |    9 +
 scripts/cli-examples/testdata/workflow.json        |   28 +
 scripts/docs-facts/main.go                         |  177 +++
 scripts/docs-facts/main_test.go                    |  278 +++++
 scripts/internal/splice/splice.go                  |  149 +++
 scripts/internal/splice/splice_test.go             |  112 ++
 scripts/skills-command-reference/main.go           |  112 +-
 sdk/CHANGELOG.md                                   |   13 +
 sdk/README.md                                      |   20 +-
 sdk/examples/reference-host/server.mjs             |   12 +-
 sdk/src/generated/models.ts                        |   15 +
 sdk/src/server.ts                                  |   17 +
 sdk/test/operation-coverage.test.mjs               |   21 +-
 sdk/test/operations.test.ts                        |   19 +
 skills/using-kilasflow-skills/SKILL.md             |    2 +-
 web/messages/en/workflows.json                     |   15 +-
 web/messages/id/workflows.json                     |   15 +-
 web/src/lib/api/generated/models/document.ts       |    2 +
 .../api/generated/models/listWorkflowsParams.ts    |    5 +
 .../api/generated/models/workflowDocumentInput.ts  |    6 +
 .../lib/api/generated/models/workflowSummary.ts    |    2 +
 .../components/dashboard/workflow-tag-chips.svelte |   27 +
 web/src/lib/dashboard/workflow-list.test.ts        |  138 ++-
 web/src/lib/dashboard/workflow-list.ts             |   99 +-
 web/src/lib/workflow-editor/document.test.ts       |   18 +-
 web/src/lib/workflow-editor/document.ts            |   14 +-
 web/src/lib/workflow-editor/workflow-input.ts      |   27 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |   81 +-
 126 files changed, 13367 insertions(+), 447 deletions(-)
```
