---
id: BUG-341sxn
title: Rename the Go module path off the unclaimed kilaslabs namespace
status: done
priority: high
labels:
    - dx
    - security
    - deferred
created: "2026-09-20T00:39:41Z"
updated: "2026-09-20T04:35:52Z"
---

Source: FEAT-edxxj7 finding "All published coordinates point at an unowned GitHub namespace kilaslabs". The distribution
coordinates were moved to the real owner (`kilaslab`, see FEAT-edxxj7) but the Go module path cannot be moved in the
same wave.

Deferred by decision (Main, 2026-09-20): `module github.com/kilaslabs/kilas-flow` in go.mod appears in 233 .go files as
an import path. A rename is an atomic repo-wide sweep, and with ~10 sibling agents holding uncommitted Go edits a
half-applied sweep is a guaranteed build break — the same failure mode as the LoadOptions field deletion earlier in
this epic, where one commit removed a struct field other slices still referenced. It needs a quiet tree, one owner, and
no concurrent Go writers.

Impact while it is deferred: `go get github.com/kilaslabs/kilas-flow/pkg/sdk` for community node authors resolves only
through GitHub's rename redirect for the repository, and `github.com/kilaslabs` is an unclaimed account name, so whoever
registers it could serve that module path. docs/guides/community-nodes.md documents the import path, so the doc and
go.mod must be changed together.

