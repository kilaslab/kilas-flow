---
id: FEAT-a5fhjw
title: 'Open-source readiness: community health files, README metadata and repository settings'
status: done
priority: high
labels:
    - dx
    - docs
created: "2026-09-20T04:23:35Z"
updated: "2026-09-20T04:35:52Z"
---

# Description

The repository is public and already carries the product, the LICENSE, CI and
release workflows and a documentation site in the tree, but it is missing the
files a stranger arriving from a search result looks for. The only contributing
page that exists is `docs/src/content/docs/contributing.md`, and it is about the
documentation site, not about the project.

Gaps at the time this ticket was opened:

- No `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md` or `CHANGELOG.md` at
  the root.
- No `.github/ISSUE_TEMPLATE/` and no pull request template, so the issue button
  opens a blank box that asks for nothing — not the version, not the deployment
  shape, not a reproduction.
- No `.editorconfig`, no `.github/dependabot.yml`.
- The GitHub repository has no description and no topics: it cannot be found by
  topic search and its link preview is empty.
- Private vulnerability reporting is disabled (`/private-vulnerability-reporting`
  returned `enabled: false`), and secret scanning, push protection and Dependabot
  security updates are all `disabled` in `security_and_analysis`. A `SECURITY.md`
  that promises a private channel has to have that channel actually switched on.

# Acceptance Criteria
- [ ] Root community health files exist and each claim in them is checkable
      against this tree.
- [ ] Issue forms ask for the version, the deployment shape and a reproduction,
      and route security reports away from public issues.
- [ ] README carries CI and licence badges and points at the contributing,
      conduct, security and changelog files.
- [ ] The repository has a description, topics, private vulnerability reporting
      enabled and Dependabot security updates enabled.
- [ ] `make docs-build` still passes (links from the docs site into these files
      resolve or are relative to the repository root, not to the site).

# Implementation Plan

1. Write the four root files against the commands that actually exist in the
   Makefile and the conventions the git log shows.
2. Add the issue forms, the PR template, `.editorconfig` and `dependabot.yml`.
3. Add badges, a Documentation section and a Community section to the README.
4. Set the repository metadata and turn on the security features the security
   policy names.

# Notes

Deliberately not added, and why:

- `GOVERNANCE.md` / `MAINTAINERS.md` — one maintainer, no committee. A governance
  document for a project with a single decision maker describes nothing.
- A CLA or DCO — the repository has no such requirement and Apache-2.0 does not
  need one; adding a gate that no contributor has been asked to sign would be
  ceremony.
- `.github/FUNDING.yml` — nobody has set up a funding channel.
- A GitHub Discussions forum — issues are enabled and the project answers there;
  a second empty venue splits the conversation.

The Go module path (`github.com/kilaslabs/kilas-flow`, an unclaimed account name)
is the other half of "published coordinates" and is tracked separately as
BUG-341sxn, because it is an atomic sweep of ~276 files rather than a document.

# Related Files

