---
id: BUG-vqghpm
title: Stop and Error evaluates its resolved message a second time, so {{ $env.X }} in item data leaks secrets into the run error
status: done
priority: high
labels:
    - security
    - expressions
parent: EPIC-8rbys7
created: "2026-10-09T12:42:30Z"
updated: "2026-10-09T13:33:35Z"
---

# Description

The Stop and Error node evaluates its message twice. `stopAndErrorMessage` in `nodes/error_workflow.go` resolves the node's parameters with `expression.Resolve`, reads the message from the resolved `errorMessage` (or from the resolved `errorObject`'s `errorMessage`, `message` and `errorDescription` fields), and then, when that text contains `{{`, runs it through `expression.Evaluate` again.

So a message built from data is executed. An author who writes the message as the expression `{{ $json.reason }}`, or sets the error object to `{{ $json.error }}`, hands control of the second evaluation to whoever wrote that item: a webhook caller who sends `reason: "{{ $env.SECRET }}"` gets the secret's value into the run's error. That error is stored on the execution, shown in the run inspector, returned to a caller waiting on the webhook's response, and handed to the workflow's error workflow, which commonly forwards it by email or chat. `$execution`, `$workflow` and every earlier node's output (`$('Other node').all()`) are reachable the same way.

It is the same class of bug as BUG-prhxdy (the Google nodes), found while fixing that one.

# Steps to Reproduce

1. A workflow: Webhook → Stop and Error, with `errorMessage` set to the expression `{{ $json.body.reason }}`, and `SECRET` in the instance's allowlisted environment.
2. POST `{"reason": "{{ $env.SECRET }}"}` to the webhook.
3. Read the failed execution's error.

The same happens with an error object set to an expression whose result's `message` holds `{{ … }}`.

# Expected

The run fails with the text `{{ $env.SECRET }}`, verbatim. An expression's result is never evaluated again.

# Actual

The run fails with the secret's value as its error message.

