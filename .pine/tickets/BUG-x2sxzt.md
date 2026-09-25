---
id: BUG-x2sxzt
title: Respond to Webhook serves tenant-written HTML on the instance's own origin with no CSP sandbox
status: done
priority: high
labels:
    - security
    - webhooks
parent: EPIC-7c3ry9
created: "2026-09-23T07:35:21Z"
updated: "2026-09-25T12:50:04Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

A "Respond to Webhook" node lets a workflow author return any body and headers as the webhook's HTTP response, defaulting to `text/html; charset=utf-8` when the body is not valid JSON (`writeResponse`, internal/webhook/webhook.go ~1115-1131). Webhook responses are served on the same router as the rest of the API (`router.Handle(WebhookPrefix, webhookHandler)`, internal/api/routes.go ~84-85), i.e. on the instance's own origin.

That makes a tenant-controlled HTML page same-origin with every editor iframe and the dashboard. A page returned this way can run script that makes cookie-authenticated same-origin calls against the host SaaS application's own API, with no isolation between "content a webhook returned" and "the application's own pages".

# Steps to Reproduce

1. Build a workflow whose trigger is a webhook, ending in a Respond to Webhook node that returns an HTML body containing a `<script>`.
2. Call the webhook and inspect the response headers.

# Expected

An HTML response from Respond to Webhook cannot execute script with access to the instance's own cookies/origin — either it carries a CSP that sandboxes it without `allow-same-origin`, or it is served from a separate origin entirely.

# Actual

The response is served with no CSP header, `text/html` (or whatever content-type the workflow set), from the same origin as the dashboard and every editor iframe.

# Acceptance Criteria
- [x] Webhook HTML responses carry a CSP that sandboxes them (`sandbox` without `allow-same-origin`), or are served from a separate origin
- [x] A test asserts a webhook HTML response carries the sandboxing header

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, refs accurate (webhook.go:1115-1131, routes.go:84-85; the only CSP is internal/web/embed.go:238-246).
- A second site: webhook.go:923-929 serves the trigger's own `responseData` acknowledgement as `text/html` with no CSP — same fix needed.
- `writeResponse` copies workflow-set headers first (:1116-1118), so a workflow can set its own `Content-Security-Policy`: force the sandbox header after the copy (or strip tenant CSP).

## Fix (2026-09-25)

`internal/webhook/sandbox.go`: `Handler.ServeHTTP` wraps its writer in `sandboxedWriter`, which, when the status line is written (after every tenant header copy, replacing rather than joining), forces `Content-Security-Policy: sandbox allow-downloads allow-forms allow-modals allow-popups allow-popups-to-escape-sandbox allow-scripts` (no allow-same-origin → opaque origin) and `X-Content-Type-Options: nosniff` on every webhook answer — Respond to Webhook (live and durable paths), the responseData acknowledgement (both audit gaps), last-node JSON, filtered/duplicate answers and every problem refusal. KilasFlow's own hosted form pages get a stricter no-script policy (`default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; sandbox allow-forms`). Browser-checked: forms still submit; a tenant page's script runs with origin "null" and cannot read the instance with its cookie. Tests in `internal/webhook/sandbox_test.go` (all failed first). Docs: concepts/webhooks.md, safety-boundaries.md, operate/security.md; CHANGELOG Security. Reviewed: clean.

# Related Files

internal/webhook/webhook.go `writeResponse`, ~1115-1131 (defaults an unrecognized body to `text/html`, sets no CSP)
internal/api/routes.go ~84-85 (webhook prefix routed on the same router as the API)

## Work Evidence

Closed by `pine close --evidence` on 2026-09-25.

- Base: `1c605e06` (last commit at or before ticket created 2026-09-23)
- Commits (5):
  - `a21592a5` — merge: every webhook answer renders in an opaque-origin CSP sandbox a workflow cannot loosen (BUG-x2sxzt)
  - `cfe6e609` — BUG-x2sxzt: every webhook answer renders in an opaque-origin CSP sandbox a workflow cannot loosen
  - `b5ced6d4` — chore(pine): BUG-9296bf, BUG-x2sxzt and BUG-jx2g0k move to doing
  - `bf841006` — chore(pine): EPIC-7c3ry9 records its dependencies and audit notes, and files four infra tickets
  - `988e2488` — chore(pine): the follow-ups the stabilise sprint found
