---
id: BUG-49vf3j
title: Credential update wipes the fields and domain scope the caller does not send, and create stores the bullet mask as a secret
status: done
priority: low
labels:
    - credentials
    - api
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T13:42:13Z"
---

# Description

- **Update drops omitted fields** (internal/repository/credentials.go:115-123). This silently clears optional secrets: jwtAuth's secret and privateKey, Google's clientSecret and refresh_token.
- **Update widens an omitted scope.** A missing `allowedDomains` becomes "unrestricted" (:140-147).
- **The docs disagree.** The OpenAPI summary says "any field sent", which implies unsent fields are kept.
- **Create stores the mask.** It stores the bullet placeholder literally as the secret.
- **The mask shows for unset fields.** `Redacted` shows bullets for optional secrets that were never set.

# Acceptance Criteria
- [x] An omitted secret field and an omitted `allowedDomains` (nil) are kept; an explicit empty list clears the scope.
- [x] Create refuses the placeholder.
- [x] `Redacted` shows the mask only for fields that are set.
- [x] Tests cover each case, and the OpenAPI descriptions say what happens.

## Fix (2026-09-25)

Merged in 8ce5ea3 (branch commit 1cae734). An update merges from the whole stored payload: an omitted or placeholder field keeps its value, an explicit "" clears it; a nil `allowedDomains` keeps the scope and `[]` clears it; create refuses the placeholder (422, in the repository); which secrets are set is recorded under a reserved `$setSecrets` key lifted into `Record.SetSecrets` (never a field in the API, Resolve or `$credentials`), so only set secrets are masked; the registry refuses field keys starting with `$` or containing a comma; type is immutable on update. Tests: internal/api/credentials_update_test.go. Review: clean.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `0cfddd40` (last commit at or before ticket created 2026-09-25)
- Commits (3):
  - `8ce5ea3d` — merge: credential updates keep what they leave out, unsaved tests stay on the stored target and scope, and Google Connect finishes only where it started (BUG-49vf3j, BUG-pder07, BUG-0grt9g)
  - `1cae734a` — BUG-49vf3j: a credential update keeps the fields and scope it leaves out, create refuses the placeholder, and only set secrets are masked
  - `506d1d87` — chore(pine): EPIC-brpz48 files the credential audit and live-test findings
- Files changed (base → working tree):