# Acceptance Criteria
- [x] A resolved `errorMessage` containing `{{ … }}` becomes the run's error verbatim; proven with `$env.SECRET` set. (`TestStopAndErrorNeverEvaluatesAResolvedMessage`, nodes/stop_and_error_leak_test.go; it failed with the secret in the error before the fix)
- [x] The same for an error object that comes from an expression: its `message`, `errorMessage` and `errorDescription` are data. (same test, the errorObject case)
- [x] A message the author typed as a plain string with a `{{ … }}` template (the shape the node's own comment documents), and an error object written as JSON text with a template inside, still render, evaluated exactly once. (same test, plus the existing `TestStopAndErrorFailsWithTheMessageTheAuthorGave`)
- [x] Every other executor that evaluates already-resolved values is either fixed or listed here with why it is safe. (see Progress)

# Notes

Found by the Track C review of FEAT-cf5n4j (2026-10-09). Fixed the way `resolveGoogleIR` was: the author's plain `{{` text is made an expression before the single resolve, and nothing resolved is evaluated again.

# Related Files
- nodes/error_workflow.go — stopAndErrorMessage
- nodes/stop_and_error_leak_test.go — reproduction

# Progress (2026-10-09)

- `stopAndErrorMessage` (nodes/error_workflow.go) resolves once. A plain-text `errorMessage` holding `{{` (optionally with n8n's `=` prefix) is made an expression marker before the single `expression.Resolve`; an error object the author wrote as JSON text has its `errorMessage`, `message` and `errorDescription` templates evaluated once after parsing; an error object or message that an expression produced is used as data and never evaluated.
- Verified: `TestStopAndErrorNeverEvaluatesAResolvedMessage` failed before the fix with `s3cr3t-value` in the run's error for both the message and the object case, and passes after it; the existing Stop and Error tests pass.
- Other places that evaluate text at run time, checked with `grep -rn "expression.Evaluate(" nodes internal cmd packs` and a search for code that detects `{{`:
  - `nodes/google.go` (Gmail, Drive): fixed by BUG-prhxdy.
  - `internal/engine/eval.go`: the editor's evaluate-this-expression endpoint; the template is the author's request, evaluated once.
  - `internal/routing/request.go` and `internal/routing/executor.go`: evaluate the pack's own routing templates (request defaults, `routing.send` values, the post-receive download URL); a parameter's resolved value is bound as `$value` or `$parameter`, data, and never parsed.
  - `internal/loadoptions/loadoptions.go`, `internal/credentials/registry.go`: plain `{{key}}` placeholder substitution, not expression evaluation; loader dependency values are path-escaped, and a credential's URL template only receives that credential's own fields.
  - `internal/sidecarnode/convert.go`: marks a community node's declared `=` default as an expression at conversion time, before any resolution.
  None of them evaluates an already-resolved value.
- Re-review (2026-10-09): the first fix was incomplete. It decided whether to evaluate the error object's fields after resolution by asking whether the object parameter was an expression, so an object the author wrote with expression fields (`{"message": {{ $json.body.reason }}}`) resolved those fields to item data and then evaluated the data again: the secret leaked through `errorMessage`, `message` and `errorDescription`. Now the author's object is normalised before the single resolve (JSON text parsed, or the object kept; its plain `{{` fields made expressions with `templateStringsAsExpressions`, the same rule the Google nodes use), and nothing is evaluated after it. `TestStopAndErrorNeverEvaluatesAResolvedMessage` gained the three nested-expression cases, an expression returning JSON text, a spaced `=` prefix and JSON text reading the item; the nested cases failed with the secret before this change and pass after it, and the reviewer's probe (`zz_rereview_*`, run as an overlay) reports no leak in any of its nine shapes.
- Second re-review (2026-10-09): the message is resolved on its own first, and the error object is normalised and resolved only when the message comes out empty, so a broken template in an object the node never shows no longer fails it; each is still evaluated once. A message that resolves to a number or a boolean is shown as its literal instead of being dropped. The importer's object mode no longer also sets `errorMessage` at the top level, which made the node show the message and never the description (`TestStopAndErrorReadsTheObjectOnlyWithoutAMessageAndKeepsScalarMessages`, `TestErrorWorkflowNodesMapOntoTheNativePair`). The reviewer's 35-shape probe still reports no leak.

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `7e39404d` (last commit at or before ticket created 2026-10-09)
- Commits (5):
  - `d2d30efe` — merge: Gmail, PostgreSQL and Telegram run as agent tools through one generic tool-form table, model values reach executors only as data, and the Google nodes and Stop and Error never evaluate a resolved value again (FEAT-cf5n4j, BUG-b8bwhw, BUG-prhxdy, BUG-vqghpm)
  - `e9a0285d` — BUG-vqghpm: Stop and Error reads its object only without a message, keeps a numeric message, and an imported object shows its description
  - `3a7b0123` — BUG-vqghpm: an error object the author wrote with expression fields is resolved once, never evaluated after
  - `6323e15d` — BUG-vqghpm: Stop and Error resolves its message once, so braces inside a resolved message stay text
  - `c9851370` — chore(pine): BUG-vqghpm files the Stop and Error node's second evaluation of its resolved message
- Files changed (base → working tree):

```
 .env.example                                       |    8 +-
 .github/workflows/ci.yml                           |   21 +
 .pine/memory/e2e.md                                |    2 +
 .pine/memory/n8n-reference.md                      |    3 +-
 .pine/memory/persistence.md                        |    2 +
 .pine/memory/web-editor.md                         |    1 +
 .pine/tickets/BUG-b8bwhw.md                        | 1279 ++++++++++++++++++-
 .pine/tickets/BUG-f85r5h.md                        |  390 +++++-
 .pine/tickets/BUG-f8b8gh.md                        |   47 +
 .pine/tickets/BUG-jx94q9.md                        |  394 +++++-
 .pine/tickets/BUG-k4tzc8.md                        |   46 +
 .pine/tickets/BUG-pdmkn7.md                        |  395 +++++-
 .pine/tickets/BUG-prhxdy.md                        |  479 ++++++++
 .pine/tickets/BUG-vqghpm.md                        |   64 +
 .pine/tickets/BUG-x28fsx.md                        | 1188 +++++++++++++++++-
 .pine/tickets/BUG-ywbvfa.md                        | 1290 ++++++++++++++++++-
 .pine/tickets/EPIC-brpz48.md                       |    7 +-
 .pine/tickets/FEAT-8752vx.md                       |   10 +-
 .pine/tickets/FEAT-cf5n4j.md                       |  549 ++++++++-
 .pine/tickets/FEAT-egm8bf.md                       |    2 +
 .pine/tickets/FEAT-kfmq1z.md                       |    2 +
 .pine/tickets/FEAT-p75n7j.md                       | 1297 +++++++++++++++++++-
 .pine/tickets/FEAT-qae4sh.md                       |  956 ++++++++++++++-
 .pine/tickets/FEAT-r87gtj.md                       |  956 ++++++++++++++-
 .pine/tickets/FEAT-r8ph93.md                       |    3 +
 .pine/tickets/FEAT-xj5tv6.md                       |    5 +
 CHANGELOG.md                                       |   64 +
 CONTRIBUTING.md                                    |   10 +
 Makefile                                           |   11 +
 cmd/kilasflow/main.go                              |  203 ++-
 cmd/kilasflow/reseal_test.go                       |  219 ++++
 cmd/kilasflow/secrets_boot_test.go                 |  102 +-
 cmd/kilasflow/shipped_catalogue_drift_test.go      |    1 +
 config.example.yaml                                |   24 +-
 docs/src/content/docs/concepts/credentials.md      |   76 +-
 docs/src/content/docs/concepts/node-registry.md    |    2 +-
 docs/src/content/docs/guides/embedding.md          |    7 +
 docs/src/content/docs/guides/n8n-migration.md      |   49 +-
 .../src/content/docs/guides/tenant-scoped-nodes.md |    6 +
 .../docs/operate/configuration-reference.md        |   36 +-
 docs/src/content/docs/operate/configuration.md     |   14 +-
 docs/src/content/docs/operate/key-rotation.md      |  158 +++
 docs/src/content/docs/operate/security.md          |   19 +-
 docs/src/content/docs/operate/tenant-deletion.md   |    3 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +
 docs/src/content/docs/reference/api/credentials.md |    3 +-
 docs/src/content/docs/reference/cli.md             |   32 +-
 docs/src/content/docs/start/install.md             |    5 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |    2 +-
 docs/src/styles/kilasflow.css                      |    2 +-
 e2e/.gitignore                                     |    4 +
 e2e/global-setup.ts                                |   26 +-
 e2e/helpers/focus-ring.ts                          |  125 ++
 e2e/helpers/stub.ts                                |    6 +-
 e2e/package.json                                   |    3 +-
 e2e/playwright.visual.config.ts                    |   60 +
 e2e/tests/embed-branding.spec.ts                   |  163 +++
 e2e/tests/focus-tab-order.spec.ts                  |  155 +++
 e2e/tests/node-coverage.spec.ts                    |   16 +
 e2e/visual/fixtures.ts                             |   71 ++
 e2e/visual/focus-ring.spec.ts                      |   50 +
 e2e/visual/force-state.ts                          |   34 +
 e2e/visual/state-matrix.spec.ts                    |   25 +
 e2e/visual/theme-subtree.spec.ts                   |   47 +
 internal/ai/ai_test.go                             |   32 +
 internal/ai/memory.go                              |   26 +-
 internal/api/auth_test.go                          |   25 +-
 internal/api/credential_audit_test.go              |  271 ++++
 internal/api/credential_usage_test.go              |  169 +++
 internal/api/handlers/actor.go                     |   33 +
 internal/api/handlers/credentials.go               |  182 ++-
 internal/api/handlers/oauth.go                     |   29 +-
 internal/api/handlers/workflows.go                 |   38 +-
 internal/api/oauth_external_test.go                |   69 ++
 internal/api/subworkflow_activation_test.go        |  219 ++++
 internal/cli/verbs_credential.go                   |   35 +-
 internal/cli/verbs_credential_test.go              |  197 +--
 internal/cli/verbs_node.go                         |   91 +-
 internal/cli/verbs_node_options_test.go            |  333 +++++
 internal/cli/verbs_workflow_subworkflow_test.go    |  144 +++
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
 internal/database/migrate_test.go                  |    1 +
 internal/engine/authenticate.go                    |    3 +
 internal/engine/expression_context_test.go         |   24 +
 internal/engine/missing_credential_test.go         |   49 +
 internal/engine/runner.go                          |    8 +
 internal/engine/service.go                         |   19 +
 internal/interop/n8n/chatmodel_import_test.go      |    3 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |    6 +-
 internal/interop/n8n/importer_tail_test.go         |   15 +-
 internal/interop/n8n/n8n.go                        |  104 +-
 internal/interop/n8n/n8n_test.go                   |  110 +-
 internal/interop/n8n/parameters.go                 |  143 ++-
 internal/interop/n8n/roundtrip_fidelity_test.go    |   26 +
 internal/interop/n8n/template6270_shape_test.go    |  305 +++++
 internal/interop/n8n/tool_form_import_test.go      |  559 +++++++++
 internal/interop/n8n/tool_forms.go                 |  398 ++++++
 internal/interop/n8n/tool_variant_import_test.go   |   16 +-
 internal/node/registry.go                          |    9 +
 internal/node/tool_form_scope_test.go              |   99 ++
 internal/node/visibility.go                        |   75 +-
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
 internal/repository/postgres_execution_test.go     |    1 +
 internal/repository/refresh.go                     |    6 +-
 internal/repository/refresh_external_test.go       |   86 ++
 internal/repository/reseal.go                      |  153 +++
 internal/repository/subworkflow_activation_test.go |  116 ++
 internal/repository/table_names_test.go            |    1 +
 internal/repository/tenant_rows.go                 |    5 +-
 internal/repository/webhook_state.go               |    7 +-
 internal/repository/workflows.go                   |  100 +-
 internal/shippedcatalogue/shippedcatalogue.go      |    5 +
 internal/skills/examples_test.go                   |   10 +-
 internal/skills/skills_test.go                     |   90 ++
 internal/tenantpurge/harness_test.go               |   32 +-
 internal/tenantpurge/purge.go                      |    3 +-
 internal/tenantpurge/purge_test.go                 |    2 +-
 .../postgres/000026_credential_events.down.sql     |    4 +
 .../postgres/000026_credential_events.up.sql       |   36 +
 .../sqlite/000026_credential_events.down.sql       |    4 +
 migrations/sqlite/000026_credential_events.up.sql  |   35 +
 nodes/ai.go                                        |    7 +-
 nodes/core.go                                      |    4 +-
 nodes/error_workflow.go                            |   81 +-
 nodes/executors.go                                 |   14 +-
 nodes/glyphs_test.go                               |    6 +-
 nodes/gmail.go                                     |   40 +-
 nodes/google.go                                    |   54 +-
 nodes/google_template_test.go                      |  191 +++
 nodes/postgres_v2.go                               |  160 ++-
 nodes/stop_and_error_leak_test.go                  |  115 ++
 nodes/tool_form.go                                 |  486 ++++++++
 nodes/tool_form_postgres_live_test.go              |   96 ++
 nodes/tool_form_run_test.go                        |  977 +++++++++++++++
 nodes/tool_form_test.go                            |  236 ++++
 scripts/cli-examples/main_test.go                  |   28 +
 scripts/cli-examples/seed.go                       |   59 +-
 sdk/CHANGELOG.md                                   |   14 +
 sdk/src/generated/models.ts                        |   43 +-
 skills/kilasflow-ai-agents/SKILL.md                |    2 +-
 skills/kilasflow-ai-agents/references/MODELS.md    |   24 +-
 skills/kilasflow-credentials/SKILL.md              |    3 +-
 .../references/SUB_WORKFLOWS.md                    |   14 +-
 .../kilasflow-import-export/references/EXAMPLE.md  |    9 +-
 .../references/MAPPING_LIMITS.md                   |   17 +-
 skills/kilasflow-node-configuration/SKILL.md       |    6 +-
 .../references/LOAD_OPTIONS.md                     |   20 +-
 .../references/PROPERTY_KINDS.md                   |    5 +-
 skills/kilasflow-triggers/SKILL.md                 |    2 +-
 skills/kilasflow-workflow-lifecycle/SKILL.md       |    2 +-
 .../references/NAMING_AND_DESCRIPTIONS.md          |    4 +-
 .../references/VALIDATION_CHECKLIST.md             |    2 +-
 skills/using-kilasflow-skills/SKILL.md             |    4 +-
 web/messages/en/credentials.json                   |    5 +
 web/messages/id/credentials.json                   |    5 +
 web/package.json                                   |    1 +
 web/src/app.css                                    |  688 ++++++++---
 web/src/app.d.ts                                   |    3 +
 .../lib/api/generated/credentials/credentials.ts   |   26 +-
 .../lib/api/generated/models/credentialResource.ts |    6 +
 .../generated/models/credentialUsageResource.ts    |   19 +
 .../api/generated/models/deleteCredentialParams.ts |   14 +
 web/src/lib/api/generated/models/index.ts          |    2 +
 .../lib/components/dashboard/dashboard-nav.svelte  |   10 +-
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
 .../workflow-editor/version-panel.svelte           |    2 +-
 .../workflow-editor/workflow-editor.svelte         |   16 +-
 web/src/lib/dashboard/credential-usage.test.ts     |   46 +
 web/src/lib/dashboard/credential-usage.ts          |   38 +
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
 web/src/lib/workflow-editor/node-visual.test.ts    |   13 +-
 web/src/lib/workflow-editor/node-visual.ts         |    8 +-
 web/src/lib/workflow-editor/shortcuts.test.ts      |   37 +
 web/src/lib/workflow-editor/shortcuts.ts           |   23 +
 web/src/lib/workflow-editor/validation.test.ts     |   24 +
 web/src/lib/workflow-editor/validation.ts          |   14 +-
 web/src/routes/(dashboard)/+layout.svelte          |   11 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   48 +-
 web/src/routes/+page.svelte                        |   34 +-
 web/src/routes/design/states/+page.svelte          |    5 +
 web/src/routes/design/states/+page.ts              |   16 +
 web/src/routes/embed/[id]/+page.svelte             |   13 +-
 web/vite.config.ts                                 |   10 +
 248 files changed, 23912 insertions(+), 880 deletions(-)
```
