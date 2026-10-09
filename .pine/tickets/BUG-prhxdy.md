---
id: BUG-prhxdy
title: Google nodes evaluate a resolved value as an expression a second time, so {{ $env.X }} in data leaks secrets
status: done
priority: high
labels:
    - security
    - google
    - expressions
parent: EPIC-8rbys7
created: "2026-10-09T11:20:46Z"
updated: "2026-10-09T13:33:35Z"
---

# Description

The Gmail and Google Drive executors evaluate expressions twice. `resolveGoogleIR` in `nodes/google.go` first resolves the node's parameters with `expression.Resolve`, then walks the *resolved* tree with `evaluateTemplateStrings` and evaluates every string in it that contains `{{`. A value an expression produced is supposed to be data, but here it is parsed and run as a second expression.

So any text that reaches a Gmail or Drive parameter through an expression (a webhook body, an email subject read by an earlier node, a row from a spreadsheet, and, once FEAT-cf5n4j ships gmailTool, a model's tool argument) can carry `{{ $env.X }}`, `{{ $execution… }}` or `{{ $('Other node').all() }}` and have the evaluator read it. The result is sent out of the instance (as an email body, a subject, a Drive query), so an outsider who controls one upstream field can exfiltrate any allowlisted environment variable or any earlier node's output.

The walk was added (EPIC-hkypt5, 018af94f) so that imported n8n parameters nested below the top level, which the importer leaves as plain `={{ … }}` strings, still evaluate. That need is real; the bug is that it runs after resolution instead of before it.

# Steps to Reproduce

1. A Gmail node, operation `send`, with `message` set to the expression `{{ $json.body }}`.
2. Run it on an item whose `body` is the text `{{ $env.SECRET }}`, with `SECRET` in the instance's allowlisted environment.
3. Look at the mail the Gmail API receives.

The same happens with a Drive `search` whose `queryString` is `{{ $json.q }}`.

# Expected

The mail body is the literal text `{{ $env.SECRET }}`. An expression's result is never evaluated again.

# Actual

The mail body is the secret's value. `nodes/google_template_test.go` reproduces it for Gmail send (subject and body) and Drive search.

# Acceptance Criteria
- [x] A resolved value containing `{{ … }}` reaches the Gmail API (send: subject and body) as literal text; proven against an httptest stub with `$env.SECRET` set. (`TestGmailSendDoesNotEvaluateBracesInsideAResolvedValue`, nodes/google_template_test.go)
- [x] The same holds for Google Drive (search `queryString`). (`TestGoogleDriveSearchDoesNotEvaluateBracesInsideAResolvedValue`)
- [x] A plain `{{ … }}` / `={{ … }}` string the workflow author wrote in a nested parameter (the imported n8n shape) is still evaluated, exactly once. (`TestGmailStillEvaluatesANestedTemplateTheAuthorWrote`: `filters.sender: "={{ $json.from }}"` becomes `from:alice@example.com`)
- [x] No resolved parameter tree is walked for templates anywhere in the Google nodes after this change. (`evaluateTemplateStrings` is deleted; `resolveGoogleIR` is the only resolution path for Gmail and Drive and calls `expression.Resolve` once)

# Notes

Found while planning FEAT-cf5n4j (agent-tool forms): gmailTool would hand model text straight into this path, so the fix has to land before gmailTool ships. Track B (Google Sheets, FEAT-vntngh) builds on `resolveGoogleIR` and rebases onto this fix.

The same class of bug exists outside the Google nodes: the Stop and Error node (`nodes/error_workflow.go`, the message builder around line 190) resolves `errorMessage` and then calls `expression.Evaluate` on the result when it contains `{{`. Its output is an error message rather than an outbound request, but an error workflow can forward it. Not fixed here (out of scope for this ticket); worth its own ticket.

# Related Files
- nodes/google.go — resolveGoogleIR, templateStringsAsExpressions
- nodes/gmail.go
- nodes/google_drive.go
- nodes/google_template_test.go — reproduction

# Progress (2026-10-09)

- `resolveGoogleIR` no longer evaluates anything after resolution. Before the single `expression.Resolve`, a new `templateStringsAsExpressions` turns every plain string holding `{{` in the *unresolved* parameter tree into an expression marker (stripping n8n's leading `=`), so the nested n8n templates the old walk existed for are still the author's templates and still evaluate, once. Values produced by resolution are never parsed again. `evaluateTemplateStrings` is deleted.
- Verified: the two injection tests failed before the fix with the secret in the sent mail and in the Drive `q`, and pass after it; `go test ./nodes -count=1` passes.

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `d3554277` (last commit at or before ticket created 2026-10-09)
- Commits (3):
  - `d2d30efe` — merge: Gmail, PostgreSQL and Telegram run as agent tools through one generic tool-form table, model values reach executors only as data, and the Google nodes and Stop and Error never evaluate a resolved value again (FEAT-cf5n4j, BUG-b8bwhw, BUG-prhxdy, BUG-vqghpm)
  - `022446c5` — BUG-prhxdy: the Google nodes resolve their parameters once, so braces inside a resolved value stay text
  - `62a9bd4e` — chore(pine): BUG-prhxdy files the Google nodes' second evaluation of resolved values
- Files changed (base → working tree):

```
 .env.example                                       |    8 +-
 .github/workflows/ci.yml                           |   57 +-
 .pine/memory/docs.md                               |    5 +-
 .pine/memory/e2e.md                                |    3 +
 .pine/memory/n8n-reference.md                      |    4 +-
 .pine/memory/persistence.md                        |    5 +-
 .pine/memory/web-editor.md                         |    2 +
 .pine/tickets/BUG-2d1pdx.md                        |   49 +
 .pine/tickets/BUG-6gkd12.md                        | 1053 +++++++++++++++-
 .pine/tickets/BUG-9pmv8y.md                        | 1074 +++++++++++++++-
 .pine/tickets/BUG-b4cb1c.md                        | 1117 ++++++++++++++++-
 .pine/tickets/BUG-b8bwhw.md                        | 1279 ++++++++++++++++++-
 .pine/tickets/BUG-f85r5h.md                        |  412 +++++++
 .pine/tickets/BUG-f8b8gh.md                        |   47 +
 .pine/tickets/BUG-jx94q9.md                        |  417 +++++++
 .pine/tickets/BUG-k4tzc8.md                        |   46 +
 .pine/tickets/BUG-kkkrmx.md                        |  158 +++
 .pine/tickets/BUG-n6p7qy.md                        | 1177 +++++++++++++++++-
 .pine/tickets/BUG-pc284d.md                        |   60 +
 .pine/tickets/BUG-pdmkn7.md                        |  419 +++++++
 .pine/tickets/BUG-prhxdy.md                        |   60 +
 .pine/tickets/BUG-vbw239.md                        |  197 +++
 .pine/tickets/BUG-vqghpm.md                        |   64 +
 .pine/tickets/BUG-x28fsx.md                        | 1217 +++++++++++++++++-
 .pine/tickets/BUG-ywbvfa.md                        | 1290 ++++++++++++++++++-
 .pine/tickets/EPIC-brpz48.md                       |    7 +-
 .pine/tickets/FEAT-8752vx.md                       |   10 +-
 .pine/tickets/FEAT-bvyc0b.md                       |   52 +
 .pine/tickets/FEAT-cf5n4j.md                       |  549 ++++++++-
 .pine/tickets/FEAT-e898nz.md                       |  195 ++-
 .pine/tickets/FEAT-egm8bf.md                       |    2 +
 .pine/tickets/FEAT-gcq50s.md                       |   11 +-
 .pine/tickets/FEAT-kfmq1z.md                       |    2 +
 .pine/tickets/FEAT-p75n7j.md                       | 1297 +++++++++++++++++++-
 .pine/tickets/FEAT-qae4sh.md                       |  956 ++++++++++++++-
 .pine/tickets/FEAT-r87gtj.md                       |  956 ++++++++++++++-
 .pine/tickets/FEAT-r8ph93.md                       |    3 +
 .pine/tickets/FEAT-v8md2e.md                       |   66 +
 .pine/tickets/FEAT-xj5tv6.md                       |    5 +
 .pine/tickets/FEAT-zm3wh2.md                       | 1126 ++++++++++++++++-
 CHANGELOG.md                                       |  121 ++
 CONTRIBUTING.md                                    |   13 +
 Makefile                                           |   41 +
 cmd/kilasflow/catalogue_docs_test.go               |  132 +-
 cmd/kilasflow/main.go                              |  226 +++-
 cmd/kilasflow/reseal_test.go                       |  219 ++++
 cmd/kilasflow/secrets_boot_test.go                 |  102 +-
 cmd/kilasflow/shipped_catalogue_drift_test.go      |  228 ++++
 config.example.yaml                                |   24 +-
 docs/src/content/docs/concepts/credentials.md      |   86 +-
 docs/src/content/docs/concepts/node-registry.md    |   20 +-
 docs/src/content/docs/guides/embedding.md          |   19 +-
 docs/src/content/docs/guides/n8n-migration.md      |   97 +-
 .../src/content/docs/guides/tenant-scoped-nodes.md |    6 +
 .../docs/operate/configuration-reference.md        |   36 +-
 docs/src/content/docs/operate/configuration.md     |   14 +-
 docs/src/content/docs/operate/key-rotation.md      |  158 +++
 docs/src/content/docs/operate/security.md          |   19 +-
 docs/src/content/docs/operate/tenant-deletion.md   |    4 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +
 docs/src/content/docs/reference/api/credentials.md |    3 +-
 docs/src/content/docs/reference/api/workflows.md   |    1 +
 docs/src/content/docs/reference/cli.md             |  225 ++--
 docs/src/content/docs/start/install.md             |    5 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   15 +-
 docs/src/styles/kilasflow.css                      |    2 +-
 e2e/.gitignore                                     |    4 +
 e2e/fixtures/error-form-nodes.ts                   |    7 +-
 e2e/global-setup.ts                                |   26 +-
 e2e/helpers/focus-ring.ts                          |  125 ++
 e2e/helpers/stub.ts                                |    6 +-
 e2e/package.json                                   |    3 +-
 e2e/playwright.visual.config.ts                    |   60 +
 e2e/tests/embed-branding.spec.ts                   |  163 +++
 e2e/tests/focus-tab-order.spec.ts                  |  155 +++
 e2e/tests/n8n-compare.spec.ts                      |    3 +-
 e2e/tests/node-coverage.spec.ts                    |   29 +-
 e2e/tests/workflow-tool-inputs.spec.ts             |  406 ++++++
 e2e/visual/fixtures.ts                             |   71 ++
 e2e/visual/focus-ring.spec.ts                      |   50 +
 e2e/visual/force-state.ts                          |   34 +
 e2e/visual/state-matrix.spec.ts                    |   25 +
 e2e/visual/theme-subtree.spec.ts                   |   47 +
 internal/ai/agent.go                               |    4 +-
 internal/ai/ai_test.go                             |   32 +
 internal/ai/fromai.go                              |   28 +
 internal/ai/fromai_whole_test.go                   |   47 +
 internal/ai/memory.go                              |   26 +-
 internal/ai/toolusage.go                           |   46 +
 internal/ai/toolusage_test.go                      |   60 +
 internal/api/auth_test.go                          |   25 +-
 internal/api/credential_audit_test.go              |  271 ++++
 internal/api/credential_usage_test.go              |  169 +++
 internal/api/handlers/actor.go                     |   33 +
 internal/api/handlers/credentials.go               |  182 ++-
 internal/api/handlers/nodes.go                     |   32 +-
 internal/api/handlers/oauth.go                     |   29 +-
 internal/api/handlers/workflows.go                 |   55 +-
 internal/api/node_types_schema_expression_test.go  |   81 ++
 internal/api/oauth_external_test.go                |   69 ++
 internal/api/subworkflow_activation_test.go        |  219 ++++
 internal/api/workflow_tags_test.go                 |  342 ++++++
 internal/cli/docs_examples_test.go                 |   70 ++
 internal/cli/exit.go                               |  136 ++
 internal/cli/exit_reference_test.go                |  286 +++++
 internal/cli/skills_not_shipped_test.go            |  307 ++++-
 internal/cli/verbs_credential.go                   |   35 +-
 internal/cli/verbs_credential_test.go              |  197 +--
 internal/cli/verbs_node.go                         |   91 +-
 internal/cli/verbs_node_options_test.go            |  333 +++++
 internal/cli/verbs_workflow.go                     |   38 +-
 internal/cli/verbs_workflow_subworkflow_test.go    |  144 +++
 internal/cli/verbs_workflow_tag_test.go            |   84 ++
 internal/config/boot_strictness_test.go            |   76 ++
 internal/config/config.go                          |   51 +-
 internal/credentials/binding_guard_test.go         |   79 ++
 internal/credentials/cipher.go                     |  330 +++++
 internal/credentials/cipher_internal_test.go       |   35 +
 internal/credentials/cipher_test.go                |  388 ++++++
 internal/credentials/credentials.go                |   78 +-
 internal/credentials/credentials_test.go           |   25 +-
 internal/credentials/external.go                   |   41 +-
 internal/credentials/keysource.go                  |   63 +-
 internal/credentials/keysource_test.go             |  103 ++
 internal/credentials/oauth.go                      |    7 +
 .../database/credential_events_migration_test.go   |   61 +
 internal/database/migrate_test.go                  |    2 +
 internal/docexamples/docexamples.go                |  318 +++++
 internal/docexamples/docexamples_test.go           |  209 ++++
 internal/engine/authenticate.go                    |    3 +
 internal/engine/expression_context_test.go         |   24 +
 internal/engine/missing_credential_test.go         |   49 +
 internal/engine/runner.go                          |    8 +
 internal/engine/service.go                         |   19 +
 internal/interop/n8n/chatmodel_import_test.go      |    3 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |    6 +-
 internal/interop/n8n/importer_tail_test.go         |   15 +-
 internal/interop/n8n/n8n.go                        |  119 +-
 internal/interop/n8n/n8n_test.go                   |  119 +-
 internal/interop/n8n/parameters.go                 |  184 ++-
 internal/interop/n8n/rag_import_test.go            |   33 +-
 internal/interop/n8n/roundtrip_fidelity_test.go    |   26 +
 internal/interop/n8n/tags.go                       |  182 +++
 internal/interop/n8n/tags_test.go                  |  312 +++++
 internal/interop/n8n/template6270_shape_test.go    |  305 +++++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   24 +
 internal/interop/n8n/testdata/n8n_rag_16706.json   |   10 +-
 internal/interop/n8n/tool_form_import_test.go      |  559 +++++++++
 internal/interop/n8n/tool_forms.go                 |  398 ++++++
 internal/interop/n8n/tool_variant_import_test.go   |   16 +-
 internal/interop/n8n/vectorstore.go                |  153 +++
 internal/interop/n8n/vectorstore_import_test.go    |  281 +++++
 internal/interop/n8n/workflow_tool_inputs.go       |  241 ++++
 internal/interop/n8n/workflow_tool_inputs_test.go  |  319 +++++
 internal/loadoptions/workflows.go                  |   79 ++
 internal/loadoptions/workflows_test.go             |  158 +++
 internal/node/registry.go                          |    9 +
 internal/node/tool_form_scope_test.go              |   99 ++
 internal/node/visibility.go                        |   75 +-
 internal/property/mapper.go                        |    5 +
 internal/property/mapper_test.go                   |   22 +
 internal/repository/credential_events.go           |  130 ++
 internal/repository/credential_events_test.go      |  122 ++
 internal/repository/credential_reseal_test.go      |  218 ++++
 internal/repository/credential_seal_test.go        |  133 ++
 internal/repository/credential_usage.go            |  204 +++
 internal/repository/credential_usage_test.go       |  194 +++
 internal/repository/credentials.go                 |   30 +-
 internal/repository/credentials_external_test.go   |    8 +-
 internal/repository/export_reseal_test.go          |    7 +
 internal/repository/models.go                      |   27 +
 internal/repository/polls_test.go                  |    8 +
 internal/repository/postgres_execution_test.go     |    2 +
 internal/repository/prefix_test.go                 |   40 +
 internal/repository/refresh.go                     |    6 +-
 internal/repository/refresh_external_test.go       |   86 ++
 internal/repository/reseal.go                      |  153 +++
 internal/repository/subworkflow_activation_test.go |  116 ++
 internal/repository/table_names_test.go            |   12 +
 internal/repository/tenant_rows.go                 |    6 +-
 internal/repository/webhook_state.go               |    7 +-
 internal/repository/workflow_history.go            |    4 +
 internal/repository/workflow_tags.go               |   87 ++
 internal/repository/workflow_tags_test.go          |  283 +++++
 internal/repository/workflows.go                   |  132 +-
 internal/shippedcatalogue/shippedcatalogue.go      |  167 +++
 internal/shippedcatalogue/shippedcatalogue_test.go |  132 ++
 internal/skills/check.go                           |  146 +++
 internal/skills/check_test.go                      |   59 +
 internal/skills/examples.go                        |  287 +++++
 internal/skills/examples_coverage_test.go          |   49 +
 internal/skills/examples_shape_test.go             |  121 ++
 internal/skills/examples_test.go                   |  642 ++++++++++
 internal/skills/skill.go                           |   11 +
 internal/skills/skills_test.go                     |  254 +++-
 .../skills/kilasflow-fixture/SKILL.md              |   32 +
 internal/tenantpurge/harness_test.go               |   34 +-
 internal/tenantpurge/purge.go                      |    3 +-
 internal/tenantpurge/purge_test.go                 |    4 +-
 internal/workflow/tags.go                          |   32 +-
 internal/workflow/tags_test.go                     |    2 +-
 migrations/postgres/000025_workflow_tags.down.sql  |    5 +
 migrations/postgres/000025_workflow_tags.up.sql    |   36 +
 .../postgres/000026_credential_events.down.sql     |    4 +
 .../postgres/000026_credential_events.up.sql       |   36 +
 migrations/sqlite/000025_workflow_tags.down.sql    |    5 +
 migrations/sqlite/000025_workflow_tags.up.sql      |   35 +
 .../sqlite/000026_credential_events.down.sql       |    4 +
 migrations/sqlite/000026_credential_events.up.sql  |   35 +
 nodes/ai.go                                        |  170 ++-
 nodes/core.go                                      |    5 +-
 nodes/error_workflow.go                            |   81 +-
 nodes/executors.go                                 |   15 +-
 nodes/glyphs_test.go                               |    5 +
 nodes/gmail.go                                     |   40 +-
 nodes/google.go                                    |   54 +-
 nodes/google_template_test.go                      |  191 +++
 nodes/openai_credential_url_test.go                |   18 +-
 nodes/pgvector.go                                  |   36 +-
 nodes/pgvector_customer.go                         |   26 +-
 nodes/pgvector_test.go                             |    9 +
 nodes/postgres_v2.go                               |  160 ++-
 nodes/rag.go                                       |   13 +
 nodes/stop_and_error_leak_test.go                  |  115 ++
 nodes/subworkflow.go                               |   55 +
 nodes/subworkflow_inputs_test.go                   |   54 +
 nodes/tool_form.go                                 |  486 ++++++++
 nodes/tool_form_postgres_live_test.go              |   96 ++
 nodes/tool_form_run_test.go                        |  977 +++++++++++++++
 nodes/tool_form_test.go                            |  236 ++++
 nodes/vectorstore_retrieve.go                      |   84 ++
 nodes/vectorstore_retrieve_test.go                 |  250 ++++
 nodes/vectorstore_tool.go                          |  325 +++++
 nodes/vectorstore_tool_export_test.go              |   20 +
 nodes/vectorstore_tool_security_test.go            |  208 ++++
 nodes/vectorstore_tool_test.go                     |  565 +++++++++
 nodes/workflow_tool_inputs_test.go                 |  263 ++++
 scripts/cli-examples/main.go                       |  583 +++++++++
 scripts/cli-examples/main_test.go                  |  215 ++++
 scripts/cli-examples/process_other.go              |   14 +
 scripts/cli-examples/process_test.go               |  110 ++
 scripts/cli-examples/process_unix.go               |   35 +
 scripts/cli-examples/seed.go                       |  330 +++++
 scripts/cli-examples/server.go                     |  183 +++
 scripts/cli-examples/testdata/credential.json      |    5 +
 scripts/cli-examples/testdata/patch.json           |   19 +
 scripts/cli-examples/testdata/payload.json         |    1 +
 scripts/cli-examples/testdata/wf.json              |    9 +
 scripts/cli-examples/testdata/workflow.json        |   28 +
 scripts/docs-facts/main.go                         |  177 +++
 scripts/docs-facts/main_test.go                    |  278 +++++
 scripts/internal/splice/splice.go                  |  149 +++
 scripts/internal/splice/splice_test.go             |  112 ++
 scripts/skills-command-reference/main.go           |  130 +-
 sdk/CHANGELOG.md                                   |   27 +
 sdk/README.md                                      |   20 +-
 sdk/examples/reference-host/server.mjs             |   12 +-
 sdk/src/generated/models.ts                        |   59 +-
 sdk/src/server.ts                                  |   17 +
 sdk/test/operation-coverage.test.mjs               |   21 +-
 sdk/test/operations.test.ts                        |   19 +
 skills/index.json                                  |  158 ++-
 skills/kilasflow-ai-agents/SKILL.md                |  114 ++
 skills/kilasflow-ai-agents/references/EXAMPLE.md   |  146 +++
 skills/kilasflow-ai-agents/references/MODELS.md    |  105 ++
 skills/kilasflow-ai-agents/references/RAG.md       |  188 +++
 skills/kilasflow-credentials/SKILL.md              |   47 +-
 .../references/CREDENTIAL_TYPES.md                 |   28 +-
 skills/kilasflow-credentials/references/EXAMPLE.md |   97 ++
 skills/kilasflow-datastore/SKILL.md                |   52 +-
 skills/kilasflow-datastore/references/EXAMPLE.md   |  130 ++
 skills/kilasflow-datastore/references/FILTERS.md   |   40 +-
 skills/kilasflow-debugging/SKILL.md                |   34 +-
 skills/kilasflow-debugging/references/EXAMPLE.md   |   70 ++
 .../references/TRACE_READING.md                    |   43 +-
 skills/kilasflow-embedding/SKILL.md                |   57 +-
 .../references/EMBED_HANDSHAKE.md                  |   15 +-
 skills/kilasflow-embedding/references/EXAMPLE.md   |   82 ++
 .../references/SESSION_AUTHORITY.md                |    4 +-
 skills/kilasflow-error-handling/SKILL.md           |   39 +-
 .../kilasflow-error-handling/references/EXAMPLE.md |  117 ++
 skills/kilasflow-expressions/SKILL.md              |   40 +-
 skills/kilasflow-expressions/references/EXAMPLE.md |  134 ++
 .../references/EXPRESSION_ROOTS.md                 |   61 +-
 skills/kilasflow-flow-patterns/SKILL.md            |  105 ++
 .../references/BINARY_DATA.md                      |  116 ++
 .../kilasflow-flow-patterns/references/EXAMPLE.md  |  134 ++
 .../references/SUB_WORKFLOWS.md                    |  173 +++
 skills/kilasflow-import-export/SKILL.md            |   34 +-
 .../kilasflow-import-export/references/EXAMPLE.md  |  108 ++
 .../references/MAPPING_LIMITS.md                   |   55 +-
 skills/kilasflow-node-configuration/SKILL.md       |   44 +-
 .../references/EXAMPLE.md                          |  141 +++
 .../references/LOAD_OPTIONS.md                     |   65 +-
 .../references/PROPERTY_KINDS.md                   |   67 +-
 skills/kilasflow-node-packs/SKILL.md               |   38 +-
 skills/kilasflow-node-packs/references/EXAMPLE.md  |   75 ++
 .../references/MODULE_AND_SIDECAR.md               |   32 +-
 .../kilasflow-node-packs/references/PACK_FORMAT.md |   28 +-
 skills/kilasflow-operations/SKILL.md               |   17 +-
 skills/kilasflow-operations/references/EXAMPLE.md  |   86 ++
 .../references/UPGRADE_ORDER.md                    |   15 +-
 skills/kilasflow-triggers/SKILL.md                 |   48 +-
 skills/kilasflow-triggers/references/EXAMPLE.md    |   80 ++
 .../references/WEBHOOK_DELIVERY.md                 |   33 +-
 skills/kilasflow-workflow-lifecycle/SKILL.md       |   36 +-
 .../references/EXAMPLE.md                          |  111 ++
 .../references/NAMING_AND_DESCRIPTIONS.md          |   25 +-
 .../references/VALIDATION_CHECKLIST.md             |   29 +-
 skills/using-kilasflow-skills/SKILL.md             |   43 +-
 .../using-kilasflow-skills/references/EXAMPLE.md   |   65 +
 web/messages/en/credentials.json                   |    5 +
 web/messages/en/properties.json                    |    8 +
 web/messages/en/workflows.json                     |   15 +-
 web/messages/id/credentials.json                   |    5 +
 web/messages/id/properties.json                    |    8 +
 web/messages/id/workflows.json                     |   15 +-
 web/package.json                                   |    1 +
 web/src/app.css                                    |  688 ++++++++---
 web/src/app.d.ts                                   |    3 +
 .../lib/api/generated/credentials/credentials.ts   |   26 +-
 .../lib/api/generated/models/credentialResource.ts |    6 +
 .../generated/models/credentialUsageResource.ts    |   19 +
 .../api/generated/models/deleteCredentialParams.ts |   14 +
 web/src/lib/api/generated/models/document.ts       |    2 +
 web/src/lib/api/generated/models/index.ts          |    2 +
 .../api/generated/models/listWorkflowsParams.ts    |    5 +
 .../generated/models/resourceMapperDeclaration.ts  |    1 +
 .../api/generated/models/workflowDocumentInput.ts  |    6 +
 .../lib/api/generated/models/workflowSummary.ts    |    2 +
 .../lib/components/dashboard/dashboard-nav.svelte  |   10 +-
 .../components/dashboard/workflow-tag-chips.svelte |   27 +
 web/src/lib/components/ui/button/button.svelte     |   14 +-
 web/src/lib/components/ui/checkbox/checkbox.svelte |    2 +-
 .../lib/components/ui/dialog/dialog-content.svelte |    2 +-
 .../dropdown-menu-checkbox-item.svelte             |    2 +-
 .../ui/dropdown-menu/dropdown-menu-content.svelte  |    2 +-
 .../ui/dropdown-menu/dropdown-menu-item.svelte     |    2 +-
 .../dropdown-menu/dropdown-menu-radio-item.svelte  |    2 +-
 .../dropdown-menu/dropdown-menu-sub-content.svelte |    2 +-
 .../dropdown-menu/dropdown-menu-sub-trigger.svelte |    2 +-
 web/src/lib/components/ui/input/input.svelte       |    4 +-
 web/src/lib/components/ui/list-row/index.ts        |    8 +
 web/src/lib/components/ui/list-row/list-row.svelte |   29 +
 .../components/ui/popover/popover-content.svelte   |    2 +-
 .../components/ui/scroll-area/scroll-area.svelte   |    2 +-
 .../lib/components/ui/select/select-content.svelte |    2 +-
 .../lib/components/ui/select/select-item.svelte    |    2 +-
 .../lib/components/ui/select/select-trigger.svelte |    2 +-
 .../lib/components/ui/sheet/sheet-content.svelte   |    2 +-
 web/src/lib/components/ui/sonner/sonner.svelte     |    2 +-
 web/src/lib/components/ui/switch/switch.svelte     |    4 +-
 web/src/lib/components/ui/tabs/tabs-content.svelte |    2 +-
 web/src/lib/components/ui/tabs/tabs-trigger.svelte |    2 +-
 web/src/lib/components/ui/textarea/textarea.svelte |    2 +-
 .../workflow-editor/canvas-chat-panel.svelte       |    4 +-
 .../components/workflow-editor/canvas-node.svelte  |   12 +-
 .../components/workflow-editor/code-editor.svelte  |    2 +-
 .../components/workflow-editor/node-picker.svelte  |    6 +-
 .../workflow-editor/properties-panel.svelte        |    2 +-
 .../workflow-editor/property-field.svelte          |  230 +++-
 .../workflow-editor/property-field.test.ts         |  164 +++
 .../workflow-editor/version-panel.svelte           |    2 +-
 .../workflow-editor/workflow-editor.svelte         |   16 +-
 web/src/lib/dashboard/credential-usage.test.ts     |   46 +
 web/src/lib/dashboard/credential-usage.ts          |   38 +
 web/src/lib/dashboard/workflow-list.test.ts        |  138 ++-
 web/src/lib/dashboard/workflow-list.ts             |   99 +-
 web/src/lib/design-system/app-css.ts               |   15 +
 web/src/lib/design-system/class-scan.ts            |  245 ++++
 web/src/lib/design-system/color.test.ts            |  120 ++
 web/src/lib/design-system/color.ts                 |  513 ++++++++
 web/src/lib/design-system/contrast-pairs.ts        |   84 ++
 web/src/lib/design-system/css-tokens.test.ts       |  110 ++
 web/src/lib/design-system/css-tokens.ts            |  236 ++++
 web/src/lib/design-system/focus-lint.test.ts       |  143 +++
 web/src/lib/design-system/focus-lint.ts            |  236 ++++
 web/src/lib/design-system/state-matrix.svelte      |  335 +++++
 web/src/lib/design-system/theme-color.test.ts      |   23 +
 web/src/lib/design-system/theme-color.ts           |   34 +
 web/src/lib/design-system/tokens.contrast.test.ts  |  116 ++
 web/src/lib/design-system/tokens.groups.test.ts    |  129 ++
 web/src/lib/design-system/tokens.hue.test.ts       |  177 +++
 web/src/lib/embed/brand-root.test.ts               |   44 +
 web/src/lib/embed/brand-root.ts                    |   24 +
 web/src/lib/workflow-editor/document.test.ts       |   18 +-
 web/src/lib/workflow-editor/document.ts            |   14 +-
 web/src/lib/workflow-editor/node-visual.test.ts    |   13 +-
 web/src/lib/workflow-editor/node-visual.ts         |    8 +-
 .../lib/workflow-editor/resource-mapper.test.ts    |  262 +++-
 web/src/lib/workflow-editor/resource-mapper.ts     |  172 ++-
 web/src/lib/workflow-editor/shortcuts.test.ts      |   37 +
 web/src/lib/workflow-editor/shortcuts.ts           |   23 +
 web/src/lib/workflow-editor/validation.test.ts     |   24 +
 web/src/lib/workflow-editor/validation.ts          |   14 +-
 web/src/lib/workflow-editor/workflow-input.ts      |   27 +
 web/src/routes/(dashboard)/+layout.svelte          |   11 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |   81 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   48 +-
 web/src/routes/+page.svelte                        |   34 +-
 web/src/routes/design/states/+page.svelte          |    5 +
 web/src/routes/design/states/+page.ts              |   16 +
 web/src/routes/embed/[id]/+page.svelte             |   13 +-
 web/vite.config.ts                                 |   10 +
 404 files changed, 45734 insertions(+), 1962 deletions(-)
```