Work:
1. Confirm `github.com/kilaslabs/kilas-flow` cannot be claimed by anyone else (claim the org, or accept the risk).
2. `gofmt`-safe sweep: go.mod, every import in .go files, docs/guides/community-nodes.md, docs/reference/* that quote
   the path, sdk/ if it references it, and any `-ldflags`/build metadata.
3. `go build ./... && go vet ./... && go test ./...` on a quiet tree, plus `make docs-build` for the links.
4. Add the module path to the coordinates guard added by FEAT-edxxj7 (scripts/check-coordinates.sh excludes it today).

Acceptance:
- `grep -rn "kilaslabs" --include="*.go" --include="go.mod" .` returns nothing.
- The tree builds, tests pass, docs links resolve.
- The coordinates guard covers the module path with no exclusion.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `7ac3c8d9` (last commit at or before ticket created 2026-09-20)
- Commits (3):
  - `e5e5f9f6` — BUG-341sxn: the Go module path names the account that owns the repository
  - `64639963` — chore(pine): close EPIC-cfe7ny with the final verification record
  - `31c424f8` — chore(pine): file editor-loop, conditions-coercion, paging and follow-up tickets
- Files changed (base → working tree):

```
 .editorconfig                                      |   33 +
 .env.example                                       |    2 +-
 .github/ISSUE_TEMPLATE/bug_report.yml              |   89 +
 .github/ISSUE_TEMPLATE/config.yml                  |   11 +
 .github/ISSUE_TEMPLATE/feature_request.yml         |   57 +
 .github/PULL_REQUEST_TEMPLATE.md                   |   25 +
 .github/dependabot.yml                             |   78 +
 .github/workflows/ci.yml                           |    6 +
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |    1 +
 .pine/MEMORY.md                                    |    3 +
 .pine/tickets/BUG-1tj5wy.md                        |  465 ++++-
 .pine/tickets/BUG-277a2m.md                        |  472 ++++-
 .pine/tickets/BUG-341sxn.md                        |   39 +
 .pine/tickets/BUG-4053h6.md                        |  573 +++++-
 .pine/tickets/BUG-57n76x.md                        |  455 +++-
 .pine/tickets/BUG-66es9z.md                        |  248 +++
 .pine/tickets/BUG-6as5y7.md                        |  474 ++++-
 .pine/tickets/BUG-6bqh51.md                        |  455 +++-
 .pine/tickets/BUG-6jvcs5.md                        |  499 ++++-
 .pine/tickets/BUG-8dmp5y.md                        |  563 ++++-
 .pine/tickets/BUG-8h4yy1.md                        |  462 ++++-
 .pine/tickets/BUG-8sb0jw.md                        |  509 ++++-
 .pine/tickets/BUG-8t94wn.md                        |  486 ++++-
 .pine/tickets/BUG-9853ay.md                        |  519 ++++-
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 10476 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  628 +++++-
 .pine/tickets/BUG-c241hm.md                        |  485 ++++-
 .pine/tickets/BUG-cq4yk3.md                        |  511 ++++-
 .pine/tickets/BUG-dndnhn.md                        |  453 +++-
 .pine/tickets/BUG-esb9sh.md                        |  456 ++++-
 .pine/tickets/BUG-f9frth.md                        |  604 +++++-
 .pine/tickets/BUG-fv5fer.md                        |  525 ++++-
 .pine/tickets/BUG-gaavr5.md                        |  508 ++++-
 .pine/tickets/BUG-hfhzq6.md                        |  467 ++++-
 .pine/tickets/BUG-hm76dq.md                        |  477 ++++-
 .pine/tickets/BUG-j7rtv3.md                        |  137 ++
 .pine/tickets/BUG-kzkvv6.md                        |  478 ++++-
 .pine/tickets/BUG-mewhrd.md                        |  455 +++-
 .pine/tickets/BUG-mz8xrb.md                        |  472 ++++-
 .pine/tickets/BUG-npfz43.md                        |  455 +++-
 .pine/tickets/BUG-pwckhd.md                        |  453 +++-
 .pine/tickets/BUG-qmgz2f.md                        |  506 ++++-
 .pine/tickets/BUG-qq4xva.md                        |  471 ++++-
 .pine/tickets/BUG-rjd6fm.md                        |  453 +++-
 .pine/tickets/BUG-rpkjpy.md                        |  143 ++
 .pine/tickets/BUG-rrkjrd.md                        |  461 ++++-
 .pine/tickets/BUG-s0wy50.md                        |  455 +++-
 .pine/tickets/BUG-t2wezf.md                        |  468 ++++-
 .pine/tickets/BUG-tcqkad.md                        |  500 ++++-
 .pine/tickets/BUG-th16c1.md                        |  218 ++
 .pine/tickets/BUG-txc9xg.md                        |  453 +++-
 .pine/tickets/BUG-wdypd2.md                        |  488 ++++-
 .pine/tickets/BUG-wp2y0y.md                        |  454 +++-
 .pine/tickets/BUG-xf1wqm.md                        |  455 +++-
 .pine/tickets/BUG-y57cz4.md                        |  532 ++++-
 .pine/tickets/BUG-ysvmaa.md                        |  594 +++++-
 .pine/tickets/BUG-ze1nn8.md                        |  454 +++-
 .pine/tickets/BUG-ztzxck.md                        |  473 ++++-
 .pine/tickets/EPIC-cfe7ny.md                       |   26 +-
 .pine/tickets/FEAT-0895qc.md                       |  457 ++++-
 .pine/tickets/FEAT-15k49d.md                       |   36 +
 .pine/tickets/FEAT-56nep4.md                       |  456 ++++-
 .pine/tickets/FEAT-a5fhjw.md                       |   82 +
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  496 ++++-
 .pine/tickets/FEAT-j5s2n4.md                       |  503 ++++-
 .pine/tickets/FEAT-jvembs.md                       |  484 ++++-
 .pine/tickets/FEAT-nqpvf6.md                       |  463 ++++-
 .pine/tickets/FEAT-qdedm0.md                       |   38 +
 .pine/tickets/FEAT-x5km1z.md                       |  454 +++-
 CHANGELOG.md                                       |   32 +
 CODE_OF_CONDUCT.md                                 |  174 ++
 CONTRIBUTING.md                                    |  142 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |   16 +-
 README.md                                          |   67 +-
 SECURITY.md                                        |  101 +
 cmd/kilasflow/main.go                              |  173 +-
 cmd/kilasflow/main_test.go                         |    6 +-
 cmd/kilasflow/retention_test.go                    |    8 +-
 cmd/kilasflow/secrets_boot_test.go                 |    4 +-
 cmd/nodepackgen/authorcmd.go                       |    2 +-
 cmd/nodepackgen/generate.go                        |    8 +-
 cmd/nodepackgen/generate_test.go                   |   12 +-
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    8 +-
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
 docs/src/content/docs/operate/deployment.md        |   26 +-
 docs/src/content/docs/operate/security.md          |   52 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +-
 docs/src/content/docs/reference/api-contract.md    |   13 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    2 +-
 docs/src/content/docs/reference/api/datastores.md  |  392 ++++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |    2 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    4 +-
 docs/src/content/docs/reference/api/tenants.md     |  200 ++
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 .../content/docs/reference/expression-grammar.md   |  369 +++-
 docs/src/content/docs/start/install.md             |   46 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   91 +-
 e2e/fixtures/pack-convert-driver.go                |    2 +-
 go.mod                                             |    2 +-
 internal/ai/agent.go                               |  104 +-
 internal/ai/agent_output_test.go                   |    2 +-
 internal/ai/ai.go                                  |   47 +-
 internal/ai/ai_test.go                             |  134 +-
 internal/ai/fromai_test.go                         |    2 +-
 internal/ai/maf/runtime.go                         |    2 +-
 internal/ai/maf/runtime_test.go                    |    2 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  146 +-
 internal/ai/openai_test.go                         |  147 +-
 internal/ai/outputschema.go                        |   16 +
 internal/api/auth_test.go                          |   28 +-
 internal/api/cors_test.go                          |  136 ++
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/credentials_test.go                   |  114 +-
 internal/api/csv_export_test.go                    |   66 +
 internal/api/datastores_csv_test.go                |    2 +-
 internal/api/datastores_test.go                    |   53 +-
 internal/api/embed_confinement_test.go             |  352 ++++
 internal/api/embed_datastore_test.go               |    4 +-
 internal/api/embed_test.go                         |   35 +-
 internal/api/events_test.go                        |   97 +-
 internal/api/handlers/admin.go                     |  524 +++++
 internal/api/handlers/admin_admin_test.go          |  579 ++++++
 internal/api/handlers/auth.go                      |  237 ++-
 internal/api/handlers/auth_test.go                 |  366 ++++
 internal/api/handlers/credentials.go               |   73 +-
 internal/api/handlers/datastores.go                |   80 +-
 internal/api/handlers/datastores_csv.go            |   80 +-
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   20 +-
 internal/api/handlers/embedscope.go                |  126 ++
 internal/api/handlers/executions.go                |  332 ++-
 internal/api/handlers/interop.go                   |  142 +-
 internal/api/handlers/nodes.go                     |   58 +-
 internal/api/handlers/problem.go                   |  112 +
 internal/api/handlers/resume.go                    |    8 +-
 internal/api/handlers/resume_test.go               |   12 +-
 internal/api/handlers/schedules.go                 |   56 +-
 internal/api/handlers/tenants.go                   |    6 +-
 internal/api/handlers/workflows.go                 |  158 +-
 internal/api/handlers/workflows_conflict_test.go   |    8 +-
 internal/api/handlers/workflows_delete_test.go     |    8 +-
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   86 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |   14 +-
 internal/api/middleware/cors_test.go               |   50 +-
 internal/api/middleware/embed.go                   |    2 +-
 internal/api/middleware/embed_test.go              |    2 +-
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/node_types_test.go                    |  109 +-
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   49 +-
 internal/api/server.go                             |  163 +-
 internal/api/server_test.go                        |    4 +-
 internal/api/workflow_history_test.go              |    2 +-
 internal/api/workflows_test.go                     |  116 +-
 internal/auth/auth_test.go                         |   44 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   17 +-
 internal/binary/binary_test.go                     |    2 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  197 +-
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  323 +++
 internal/config/config.go                          |  316 ++-
 internal/config/config_test.go                     |   22 +-
 internal/credentials/builtin.go                    |    2 +-
 internal/credentials/credentials_test.go           |    4 +-
 internal/credentials/external.go                   |    4 +-
 internal/credentials/external_test.go              |    6 +-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   20 +-
 internal/credentials/vault.go                      |    2 +-
 internal/database/database.go                      |    2 +-
 internal/database/database_test.go                 |    2 +-
 internal/database/migrate.go                       |    2 +-
 internal/database/migrate_test.go                  |    7 +-
 internal/database/prefix_test.go                   |    4 +-
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 ++
 internal/datastore/config_bind_test.go             |    2 +-
 internal/datastore/engine.go                       |    4 +-
 internal/datastore/engine_test.go                  |    4 +-
 internal/datastore/fleet.go                        |   11 +-
 internal/datastore/idents_test.go                  |    2 +-
 internal/datastore/trace_test.go                   |    2 +-
 internal/datetime/datetime_test.go                 |    2 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |   25 +
 internal/embed/embed_test.go                       |   16 +-
 internal/engine/approval.go                        |   19 +-
 internal/engine/approval_test.go                   |    4 +-
 internal/engine/authenticate.go                    |   23 +-
 internal/engine/authenticate_test.go               |  135 ++
 internal/engine/checkpoint.go                      |   19 +-
 internal/engine/error_workflow_test.go             |  230 +++
 internal/engine/expression_context_test.go         |    6 +-
 internal/engine/lease_test.go                      |  401 ++++
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiproc.go                       |    4 +-
 internal/engine/multiprocess_test.go               |  141 +-
 internal/engine/runner.go                          | 1554 +++++++++-----
 internal/engine/runner_test.go                     | 1118 +++++++++-
 internal/engine/service.go                         |  738 ++++++-
 internal/engine/service_test.go                    |  407 +++-
 internal/engine/subworkflow_test.go                |   24 +-
 internal/engine/trace.go                           |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |   28 +-
 internal/engine/wait_service.go                    |  186 +-
 internal/engine/wait_service_test.go               |  395 +++-
 internal/engine/worker_test.go                     |   23 +-
 internal/events/events.go                          |    2 +-
 internal/events/events_test.go                     |    2 +-
 internal/execution/records.go                      |   11 +
 internal/execution/redact_datastore_test.go        |    2 +-
 internal/execution/redact_test.go                  |    2 +-
 internal/expression/doc.go                         |   13 +
 internal/expression/evaluator.go                   |  227 +-
 internal/expression/expression.go                  |  165 +-
 internal/expression/expression_test.go             |    2 +-
 internal/expression/globals.go                     |   45 +-
 internal/expression/methods.go                     |   40 +-
 internal/expression/parity_test.go                 |  538 ++++-
 internal/expression/roots.go                       |   26 +
 internal/guardrails/licence_boundary_test.go       |    4 +-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |   36 +-
 internal/interop/n8n/gowa.go                       |   39 +-
 internal/interop/n8n/gowa_test.go                  |   19 +-
 internal/interop/n8n/importer_tail_test.go         | 1087 ++++++++++
 internal/interop/n8n/n8n.go                        |  495 ++++-
 internal/interop/n8n/n8n_test.go                   |  193 +-
 internal/interop/n8n/parameters.go                 | 2164 ++++++++++++++++++--
 internal/interop/n8n/sqlfidelity_test.go           |    8 +-
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 +++++
 internal/loadoptions/datastores.go                 |    2 +-
 internal/loadoptions/datastores_test.go            |    6 +-
 internal/loadoptions/loadoptions.go                |   17 +-
 internal/loadoptions/loadoptions_test.go           |   10 +-
 internal/loadoptions/redirect_test.go              |  117 ++
 internal/loadoptions/schema.go                     |    2 +-
 internal/loadoptions/sql.go                        |    4 +-
 internal/loadoptions/sql_test.go                   |   10 +-
 internal/loadoptions/workflows.go                  |    2 +-
 internal/node/registry.go                          |    4 +-
 internal/node/registry_test.go                     |    6 +-
 internal/nodepack/author.go                        |    6 +-
 internal/nodepack/author_test.go                   |   47 +-
 internal/nodepack/convert.go                       |    8 +-
 internal/nodepack/convert_test.go                  |   10 +-
 internal/nodepack/loaddir.go                       |    8 +-
 internal/nodepack/loaddir_test.go                  |   16 +-
 internal/nodepack/nodepack.go                      |   12 +-
 internal/nodepack/startcase_test.go                |    2 +-
 internal/nodepack/trigger.go                       |   10 +-
 internal/nodepack/validate.go                      |   12 +-
 internal/property/locator_test.go                  |    4 +-
 internal/property/mapper_test.go                   |    2 +-
 internal/property/visibility_test.go               |    2 +-
 internal/repository/auth.go                        |  392 +++-
 internal/repository/auth_admin_test.go             |  477 +++++
 internal/repository/auth_test.go                   |    8 +-
 internal/repository/claim_lease_test.go            |  266 +++
 internal/repository/claim_wake_test.go             |   14 +-
 internal/repository/credentials.go                 |  107 +-
 internal/repository/credentials_external_test.go   |    8 +-
 internal/repository/execution_retention.go         |    2 +-
 internal/repository/execution_retention_test.go    |   15 +-
 internal/repository/executions.go                  |  550 ++++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   59 +-
 internal/repository/models_test.go                 |  151 +-
 internal/repository/postgres_execution_test.go     |   14 +-
 internal/repository/prefix_test.go                 |   16 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  114 +-
 internal/repository/subworkflow_activation_test.go |  234 +++
 internal/repository/tenant_purge_test.go           |   10 +-
 internal/repository/waits.go                       |    2 +-
 internal/repository/waits_test.go                  |   12 +-
 internal/repository/webhooks.go                    |  190 +-
 internal/repository/webhooks_test.go               |  306 +++
 internal/repository/workflow_history.go            |   16 +-
 internal/repository/workflow_history_test.go       |   10 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  235 ++-
 internal/routing/executor.go                       |   12 +-
 internal/routing/request.go                        |    8 +-
 internal/routing/response.go                       |    4 +-
 internal/routing/routing.go                        |    2 +-
 internal/routing/routing_test.go                   |   10 +-
 internal/runcode/runcode_test.go                   |    2 +-
 internal/safehttp/safehttp.go                      |    2 +-
 internal/safehttp/safehttp_test.go                 |    2 +-
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   56 +-
 internal/scheduler/item.go                         |    2 +-
 internal/scheduler/rule_test.go                    |    2 +-
 internal/scheduler/scheduler.go                    |    2 +-
 internal/scheduler/scheduler_test.go               |   81 +-
 internal/sqlbuild/sqlbuild.go                      |    2 +-
 internal/sqlbuild/sqlbuild_test.go                 |    6 +-
 internal/sqlguard/attack_test.go                   |    2 +-
 internal/sqlguard/sqlguard_test.go                 |    2 +-
 internal/sqlnode/guard_test.go                     |    4 +-
 internal/sqlnode/internal_test.go                  |    4 +-
 internal/sqlnode/policy_test.go                    |    4 +-
 internal/sqlnode/sqlnode.go                        |    6 +-
 internal/sqlnode/sqlnode_test.go                   |    2 +-
 internal/web/embed.go                              |    2 +-
 internal/webhook/export_test.go                    |    4 +-
 internal/webhook/form.go                           |  262 +++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/lifecycle.go                      |    6 +-
 internal/webhook/lifecycle_test.go                 |   10 +-
 internal/webhook/request_lifecycle.go              |  431 +++-
 internal/webhook/request_lifecycle_test.go         |  411 ++++
 internal/webhook/shape.go                          |  301 ++-
 internal/webhook/shape_test.go                     |  150 +-
 internal/webhook/webhook.go                        |  835 ++++++--
 internal/webhook/webhook_test.go                   |  813 +++++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/compiler_test.go                 |    2 +-
 internal/workflow/document.go                      |    4 +
 internal/workflow/document_test.go                 |    8 +-
 internal/workflow/typeversion_test.go              |    2 +-
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../postgres/000013_node_run_response.down.sql     |    9 +
 .../postgres/000013_node_run_response.up.sql       |   28 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 .../sqlite/000013_node_run_response.down.sql       |    9 +
 migrations/sqlite/000013_node_run_response.up.sql  |   23 +
 nodes/ai.go                                        |  555 ++++-
 nodes/ai_mcp_test.go                               |   10 +-
 nodes/ai_ollama_test.go                            |   16 +-
 nodes/ai_test.go                                   |  890 +++++++-
 nodes/ai_tools_test.go                             |    8 +-
 nodes/annotation.go                                |    6 +-
 nodes/apostrophe_live_test.go                      |    4 +-
 nodes/assignments.go                               |   53 +-
 nodes/bindings_test.go                             |   10 +-
 nodes/code.go                                      |    8 +-
 nodes/code_test.go                                 |   10 +-
 nodes/conditions.go                                |    4 +-
 nodes/core.go                                      |    7 +-
 nodes/database.go                                  |   10 +-
 nodes/database_test.go                             |   12 +-
 nodes/datastore.go                                 |   57 +-
 nodes/datastore_test.go                            |  248 ++-
 nodes/datastore_tool_test.go                       |   12 +-
 nodes/datetime.go                                  |   10 +-
 nodes/datetime_test.go                             |  133 +-
 nodes/embedscope.go                                |  217 ++
 nodes/embedscope_test.go                           |  288 +++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 ++
 nodes/executors.go                                 |   22 +-
 nodes/executors_test.go                            |   78 +-
 nodes/flow.go                                      |   10 +-
 nodes/flow_test.go                                 |   10 +-
 nodes/http.go                                      |  155 +-
 nodes/http_test.go                                 |  126 +-
 nodes/jscode.go                                    |    6 +-
 nodes/jscode_test.go                               |    6 +-
 nodes/loop.go                                      |    6 +-
 nodes/mysql_v2.go                                  |   10 +-
 nodes/mysql_v2_test.go                             |   10 +-
 nodes/pgvector.go                                  |    8 +-
 nodes/pgvector_test.go                             |   12 +-
 nodes/postgres_v2.go                               |   16 +-
 nodes/postgres_v2_test.go                          |   10 +-
 nodes/presentation_test.go                         |    4 +-
 nodes/routing.go                                   |    6 +-
 nodes/sql_options.go                               |    4 +-
 nodes/sql_options_live_test.go                     |   14 +-
 nodes/sql_options_test.go                          |    6 +-
 nodes/sqlite_attach_test.go                        |    8 +-
 nodes/subworkflow.go                               |  168 +-
 nodes/subworkflow_calls_test.go                    |   90 +
 nodes/telegram.go                                  |    6 +-
 nodes/telegram_download.go                         |    9 +-
 nodes/telegram_lifecycle.go                        |   13 +-
 nodes/telegram_test.go                             |   18 +-
 nodes/transform.go                                 |    6 +-
 nodes/transform_test.go                            |    6 +-
 nodes/unsupported.go                               |   22 +-
 nodes/wait.go                                      |  227 +-
 nodes/webhook.go                                   |  490 ++++-
 packs/telegram/telegram.go                         |    8 +-
 packs/telegram/telegram_test.go                    |   20 +-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   93 +-
 packs/waha/waha_test.go                            |  411 +++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 pkg/sdk/example/echo/main.go                       |    2 +-
 pkg/sdk/sdk_test.go                                |    2 +-
 pkg/sdk/wasm_exec_test.go                          |    2 +-
 scripts/check-coordinates.sh                       |   66 +
 scripts/config-reference.go                        |    2 +-
 scripts/config-reference_test.go                   |    2 +-
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/smoke-dev.sh                               |   27 +
 sdk/README.md                                      |   12 +
 sdk/examples/host-page/README.md                   |   11 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 sdk/package.json                                   |    6 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++++
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
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  376 ++--
 .../components/workflow-editor/node-picker.svelte  |  148 +-
 .../workflow-editor/properties-panel.svelte        |   66 +-
 .../workflow-editor/property-field.svelte          |  236 ++-
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  724 ++++++-
 web/src/lib/dashboard/cursor-page.test.ts          |   85 +-
 web/src/lib/dashboard/cursor-page.ts               |   57 +
 web/src/lib/embed/embed-editor.svelte              |  176 +-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 ++
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   35 +-
 web/src/lib/workflow-editor/conditions.ts          |   59 +-
 web/src/lib/workflow-editor/credentials.test.ts    |    4 +-
 web/src/lib/workflow-editor/document.test.ts       |  120 ++
 web/src/lib/workflow-editor/document.ts            |  257 ++-
 .../lib/workflow-editor/expression-assist.test.ts  |   55 +-
 web/src/lib/workflow-editor/expression-assist.ts   |   50 +
 .../lib/workflow-editor/expression-grammar.test.ts |    3 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   16 +-
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 ++-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 ++
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/ports.test.ts          |  224 +-
 web/src/lib/workflow-editor/ports.ts               |  122 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |  109 +
 web/src/lib/workflow-editor/shortcuts.ts           |  139 ++
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |   81 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  378 +++-
 .../(dashboard)/app/workflows/import-dialog.svelte |   15 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   60 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |   59 +-
 web/src/routes/(dashboard)/executions/+page.svelte |   53 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   93 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |   56 +-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/vite.config.ts                                 |    7 +-
 552 files changed, 66128 insertions(+), 4646 deletions(-)
```

## Progress 2026-09-20 (the rename itself)

Executed on a quiet tree: no other agent was running and the only uncommitted
files were `.pine/` tickets and untracked e2e specs — the condition the deferral
asked for.

Work item 1 was answered rather than postponed: `github.com/kilaslabs` is still
unclaimed (`gh api /users/kilaslabs` → 404, checked 2026-09-20), so the module
path could not be made safe by ownership and was moved to the account that
actually holds the repository. No exclusion remains in the coordinates guard, so
reintroducing the old name fails `make coordinates-check` in CI.

Verified on the committed tree: `go build ./...`, `go vet ./...`, `gofmt -l`
clean, `go test ./internal/nodepack/ -race -count=2`, `make coordinates-check`,
and `make docs-build` (43 pages, every internal link valid). The full suite has
one unrelated red package, tracked as BUG-rpkjpy.