```
 .github/workflows/ci.yml                           |   25 +
 .github/workflows/code-corpus.yml                  |   54 +
 .pine/memory/code-node.md                          |    3 +
 .pine/memory/licensing.md                          |    1 +
 .pine/tickets/BUG-0592hz.md                        |   62 +
 .pine/tickets/BUG-0bzsa1.md                        |   27 +
 .pine/tickets/BUG-0grt9g.md                        |   29 +
 .pine/tickets/BUG-14gp8r.md                        |  127 +-
 .pine/tickets/BUG-2vcwjf.md                        |  126 +
 .pine/tickets/BUG-3mem9s.md                        |  105 +
 .pine/tickets/BUG-46g75c.md                        |  219 +
 .pine/tickets/BUG-49vf3j.md                        |   30 +
 .pine/tickets/BUG-4ch186.md                        |   85 +
 .pine/tickets/BUG-548bk9.md                        |  141 +-
 .pine/tickets/BUG-5dn8hr.md                        |   22 +
 .pine/tickets/BUG-5fhcx7.md                        |   26 +
 .pine/tickets/BUG-5xkexq.md                        |   36 +
 .pine/tickets/BUG-9296bf.md                        |  489 +-
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-a7p6c8.md                        |   21 +
 .pine/tickets/BUG-a9d2hb.md                        |  212 +
 .pine/tickets/BUG-argnka.md                        |   22 +
 .pine/tickets/BUG-asdh5q.md                        |    5 +
 .pine/tickets/BUG-c19kyx.md                        |  149 +
 .pine/tickets/BUG-c7s5ss.md                        |   23 +
 .pine/tickets/BUG-cmnsfz.md                        |   21 +
 .pine/tickets/BUG-cqbq25.md                        |   23 +
 .pine/tickets/BUG-djp647.md                        |  130 +
 .pine/tickets/BUG-e8ytyq.md                        |   36 +
 .pine/tickets/BUG-fthahg.md                        |  196 +-
 .pine/tickets/BUG-g9zf51.md                        |   44 +
 .pine/tickets/BUG-gk7mf5.md                        |   69 +
 .pine/tickets/BUG-h6tj4e.md                        |   93 +
 .pine/tickets/BUG-hejyb9.md                        |  109 +
 .pine/tickets/BUG-hnvn3r.md                        |   36 +
 .pine/tickets/BUG-jwhj6y.md                        |  122 +
 .pine/tickets/BUG-jx2g0k.md                        |  490 +-
 .pine/tickets/BUG-k99658.md                        |  213 +
 .pine/tickets/BUG-kvpx6x.md                        |  144 +
 .pine/tickets/BUG-mzk0xn.md                        |    6 +
 .pine/tickets/BUG-ngt25j.md                        |  697 ++-
 .pine/tickets/BUG-pder07.md                        |   32 +
 .pine/tickets/BUG-pdsydm.md                        |  125 +
 .pine/tickets/BUG-qe71kf.md                        |  142 +
 .pine/tickets/BUG-t9e3k5.md                        |   24 +
 .pine/tickets/BUG-txafja.md                        |  719 ++-
 .pine/tickets/BUG-w18vn3.md                        |   62 +
 .pine/tickets/BUG-x2sxzt.md                        |  491 +-
 .pine/tickets/BUG-x94b3d.md                        |   33 +
 .pine/tickets/BUG-xkz7qx.md                        |   32 +
 .pine/tickets/BUG-xpr3jj.md                        |  416 ++
 .pine/tickets/EPIC-7c3ry9.md                       |   22 +
 .pine/tickets/EPIC-brpz48.md                       |   33 +
 .pine/tickets/EPIC-tjnr1z.md                       |  719 ++-
 .pine/tickets/FEAT-0xsc1s.md                       |   12 +-
 .pine/tickets/FEAT-0ynje5.md                       |   35 +
 .pine/tickets/FEAT-1ge0xc.md                       |    9 +-
 .pine/tickets/FEAT-21h6xp.md                       |  223 +-
 .pine/tickets/FEAT-27g2za.md                       |    9 +-
 .pine/tickets/FEAT-2npfgy.md                       |   46 +
 .pine/tickets/FEAT-39ttf6.md                       |    9 +-
 .pine/tickets/FEAT-4fp51b.md                       |   42 +
 .pine/tickets/FEAT-4jhtny.md                       |    5 +
 .pine/tickets/FEAT-5g42rz.md                       |   10 +-
 .pine/tickets/FEAT-6r663e.md                       |   12 +-
 .pine/tickets/FEAT-7t0xks.md                       |   12 +-
 .pine/tickets/FEAT-83rcve.md                       |   36 +
 .pine/tickets/FEAT-8zgwp6.md                       |    5 +
 .pine/tickets/FEAT-9ep5pw.md                       |    9 +-
 .pine/tickets/FEAT-9we7kw.md                       |  977 +++-
 .pine/tickets/FEAT-afkx3k.md                       |   93 +-
 .pine/tickets/FEAT-f40kg4.md                       |  142 +
 .pine/tickets/FEAT-g33qf6.md                       |   42 +
 .pine/tickets/FEAT-gzd32h.md                       |    5 +
 .pine/tickets/FEAT-hxztwz.md                       |   12 +-
 .pine/tickets/FEAT-mammrz.md                       |   80 +-
 .pine/tickets/FEAT-mccadj.md                       |    6 +
 .pine/tickets/FEAT-mh4e8g.md                       |    6 +
 .pine/tickets/FEAT-mngmn1.md                       |    9 +-
 .pine/tickets/FEAT-n12211.md                       |    6 +
 .pine/tickets/FEAT-pt6ge9.md                       |   12 +-
 .pine/tickets/FEAT-qae4sh.md                       |   23 +
 .pine/tickets/FEAT-qdwm1k.md                       |    7 +-
 .pine/tickets/FEAT-r267jj.md                       |    6 +
 .pine/tickets/FEAT-r87gtj.md                       |   26 +
 .pine/tickets/FEAT-rdfjh1.md                       |   12 +-
 .pine/tickets/FEAT-s3sfx5.md                       |    5 +
 .pine/tickets/FEAT-t58m89.md                       |    5 +
 .pine/tickets/FEAT-vjjs8t.md                       |  925 +++-
 .pine/tickets/FEAT-wdxkvw.md                       |   43 +
 .pine/tickets/FEAT-x9gq0s.md                       |  295 +-
 .pine/tickets/FEAT-yrnkz0.md                       |    5 +
 .pine/tickets/FEAT-z90r5a.md                       |    6 +
 CHANGELOG.md                                       |  149 +
 Makefile                                           |   35 +
 cmd/kilasflow/javascript_test.go                   |   29 +
 cmd/kilasflow/main.go                              |   63 +-
 cmd/kilasflow/sqlite_files_test.go                 |   50 +
 config.example.yaml                                |   52 +-
 docs/src/content/docs/concepts/credentials.md      |  119 +-
 docs/src/content/docs/concepts/execution-model.md  |    8 +
 docs/src/content/docs/concepts/expressions.md      |   12 +-
 .../src/content/docs/concepts/items-and-lineage.md |   22 +-
 .../src/content/docs/concepts/safety-boundaries.md |  161 +-
 docs/src/content/docs/concepts/webhooks.md         |   59 +-
 docs/src/content/docs/guides/code-javascript.md    |  419 ++
 docs/src/content/docs/guides/community-nodes.md    |    3 +-
 docs/src/content/docs/guides/n8n-migration.md      |  242 +-
 .../docs/operate/configuration-reference.md        |   82 +-
 docs/src/content/docs/operate/configuration.md     |    7 +
 docs/src/content/docs/operate/deployment.md        |   18 +-
 docs/src/content/docs/operate/security.md          |   43 +-
 docs/src/content/docs/operate/tenant-deletion.md   |    3 +-
 docs/src/content/docs/reference/api-contract.md    |    6 +-
 docs/src/content/docs/reference/api.md             |    4 +-
 docs/src/content/docs/reference/api/credentials.md |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    2 +-
 docs/src/content/docs/reference/api/interop.md     |   17 +
 docs/src/content/docs/reference/api/workflows.md   |    4 +-
 .../content/docs/reference/expression-grammar.md   |    8 +-
 docs/src/content/docs/reference/node-packs.md      |    8 +
 docs/src/content/docs/start/what-kilasflow-is.md   |    2 +-
 e2e/fixtures/epic-code.ts                          |  271 +
 e2e/fixtures/epic-proofs.ts                        |   23 +
 e2e/helpers/server.ts                              |    8 +
 e2e/tests/code-editor.spec.ts                      |  154 +
 e2e/tests/google-oauth.spec.ts                     |   28 +
 e2e/tests/js-code.spec.ts                          |  129 +-
 e2e/tests/n8n-compare.spec.ts                      |    3 +-
 e2e/tests/n8n-paste.spec.ts                        |  116 +
 e2e/tests/node-coverage.spec.ts                    |    9 +-
 go.mod                                             |    2 +-
 internal/api/convert_fragment_test.go              |   82 +
 internal/api/credentials_probe_scope_test.go       |  235 +
 internal/api/credentials_test.go                   |   59 +-
 internal/api/credentials_update_test.go            |  206 +
 internal/api/debug_ops_test.go                     |   30 +
 internal/api/handlers/credentials.go               |  235 +-
 internal/api/handlers/credentials_probe_test.go    |   75 +
 internal/api/handlers/execution_retry.go           |    9 +-
 internal/api/handlers/executions.go                |    4 +-
 internal/api/handlers/interop.go                   |   59 +
 internal/api/handlers/oauth.go                     |  182 +-
 internal/api/middleware/scope.go                   |    8 +
 internal/api/middleware/scope_test.go              |    3 +
 internal/api/oauth_test.go                         |  364 +-
 internal/api/routes.go                             |    7 +
 internal/cli/openapi_contract_test.go              |    2 +-
 internal/config/config.go                          |   95 +-
 internal/config/config_test.go                     |   37 +
 internal/config/sqlite_root_test.go                |   51 +
 internal/credentials/builtin.go                    |    4 +-
 internal/credentials/credentials.go                |  111 +-
 internal/credentials/credentials_test.go           |   80 +
 internal/credentials/oauth.go                      |   82 +-
 internal/credentials/oauth_test.go                 |   93 +-
 internal/credentials/registry.go                   |    6 +
 internal/database/migrate_test.go                  |    1 +
 internal/engine/always_output_test.go              |  379 ++
 internal/engine/authenticate.go                    |    7 +-
 internal/engine/batch_failure_test.go              |  144 +
 internal/engine/checkpoint.go                      |   67 +-
 internal/engine/checkpoint_test.go                 |  101 +
 internal/engine/eval.go                            |   21 +
 internal/engine/eval_test.go                       |   40 +
 internal/engine/export_test.go                     |   13 +-
 internal/engine/fanout_lineage_test.go             |  241 +
 internal/engine/item_outcomes.go                   |   17 +
 internal/engine/lineage_internal_test.go           |   74 +
 internal/engine/node_branch_test.go                |   92 +
 internal/engine/node_branches.go                   |   78 +
 internal/engine/per_item_suspend_test.go           |  438 ++
 internal/engine/runindex_skip_test.go              |  105 +
 internal/engine/runner.go                          |  470 +-
 internal/engine/service.go                         |   57 +
 internal/engine/static_data.go                     |  136 +
 internal/engine/static_data_service_test.go        |  268 +
 internal/engine/static_data_test.go                |   69 +
 internal/engine/wait_service.go                    |    5 +-
 internal/engine/wait_service_test.go               |  143 +
 internal/expression/doc.go                         |    2 +-
 internal/expression/expression.go                  |    5 +
 internal/expression/globals.go                     |    5 +-
 internal/expression/parity_test.go                 |   48 +
 internal/expression/roots.go                       |  144 +-
 internal/guardrails/compile_scope_test.go          |    7 +-
 internal/idempotency/once.go                       |   64 +
 internal/idempotency/once_test.go                  |   56 +
 internal/interop/n8n/cycle_import_test.go          |  178 +
 internal/interop/n8n/fragment_import_test.go       |  134 +
 internal/interop/n8n/loose.go                      |  252 +
 internal/interop/n8n/loose_json_test.go            |  239 +
 internal/interop/n8n/n8n.go                        |  111 +-
 internal/interop/n8n/n8n_test.go                   |  137 +-
 internal/interop/n8n/parameters.go                 |   62 +-
 internal/jsrun/analyze.go                          |  155 +-
 internal/jsrun/analyze_errors.go                   |  187 +
 internal/jsrun/analyze_html.go                     |  575 +++
 internal/jsrun/analyze_html_test.go                |   63 +
 internal/jsrun/analyze_test.go                     |   24 +-
 internal/jsrun/bounds_test.go                      |  115 +-
 internal/jsrun/buffer_test.go                      |   37 +
 internal/jsrun/clock.go                            |   17 +-
 internal/jsrun/codec.go                            |  149 +-
 internal/jsrun/codec_internal_test.go              |   48 +
 internal/jsrun/comparator_test.go                  |  221 +
 internal/jsrun/console_test.go                     |   14 +-
 internal/jsrun/corpus/BASELINE.md                  |  412 ++
 internal/jsrun/corpus/MANIFEST.json                |  769 +++
 internal/jsrun/corpus/baseline.json                | 3464 +++++++++++++
 internal/jsrun/corpus/corpus_test.go               |  388 ++
 internal/jsrun/corpus/doc.go                       |   32 +
 internal/jsrun/corpus/jsdiff_test.go               |  317 ++
 internal/jsrun/corpus/scoreboard_test.go           |  710 +++
 internal/jsrun/corpus/testdata/control/orders.json |  141 +
 internal/jsrun/doc.go                              |   38 +-
 internal/jsrun/engine.go                           |  186 +-
 internal/jsrun/engine_host.go                      |  174 +
 internal/jsrun/engine_rejections.go                |   94 +
 internal/jsrun/errors.go                           |   51 +
 internal/jsrun/export_test.go                      |   15 +-
 internal/jsrun/guards_test.go                      |   28 +-
 internal/jsrun/helpers.go                          |  298 ++
 internal/jsrun/helpers_test.go                     |  600 +++
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/inline.go                           |  149 +
 internal/jsrun/intl.go                             |  868 +++-
 internal/jsrun/intl_internal_test.go               |  204 +
 internal/jsrun/intl_test.go                        |  314 +-
 internal/jsrun/items.go                            |  101 +-
 internal/jsrun/js/modules/crypto.js                |    3 -
 internal/jsrun/js/modules/errors.js                |  298 ++
 internal/jsrun/js/modules/helpers.js               |  342 ++
 internal/jsrun/js/modules/intl.js                  |   14 +-
 internal/jsrun/js/modules/web.js                   |    8 +-
 internal/jsrun/js/runtime.js                       |  189 +-
 internal/jsrun/jsrun.go                            |   31 +-
 internal/jsrun/jsrun_test.go                       |   58 +-
 internal/jsrun/modules.go                          |    2 +-
 internal/jsrun/programs.go                         |    4 +
 internal/jsrun/rejections_test.go                  |  190 +
 internal/jsrun/roots.go                            |   26 +
 internal/jsrun/roots_test.go                       |  174 +-
 internal/jsrun/run.go                              |  169 +-
 internal/jsrun/security_test.go                    |  284 ++
 internal/jsrun/surface_internal_test.go            |  596 +++
 internal/jsrun/testdata/parity/date-options.json   | 5382 +++++++++++++-------
 internal/jsrun/testdata/parity/dates.json          |   13 +-
 internal/jsrun/testdata/parity/errors.json         |  304 ++
 internal/jsrun/testdata/parity/html-comments.json  |   37 +
 internal/jsrun/testdata/parity/luxon.json          |    7 +
 internal/jsrun/testdata/parity/utf8.json           | 2598 ++++++++++
 internal/jsrun/testdata/parity/zones.json          |  228 +
 internal/jsrun/testdata/surface.txt                |  512 ++
 internal/jsrun/web_test.go                         |   50 +
 internal/jsrun/wire.go                             |   13 +
 internal/jsrun/wording.go                          |  150 +
 internal/jsrun/wording_internal_test.go            |   48 +
 internal/jsrun/wording_test.go                     |  327 ++
 internal/jsrun/wrapper.go                          |   40 +-
 internal/jsworker/confine.go                       |  192 +
 internal/jsworker/confine_linux.go                 |  234 +
 internal/jsworker/confine_linux_test.go            |  501 ++
 internal/jsworker/confine_test.go                  |  250 +
 internal/jsworker/doc.go                           |   57 +-
 internal/jsworker/helpers_test.go                  |  275 +
 internal/jsworker/jsworker_test.go                 |  177 +-
 internal/jsworker/limits_linux.go                  |   12 +-
 internal/jsworker/limits_other.go                  |   22 +-
 internal/jsworker/load_test.go                     |  144 +
 internal/jsworker/pool.go                          |  473 +-
 internal/jsworker/probe_other_test.go              |    6 +
 internal/jsworker/protocol.go                      |   50 +-
 internal/jsworker/security_test.go                 |   52 +
 internal/jsworker/tenants_test.go                  |  194 +
 internal/jsworker/worker.go                        |  289 +-
 internal/loadoptions/sql.go                        |    4 +-
 internal/loadoptions/sql_test.go                   |   12 +-
 internal/node/registry.go                          |   33 +-
 internal/property/editor_test.go                   |   51 +
 internal/property/property.go                      |   52 +
 internal/repository/credentials.go                 |  122 +-
 internal/repository/executions.go                  |   32 +-
 internal/repository/static_data.go                 |   81 +
 internal/repository/static_data_test.go            |   50 +
 internal/repository/table_names_test.go            |   11 +
 internal/repository/tenant_rows.go                 |    5 +-
 internal/repository/workflows.go                   |    8 +-
 internal/sqlnode/export_test.go                    |   32 +-
 internal/sqlnode/guard_test.go                     |    2 +-
 internal/sqlnode/internal_test.go                  |    1 +
 internal/sqlnode/sqlite.go                         |  302 ++
 internal/sqlnode/sqlite_bounded_test.go            |  242 +
 internal/sqlnode/sqlite_confinement_test.go        |  300 ++
 internal/sqlnode/sqlite_unix_test.go               |  116 +
 internal/sqlnode/sqlnode.go                        |   63 +-
 internal/sqlnode/sqlnode_test.go                   |   37 +-
 internal/tenantpurge/harness_test.go               |    5 +
 internal/tenantpurge/purge.go                      |    2 +-
 internal/tenantpurge/purge_test.go                 |    2 +-
 internal/webhook/sandbox.go                        |   90 +
 internal/webhook/sandbox_test.go                   |  231 +
 internal/webhook/webhook.go                        |   14 +-
 internal/workflow/compiler.go                      |  160 +-
 internal/workflow/cycle.go                         |  192 +
 internal/workflow/cycle_test.go                    |  161 +
 internal/workflow/document.go                      |    9 +
 .../postgres/000024_workflow_static_data.down.sql  |    4 +
 .../postgres/000024_workflow_static_data.up.sql    |   25 +
 .../sqlite/000024_workflow_static_data.down.sql    |    4 +
 .../sqlite/000024_workflow_static_data.up.sql      |   25 +
 nodes/annotation.go                                |    1 +
 nodes/code.go                                      |    3 +-
 nodes/code_test.go                                 |   48 +
 nodes/database.go                                  |    4 +-
 nodes/database_test.go                             |   60 +-
 nodes/executors.go                                 |   11 +-
 nodes/jscode.go                                    |   70 +-
 nodes/jscode_helpers.go                            |  272 +
 nodes/jscode_helpers_test.go                       |  270 +
 nodes/jscode_lineage_test.go                       |  207 +
 nodes/jscode_roots.go                              |    3 +-
 nodes/jscode_roots_test.go                         |   54 +
 nodes/jscode_run_test.go                           |  171 +
 nodes/pgvector_customer.go                         |    2 +-
 nodes/postgres_v2.go                               |    2 +-
 nodes/sort_code_test.go                            |  206 +
 nodes/sqlite_attach_test.go                        |    4 +-
 nodes/transform.go                                 |  110 +-
 scripts/code-corpus-sync.sh                        |  312 ++
 scripts/generate-api-reference.mjs                 |    9 +-
 scripts/js-diff/harness.mjs                        |  310 ++
 scripts/js-parity/record-engine.mjs                |  341 ++
 scripts/js-parity/record.mjs                       |  346 +-
 sdk/src/generated/models.ts                        |  162 +-
 .../references/EXPRESSION_ROOTS.md                 |    2 +-
 web/messages/en/editor.json                        |   25 +-
 web/messages/en/executions.json                    |    1 +
 web/messages/id/editor.json                        |   35 +-
 web/messages/id/executions.json                    |    1 +
 web/package.json                                   |   10 +
 web/pnpm-lock.yaml                                 |  160 +-
 web/src/app.css                                    |   16 +
 .../lib/api/generated/credentials/credentials.ts   |    6 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  103 +-
 .../generated/models/convertFragmentInputBody.ts   |   16 +
 .../generated/models/convertedFragmentResource.ts  |   21 +
 web/src/lib/api/generated/models/credentialBody.ts |    4 +-
 .../api/generated/models/credentialBodyFields.ts   |    2 +-
 web/src/lib/api/generated/models/index.ts          |    4 +
 .../lib/api/generated/models/testPayloadBody.ts    |    4 +-
 web/src/lib/api/generated/models/typeOptions.ts    |    4 +
 .../lib/api/generated/models/typeOptionsEditor.ts  |   14 +
 .../generated/models/typeOptionsEditorLanguage.ts  |   17 +
 .../components/workflow-editor/code-editor.svelte  |   92 +
 .../workflow-editor}/diagnostics-section.svelte    |    0
 .../workflow-editor}/import-report.svelte          |    0
 .../components/workflow-editor/node-console.svelte |   27 +
 .../workflow-editor/node-console.test.ts           |   70 +
 .../workflow-editor/paste-report-sheet.svelte      |   45 +
 .../workflow-editor/properties-panel.svelte        |    5 +-
 .../workflow-editor/property-field.svelte          |   28 +-
 .../workflow-editor/property-field.test.ts         |   50 +
 .../workflow-editor/workflow-editor.svelte         |   76 +-
 web/src/lib/workflow-editor/authoring.test.ts      |    6 +-
 web/src/lib/workflow-editor/clipboard.test.ts      |  219 +-
 web/src/lib/workflow-editor/clipboard.ts           |  276 +-
 web/src/lib/workflow-editor/code-editor.test.ts    |   90 +
 web/src/lib/workflow-editor/code-editor.ts         |  264 +
 web/src/lib/workflow-editor/document.test.ts       |   30 +
 web/src/lib/workflow-editor/document.ts            |   25 +
 web/src/lib/workflow-editor/event-stream.svelte.ts |   26 +
 web/src/lib/workflow-editor/event-stream.test.ts   |   27 +-
 web/src/lib/workflow-editor/execution.test.ts      |   51 +
 web/src/lib/workflow-editor/execution.ts           |   52 +
 .../lib/workflow-editor/expression-assist.test.ts  |    7 +
 web/src/lib/workflow-editor/expression-assist.ts   |   12 +-
 .../app/workflows/[id]/export-dialog.svelte        |    2 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |    2 +-
 .../app/workflows/import-report-drawer.svelte      |    2 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   20 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  236 +-
 383 files changed, 49422 insertions(+), 3856 deletions(-)
```
