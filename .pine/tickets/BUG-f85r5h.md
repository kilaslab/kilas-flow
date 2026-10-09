---
id: BUG-f85r5h
title: kilasflow node options sends no parameters, so a property whose options depend on other parameters cannot load
status: done
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T12:25:15Z"
updated: "2026-10-09T13:06:30Z"
---

# Description

`kilasflow node options <type> --property <name>` sends no node parameters to `POST /node-types/{type}/load-options`. A property whose options depend on other parameters (for example a chat model's model list, which needs `parameters.baseUrl` and a credential) therefore cannot be loaded through the verb; agents must fall back to `kilasflow api load-node-property-options` with a hand-written body.

Reported by the Track H2 coder in the 2026-10-09 parallel wave while writing the kilasflow-ai-agents skill.

# Steps to Reproduce

1. `kilasflow node options kilasflow.lmChatOpenAi --property model` (with a credential).

# Expected

The verb can pass the parameters (and credential) the loader depends on, e.g. `--param baseUrl=...`, `--credential <type>=<id>`, or `--parameters @file.json`, and prints the options.

# Actual

The loader gets no parameters and cannot answer.

# Acceptance Criteria
- [x] `node options` accepts the dependent parameters and credential (flags documented in `--help` and cli.md, with a runnable example). Evidence: `--param name=value` (repeatable) and `--parameters <json|@file|->` beside the existing `--credential <id>`; `--help` lists them (flag usage strings in internal/cli/verbs_node.go); docs/src/content/docs/reference/cli.md `kilasflow node` has the bullet and two examples that `make smoke-cli-examples` runs (`cli-examples: 89 executed, 3 skipped, 0 failed`): one with `--param baseUrl=... --credential "$CREDENTIAL_ID"` annotated `# exit 1` (the egress policy refuses that host, which is the proof the loader received the baseUrl; without the flag the same call says "need the service address to be filled in first"), one with `--parameters` carrying an expression dependency that exits 0 with an empty list and a reason.
- [x] A test proves the parameters reach the load-options request body. Evidence: `TestNodeOptionsSendsEachParamAsAStringParameter`, `...KeepsAValueWithAnEqualsSignWhole`, `...ParametersTakesTypedJSON`, `...ParametersReadsAFileAndStdin`, `...ParamOverridesTheSameKeyOfParameters`, `...OmitsParametersWhenNoneAreGiven` (recording stub of the request body), `TestNodeOptionsRefusesParametersItCannotSendCleanly` (six usage refusals that never reach the server), `TestNodeOptionsLoadsADependentPropertyThroughTheRealServer` (real handler + loader) and `TestMCPNodeOptionsPublishesAndSendsTheParameterFlags` (the MCP tool publishes `param` as an array of strings and `parameters` as a string, and both reach the request body). All in internal/cli/verbs_node_options_test.go.
- [x] The AI skill uses the verb instead of the escape hatch where it can. Evidence on the merged tree: skills/kilasflow-ai-agents/SKILL.md (model-node bullet) and references/MODELS.md (listing the models a server offers) use `kilasflow node options <type> --property model --param baseUrl=<address>` with `--credential`, keeping the escape hatch only for a body kept in a file; skills/kilasflow-node-configuration/SKILL.md and references/LOAD_OPTIONS.md teach `--param`/`--parameters`, all citation-free; the router's generated command table is current.


## Plan (2026-10-09)

Root cause. `runNodeOptions` (internal/cli/verbs_node.go:148) marshals a body of `property`, `version`, `mode` and `credentialId` only. The operation's body also takes `parameters` (internal/api/handlers/nodes.go `loadOptionsInput`), and that map is what `scopeFor` reads the loader's `dependsOn` keys from (nodes/ai.go:266 and :417 depend on `baseUrl`). So a property with dependencies always loads with an empty dependency set; the only way to send them was `kilasflow api load-node-property-options --body`. The credential half of the ticket already exists: `--credential <credentialId>` is sent as `credentialId`.

The change.
1. `node options` gets `--param key=value` (repeatable, collected with the CLI's existing `pairFlags`, the same flag shape `kilasflow api` uses for `--path/--query/--header`) and `--parameters <JSON object | @file | ->` (the shape `run --input` and `api --body` use, read with `readDocument`). Values from `--param` are strings; numbers, booleans and resource-locator objects go through `--parameters`. When both are given `--param` overrides a same-named key of `--parameters`; the same key repeated in `--param` is a usage error rather than a silent choice; `--parameters` that is not a JSON object is a usage error naming the flag. The body carries `parameters` only when something was given, as it does for the other optional fields.
2. `--credential` stays a bare credential id (the API takes a single `credentialId`; the loader's `credentialType` already says which type it must be, so the `type=id` shape the ticket floats would carry nothing the server reads and would break callers of today's flag). Its help text and the verb summary are made explicit.
3. Docs: reference/cli.md `kilasflow node` section documents both flags and gets a runnable example (the runner seeds `$WORKFLOW_ID`; the example loads `executeWorkflow`'s `workflowId` list, which depends on nothing, plus one with `--param` against a property that depends on a parameter and ends `# exit N` if it does not pass on the seeded server; I will check what the booted server answers before choosing). Then `make generate-skills-command-reference` for the router skill's command table, and the node-configuration skill text/LOAD_OPTIONS.md that say the verb cannot carry parameters. The ai-agents skill from the ticket is not on main, so the "skill uses the verb" criterion is met by updating the skills that exist (kilasflow-node-configuration) and noted in the report.
4. MCP needs no code: `pairFlags` is a non-Getter flag.Value, which the adapter already publishes as an array of strings (`mcpFlagList`), so the new tool schema gains `param` (array) and `parameters` (string) from the flag set.

Proof (test first).
- internal/cli/verbs_node_test.go: a recording stub of `/node-types/{type}/load-options` asserts the request body holds `parameters` with the `--param` strings, with a `--parameters` object, with `@file`, with the override order, with `--credential` beside them, and omits `parameters` when none were given; plus the three usage errors (duplicate `--param`, malformed pair, non-object `--parameters`).
- An end-to-end test against the real handler: a node type registered with an `http` loader that `dependsOn` a `baseUrl` parameter, served by an httptest upstream whose path/host proves the parameter arrived, reached through `kilasflow node options --param baseUrl=...` (not only a stub body), so "the parameters reach the load-options request" is proven through the server and not just on the wire.
- `make generate-skills-command-reference-check`, the cli.md example under `make build && make smoke-cli-examples`, and the existing tree/MCP tests (the schema gains two properties).


## Progress (2026-10-09)

Root cause as planned: `runNodeOptions` built its body from `property`, `version`, `mode` and `credentialId` only, so the handler's `scopeFor` always saw an empty dependency set for a loader with `dependsOn` (a chat model's `baseUrl`).

What changed.
- internal/cli/verbs_node.go: `--param name=value` (repeatable, the existing `pairFlags`) and `--parameters` (inline JSON, `@file`, or `-`, via `readDocument`) feed a `parameters` member that is omitted when empty. Raw JSON values are kept (`map[string]json.RawMessage`) so a number or resource locator in `--parameters` reaches the server untouched; `--param` values are strings and override a same-named key of `--parameters`. Usage errors (exit 2, nothing sent): a repeated `--param` name, a pair with no `=` or no name, `--parameters` that is not JSON, not an object, or an unreadable file. The verb summary names the new flags, so the router skill's generated table changed.
- MCP: no code. `pairFlags` is published by the adapter as an array of strings, so `node_options` gained `param` (array) and `parameters` (string) from the flag set; the existing catalogue/round-trip tests cover it.
- Docs and skills as described in the criteria.

Deviations from the plan and the ticket text.
- `--credential` stays a bare credential id rather than the `type=id` shape the ticket floats: the API's body has one `credentialId` and the loader's declared `credentialType` already says which type it must be, so a type prefix would be parsed and thrown away, and would break callers of the flag as it exists today.
- The plan's end-to-end test used an `http` loader against an httptest upstream; the egress policy refuses loopback without `allowed_private_endpoints`, so I used an `internal` loader that depends on `baseUrl` and echoes it. It exercises the same `scopeFor` path with nothing to weaken.
- The cli.md happy-path example is annotated `# exit 1`, because the examples server has no model endpoint to list (its egress policy admits only itself). It is still a real run of the new flags, and the `--parameters` example beside it exits 0.

Left: nothing.

## Review round (2026-10-09)

Merged main into the branch; the node-configuration conflicts went to main's side and the reviewer's follow-up patch moved kilasflow-ai-agents (SKILL.md, references/MODELS.md) and kilasflow-node-configuration onto `node options --param/--parameters`, so the Progress note above that says the ai-agents skill is not on main is superseded. Added the MCP test the review asked for and the CHANGELOG [Unreleased] Added entry. `--credential` keeps its bare-id shape (see Progress).

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `277dd011` (last commit at or before ticket created 2026-10-09)
- Commits (8):
  - `6b1cadae` — merge: an inactive sub-workflow is a 422 naming every bad call, n8n's executionOrder v1 round-trips silently, and node options sends dependent parameters (BUG-pdmkn7, BUG-jx94q9, BUG-f85r5h)
  - `22a23d25` — chore(pine): the three tickets' ticks and evidence are true on the merged tree, with a review-round note each
  - `513a812a` — BUG-pdmkn7: the editor replaces a node id in a server message only where it stands alone
  - `b77e79ad` — merge: main into the BUG-pdmkn7 / BUG-jx94q9 / BUG-f85r5h branch, skills conflicts resolved to main's side
  - `b7b85c4a` — chore(pine): BUG-f85r5h criteria ticked with evidence, progress written back, one durable note on typed repository refusals
  - `2067c56d` — BUG-f85r5h: kilasflow node options sends the dependent parameters with --param and --parameters, so a property like a chat model's list loads through the verb
  - `415d669b` — chore(pine): BUG-f85r5h plans --param and --parameters on node options
  - `0622e2c2` — chore(pine): three bugs found while validating skill examples: a 500 on inactive sub-workflows, executionOrder not round-tripping, node options sending no parameters
- Files changed (base → working tree):

```
 .env.example                                       |    8 +-
 .github/workflows/ci.yml                           |   57 +-
 .pine/memory/docs.md                               |    5 +-
 .pine/memory/e2e.md                                |    3 +
 .pine/memory/n8n-reference.md                      |    3 +-
 .pine/memory/persistence.md                        |    5 +-
 .pine/memory/web-editor.md                         |    2 +
 .pine/tickets/BUG-2d1pdx.md                        |   49 +
 .pine/tickets/BUG-6gkd12.md                        | 1053 +++++++++++++++-
 .pine/tickets/BUG-9pmv8y.md                        | 1074 +++++++++++++++-
 .pine/tickets/BUG-b4cb1c.md                        | 1117 ++++++++++++++++-
 .pine/tickets/BUG-f85r5h.md                        |   69 ++
 .pine/tickets/BUG-jx94q9.md                        |  417 +++++++
 .pine/tickets/BUG-kkkrmx.md                        |  158 +++
 .pine/tickets/BUG-n6p7qy.md                        | 1177 +++++++++++++++++-
 .pine/tickets/BUG-pc284d.md                        |   60 +
 .pine/tickets/BUG-pdmkn7.md                        |  419 +++++++
 .pine/tickets/BUG-vbw239.md                        |  197 +++
 .pine/tickets/BUG-x28fsx.md                        | 1217 +++++++++++++++++-
 .pine/tickets/BUG-ywbvfa.md                        | 1290 ++++++++++++++++++-
 .pine/tickets/EPIC-brpz48.md                       |    7 +-
 .pine/tickets/FEAT-8752vx.md                       |   10 +-
 .pine/tickets/FEAT-bvyc0b.md                       |   52 +
 .pine/tickets/FEAT-e898nz.md                       |  195 ++-
 .pine/tickets/FEAT-egm8bf.md                       |    2 +
 .pine/tickets/FEAT-gcq50s.md                       |   11 +-
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
 cmd/kilasflow/main.go                              |  219 +++-
 cmd/kilasflow/reseal_test.go                       |  219 ++++
 cmd/kilasflow/secrets_boot_test.go                 |  102 +-
 cmd/kilasflow/shipped_catalogue_drift_test.go      |  227 ++++
 config.example.yaml                                |   24 +-
 docs/src/content/docs/concepts/credentials.md      |   86 +-
 docs/src/content/docs/concepts/node-registry.md    |   20 +-
 docs/src/content/docs/guides/embedding.md          |   12 +-
 docs/src/content/docs/guides/n8n-migration.md      |   59 +-
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
 e2e/fixtures/error-form-nodes.ts                   |    7 +-
 e2e/global-setup.ts                                |   15 +-
 e2e/helpers/focus-ring.ts                          |   45 +-
 e2e/package.json                                   |    2 +-
 e2e/tests/embed-branding.spec.ts                   |   81 +-
 e2e/tests/focus-tab-order.spec.ts                  |   15 +
 e2e/tests/n8n-compare.spec.ts                      |    3 +-
 e2e/tests/node-coverage.spec.ts                    |   13 +-
 e2e/tests/workflow-tool-inputs.spec.ts             |  406 ++++++
 e2e/visual/state-matrix.spec.ts                    |    3 +-
 internal/ai/agent.go                               |    4 +-
 internal/ai/fromai.go                              |   28 +
 internal/ai/fromai_whole_test.go                   |   47 +
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
 internal/engine/missing_credential_test.go         |   49 +
 internal/engine/service.go                         |   19 +
 internal/interop/n8n/n8n.go                        |   37 +-
 internal/interop/n8n/n8n_test.go                   |  108 +-
 internal/interop/n8n/parameters.go                 |   41 +-
 internal/interop/n8n/rag_import_test.go            |   33 +-
 internal/interop/n8n/roundtrip_fidelity_test.go    |   26 +
 internal/interop/n8n/tags.go                       |  182 +++
 internal/interop/n8n/tags_test.go                  |  312 +++++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   24 +
 internal/interop/n8n/testdata/n8n_rag_16706.json   |   10 +-
 internal/interop/n8n/vectorstore.go                |  153 +++
 internal/interop/n8n/vectorstore_import_test.go    |  281 +++++
 internal/interop/n8n/workflow_tool_inputs.go       |  241 ++++
 internal/interop/n8n/workflow_tool_inputs_test.go  |  319 +++++
 internal/loadoptions/workflows.go                  |   79 ++
 internal/loadoptions/workflows_test.go             |  158 +++
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
 internal/shippedcatalogue/shippedcatalogue.go      |  162 +++
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
 internal/workflow/document.go                      |    9 +
 internal/workflow/tags.go                          |  113 ++
 internal/workflow/tags_test.go                     |  188 +++
 migrations/postgres/000025_workflow_tags.down.sql  |    5 +
 migrations/postgres/000025_workflow_tags.up.sql    |   36 +
 .../postgres/000026_credential_events.down.sql     |    4 +
 .../postgres/000026_credential_events.up.sql       |   36 +
 migrations/sqlite/000025_workflow_tags.down.sql    |    5 +
 migrations/sqlite/000025_workflow_tags.up.sql      |   35 +
 .../sqlite/000026_credential_events.down.sql       |    4 +
 migrations/sqlite/000026_credential_events.up.sql  |   35 +
 nodes/ai.go                                        |  163 ++-
 nodes/core.go                                      |    1 +
 nodes/executors.go                                 |    1 +
 nodes/glyphs_test.go                               |    1 +
 nodes/openai_credential_url_test.go                |   18 +-
 nodes/pgvector.go                                  |   36 +-
 nodes/pgvector_customer.go                         |   26 +-
 nodes/pgvector_test.go                             |    9 +
 nodes/rag.go                                       |   13 +
 nodes/subworkflow.go                               |   55 +
 nodes/subworkflow_inputs_test.go                   |   54 +
 nodes/vectorstore_retrieve.go                      |   84 ++
 nodes/vectorstore_retrieve_test.go                 |  250 ++++
 nodes/vectorstore_tool.go                          |  325 +++++
 nodes/vectorstore_tool_export_test.go              |   20 +
 nodes/vectorstore_tool_security_test.go            |  208 ++++
 nodes/vectorstore_tool_test.go                     |  565 +++++++++
 nodes/workflow_tool_inputs_test.go                 |  263 ++++
 schemas/workflow-v1.schema.json                    |    8 +-
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
 web/package.json                                   |    2 +-
 web/src/app.css                                    |   22 +-
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
 .../components/workflow-editor/node-picker.svelte  |    2 +-
 .../workflow-editor/property-field.svelte          |  230 +++-
 .../workflow-editor/property-field.test.ts         |  164 +++
 web/src/lib/dashboard/credential-usage.test.ts     |   46 +
 web/src/lib/dashboard/credential-usage.ts          |   38 +
 web/src/lib/dashboard/workflow-list.test.ts        |  138 ++-
 web/src/lib/dashboard/workflow-list.ts             |   99 +-
 web/src/lib/design-system/class-scan.ts            |   32 +-
 web/src/lib/design-system/contrast-pairs.ts        |   16 +-
 web/src/lib/design-system/css-tokens.test.ts       |   16 +
 web/src/lib/design-system/css-tokens.ts            |   13 +
 web/src/lib/design-system/focus-lint.test.ts       |   36 +-
 web/src/lib/design-system/focus-lint.ts            |   50 +-
 web/src/lib/design-system/state-matrix.svelte      |   29 +
 web/src/lib/design-system/tokens.contrast.test.ts  |    8 +-
 web/src/lib/design-system/tokens.groups.test.ts    |    2 +-
 web/src/lib/embed/brand-root.test.ts               |   44 +
 web/src/lib/embed/brand-root.ts                    |   24 +
 web/src/lib/workflow-editor/document.test.ts       |   18 +-
 web/src/lib/workflow-editor/document.ts            |   14 +-
 .../lib/workflow-editor/resource-mapper.test.ts    |  262 +++-
 web/src/lib/workflow-editor/resource-mapper.ts     |  172 ++-
 web/src/lib/workflow-editor/shortcuts.test.ts      |   15 +
 web/src/lib/workflow-editor/shortcuts.ts           |    4 +
 web/src/lib/workflow-editor/validation.test.ts     |   24 +
 web/src/lib/workflow-editor/validation.ts          |   14 +-
 web/src/lib/workflow-editor/workflow-input.ts      |   27 +
 web/src/routes/(dashboard)/+layout.svelte          |    2 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |   81 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   48 +-
 web/src/routes/embed/[id]/+page.svelte             |    4 +
 323 files changed, 35705 insertions(+), 1651 deletions(-)
```
