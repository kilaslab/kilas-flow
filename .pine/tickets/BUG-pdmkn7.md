---
id: BUG-pdmkn7
title: Activating a parent whose sub-workflow is inactive answers 500 instead of a refusal naming the node and the child
status: done
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T12:25:15Z"
updated: "2026-10-09T13:06:29Z"
---

# Description

Activating a parent workflow whose Execute Workflow / Workflow Tool target is inactive is refused (`refuseInactiveSubworkflows` in internal/repository/workflows.go), but the API answers a generic **500 "workflow operation failed"**. Only the server log names the child workflow. The caller (dashboard, CLI, an agent) gets no actionable reason, and the triggers skill's claim that the refusal names the node is false.

Reported by the Track H2 coder in the 2026-10-09 parallel wave while validating skill examples against a real server.

# Steps to Reproduce

1. Create a child workflow starting with an Execute Workflow Trigger; leave it inactive.
2. Create a parent with an Execute Workflow node pointing at the child.
3. `kilasflow workflow activate <parent> --yes` (or POST /workflows/{id}/activate).

# Expected

A 4xx (422 `config.invalid` or 409) whose error names the parent node, and the child workflow id/name, and says to activate the child first.

# Actual

500 "workflow operation failed"; the reason only appears in the server log.

# Acceptance Criteria
- [x] Activation of a parent with an inactive sub-workflow answers a 4xx naming the node and the child workflow, through the API, the CLI and MCP. Evidence: `TestActivatingAParentWhoseSubworkflowIsInactiveAnswers422NamingTheNodeAndTheChild` (internal/api), `TestActivatingAParentWithTwoBadSubworkflowCallsNamesBothNodes` (one `errors[]` entry per bad node), `TestWorkflowActivateSaysWhichNodeNeedsWhichInactiveChild` and `TestMCPWorkflowActivateReturnsTheSameRefusal` (internal/cli, real API + stores).
- [x] A test pins the status code and message; the 500 path is gone. Evidence: the API and CLI tests above assert 422, `application/problem+json`, the full sentence (node, child name and id, "not active", "activate that workflow first"), `errors[]` entries of `config.invalid` on each `nodeId` at `body/nodes/<i>/parameters/workflowId`, and that the detail is not "workflow operation failed"; they failed with the 500 before the change. `TestPublishingAParentWhoseSubworkflowIsInactiveAnswersTheSame422` covers the publish route, `TestActivatingAParentWhoseSubworkflowIsGoneAnswers422SayingItDoesNotExist` the missing-target variant, `TestActivationRefusesEveryBadSubWorkflowCallAtOnce` (internal/repository) three bad calls and one live one.
- [x] The triggers skill / SUB_WORKFLOWS.md describe the real behaviour. Evidence on the merged tree: skills/kilasflow-triggers/SKILL.md (sub-workflow bullet) and skills/kilasflow-flow-patterns/references/SUB_WORKFLOWS.md ("The child must be active") state the 422 `config.invalid`, the node and the child named by name and id, and the separate "does not exist" sentence, citation-free as main's source-citations rule requires; `go test ./internal/skills/...` passes.


## Plan (2026-10-09)

Root cause. `refuseInactiveSubworkflows` (internal/repository/workflows.go:561) builds its refusal with `fmt.Errorf`, so the error that comes out of `publish` (workflows.go:513) is an untyped string. Both the activate and the publish handlers send it through `Workflows.problem` (internal/api/handlers/workflows.go:1027), which knows `ErrNotFound`, `WebhookConflictError`, `workflow.ValidationErrors` and `ErrNeverFires` and sends everything else to `serverProblem(ctx, "workflow operation failed", err)`: a 500 whose reason only reaches the log. The refusal already names the node and the child *id*, but never the child's *name*, and no caller can see it.