- `README.md`
- `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `CHANGELOG.md`
- `.github/ISSUE_TEMPLATE/`, `.github/PULL_REQUEST_TEMPLATE.md`
- `.github/dependabot.yml`, `.editorconfig`
- `scripts/check-coordinates.sh` (unrelated guard, referenced by BUG-341sxn)

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `458b038c` (last commit at or before ticket created 2026-09-20)
- Commits (1):
  - `61afc0be` — FEAT-a5fhjw: the community health files a public repository is expected to have
- Files changed (base → working tree):

```
 .editorconfig                                      |  33 ++
 .github/ISSUE_TEMPLATE/bug_report.yml              |  89 ++++
 .github/ISSUE_TEMPLATE/config.yml                  |  11 +
 .github/ISSUE_TEMPLATE/feature_request.yml         |  57 ++
 .github/PULL_REQUEST_TEMPLATE.md                   |  25 +
 .github/dependabot.yml                             |  78 +++
 .pine/MEMORY.md                                    |   1 +
 .pine/tickets/BUG-341sxn.md                        | 571 ++++++++++++++++++++-
 .pine/tickets/BUG-rpkjpy.md                        | 143 ++++++
 .pine/tickets/FEAT-a5fhjw.md                       |  82 +++
 CHANGELOG.md                                       |  32 ++
 CODE_OF_CONDUCT.md                                 | 174 +++++++
 CONTRIBUTING.md                                    | 142 +++++
 Makefile                                           |  13 +-
 README.md                                          |  40 ++
 SECURITY.md                                        | 101 ++++
 cmd/kilasflow/main.go                              |  50 +-
 cmd/kilasflow/main_test.go                         |   6 +-
 cmd/kilasflow/retention_test.go                    |   8 +-
 cmd/kilasflow/secrets_boot_test.go                 |   4 +-
 cmd/nodepackgen/authorcmd.go                       |   2 +-
 cmd/nodepackgen/generate.go                        |   8 +-
 cmd/nodepackgen/generate_test.go                   |  12 +-
 docs/astro.config.mjs                              |   6 +-
 docs/src/content/docs/guides/community-nodes.md    |   2 +-
 e2e/fixtures/pack-convert-driver.go                |   2 +-
 go.mod                                             |   2 +-
 internal/ai/agent_output_test.go                   |   2 +-
 internal/ai/ai_test.go                             |   2 +-
 internal/ai/fromai_test.go                         |   2 +-
 internal/ai/maf/runtime.go                         |   2 +-
 internal/ai/maf/runtime_test.go                    |   2 +-
 internal/ai/openai_test.go                         |   2 +-
 internal/api/auth_test.go                          |  28 +-
 internal/api/cors_test.go                          |   4 +-
 internal/api/credentials_pagination_test.go        |   2 +-
 internal/api/credentials_test.go                   |  16 +-
 internal/api/datastores_csv_test.go                |   2 +-
 internal/api/datastores_test.go                    |  12 +-
 internal/api/embed_confinement_test.go             |   2 +-
 internal/api/embed_datastore_test.go               |   4 +-
 internal/api/embed_test.go                         |   8 +-
 internal/api/events_test.go                        |  14 +-
 internal/api/handlers/admin.go                     |   4 +-
 internal/api/handlers/admin_admin_test.go          |   8 +-
 internal/api/handlers/auth.go                      |   6 +-
 internal/api/handlers/auth_test.go                 |   6 +-
 internal/api/handlers/credentials.go               |   8 +-
 internal/api/handlers/datastores.go                |   4 +-
 internal/api/handlers/datastores_csv.go            |   2 +-
 internal/api/handlers/datastores_csv_test.go       |   2 +-
 internal/api/handlers/embed.go                     |   6 +-
 internal/api/handlers/embedscope.go                |   8 +-
 internal/api/handlers/executions.go                |  10 +-
 internal/api/handlers/interop.go                   |   8 +-
 internal/api/handlers/nodes.go                     |  14 +-
 internal/api/handlers/problem.go                   |   4 +-
 internal/api/handlers/resume.go                    |   8 +-
 internal/api/handlers/resume_test.go               |  12 +-
 internal/api/handlers/schedules.go                 |   4 +-
 internal/api/handlers/tenants.go                   |   6 +-
 internal/api/handlers/workflows.go                 |  16 +-
 internal/api/handlers/workflows_conflict_test.go   |   4 +-
 internal/api/handlers/workflows_delete_test.go     |   4 +-
 internal/api/list_pagination_test.go               |  12 +-
 internal/api/middleware/auth.go                    |   4 +-
 internal/api/middleware/auth_test.go               |   4 +-
 internal/api/middleware/cors.go                    |   2 +-
 internal/api/middleware/embed.go                   |   2 +-
 internal/api/middleware/embed_test.go              |   2 +-
 internal/api/middleware/sessioncache.go            |   2 +-
 internal/api/node_types_test.go                    |  24 +-
 internal/api/openapi_security_test.go              |   4 +-
 internal/api/routes.go                             |   8 +-
 internal/api/server.go                             |  24 +-
 internal/api/server_test.go                        |   4 +-
 internal/api/workflow_history_test.go              |   2 +-
 internal/api/workflows_test.go                     |  28 +-
 internal/auth/auth_test.go                         |   2 +-
 internal/binary/binary.go                          |   2 +-
 internal/binary/binary_test.go                     |   2 +-
 internal/conditions/conditions.go                  |   2 +-
 internal/conditions/conditions_test.go             |   4 +-
 internal/config/config.go                          |   4 +-
 internal/config/config_test.go                     |  22 +-
 internal/credentials/builtin.go                    |   2 +-
 internal/credentials/credentials_test.go           |   4 +-
 internal/credentials/external.go                   |   4 +-
 internal/credentials/external_test.go              |   6 +-
 internal/credentials/redirect_test.go              |   6 +-
 internal/credentials/registry.go                   |   4 +-
 internal/credentials/vault.go                      |   2 +-
 internal/database/database.go                      |   2 +-
 internal/database/database_test.go                 |   2 +-
 internal/database/migrate.go                       |   2 +-
 internal/database/migrate_test.go                  |   7 +-
 internal/database/prefix_test.go                   |   4 +-
 internal/datastore/config_bind_test.go             |   2 +-
 internal/datastore/engine.go                       |   4 +-
 internal/datastore/engine_test.go                  |   4 +-
 internal/datastore/fleet.go                        |   2 +-
 internal/datastore/idents_test.go                  |   2 +-
 internal/datastore/trace_test.go                   |   2 +-
 internal/datetime/datetime_test.go                 |   2 +-
 internal/embed/embed_test.go                       |   2 +-
 internal/engine/approval.go                        |   2 +-
 internal/engine/approval_test.go                   |   4 +-
 internal/engine/authenticate.go                    |   8 +-
 internal/engine/authenticate_test.go               |   6 +-
 internal/engine/checkpoint.go                      |   4 +-
 internal/engine/error_workflow_test.go             |  10 +-
 internal/engine/expression_context_test.go         |   6 +-
 internal/engine/lease_test.go                      |  22 +-
 internal/engine/live_progress_test.go              |   8 +-
 internal/engine/loopstate_test.go                  |  14 +-
 internal/engine/multiproc.go                       |   4 +-
 internal/engine/multiprocess_test.go               |  26 +-
 internal/engine/runner.go                          |   4 +-
 internal/engine/runner_test.go                     |  16 +-
 internal/engine/service.go                         |  12 +-
 internal/engine/service_test.go                    |  22 +-
 internal/engine/subworkflow_test.go                |  22 +-
 internal/engine/trace.go                           |   2 +-
 internal/engine/trace_persist_test.go              |  10 +-
 internal/engine/trace_test.go                      |  20 +-
 internal/engine/wait_service.go                    |  10 +-
 internal/engine/wait_service_test.go               |  26 +-
 internal/engine/worker_test.go                     |   8 +-
 internal/events/events.go                          |   2 +-
 internal/events/events_test.go                     |   2 +-
 internal/execution/redact_datastore_test.go        |   2 +-
 internal/execution/redact_test.go                  |   2 +-
 internal/expression/expression_test.go             |   2 +-
 internal/expression/parity_test.go                 |   2 +-
 internal/guardrails/licence_boundary_test.go       |   4 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |  36 +-
 internal/interop/n8n/gowa.go                       |   2 +-
 internal/interop/n8n/gowa_test.go                  |   2 +-
 internal/interop/n8n/importer_tail_test.go         |   4 +-
 internal/interop/n8n/n8n.go                        |   2 +-
 internal/interop/n8n/n8n_test.go                   |  26 +-
 internal/interop/n8n/parameters.go                 |  10 +-
 internal/interop/n8n/sqlfidelity_test.go           |   8 +-
 internal/interop/n8n/waitsubworkflow_test.go       |   4 +-
 internal/loadoptions/datastores.go                 |   2 +-
 internal/loadoptions/datastores_test.go            |   6 +-
 internal/loadoptions/loadoptions.go                |   6 +-
 internal/loadoptions/loadoptions_test.go           |  10 +-
 internal/loadoptions/redirect_test.go              |   8 +-
 internal/loadoptions/schema.go                     |   2 +-
 internal/loadoptions/sql.go                        |   4 +-
 internal/loadoptions/sql_test.go                   |  10 +-
 internal/loadoptions/workflows.go                  |   2 +-
 internal/node/registry.go                          |   4 +-
 internal/node/registry_test.go                     |   6 +-
 internal/nodepack/author.go                        |   6 +-
 internal/nodepack/author_test.go                   |  47 +-
 internal/nodepack/convert.go                       |   8 +-
 internal/nodepack/convert_test.go                  |  10 +-
 internal/nodepack/loaddir.go                       |   8 +-
 internal/nodepack/loaddir_test.go                  |  16 +-
 internal/nodepack/nodepack.go                      |  12 +-
 internal/nodepack/startcase_test.go                |   2 +-
 internal/nodepack/trigger.go                       |  10 +-
 internal/nodepack/validate.go                      |  12 +-
 internal/property/locator_test.go                  |   4 +-
 internal/property/mapper_test.go                   |   2 +-
 internal/property/visibility_test.go               |   2 +-
 internal/repository/auth.go                        |   4 +-
 internal/repository/auth_admin_test.go             |   4 +-
 internal/repository/auth_test.go                   |   8 +-
 internal/repository/claim_lease_test.go            |  10 +-
 internal/repository/claim_wake_test.go             |  10 +-
 internal/repository/credentials.go                 |   4 +-
 internal/repository/credentials_external_test.go   |   8 +-
 internal/repository/execution_retention.go         |   2 +-
 internal/repository/execution_retention_test.go    |  12 +-
 internal/repository/executions.go                  |   4 +-
 internal/repository/import_diagnostics.go          |   2 +-
 internal/repository/import_diagnostics_test.go     |   8 +-
 internal/repository/models_test.go                 |  10 +-
 internal/repository/postgres_execution_test.go     |  10 +-
 internal/repository/prefix_test.go                 |   8 +-
 internal/repository/schedule_list_test.go          |   4 +-
 internal/repository/schedules.go                   |   2 +-
 internal/repository/subworkflow_activation_test.go |   6 +-
 internal/repository/tenant_purge_test.go           |   8 +-
 internal/repository/waits.go                       |   2 +-
 internal/repository/waits_test.go                  |  10 +-
 internal/repository/webhooks.go                    |   2 +-
 internal/repository/webhooks_test.go               |   6 +-
 internal/repository/workflow_history.go            |   2 +-
 internal/repository/workflow_history_test.go       |  10 +-
 internal/repository/workflow_list_test.go          |   4 +-
 internal/repository/workflows.go                   |   2 +-
 internal/routing/executor.go                       |  12 +-
 internal/routing/request.go                        |   8 +-
 internal/routing/response.go                       |   4 +-
 internal/routing/routing.go                        |   2 +-
 internal/routing/routing_test.go                   |  10 +-
 internal/runcode/runcode_test.go                   |   2 +-
 internal/safehttp/safehttp.go                      |   2 +-
 internal/safehttp/safehttp_test.go                 |   2 +-
 internal/scheduler/extract.go                      |   4 +-
 internal/scheduler/item.go                         |   2 +-
 internal/scheduler/rule_test.go                    |   2 +-
 internal/scheduler/scheduler.go                    |   2 +-
 internal/scheduler/scheduler_test.go               |  14 +-
 internal/sqlbuild/sqlbuild.go                      |   2 +-
 internal/sqlbuild/sqlbuild_test.go                 |   6 +-
 internal/sqlguard/attack_test.go                   |   2 +-
 internal/sqlguard/sqlguard_test.go                 |   2 +-
 internal/sqlnode/guard_test.go                     |   4 +-
 internal/sqlnode/internal_test.go                  |   4 +-
 internal/sqlnode/policy_test.go                    |   4 +-
 internal/sqlnode/sqlnode.go                        |   6 +-
 internal/sqlnode/sqlnode_test.go                   |   2 +-
 internal/web/embed.go                              |   2 +-
 internal/webhook/export_test.go                    |   2 +-
 internal/webhook/form_test.go                      |   2 +-
 internal/webhook/lifecycle.go                      |   6 +-
 internal/webhook/lifecycle_test.go                 |  10 +-
 internal/webhook/request_lifecycle.go              |   6 +-
 internal/webhook/request_lifecycle_test.go         |   8 +-
 internal/webhook/shape.go                          |   2 +-
 internal/webhook/shape_test.go                     |   4 +-
 internal/webhook/webhook.go                        |  12 +-
 internal/webhook/webhook_test.go                   |  28 +-
 internal/workflow/compiler_test.go                 |   2 +-
 internal/workflow/document_test.go                 |   8 +-
 internal/workflow/typeversion_test.go              |   2 +-
 nodes/ai.go                                        |  14 +-
 nodes/ai_mcp_test.go                               |  10 +-
 nodes/ai_ollama_test.go                            |  16 +-
 nodes/ai_test.go                                   |  18 +-
 nodes/ai_tools_test.go                             |   8 +-
 nodes/annotation.go                                |   6 +-
 nodes/apostrophe_live_test.go                      |   4 +-
 nodes/assignments.go                               |   6 +-
 nodes/bindings_test.go                             |  10 +-
 nodes/code.go                                      |   8 +-
 nodes/code_test.go                                 |  10 +-
 nodes/conditions.go                                |   4 +-
 nodes/core.go                                      |   4 +-
 nodes/database.go                                  |  10 +-
 nodes/database_test.go                             |  12 +-
 nodes/datastore.go                                 |  16 +-
 nodes/datastore_test.go                            |  20 +-
 nodes/datastore_tool_test.go                       |  12 +-
 nodes/datetime.go                                  |  10 +-
 nodes/datetime_test.go                             |   8 +-
 nodes/embedscope.go                                |   6 +-
 nodes/embedscope_test.go                           |   4 +-
 nodes/error_workflow.go                            |   8 +-
 nodes/error_workflow_test.go                       |   8 +-
 nodes/executors.go                                 |  20 +-
 nodes/executors_test.go                            |  12 +-
 nodes/flow.go                                      |  10 +-
 nodes/flow_test.go                                 |  10 +-
 nodes/http.go                                      |  12 +-
 nodes/http_test.go                                 |  12 +-
 nodes/jscode.go                                    |   6 +-
 nodes/jscode_test.go                               |   6 +-
 nodes/loop.go                                      |   6 +-
 nodes/mysql_v2.go                                  |  10 +-
 nodes/mysql_v2_test.go                             |  10 +-
 nodes/pgvector.go                                  |   8 +-
 nodes/pgvector_test.go                             |  12 +-
 nodes/postgres_v2.go                               |  16 +-
 nodes/postgres_v2_test.go                          |  10 +-
 nodes/presentation_test.go                         |   4 +-
 nodes/routing.go                                   |   6 +-
 nodes/sql_options.go                               |   4 +-
 nodes/sql_options_live_test.go                     |  14 +-
 nodes/sql_options_test.go                          |   6 +-
 nodes/sqlite_attach_test.go                        |   8 +-
 nodes/subworkflow.go                               |  12 +-
 nodes/subworkflow_calls_test.go                    |   4 +-
 nodes/telegram.go                                  |   6 +-
 nodes/telegram_download.go                         |   8 +-
 nodes/telegram_lifecycle.go                        |   6 +-
 nodes/telegram_test.go                             |  18 +-
 nodes/transform.go                                 |   6 +-
 nodes/transform_test.go                            |   6 +-
 nodes/unsupported.go                               |   6 +-
 nodes/wait.go                                      |  10 +-
 nodes/webhook.go                                   |  12 +-
 packs/telegram/telegram.go                         |   8 +-
 packs/telegram/telegram_test.go                    |  20 +-
 packs/waha/waha.go                                 |  10 +-
 packs/waha/waha_test.go                            |  38 +-
 pkg/sdk/example/echo/main.go                       |   2 +-
 pkg/sdk/sdk_test.go                                |   2 +-
 pkg/sdk/wasm_exec_test.go                          |   2 +-
 scripts/check-coordinates.sh                       |  52 +-
 scripts/config-reference.go                        |   2 +-
 scripts/config-reference_test.go                   |   2 +-
 297 files changed, 2688 insertions(+), 1074 deletions(-)
```

## Note on the diffstat above

The range runs from this ticket's creation to the working tree, so it also
contains BUG-341sxn's 279-file module rename, which landed in the same session
and touches every Go file. This ticket's own change is `61afc0b` alone: twelve
files, 864 insertions — the four root documents, the issue forms, the pull
request template, `.editorconfig`, `.github/dependabot.yml`, the README's badges
and Community/Documentation sections, and this ticket.