- Files changed (base → working tree):

```
 .github/workflows/ci.yml                           |   25 +
 .github/workflows/code-corpus.yml                  |   54 +
 .pine/memory/code-node.md                          |    3 +
 .pine/memory/licensing.md                          |    1 +
 .pine/tickets/BUG-0592hz.md                        |   62 +
 .pine/tickets/BUG-0bzsa1.md                        |   27 +
 .pine/tickets/BUG-0grt9g.md                        |   25 +
 .pine/tickets/BUG-14gp8r.md                        |  142 +-
 .pine/tickets/BUG-2eryxn.md                        |   54 +-
 .pine/tickets/BUG-2vcwjf.md                        |  126 +
 .pine/tickets/BUG-2xrz6c.md                        |   41 +
 .pine/tickets/BUG-2z8geh.md                        |   94 +-
 .pine/tickets/BUG-3mem9s.md                        |  105 +
 .pine/tickets/BUG-46g75c.md                        |  219 +
 .pine/tickets/BUG-49vf3j.md                        |   26 +
 .pine/tickets/BUG-4ch186.md                        |   85 +
 .pine/tickets/BUG-548bk9.md                        |  141 +-
 .pine/tickets/BUG-5dn8hr.md                        |   22 +
 .pine/tickets/BUG-5fhcx7.md                        |   26 +
 .pine/tickets/BUG-5xkexq.md                        |   36 +
 .pine/tickets/BUG-9296bf.md                        |   53 +
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-a7p6c8.md                        |   21 +
 .pine/tickets/BUG-a9d2hb.md                        |  212 +
 .pine/tickets/BUG-argnka.md                        |   22 +
 .pine/tickets/BUG-asdh5q.md                        |   52 +
 .pine/tickets/BUG-b3p8va.md                        |   71 +-
 .pine/tickets/BUG-bw2zc1.md                        |   50 +-
 .pine/tickets/BUG-c19kyx.md                        |  149 +
 .pine/tickets/BUG-c3fgw5.md                        |   47 +
 .pine/tickets/BUG-c73h98.md                        |   42 +
 .pine/tickets/BUG-c7s5ss.md                        |   23 +
 .pine/tickets/BUG-cmnsfz.md                        |   21 +
 .pine/tickets/BUG-cqbq25.md                        |   23 +
 .pine/tickets/BUG-d2t3kp.md                        |   73 +
 .pine/tickets/BUG-djp647.md                        |  130 +
 .pine/tickets/BUG-e7dwpk.md                        |   75 +-
 .pine/tickets/BUG-e8ytyq.md                        |   36 +
 .pine/tickets/BUG-fthahg.md                        |  244 +
 .pine/tickets/BUG-g7ffj1.md                        |   52 +-
 .pine/tickets/BUG-g9zf51.md                        |   44 +
 .pine/tickets/BUG-gk7mf5.md                        |   69 +
 .pine/tickets/BUG-h6tj4e.md                        |   93 +
 .pine/tickets/BUG-hejyb9.md                        |  109 +
 .pine/tickets/BUG-hnvn3r.md                        |   36 +
 .pine/tickets/BUG-jwhj6y.md                        |  122 +
 .pine/tickets/BUG-jx2g0k.md                        |   45 +
 .pine/tickets/BUG-k99658.md                        |  213 +
 .pine/tickets/BUG-kvpx6x.md                        |  144 +
 .pine/tickets/BUG-mzk0xn.md                        |    6 +
 .pine/tickets/BUG-ngt25j.md                        |  697 ++-
 .pine/tickets/BUG-pder07.md                        |   28 +
 .pine/tickets/BUG-pdsydm.md                        |  125 +
 .pine/tickets/BUG-qe71kf.md                        |  142 +
 .pine/tickets/BUG-r1m83f.md                        |   52 +-
 .pine/tickets/BUG-rytwy7.md                        |   75 +-
 .pine/tickets/BUG-t12ffz.md                        |  149 +-
 .pine/tickets/BUG-t9e3k5.md                        |   24 +
 .pine/tickets/BUG-tpyg0q.md                        |   46 +
 .pine/tickets/BUG-txafja.md                        |  719 ++-
 .pine/tickets/BUG-vsmnby.md                        |  130 +-
 .pine/tickets/BUG-w18vn3.md                        |   62 +
 .pine/tickets/BUG-w9k234.md                        |   48 +
 .pine/tickets/BUG-x2sxzt.md                        |   52 +
 .pine/tickets/BUG-xkz7qx.md                        |   32 +
 .pine/tickets/BUG-xpr3jj.md                        |   32 +
 .pine/tickets/BUG-y38bss.md                        |   46 +-
 .pine/tickets/BUG-zf4pnj.md                        |   70 +-
 .pine/tickets/EPIC-7c3ry9.md                       |   22 +
 .pine/tickets/EPIC-brpz48.md                       |   33 +
 .pine/tickets/EPIC-tjnr1z.md                       |  744 ++-
 .pine/tickets/FEAT-0xsc1s.md                       |   12 +-
 .pine/tickets/FEAT-0ynje5.md                       |   35 +
 .pine/tickets/FEAT-1ge0xc.md                       |    9 +-
 .pine/tickets/FEAT-21h6xp.md                       |  223 +-
 .pine/tickets/FEAT-27g2za.md                       |    9 +-
 .pine/tickets/FEAT-2npfgy.md                       |   46 +
 .pine/tickets/FEAT-39ttf6.md                       |    9 +-
 .pine/tickets/FEAT-3kwr8j.md                       |   38 +
 .pine/tickets/FEAT-4fp51b.md                       |   42 +
 .pine/tickets/FEAT-4jhtny.md                       |    5 +
 .pine/tickets/FEAT-5g42rz.md                       |   10 +-
 .pine/tickets/FEAT-6r663e.md                       |   12 +-
 .pine/tickets/FEAT-7t0xks.md                       |   12 +-
 .pine/tickets/FEAT-83rcve.md                       |   36 +
 .pine/tickets/FEAT-8zgwp6.md                       |    5 +
 .pine/tickets/FEAT-9ep5pw.md                       |    9 +-
 .pine/tickets/FEAT-9we7kw.md                       |  977 +++-
 .pine/tickets/FEAT-9yr3tt.md                       |   40 +
 .pine/tickets/FEAT-afkx3k.md                       |   93 +-
 .pine/tickets/FEAT-f40kg4.md                       |  142 +
 .pine/tickets/FEAT-g33qf6.md                       |   42 +
 .pine/tickets/FEAT-g6k3y9.md                       |  116 +-
 .pine/tickets/FEAT-gzd32h.md                       |    5 +
 .pine/tickets/FEAT-hxztwz.md                       |   12 +-
 .pine/tickets/FEAT-mammrz.md                       |   80 +-
 .pine/tickets/FEAT-mccadj.md                       |    6 +
 .pine/tickets/FEAT-mh4e8g.md                       |    6 +
 .pine/tickets/FEAT-mngmn1.md                       |    9 +-
 .pine/tickets/FEAT-n12211.md                       |    6 +
 .pine/tickets/FEAT-pt6ge9.md                       |   12 +-
 .pine/tickets/FEAT-pxcbqj.md                       |  425 +-
 .pine/tickets/FEAT-qae4sh.md                       |   23 +
 .pine/tickets/FEAT-qdwm1k.md                       |   50 +
 .pine/tickets/FEAT-r267jj.md                       |    6 +
 .pine/tickets/FEAT-r87gtj.md                       |   26 +
 .pine/tickets/FEAT-rdfjh1.md                       |   12 +-
 .pine/tickets/FEAT-s3sfx5.md                       |    5 +
 .pine/tickets/FEAT-t58m89.md                       |    5 +
 .pine/tickets/FEAT-vjjs8t.md                       |  925 +++-
 .pine/tickets/FEAT-wdxkvw.md                       |   43 +
 .pine/tickets/FEAT-x9gq0s.md                       |  295 +-
 .pine/tickets/FEAT-yrnkz0.md                       |    5 +
 .pine/tickets/FEAT-yxhgeh.md                       |  415 +-
 .pine/tickets/FEAT-z90r5a.md                       |    6 +
 .pine/tickets/FEAT-zjrw76.md                       |  421 +-
 CHANGELOG.md                                       |  185 +-
 Makefile                                           |   35 +
 cmd/kilasflow/javascript_test.go                   |   29 +
 cmd/kilasflow/main.go                              |   63 +-
 config.example.yaml                                |   32 +-
 docs/src/content/docs/concepts/execution-model.md  |    8 +
 docs/src/content/docs/concepts/expressions.md      |   12 +-
 .../src/content/docs/concepts/items-and-lineage.md |   22 +-
 .../src/content/docs/concepts/safety-boundaries.md |  133 +-
 .../content/docs/concepts/tenancy-and-embedding.md |   22 +-
 docs/src/content/docs/concepts/webhooks.md         |   59 +-
 docs/src/content/docs/guides/code-javascript.md    |  419 ++
 docs/src/content/docs/guides/community-nodes.md    |    3 +-
 docs/src/content/docs/guides/embedding.md          |   27 +-
 docs/src/content/docs/guides/n8n-migration.md      |  235 +-
 .../docs/operate/configuration-reference.md        |   50 +-
 docs/src/content/docs/operate/deployment.md        |   18 +-
 docs/src/content/docs/operate/security.md          |    8 +
 docs/src/content/docs/operate/tenant-deletion.md   |    3 +-
 docs/src/content/docs/reference/api-contract.md    |    6 +-
 docs/src/content/docs/reference/api.md             |    4 +-
 docs/src/content/docs/reference/api/executions.md  |    2 +-
 docs/src/content/docs/reference/api/interop.md     |   17 +
 docs/src/content/docs/reference/api/workflows.md   |    4 +-
 docs/src/content/docs/reference/cli.md             |   62 +-
 .../content/docs/reference/expression-grammar.md   |    8 +-
 docs/src/content/docs/reference/node-packs.md      |    8 +
 docs/src/content/docs/start/what-kilasflow-is.md   |    2 +-
 e2e/fixtures/epic-code.ts                          |  271 +
 e2e/fixtures/epic-proofs.ts                        |   23 +
 e2e/helpers/stub.ts                                |    7 +-
 e2e/tests/code-editor.spec.ts                      |  154 +
 e2e/tests/datastore.spec.ts                        |  103 +
 e2e/tests/js-code.spec.ts                          |  129 +-
 e2e/tests/n8n-paste.spec.ts                        |  116 +
 e2e/tests/smoke.spec.ts                            |   86 +
 go.mod                                             |    2 +-
 internal/ai/fromai.go                              |   30 +
 internal/ai/fromai_test.go                         |   37 +
 internal/api/convert_fragment_test.go              |   82 +
 internal/api/datastores_test.go                    |   57 +
 internal/api/debug_ops_test.go                     |   30 +
 internal/api/embed_test.go                         |  106 +-
 internal/api/handlers/admin.go                     |    9 +-
 internal/api/handlers/admin_admin_test.go          |    2 +-
 internal/api/handlers/auth.go                      |    4 +-
 internal/api/handlers/credentials.go               |    5 +-
 internal/api/handlers/datastores.go                |   13 +-
 internal/api/handlers/execution_retry.go           |    9 +-
 internal/api/handlers/executions.go                |    4 +-
 internal/api/handlers/interop.go                   |   59 +
 internal/api/handlers/oauth.go                     |   17 +-
 internal/api/handlers/problem.go                   |   16 +
 internal/api/handlers/resume_test.go               |    2 +-
 internal/api/handlers/workflows.go                 |   22 +
 internal/api/handlers/workflows_delete_test.go     |   81 +-
 internal/api/middleware/embed.go                   |   54 +-
 internal/api/middleware/embed_test.go              |   88 +
 internal/api/middleware/scope.go                   |    8 +
 internal/api/middleware/scope_test.go              |    3 +
 internal/api/problems.go                           |   97 +
 internal/api/problems_test.go                      |  171 +
 internal/api/schedules_test.go                     |   28 +
 internal/api/server.go                             |    4 +-
 internal/api/workflows_test.go                     |    8 +
 internal/cli/cli.go                                |   22 +
 internal/cli/client.go                             |  132 +-
 internal/cli/guard_test.go                         |  144 +-
 internal/cli/mcp.go                                |   44 +-
 internal/cli/mcp_test.go                           |  317 +-
 internal/cli/openapi.go                            |   38 +-
 internal/cli/openapi_contract_test.go              |   10 +-
 internal/cli/verbs_api.go                          |   59 +-
 internal/cli/verbs_api_test.go                     |  256 +
 internal/cli/verbs_credential_test.go              |   71 +
 internal/config/config.go                          |   71 +-
 internal/config/config_test.go                     |   37 +
 internal/credentials/credentials_test.go           |   26 +-
 .../datastore_unique_names_migration_test.go       |  212 +
 internal/database/migrate_test.go                  |    1 +
 ...webhook_route_lifecycle_state_migration_test.go |   78 +
 internal/database/workflow_actor_migration_test.go |   26 +-
 internal/datastore/catalogue.go                    |   46 +-
 internal/datastore/engine.go                       |   26 +-
 internal/datastore/engine_test.go                  |   13 +-
 internal/datastore/names.go                        |  132 +
 internal/datastore/names_test.go                   |  271 +
 internal/embed/confinement.go                      |   12 -
 internal/embed/confinement_test.go                 |   14 +-
 internal/embed/embed.go                            |   29 +
 internal/embed/embed_test.go                       |   60 +
 internal/engine/always_output_test.go              |  379 ++
 internal/engine/authenticate.go                    |    7 +-
 internal/engine/batch_failure_test.go              |  144 +
 internal/engine/checkpoint.go                      |   14 +-
 internal/engine/eval.go                            |   23 +-
 internal/engine/eval_test.go                       |   40 +
 internal/engine/export_test.go                     |   13 +-
 internal/engine/fanout_lineage_test.go             |  241 +
 internal/engine/item_outcomes.go                   |   46 +-
 internal/engine/item_outcomes_test.go              |  126 +-
 internal/engine/lineage_internal_test.go           |   74 +
 internal/engine/node_branch_test.go                |   92 +
 internal/engine/node_branches.go                   |   78 +
 internal/engine/runindex_skip_test.go              |  151 +
 internal/engine/runner.go                          |  630 ++-
 internal/engine/runner_test.go                     |  968 ++++
 internal/engine/service.go                         |   71 +-
 internal/engine/static_data.go                     |  136 +
 internal/engine/static_data_service_test.go        |  268 +
 internal/engine/static_data_test.go                |   69 +
 internal/engine/subworkflow_test.go                |  168 +-
 internal/engine/wait_service.go                    |    5 +-
 internal/engine/wait_service_test.go               |  492 +-
 internal/expression/doc.go                         |    2 +-
 internal/expression/expression.go                  |   17 +
 internal/expression/expression_test.go             |   63 +
 internal/expression/globals.go                     |    9 +-
 internal/expression/parity_test.go                 |   48 +
 internal/expression/roots.go                       |  178 +-
 internal/guardrails/compile_scope_test.go          |    7 +-
 internal/interop/n8n/cycle_import_test.go          |  178 +
 internal/interop/n8n/export_test.go                |    7 +
 internal/interop/n8n/fragment_import_test.go       |  134 +
 internal/interop/n8n/loose.go                      |  252 +
 internal/interop/n8n/loose_json_test.go            |  239 +
 internal/interop/n8n/n8n.go                        |  129 +-
 internal/interop/n8n/n8n_test.go                   |  505 +-
 internal/interop/n8n/parameters.go                 |  180 +-
 internal/interop/n8n/rag_import_test.go            |   40 +-
 internal/jsrun/analyze.go                          |  211 +-
 internal/jsrun/analyze_errors.go                   |  187 +
 internal/jsrun/analyze_html.go                     |  575 +++
 internal/jsrun/analyze_html_test.go                |   63 +
 internal/jsrun/analyze_test.go                     |   24 +-
 internal/jsrun/bounds_test.go                      |  115 +-
 internal/jsrun/buffer_test.go                      |   78 +
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
 internal/jsrun/doc.go                              |   59 +-
 internal/jsrun/engine.go                           |  184 +-
 internal/jsrun/engine_host.go                      |  174 +
 internal/jsrun/engine_rejections.go                |   94 +
 internal/jsrun/errors.go                           |   51 +
 internal/jsrun/export_test.go                      |   15 +-
 internal/jsrun/guards_test.go                      |   64 +-
 internal/jsrun/helpers.go                          |  298 ++
 internal/jsrun/helpers_test.go                     |  600 +++
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/inline.go                           |  149 +
 internal/jsrun/intl.go                             |  868 +++-
 internal/jsrun/intl_internal_test.go               |  204 +
 internal/jsrun/intl_test.go                        |  314 +-
 internal/jsrun/items.go                            |  101 +-
 internal/jsrun/js/modules/buffer.js                |   59 +-
 internal/jsrun/js/modules/crypto.js                |    3 -
 internal/jsrun/js/modules/errors.js                |  298 ++
 internal/jsrun/js/modules/helpers.js               |  342 ++
 internal/jsrun/js/modules/intl.js                  |   14 +-
 internal/jsrun/js/modules/web.js                   |    8 +-
 internal/jsrun/js/runtime.js                       |  227 +-
 internal/jsrun/jsrun.go                            |   35 +-
 internal/jsrun/jsrun_test.go                       |   58 +-
 internal/jsrun/modules.go                          |    2 +-
 internal/jsrun/programs.go                         |   22 +-
 internal/jsrun/rejections_test.go                  |  190 +
 internal/jsrun/roots.go                            |   26 +
 internal/jsrun/roots_test.go                       |  202 +-
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
 internal/jsworker/jsworker_test.go                 |  207 +-
 internal/jsworker/limits_linux.go                  |   12 +-
 internal/jsworker/limits_other.go                  |   22 +-
 internal/jsworker/load_test.go                     |  144 +
 internal/jsworker/pool.go                          |  473 +-
 internal/jsworker/probe_other_test.go              |    6 +
 internal/jsworker/protocol.go                      |   50 +-
 internal/jsworker/security_test.go                 |   52 +
 internal/jsworker/tenants_test.go                  |  194 +
 internal/jsworker/worker.go                        |  296 +-
 internal/loadoptions/datastores.go                 |   31 +-
 internal/loadoptions/datastores_test.go            |   35 +
 internal/mcp/server.go                             |   32 +-
 internal/node/registry.go                          |   33 +-
 internal/nodepack/trigger.go                       |   75 +-
 internal/nodepack/trigger_capture_test.go          |  221 +
 internal/property/editor_test.go                   |   51 +
 internal/property/property.go                      |   52 +
 internal/repository/executions.go                  |   32 +-
 internal/repository/models.go                      |    5 +
 internal/repository/schedules.go                   |   43 +-
 internal/repository/static_data.go                 |   81 +
 internal/repository/static_data_test.go            |   50 +
 internal/repository/table_names_test.go            |   11 +
 internal/repository/tenant_rows.go                 |    5 +-
 internal/repository/webhook_state.go               |  108 +
 internal/repository/webhook_state_test.go          |  148 +
 internal/repository/webhooks.go                    |   18 +-
 internal/repository/workflows.go                   |   12 +-
 internal/routing/executor.go                       |    2 +-
 internal/safehttp/path.go                          |   15 +
 internal/safehttp/safehttp_test.go                 |   20 +
 internal/scheduler/scheduler.go                    |   51 +-
 internal/scheduler/scheduler_test.go               |   83 +-
 internal/tenantpurge/harness_test.go               |    5 +
 internal/tenantpurge/purge.go                      |    2 +-
 internal/tenantpurge/purge_test.go                 |    2 +-
 internal/webhook/lifecycle.go                      |   74 +-
 internal/webhook/lifecycle_test.go                 |  114 +
 internal/webhook/request_lifecycle.go              |  563 +-
 internal/webhook/request_lifecycle_capture_test.go |  401 ++
 internal/webhook/request_lifecycle_test.go         |  275 +
 internal/webhook/route_state_test.go               |   75 +
 internal/webhook/sandbox.go                        |   90 +
 internal/webhook/sandbox_test.go                   |  231 +
 internal/webhook/webhook.go                        |   43 +-
 internal/workflow/compiler.go                      |  160 +-
 internal/workflow/cycle.go                         |  192 +
 internal/workflow/cycle_test.go                    |  161 +
 internal/workflow/document.go                      |    9 +
 .../000022_datastore_unique_names.down.sql         |    8 +
 .../postgres/000022_datastore_unique_names.up.sql  |   64 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../postgres/000024_workflow_static_data.down.sql  |    4 +
 .../postgres/000024_workflow_static_data.up.sql    |   25 +
 .../sqlite/000022_datastore_unique_names.down.sql  |    8 +
 .../sqlite/000022_datastore_unique_names.up.sql    |   57 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../sqlite/000024_workflow_static_data.down.sql    |    4 +
 .../sqlite/000024_workflow_static_data.up.sql      |   25 +
 nodes/ai.go                                        |   18 +-
 nodes/annotation.go                                |    1 +
 nodes/code.go                                      |    3 +-
 nodes/code_test.go                                 |   48 +
 nodes/datastore.go                                 |  718 ++-
 nodes/datastore_byname_test.go                     |   76 +
 nodes/datastore_tool_test.go                       | 1299 ++++-
 nodes/embedscope.go                                |   36 +-
 nodes/embedscope_test.go                           |   56 +-
 nodes/executors.go                                 |   11 +-
 nodes/jscode.go                                    |   76 +-
 nodes/jscode_helpers.go                            |  272 +
 nodes/jscode_helpers_test.go                       |  270 +
 nodes/jscode_lineage_test.go                       |  207 +
 nodes/jscode_roots.go                              |    3 +-
 nodes/jscode_roots_test.go                         |   54 +
 nodes/jscode_run_test.go                           |  171 +
 nodes/sort_code_test.go                            |  206 +
 nodes/transform.go                                 |  110 +-
 packs/waha/waha_test.go                            |   55 +-
 scripts/code-corpus-sync.sh                        |  312 ++
 scripts/generate-api-reference.mjs                 |    9 +-
 scripts/js-diff/harness.mjs                        |  310 ++
 scripts/js-parity/record-engine.mjs                |  341 ++
 scripts/js-parity/record.mjs                       |  346 +-
 sdk/src/generated/models.ts                        |  146 +-
 skills/kilasflow-datastore/SKILL.md                |    3 +-
 skills/kilasflow-datastore/references/FILTERS.md   |    2 +-
 .../references/EXPRESSION_ROOTS.md                 |    2 +-
 web/messages/en/editor.json                        |   25 +-
 web/messages/en/executions.json                    |    1 +
 web/messages/id/editor.json                        |   35 +-
 web/messages/id/executions.json                    |    1 +
 web/package.json                                   |   10 +
 web/pnpm-lock.yaml                                 |  160 +-
 web/src/app.css                                    |   16 +
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  103 +-
 .../generated/models/convertFragmentInputBody.ts   |   16 +
 .../generated/models/convertedFragmentResource.ts  |   21 +
 web/src/lib/api/generated/models/index.ts          |    4 +
 web/src/lib/api/generated/models/typeOptions.ts    |    4 +
 .../lib/api/generated/models/typeOptionsEditor.ts  |   14 +
 .../generated/models/typeOptionsEditorLanguage.ts  |   17 +
 web/src/lib/api/http.ts                            |   32 +-
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
 web/src/lib/embed/session.svelte.ts                |    6 +-
 web/src/lib/embed/session.test.ts                  |   47 +-
 web/src/lib/workflow-editor/authoring.test.ts      |    6 +-
 web/src/lib/workflow-editor/clipboard.test.ts      |  219 +-
 web/src/lib/workflow-editor/clipboard.ts           |  276 +-
 web/src/lib/workflow-editor/code-editor.test.ts    |   90 +
 web/src/lib/workflow-editor/code-editor.ts         |  264 +
 web/src/lib/workflow-editor/document.test.ts       |   30 +
 web/src/lib/workflow-editor/document.ts            |   25 +
 web/src/lib/workflow-editor/event-stream.svelte.ts |   26 +
 web/src/lib/workflow-editor/event-stream.test.ts   |   27 +-
 web/src/lib/workflow-editor/execution.test.ts      |   70 +
 web/src/lib/workflow-editor/execution.ts           |   62 +-
 .../lib/workflow-editor/expression-assist.test.ts  |    7 +
 web/src/lib/workflow-editor/expression-assist.ts   |   12 +-
 .../app/workflows/[id]/export-dialog.svelte        |    2 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |    2 +-
 .../app/workflows/import-report-drawer.svelte      |    2 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |   61 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  236 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |    2 +-
 456 files changed, 57097 insertions(+), 4196 deletions(-)
```