The change.
1. internal/repository/workflows.go: a typed `SubworkflowRefusalError` (node id and name, the path of the node's `workflowId` parameter, the child workflow id and name, and whether the child is missing or only inactive). `refuseInactiveSubworkflows` returns it; its `Error()` keeps today's wording ("node X (id) calls workflow ..., which is not active: activate that workflow first, then activate this one" / "does not exist in this workspace") and adds the child's name when it has one.
2. internal/api/handlers/workflows.go: `problem` maps it with `errors.As` to a 422, the status the compile refusals and the never-fires cron refusal already use for "the document you activated cannot go live". `detail` is the full sentence (so the CLI envelope's `error.message` and an MCP tool result carry it, because both read `detail`), and `errors[]` carries one entry at `body/nodes/<i>/parameters/workflowId` whose value is a `WorkflowValidationIssue{code: "config.invalid", nodeId}`, so the editor can still point at the node. There is no separate unknown-credential refusal on this path (a missing credential is the compiler's `config.required`, mapped by `compileProblem`); the new mapping reuses the same `WorkflowValidationIssue` value shape.
3. The CLI and MCP need no code: the CLI client turns any problem document into the envelope with `problem.detail` as the message, and `kilasflow mcp serve` returns the CLI envelope verbatim. They are proven by a test, not changed.
4. Docs: skills/kilasflow-triggers/SKILL.md line 66 says what the refusal is (422 `config.invalid`, node and child named) instead of implying it. `skills/kilasflow-flow-patterns/references/SUB_WORKFLOWS.md` does not exist on main (no `kilasflow-flow-patterns` skill yet), so there is nothing to fix there and I will say so in the report.

Proof (test first).
- internal/repository/subworkflow_activation_test.go: `errors.As` finds `*SubworkflowRefusalError` with the node, child id, child name and `Missing` set correctly for an inactive and for a missing target.
- A new internal/api test with the production `nodes.SubworkflowCalls` wired into the store: an inactive child gives 422 `application/problem+json`, a `detail` naming the node and the child's name and id, `errors[0].value.code == "config.invalid"`, `nodeId`, the workflow stays inactive, and activating the child first makes the parent activate (200). The same through `POST /workflows/{id}/versions/{versionId}/publish`.
- A CLI test that runs `workflow activate` against that real handler stack and asserts exit code, `error.status` 422 and `error.message` naming the node and child; the MCP tool result is that same envelope, asserted in the mcp-serve test if the harness allows it.


## Progress (2026-10-09)

Root cause as planned: `refuseInactiveSubworkflows` returned a `fmt.Errorf`, `Workflows.problem` had no case for it and answered `serverProblem(..."workflow operation failed")`.

What changed.
- internal/repository/workflows.go: `SubworkflowRefusalError` (node id/name, JSON-pointer path of the `workflowId` parameter, child id and name, `Missing`). The inactive sentence now names the child as `"Billing" (wf_...)`; the missing-target sentence is unchanged. `errors.As`-able, so the editor and every API caller can tell the two apart.
- internal/api/handlers/workflows.go: `problem` maps it through `subworkflowProblem` to a 422 whose `detail` is the whole sentence (the CLI envelope's `error.message` and the MCP tool result are built from `detail`, so neither needed code) plus one `errors[]` entry at `body/nodes/<i>/parameters/workflowId` carrying a `WorkflowValidationIssue{code: config.invalid, nodeId}`. There is no unknown-credential activation refusal on main (a missing credential is the compiler's `config.required` via `compileProblem`), so I matched the compile refusals' shape: 422 plus that value.
- Tests: internal/repository/subworkflow_activation_test.go (typed error, fields, path), internal/api/subworkflow_activation_test.go (new file, own `newSubworkflowAPI` helper because the shared helper leaves the extractor off, which is why this went unnoticed), internal/cli/verbs_workflow_subworkflow_test.go (CLI exit 2 / `bad_request` / status 422 and MCP `workflow_activate`, against real stores).
- skills/kilasflow-triggers/SKILL.md: sentence corrected; `make generate-skills-index-check` still clean.

Deviations. The ticket mentions SUB_WORKFLOWS.md: the `kilasflow-flow-patterns` skill is not on main, so no edit there. The CLI exit for a 422 is the existing usage-class exit (`bad_request`); I did not add a new CLI ErrCode.

Left: nothing.

## Review round (2026-10-09)

Merged main (Track H2's skills) into the branch. The three skills conflicts (kilasflow-triggers, kilasflow-node-configuration SKILL.md and references/LOAD_OPTIONS.md) were resolved to main's side, because mine cited repository source paths, which main's `source-citations` rule forbids, and used `api activate-workflow` where main uses `workflow activate --yes`. The reviewer's follow-up patch was then applied and updated triggers SKILL.md and flow-patterns references/SUB_WORKFLOWS.md to the 422 behaviour, so the Progress note above that says SUB_WORKFLOWS.md "does not exist on main" is superseded: it exists now and is correct.

Review fixes.
- `refuseInactiveSubworkflows` no longer returns on the first bad call: `SubworkflowRefusalError` now holds `Refusals []SubworkflowRefusal` (one per bad node, in document order, a live child left out), and `subworkflowProblem` answers one `errors[]` entry per node with the sentences joined in `detail`. Tests: `TestActivationRefusesEveryBadSubWorkflowCallAtOnce`, `TestActivatingAParentWithTwoBadSubworkflowCallsNamesBothNodes`.
- web/src/lib/workflow-editor/validation.ts `withNodeNames` replaced a node id as a bare substring, so an id like `call` corrupted `calls workflow` and `Run billings`. It now replaces only occurrences that stand alone (no word, dot or dash character on either side), reading the id literally. Three vitest cases in validation.test.ts; `pnpm check` 0 errors and `pnpm test` 895 passed.
- CHANGELOG.md [Unreleased] has the Fixed entry.
- The main-side `refuseUnknownCredentials` refusal returns a `workflow.ValidationErrors` (message "workflow validation failed"); this refusal keeps its own typed error so that `detail` carries the sentence itself, which is what the CLI envelope and the MCP result print. Both are 422 `config.invalid` with one entry per node.

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `277dd011` (last commit at or before ticket created 2026-10-09)
- Commits (9):
  - `6b1cadae` — merge: an inactive sub-workflow is a 422 naming every bad call, n8n's executionOrder v1 round-trips silently, and node options sends dependent parameters (BUG-pdmkn7, BUG-jx94q9, BUG-f85r5h)
  - `22a23d25` — chore(pine): the three tickets' ticks and evidence are true on the merged tree, with a review-round note each
  - `513a812a` — BUG-pdmkn7: the editor replaces a node id in a server message only where it stands alone
  - `4967d760` — BUG-pdmkn7: the sub-workflow refusal lists every bad call, one errors[] entry per node
  - `b77e79ad` — merge: main into the BUG-pdmkn7 / BUG-jx94q9 / BUG-f85r5h branch, skills conflicts resolved to main's side
  - `172ca2e7` — chore(pine): BUG-pdmkn7 criteria ticked with evidence and progress written back
  - `efd7a52e` — BUG-pdmkn7: activating a parent whose sub-workflow is inactive answers 422 config.invalid naming the node and the child instead of a 500
  - `71f8beb4` — chore(pine): BUG-pdmkn7 plans a typed sub-workflow activation refusal mapped to 422 config.invalid
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
 .pine/tickets/BUG-jx94q9.md                        |   74 ++
 .pine/tickets/BUG-kkkrmx.md                        |  158 +++
 .pine/tickets/BUG-n6p7qy.md                        | 1177 +++++++++++++++++-
 .pine/tickets/BUG-pc284d.md                        |   60 +
 .pine/tickets/BUG-pdmkn7.md                        |   75 ++
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
 323 files changed, 35018 insertions(+), 1651 deletions(-)
```
