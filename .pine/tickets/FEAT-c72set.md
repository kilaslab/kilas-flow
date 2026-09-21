---
id: FEAT-c72set
title: 'Router skill: using-kilasflow-skills, the always-on non-negotiables and command reference'
status: done
priority: medium
labels:
    - agent
    - docs
deps:
    - FEAT-x5qqpm
parent: EPIC-r0yg5q
phase: p1
created: "2026-09-20T07:47:52Z"
updated: "2026-09-21T02:14:03Z"
---

## Scope

Design §5.5 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): the always-on router with the non-negotiables, red-flag rationalisations, skill index, compact command/tool reference and protocol order.

## Acceptance criteria

- [x] States the guardrails of FEAT-mha6a0: `activate` publishes a public endpoint; `delete` and `import` create or destroy authority; secrets are referenced by credential id only; an embed session can never activate/delete/import/manage datastores; anything unverifiable is fetched from the instance.
- [x] Idempotency guidance matches what shipped (FEAT-hj8pyx): `Idempotency-Key` on run and datastore writes.
- [x] The compact command reference is generated from the command tree, not typed.
- [x] Passes the same gates as the domain skills (skills drift gates ticket).

## Implementation notes

The router is `skills/using-kilasflow-skills/SKILL.md` — the thirteenth skill, one always-on file, no
reference files (§5.4 gives it none, so it has no `## Reference files` section). Its body uses the
six §5.3 sections and folds §5.5's eight items into them: the non-negotiables (1, 2), the strong
defaults (3), the red-flag table as `## Anti-patterns` (4), the skill index, the generated command
reference and the protocol order as three H3s under `## Decision tree` (5, 6, 7), and “reporting
skills used” (8) in `## Not shipped yet`, because no mutating verb records `--skills-used` in this
tree.

### The compact command reference is generated, not typed (criterion 3)

- `scripts/skills-command-reference/main.go` — renders the section into the SKILL.md between
  `<!-- generated:command-reference -->` and `<!-- /generated:command-reference -->`. The tree comes
  from `go run ./cmd/kilasflow help --json`, the document the binary hands an agent; nothing in the
  section is typed: `kilasflow <path>` is the tree's path, the operation column is the tree's
  `operation` (or `local` when the verb drives none), and the sentence is the tree's own summary.
  It lives in its own directory for the same reason `scripts/skills-index` does —
  `scripts/config-reference.go` already declares `func main` in package `main`.
- It refuses to write, or to pass `--check`, when the rendered document would break the bundle's
  rules (`skills.Parse` + `skills.Check` on the spliced document, the same guard `skills-index`
  applies) and when the frontmatter declares a command the tree does not implement. That second
  guard is why a splice cannot quietly satisfy a declaration: a verb that leaves the tree fails the
  generator instead of writing a reference that still teaches it.
- `Makefile` — `generate-skills-command-reference` and `generate-skills-command-reference-check`,
  appended after `generate-skills-index-check`, in the same shape (regenerate / fail if stale,
  printing `run: make generate-skills-command-reference`). No binary is built by hand: the generator
  builds and runs the CLI itself, so the targets need the Go toolchain only.
- `scripts/skills-command-reference/main_test.go` — the splice refuses a document with no marker, one
  marker, two sections or an inverted pair; render + splice is a fixed point that names every verb;
  `resolves` matches a whole verb path and not a prefix (`kilasflow workflow` is not a command).

### Idempotency guidance matches what shipped (criterion 2)

One sentence, worded exactly as `kilasflow-workflow-lifecycle`, `kilasflow-triggers` and
`kilasflow-datastore` word it: the server honours `Idempotency-Key` on `run-workflow` and on a
datastore row write, the same key with the same request replays the first outcome marked
`Idempotent-Replayed: true`, the same key with a different request is refused `409`, and no verb
carries the key yet, so a retry-safe run is
`kilasflow api run-workflow --path id=<workflowId> --header Idempotency-Key=<key> --body @input.json`
and a retry-safe write is the same header on `upsert-datastore-row`/`insert-datastore-row`.
`grep -rn 'Idempotency-Key' --include=*.go internal | grep -v _test` shows the surface the sentence
describes (handlers `datastores.go`, `idempotency.go`; `internal/idempotency/**`).

### Declaration cross-checks, against this worktree's tree

- `go run ./cmd/kilasflow help --json | jq -r '.data[].path' | sort > /tmp/paths.txt` → **39 verbs**.
- `grep -rh 'OperationID: *"' internal/api/handlers/ | sed 's/.*OperationID: *"\([^"]*\)".*/\1/' |
  sort -u` → **76 operation ids**.
- `comm -23 /tmp/decl.txt /tmp/paths.txt` → **0 unresolved** for the 13 declared `kilasflow_commands`
  (`api`, `context`, `credential list`, `datastore rows`, `exec get`, `exec trace`, `node describe`,
  `node list`, `node options`, `run`, `workflow create`, `workflow get`, `workflow list`).
- the same set difference against the 76 ids → **0 unresolved** for the 11 declared
  `kilasflow_operations`.
- a token walk over the whole body (longest prefix over the 39 paths) → **41 distinct tokens, 0
  unresolved**, including the generated section, whose every line is therefore a command the binary
  implements.

### Negative proofs

- Planted staleness: replaced one generated row with `kilasflow pack publish` →
  `make generate-skills-command-reference-check` exited 1 printing
  `run: make generate-skills-command-reference`; restoring the row made `--check` print
  `skills/using-kilasflow-skills/SKILL.md: 39 verbs, up to date`, exit 0.
- Planted declaration: renamed a declared command to `kilasflow datastore insert` → the generator
  refused with `using-kilasflow-skills [declarations] kilasflow_commands declares "kilasflow
  datastore insert", which the body never names`.
- Planted tree change: renamed the `context` verb in `internal/cli/context.go` to `briefing` → the
  generator refused with `using-kilasflow-skills declares 1 command(s) the binary does not implement:
  kilasflow context`, even though the prose still names it. Restored (`md5sum` identical).
- Planted prefix: declared `kilasflow node` instead of `kilasflow node list` → refused with
  `declares 1 command(s) the binary does not implement: kilasflow node`.

### Gates run, with outcomes

- `make generate-skills-index-check` → `skills/index.json: 13 skills, up to date`, exit 0.
- `make generate-skills-command-reference-check` → `skills/using-kilasflow-skills/SKILL.md: 39 verbs,
  up to date`, exit 0.
- `go test -count=1 ./internal/skills/...` → ok; `go test -count=1 ./scripts/skills-command-reference/...`
  → ok.
- `gofmt -l scripts/skills-command-reference/` → no output; `go vet` and `go build ./...` → clean.
- Criterion 4 is ticked for the gates that exist today: the two bundle generators' checks and the
  `internal/skills` suite, the same ones the twelve domain skills pass. Design §5.7's G1–G5 do not
  exist yet — they are FEAT-bb4s6e — and G1 (every declared command, and every `kilasflow …` inside a
  fenced block, resolves in the real tree) is the one this section's generator already enforces for
  the router. `make skills-check` should include `generate-skills-command-reference-check` when
  FEAT-bb4s6e wires it.

### Notes for the orchestrator

- The `## Not shipped yet` rows and the guardrail sentence are measured against **this worktree's**
  tree, where no verb is guarded: activation, deletion and import are described as
  `kilasflow api <operation-id>` calls with no `--yes` to lean on, and `--skills-used` as absent.
  FEAT-m4d2y1's guarded verbs (and FEAT-4jns31's `skills` verbs) land in parallel; when they do, the
  generated block is what goes stale first (`make generate-skills-command-reference-check` fails with
  the line that changed, and the generator adds `[guarded]` to a guarded verb's row automatically),
  and the prose rows need re-measuring by hand. The `--yes` rule itself is worded to survive that:
  it states the rule and lets the generated section say which verbs are guarded.
- The generated section is the only part of the bundle that is a product fact rather than prose; it
  is regenerated with `make generate-skills-command-reference`, and `skills/index.json` with
  `make generate-skills-index`.
- The throwaway cross-check script used above was not committed: the generator enforces the command
  half permanently, and the operation half is FEAT-bb4s6e's G2.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `b3c9f3f9` (last commit at or before ticket created 2026-09-20)
- Commits (2):
  - `b3637a84` — FEAT-c72set: the always-on router skill, and the command reference it generates
  - `aa307d46` — FEAT-mha6a0: cut the agent-surface implementation tickets from the design
- Files changed (base → working tree):

```
 .github/actions/js-toolchain/action.yml            |   11 +-
 .github/workflows/capstone.yml                     |  127 +
 .github/workflows/ci.yml                           |   44 +-
 .github/workflows/release.yml                      |  120 +-
 .pine/memory/docs.md                               |    8 +
 .pine/memory/tenancy.md                            |    8 +
 .pine/memory/testing.md                            |    9 +
 .pine/tickets/BUG-fng4m2.md                        |   88 +
 .pine/tickets/BUG-fvdz46.md                        |  368 +-
 .pine/tickets/BUG-p3t7yq.md                        |  212 +
 .pine/tickets/BUG-rpkjpy.md                        |  864 ++-
 .pine/tickets/BUG-t9j2ek.md                        |  410 +-
 .pine/tickets/BUG-vzzkg3.md                        |  558 +-
 .pine/tickets/BUG-w8h3km.md                        |  123 +
 .pine/tickets/BUG-xmr673.md                        |  707 ++-
 .pine/tickets/EPIC-bkj6yf.md                       |  680 ++-
 .pine/tickets/EPIC-r0yg5q.md                       |   27 +
 .pine/tickets/FEAT-15k49d.md                       | 1343 ++++-
 .pine/tickets/FEAT-1axhdn.md                       | 2017 ++++++-
 .pine/tickets/FEAT-3taswf.md                       | 1205 +++-
 .pine/tickets/FEAT-48hreg.md                       | 1441 ++++-
 .pine/tickets/FEAT-4jns31.md                       |   26 +
 .pine/tickets/FEAT-5fhj6p.md                       |  292 +-
 .pine/tickets/FEAT-7cg0cd.md                       | 1339 ++++-
 .pine/tickets/FEAT-8mymac.md                       | 1115 +++-
 .pine/tickets/FEAT-bb4s6e.md                       |   27 +
 .pine/tickets/FEAT-bp59m4.md                       |  420 ++
 .pine/tickets/FEAT-c72set.md                       |  130 +
 .pine/tickets/FEAT-emf6k5.md                       |  536 +-
 .pine/tickets/FEAT-ew46cb.md                       |   65 +
 .pine/tickets/FEAT-fpqvwx.md                       |  893 ++-
 .pine/tickets/FEAT-hj8pyx.md                       |  714 ++-
 .pine/tickets/FEAT-m4d2y1.md                       |  763 +++
 .pine/tickets/FEAT-mha6a0.md                       |  123 +-
 .pine/tickets/FEAT-qdedm0.md                       |  993 +++-
 .pine/tickets/FEAT-x5qqpm.md                       |  850 +++
 .pine/tickets/FEAT-yxwyav.md                       |   26 +
 CHANGELOG.md                                       |  176 +-
 CONTRIBUTING.md                                    |    6 +
 Makefile                                           |  126 +-
 README.md                                          |   11 +-
 cmd/kilasflow/embed_issuer.go                      |   18 +
 cmd/kilasflow/embed_issuer_test.go                 |  134 +
 cmd/kilasflow/fleet.go                             |   92 +
 cmd/kilasflow/fleet_test.go                        |  395 ++
 cmd/kilasflow/idempotency_test.go                  |  225 +
 cmd/kilasflow/main.go                              |  325 +-
 cmd/kilasflow/main_test.go                         |  115 +
 cmd/kilasflow/sidecar.go                           |  400 ++
 cmd/kilasflow/sidecar_test.go                      |  362 ++
 cmd/kilasflow/webhook_wiring_test.go               |  138 +
 config.example.yaml                                |  154 +-
 .../content/docs/concepts/datastore-concurrency.md |  179 +
 docs/src/content/docs/concepts/execution-model.md  |    1 +
 docs/src/content/docs/concepts/node-registry.md    |   42 +-
 .../src/content/docs/concepts/safety-boundaries.md |   41 +-
 .../content/docs/concepts/tenancy-and-embedding.md |   27 +-
 docs/src/content/docs/concepts/webhooks.md         |   55 +
 docs/src/content/docs/guides/community-nodes.md    |  147 +-
 docs/src/content/docs/guides/embedding.md          |   41 +-
 docs/src/content/docs/guides/idempotency.md        |  197 +
 docs/src/content/docs/guides/node-authoring.md     |    6 +
 .../src/content/docs/guides/tenant-scoped-nodes.md |  183 +
 .../content/docs/operate/acceptance-capstone.md    |  184 +
 docs/src/content/docs/operate/benchmark.md         |  229 +-
 .../docs/operate/configuration-reference.md        |  313 +-
 docs/src/content/docs/operate/deployment.md        |   31 +-
 .../src/content/docs/operate/javascript-sidecar.md |  193 +
 docs/src/content/docs/operate/security.md          |   23 +-
 docs/src/content/docs/operate/tenant-deletion.md   |  194 +
 docs/src/content/docs/operate/upgrades.md          |   61 +-
 docs/src/content/docs/reference/api-contract.md    |   31 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/datastores.md  |   33 +-
 docs/src/content/docs/reference/api/errors.md      |   13 +-
 docs/src/content/docs/reference/api/interop.md     |    6 +
 docs/src/content/docs/reference/api/nodes.md       |   16 +-
 docs/src/content/docs/reference/api/system.md      |    3 +-
 docs/src/content/docs/reference/api/tenants.md     |   21 +
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 docs/src/content/docs/reference/cli.md             |  707 +++
 docs/src/content/docs/reference/node-packs.md      |   80 +-
 docs/src/content/docs/start/install.md             |   12 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   11 +-
 e2e/.gitignore                                     |    1 +
 e2e/benchmark/README.md                            |  217 +-
 e2e/benchmark/SUMMARY.md                           |  109 +-
 e2e/benchmark/bench-2026-09-20T11-37-55.json       | 5978 ++++++++++++++++++++
 e2e/benchmark/bench-2026-09-20T12-01-21.json       | 5976 +++++++++++++++++++
 e2e/benchmark/lib.mjs                              |  405 +-
 e2e/benchmark/method.mjs                           |  393 ++
 e2e/benchmark/method.test.mjs                      |  366 ++
 e2e/benchmark/n8n-container.mjs                    |  458 ++
 e2e/benchmark/n8n-container.test.mjs               |  131 +
 e2e/benchmark/n8n.mjs                              |  416 +-
 e2e/benchmark/run.mjs                              |  877 ++-
 e2e/benchmark/scan-secrets.mjs                     |  196 +
 e2e/benchmark/scan-secrets.test.mjs                |   70 +
 e2e/benchmark/summarise.mjs                        |  393 +-
 e2e/benchmark/summarise.test.mjs                   |  303 +
 e2e/benchmark/workflows.mjs                        |  670 ++-
 e2e/benchmark/workflows.test.mjs                   |  223 +
 e2e/capstone/epic-capstone.spec.ts                 |  364 ++
 e2e/capstone/global-teardown.ts                    |   26 +
 e2e/fixtures/epic-config.ts                        |  172 +
 e2e/fixtures/epic-corpus.ts                        |  307 +
 e2e/fixtures/epic-external.ts                      |  293 +-
 e2e/fixtures/epic-hermetic.ts                      |  201 +
 e2e/fixtures/epic-image.ts                         |  456 ++
 e2e/fixtures/epic-proofs.ts                        |  854 +++
 e2e/fixtures/error-form-nodes.ts                   |  218 +
 e2e/fixtures/n8n-live.ts                           |    5 +
 e2e/helpers/stub.ts                                |   16 +-
 e2e/playwright.capstone.config.ts                  |   45 +
 e2e/scripts/capstone-lib.mjs                       |  519 ++
 e2e/scripts/capstone-report.mjs                    |  238 +
 e2e/tests/dashboard-lists.spec.ts                  |  187 +
 e2e/tests/datastore.spec.ts                        |    2 +-
 e2e/tests/epic-acceptance.spec.ts                  |  694 +--
 e2e/tests/epic-machinery.spec.ts                   |  581 ++
 e2e/tests/i18n.spec.ts                             |   62 +
 e2e/tests/library-import.spec.ts                   |    6 +-
 e2e/tests/n8n-compare.spec.ts                      |   53 +-
 e2e/tests/node-coverage.spec.ts                    |   56 +-
 e2e/tests/pack-editor.spec.ts                      |   39 +-
 e2e/tests/waha-migration.spec.ts                   |   15 +-
 go.mod                                             |    1 +
 go.sum                                             |    2 +
 internal/api/cors_test.go                          |   19 +-
 internal/api/datastores_test.go                    |  418 ++
 internal/api/embed_datastore_test.go               |   28 +
 internal/api/embed_defaults_test.go                |  150 +
 internal/api/handlers/admin.go                     |  154 +
 internal/api/handlers/admin_admin_test.go          |  268 +-
 internal/api/handlers/api_key_scopes_test.go       |  138 +
 internal/api/handlers/auth.go                      |   83 +-
 internal/api/handlers/auth_test.go                 |   63 +-
 internal/api/handlers/datastores.go                |  291 +-
 internal/api/handlers/idempotency.go               |  115 +
 internal/api/handlers/interop.go                   |   13 +-
 internal/api/handlers/nodes.go                     |   76 +-
 internal/api/handlers/problem.go                   |   17 +
 internal/api/handlers/system.go                    |  142 +-
 internal/api/handlers/workflows.go                 |  171 +-
 internal/api/idempotency_test.go                   | 1004 ++++
 internal/api/middleware/auth.go                    |    6 +
 internal/api/middleware/cors.go                    |   11 +-
 internal/api/middleware/cors_test.go               |    2 +-
 internal/api/middleware/embed.go                   |  140 +-
 internal/api/middleware/embed_sentences_test.go    |   35 +
 internal/api/middleware/embed_test.go              |   10 +-
 internal/api/middleware/loginlimit.go              |   13 +
 internal/api/middleware/loginlimit_test.go         |    3 +-
 internal/api/middleware/scope.go                   |  354 ++
 internal/api/middleware/scope_test.go              |  276 +
 internal/api/node_types_sidecar_test.go            |   82 +
 internal/api/node_visibility_test.go               |  544 ++
 internal/api/ready_fleet_test.go                   |  358 ++
 internal/api/routes.go                             |   16 +-
 internal/api/scoped_token_test.go                  |  154 +
 internal/api/server.go                             |   19 +-
 internal/api/tenant_delete_test.go                 |  386 ++
 internal/api/workflow_audit_test.go                |  209 +
 internal/auth/auth.go                              |   20 +
 internal/binary/binary.go                          |  117 +
 internal/binary/binary_test.go                     |  217 +
 internal/cli/api_prefix.go                         |   43 +
 internal/cli/cli.go                                |  446 ++
 internal/cli/cli_test.go                           |  248 +
 internal/cli/client.go                             |  424 ++
 internal/cli/client_test.go                        |  320 ++
 internal/cli/command.go                            |  139 +
 internal/cli/command_test.go                       |  319 ++
 internal/cli/config.go                             |  258 +
 internal/cli/config_test.go                        |  749 +++
 internal/cli/context.go                            |  318 ++
 internal/cli/context_test.go                       |  236 +
 internal/cli/doc.go                                |   35 +
 internal/cli/exit.go                               |  125 +
 internal/cli/exit_test.go                          |  101 +
 internal/cli/flags.go                              |   88 +
 internal/cli/guard.go                              |   43 +
 internal/cli/guard_test.go                         |  543 ++
 internal/cli/openapi.go                            |  202 +
 internal/cli/openapi_contract_test.go              |  411 ++
 internal/cli/output.go                             |  116 +
 internal/cli/output_test.go                        |  246 +
 internal/cli/skills_used_test.go                   |   87 +
 internal/cli/sse.go                                |  151 +
 internal/cli/sse_test.go                           |  149 +
 internal/cli/verbs_api.go                          |  315 ++
 internal/cli/verbs_api_test.go                     |  820 +++
 internal/cli/verbs_auth.go                         |  314 +
 internal/cli/verbs_credential.go                   |  256 +
 internal/cli/verbs_credential_test.go              |  119 +
 internal/cli/verbs_datastore.go                    |  482 ++
 internal/cli/verbs_datastore_test.go               |  215 +
 internal/cli/verbs_exec.go                         |  413 ++
 internal/cli/verbs_exec_test.go                    |  436 ++
 internal/cli/verbs_node.go                         |  292 +
 internal/cli/verbs_node_test.go                    |  196 +
 internal/cli/verbs_pack.go                         |  146 +
 internal/cli/verbs_pack_test.go                    |  195 +
 internal/cli/verbs_run.go                          |  257 +
 internal/cli/verbs_run_test.go                     |  314 +
 internal/cli/verbs_schedule.go                     |   36 +
 internal/cli/verbs_schedule_test.go                |   46 +
 internal/cli/verbs_system.go                       |  282 +
 internal/cli/verbs_system_test.go                  |  182 +
 internal/cli/verbs_tenant.go                       |  169 +
 internal/cli/verbs_tenant_test.go                  |  116 +
 internal/cli/verbs_workflow.go                     |  739 +++
 internal/cli/verbs_workflow_test.go                |  425 ++
 internal/config/config.go                          |  387 +-
 internal/config/config_test.go                     |  365 ++
 internal/config/embed_branding_test.go             |  192 +
 internal/config/embed_validate.go                  |   34 +
 internal/config/packs_visibility.go                |   83 +
 internal/config/packs_visibility_test.go           |  168 +
 internal/config/webhook_require_auth_test.go       |   50 +
 internal/credentials/registry.go                   |    5 +
 internal/database/migrate.go                       |   40 +-
 internal/database/migrate_test.go                  |   83 +-
 internal/database/tenant_columns_test.go           |  309 +
 internal/database/webhook_route_backfill_test.go   |  491 ++
 internal/database/workflow_actor_migration_test.go |  181 +
 internal/datastore/catalogue.go                    |   16 +-
 internal/datastore/column_tenant_test.go           |  152 +
 internal/datastore/concurrency.go                  |  224 +-
 internal/datastore/concurrency_test.go             |  579 +-
 internal/datastore/doc.go                          |   13 +-
 internal/datastore/engine.go                       |   27 +-
 internal/datastore/engine_test.go                  |   66 +-
 internal/datastore/fleet.go                        |  364 +-
 internal/datastore/fleet_engine_test.go            |  711 +++
 internal/datastore/isolation.go                    |   96 +-
 internal/datastore/isolation_test.go               |  245 +-
 internal/datastore/migrate_test.go                 |    1 +
 internal/datastore/model.go                        |    9 +-
 internal/datastore/rows.go                         |  124 +-
 internal/datastore/upsert_id.go                    |  247 +
 internal/datastore/upsert_id_test.go               |  498 ++
 internal/embed/embed.go                            |  129 +-
 internal/embed/embed_branding_test.go              |  164 +
 internal/embed/embed_lifetime_test.go              |  146 +
 internal/engine/authenticate.go                    |   72 +-
 internal/engine/authenticate_test.go               |  130 +
 internal/engine/datastore_concurrency_test.go      |  393 ++
 internal/engine/export_test.go                     |   20 +
 internal/engine/service.go                         |   30 +-
 internal/engine/tenant_visibility_test.go          |  379 ++
 internal/engine/wait_service.go                    |   47 +-
 internal/engine/wait_service_test.go               |  219 +-
 internal/guardrails/compile_scope_test.go          |  440 ++
 internal/idempotency/hash.go                       |   64 +
 internal/idempotency/hash_test.go                  |  142 +
 internal/idempotency/idempotency.go                |  432 ++
 internal/idempotency/idempotency_test.go           | 1120 ++++
 internal/idempotency/sweeper.go                    |   94 +
 internal/idempotency/sweeper_test.go               |  146 +
 internal/interop/n8n/n8n_test.go                   |   55 +
 internal/interop/n8n/parameters.go                 |   11 +
 internal/node/registry.go                          |   31 +
 internal/node/registry_bench_test.go               |  112 +
 internal/node/visibility.go                        |  347 ++
 internal/node/visibility_test.go                   |  796 +++
 internal/nodepack/loaddir.go                       |   86 +
 internal/nodepack/loaddir_module_test.go           |  178 +
 internal/nodepack/module.go                        |  350 ++
 internal/nodepack/module_e2e_test.go               |   98 +
 internal/nodepack/module_test.go                   |  173 +
 internal/nodepack/nodepack.go                      |  116 +-
 internal/nodepack/trigger.go                       |    8 +
 internal/nodepack/trigger_require_auth_test.go     |   43 +
 internal/nodepack/validate.go                      |   67 +-
 internal/nodepack/visibility_test.go               |  262 +
 internal/repository/api_key_scopes_test.go         |   78 +
 internal/repository/auth.go                        |   91 +-
 internal/repository/idempotency.go                 |  360 ++
 internal/repository/idempotency_test.go            |  615 ++
 internal/repository/models.go                      |   66 +-
 internal/repository/postgres_execution_test.go     |   16 +
 internal/repository/table_names_test.go            |    1 +
 internal/repository/tenant_purge.go                |   48 +-
 internal/repository/tenant_purge_test.go           |   48 +
 internal/repository/tenant_rows.go                 |  283 +
 internal/repository/tenant_rows_test.go            |  420 ++
 internal/repository/webhooks.go                    |  124 +-
 internal/repository/webhooks_delivery_test.go      |  147 +
 internal/repository/webhooks_test.go               |  184 +-
 internal/repository/workflow_history.go            |  119 +-
 internal/repository/workflow_history_test.go       |  102 +-
 internal/repository/workflows.go                   |   22 +-
 internal/runcode/diagnostic.go                     |   72 +
 internal/runcode/diagnostic_internal_test.go       |   91 +
 internal/runcode/diagnostic_test.go                |   78 +
 internal/runcode/doc.go                            |   73 +-
 internal/runcode/errors.go                         |   25 +
 internal/runcode/inspect.go                        |   90 +
 internal/runcode/inspect_test.go                   |  120 +
 internal/runcode/persist.go                        |  465 ++
 internal/runcode/persist_test.go                   |  696 +++
 internal/runcode/runcode.go                        |  227 +-
 internal/runcode/runcode_test.go                   |   23 +-
 internal/runcode/sandbox.go                        |  334 ++
 internal/runcode/sandbox_test.go                   |  279 +
 internal/sidecarnode/catalogue.go                  |  115 +
 internal/sidecarnode/convert.go                    |  854 +++
 internal/sidecarnode/convert_test.go               |  554 ++
 internal/sidecarnode/load.go                       |  217 +
 internal/sidecarnode/load_test.go                  |  360 ++
 internal/skills/bundle_test.go                     |  273 +
 internal/skills/check.go                           |  584 ++
 internal/skills/check_test.go                      |  130 +
 internal/skills/index.go                           |   98 +
 internal/skills/skill.go                           |  368 ++
 .../clean/skills/kilasflow-fixture/SKILL.md        |   43 +
 .../skills/kilasflow-fixture/references/NOTES.md   |    3 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   34 +
 .../skills/kilasflow-fixture/SKILL.md              |   42 +
 .../skills/kilasflow-fixture/SKILL.md              |   36 +
 .../skills/kilasflow-fixture/SKILL.md              |   40 +
 .../skills/kilasflow-fixture/references/PRESENT.md |    3 +
 .../missing-key/skills/kilasflow-fixture/SKILL.md  |   32 +
 .../skills/kilasflow-fixture/SKILL.md              |   41 +
 .../skills/kilasflow-fixture/references/PRESENT.md |    3 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   34 +
 .../skills/kilasflow-fixture/SKILL.md              |   38 +
 .../skills/kilasflow-credentials/SKILL.md          |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   39 +
 .../skills/kilasflow-fixture/references/EXTRA.md   |    3 +
 .../unknown-key/skills/kilasflow-fixture/SKILL.md  |   34 +
 .../skills/kilasflow-fixture/SKILL.md              |   43 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 internal/tenantpurge/completeness_test.go          |  368 ++
 internal/tenantpurge/doc.go                        |  120 +
 internal/tenantpurge/docs_test.go                  |  115 +
 internal/tenantpurge/harness_test.go               |  614 ++
 internal/tenantpurge/purge.go                      |  412 ++
 internal/tenantpurge/purge_test.go                 |  507 ++
 internal/wasmpack/audit.go                         |  116 +
 internal/wasmpack/audit_test.go                    |  234 +
 internal/wasmpack/caps.go                          |  130 +
 internal/wasmpack/executor.go                      |  218 +
 internal/wasmpack/executor_test.go                 |  108 +
 internal/wasmpack/host.go                          |  480 ++
 internal/wasmpack/host_internal_test.go            |  161 +
 internal/wasmpack/hostcalls_binary.go              |  114 +
 internal/wasmpack/hostcalls_credentials.go         |   89 +
 internal/wasmpack/hostcalls_http.go                |  248 +
 internal/wasmpack/hostcalls_test.go                |  343 ++
 internal/wasmpack/legacy_test.go                   |   53 +
 internal/wasmpack/limits.go                        |   99 +
 internal/wasmpack/probe_test.go                    |  521 ++
 internal/wasmpack/registry.go                      |   89 +
 internal/wasmpack/registry_test.go                 |  116 +
 internal/wasmpack/spec.go                          |   90 +
 internal/wasmpack/support_test.go                  |  395 ++
 internal/wasmpack/testdata/probe/main.go           |  131 +
 internal/wasmtest/wasmtest.go                      |  359 ++
 internal/wasmtest/wasmtest_test.go                 |  191 +
 internal/webhook/require_auth.go                   |   74 +
 internal/webhook/require_auth_test.go              |  367 ++
 internal/webhook/route_label_test.go               |  172 +
 internal/webhook/shape.go                          |   10 +
 internal/webhook/webhook.go                        |   17 +-
 internal/webhook/webhook_test.go                   |    2 +
 internal/workflow/catalog_scope.go                 |   39 +
 internal/workflow/compiler.go                      |   24 +-
 internal/workflow/compiler_visibility_test.go      |  280 +
 internal/workflow/lifecycle.go                     |   21 +-
 .../000014_webhook_route_backfill.down.sql         |   14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |   62 +
 .../postgres/000015_idempotency_keys.down.sql      |    6 +
 migrations/postgres/000015_idempotency_keys.up.sql |   49 +
 .../000016_webhook_deliveries_tenant.down.sql      |   14 +
 .../000016_webhook_deliveries_tenant.up.sql        |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   14 +
 .../000017_datastore_columns_tenant.up.sql         |   39 +
 migrations/postgres/000018_api_key_scopes.down.sql |   10 +
 migrations/postgres/000018_api_key_scopes.up.sql   |   18 +
 migrations/postgres/000019_workflow_actor.down.sql |   21 +
 migrations/postgres/000019_workflow_actor.up.sql   |   55 +
 .../sqlite/000014_webhook_route_backfill.down.sql  |   14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |   58 +
 migrations/sqlite/000015_idempotency_keys.down.sql |    6 +
 migrations/sqlite/000015_idempotency_keys.up.sql   |   48 +
 .../000016_webhook_deliveries_tenant.down.sql      |   13 +
 .../sqlite/000016_webhook_deliveries_tenant.up.sql |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   12 +
 .../sqlite/000017_datastore_columns_tenant.up.sql  |   38 +
 migrations/sqlite/000018_api_key_scopes.down.sql   |   10 +
 migrations/sqlite/000018_api_key_scopes.up.sql     |   18 +
 migrations/sqlite/000019_workflow_actor.down.sql   |   21 +
 migrations/sqlite/000019_workflow_actor.up.sql     |   56 +
 nodes/code.go                                      |   26 +-
 nodes/code_test.go                                 |  131 +
 nodes/datastore.go                                 |   83 +-
 nodes/datastore_increment.go                       |   85 +
 nodes/datastore_test.go                            |  177 +
 nodes/executors.go                                 |   28 +-
 nodes/pgvector_test.go                             |   42 +-
 nodes/sidecar.go                                   |  383 ++
 nodes/sidecar_egress.go                            |  207 +
 nodes/sidecar_egress_internal_test.go              |  257 +
 nodes/sidecar_egress_test.go                       |  276 +
 nodes/sidecar_engine_test.go                       |  393 ++
 nodes/sidecar_test.go                              |  666 +++
 nodes/sql_options_live_test.go                     |   17 +-
 pkg/sdk/abi.go                                     |  298 +
 pkg/sdk/abi_test.go                                |  360 ++
 pkg/sdk/call.go                                    |  149 +
 pkg/sdk/capabilities.go                            |  169 +
 pkg/sdk/doc.go                                     |   83 +-
 pkg/sdk/example/fetch/main.go                      |   70 +
 pkg/sdk/guest_wasip1.go                            |   69 +
 pkg/sdk/host_other.go                              |  174 +
 pkg/sdk/host_wasip1.go                             |   41 +
 pkg/sdk/internal/abigen/abigen.go                  |   85 +
 pkg/sdk/internal/abigen/cmd/main.go                |   33 +
 pkg/sdk/sdk.go                                     |   55 +-
 pkg/sdk/sdk_test.go                                |   91 +
 scripts/check-coordinates.sh                       |   21 +
 scripts/generate-api-reference.mjs                 |   19 +-
 scripts/skills-command-reference/main.go           |  375 ++
 scripts/skills-command-reference/main_test.go      |  124 +
 scripts/skills-index/main.go                       |  144 +
 scripts/smoke-cli.sh                               |  228 +
 scripts/smoke-postgres.sh                          |   22 +
 sdk/CHANGELOG.md                                   |   33 +-
 sdk/LICENSE                                        |  202 +
 sdk/README.md                                      |  101 +-
 sdk/RELEASING.md                                   |  188 +
 sdk/examples/host-page/README.md                   |   64 +-
 sdk/examples/host-page/server.mjs                  |   68 +-
 sdk/examples/reference-host/server.mjs             |    8 +-
 sdk/examples/reference-host/tenant.html            |    3 +
 sdk/package.json                                   |   11 +-
 sdk/pnpm-lock.yaml                                 |    3 +
 sdk/scripts/check-example.mjs                      |  320 ++
 sdk/scripts/check-package.mjs                      |  315 ++
 sdk/scripts/lib/pack.mjs                           |   77 +
 sdk/scripts/lib/release.mjs                        |  266 +
 sdk/scripts/release.mjs                            |  149 +
 sdk/src/browser.ts                                 |   13 +-
 sdk/src/generated/models.ts                        |  308 +-
 sdk/src/http.ts                                    |   35 +-
 sdk/src/server.ts                                  |  161 +-
 sdk/test/browser.test.ts                           |   24 +
 sdk/test/operation-coverage.test.mjs               |   23 +
 sdk/test/operations.test.ts                        |   31 +
 sdk/test/release-workflow.test.mjs                 |  223 +
 sdk/test/release.test.mjs                          |  390 ++
 sdk/test/server.test.ts                            |  131 +-
 sidecar/discover.go                                |   71 +
 sidecar/doc.go                                     |   68 +-
 sidecar/fixture/crash.js                           |   19 +
 sidecar/fixture/dial.js                            |   32 +
 sidecar/fixture/env.js                             |   48 +
 sidecar/fixture/forged.js                          |   52 +
 sidecar/fixture/hang.js                            |   27 +
 sidecar/fixture/heap.js                            |   27 +
 sidecar/fixture/perm.js                            |   57 +
 sidecar/fixture/rss.js                             |   39 +
 sidecar/fixture_test.go                            |    9 +-
 sidecar/hostcall_test.go                           |  346 ++
 sidecar/hostile_test.go                            |  199 +
 sidecar/isolation_test.go                          |  845 +++
 sidecar/logwriter.go                               |   77 +
 sidecar/logwriter_test.go                          |  111 +
 sidecar/memory.go                                  |   51 +
 sidecar/memory_darwin.go                           |   30 +
 sidecar/memory_linux.go                            |   30 +
 sidecar/memory_other.go                            |   12 +
 sidecar/procattr_other.go                          |   30 +
 sidecar/procattr_unix.go                           |   50 +
 sidecar/process.go                                 |  347 ++
 sidecar/process_test.go                            |  238 +
 sidecar/protocol.go                                |  159 +-
 sidecar/runner.go                                  |  182 +
 sidecar/runner/runner.cjs                          |  966 ++++
 sidecar/runner_test.go                             | 1094 ++++
 sidecar/sidecar.go                                 |  654 ++-
 sidecar/sidecar_test.go                            |    2 +-
 sidecar/sidecartest/sidecartest.go                 |   96 +
 .../kf-fixture-badload/dist/nodes/Bad/Bad.node.js  |   19 +
 .../packages/kf-fixture-badload/package.json       |   12 +
 .../dist/credentials/Escape.credentials.js         |    1 +
 .../dist/nodes/Ok/Ok.node.js                       |   16 +
 .../packages/kf-fixture-badmanifest/package.json   |   16 +
 .../dist/nodes/Hostile/Hostile.node.js             |  139 +
 .../dist/nodes/Noisy/Noisy.node.js                 |   36 +
 .../packages/kf-fixture-hostile/package.json       |   13 +
 .../kf-fixture-netload/dist/nodes/Net/Net.node.js  |   19 +
 .../packages/kf-fixture-netload/package.json       |   12 +
 .../dist/credentials/FixtureApi.credentials.js     |   36 +
 .../dist/nodes/Greet/Greet.node.js                 |  154 +
 .../dist/nodes/Relay/Relay.node.js                 |   60 +
 .../packages/kf-fixture-nodes/package.json         |   17 +
 .../dist/nodes/Distinct/Distinct.node.js           |   56 +
 .../dist/nodes/Trigger/Trigger.node.js             |   30 +
 .../packages/kf-fixture-unsupported/package.json   |   13 +
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |   35 +
 .../dist/nodes/Trigger/Trigger.node.js             |   29 +
 .../dist/nodes/v2/FooV2.node.js                    |   33 +
 .../packages/kf-fixture-versions/package.json      |   14 +
 skills/index.json                                  |  515 ++
 skills/kilasflow-credentials/SKILL.md              |  114 +
 .../references/CREDENTIAL_TYPES.md                 |  119 +
 skills/kilasflow-datastore/SKILL.md                |  127 +
 skills/kilasflow-datastore/references/FILTERS.md   |  140 +
 skills/kilasflow-debugging/SKILL.md                |  120 +
 .../references/TRACE_READING.md                    |  135 +
 skills/kilasflow-embedding/SKILL.md                |  122 +
 .../references/EMBED_HANDSHAKE.md                  |  134 +
 .../references/SESSION_AUTHORITY.md                |   67 +
 skills/kilasflow-error-handling/SKILL.md           |   97 +
 skills/kilasflow-expressions/SKILL.md              |  112 +
 .../references/EXPRESSION_ROOTS.md                 |  161 +
 skills/kilasflow-import-export/SKILL.md            |   96 +
 .../references/MAPPING_LIMITS.md                   |  143 +
 skills/kilasflow-node-configuration/SKILL.md       |  105 +
 .../references/LOAD_OPTIONS.md                     |  141 +
 .../references/PROPERTY_KINDS.md                   |  165 +
 skills/kilasflow-node-packs/SKILL.md               |   99 +
 .../references/MODULE_AND_SIDECAR.md               |   95 +
 .../kilasflow-node-packs/references/PACK_FORMAT.md |  114 +
 skills/kilasflow-operations/SKILL.md               |  109 +
 .../references/UPGRADE_ORDER.md                    |  210 +
 skills/kilasflow-triggers/SKILL.md                 |  132 +
 .../references/WEBHOOK_DELIVERY.md                 |  148 +
 skills/kilasflow-workflow-lifecycle/SKILL.md       |  115 +
 .../references/NAMING_AND_DESCRIPTIONS.md          |   81 +
 .../references/VALIDATION_CHECKLIST.md             |   95 +
 skills/using-kilasflow-skills/SKILL.md             |  218 +
 web/messages/en/auth.json                          |   36 +
 web/messages/en/canvas.json                        |   52 +
 web/messages/en/common.json                        |   27 +
 web/messages/en/credentials.json                   |   40 +
 web/messages/en/datastores.json                    |  176 +
 web/messages/en/editor.json                        |  134 +
 web/messages/en/embed.json                         |   16 +
 web/messages/en/executions.json                    |   94 +
 web/messages/en/home.json                          |   20 +
 web/messages/en/nav.json                           |   10 +
 web/messages/en/properties.json                    |  118 +
 web/messages/en/schedules.json                     |   33 +
 web/messages/en/settings.json                      |   46 +
 web/messages/en/versions.json                      |   89 +
 web/messages/en/workflows.json                     |  223 +
 web/messages/id/auth.json                          |   36 +
 web/messages/id/canvas.json                        |   52 +
 web/messages/id/common.json                        |   27 +
 web/messages/id/credentials.json                   |   40 +
 web/messages/id/datastores.json                    |  209 +
 web/messages/id/editor.json                        |  164 +
 web/messages/id/embed.json                         |   16 +
 web/messages/id/executions.json                    |   94 +
 web/messages/id/home.json                          |   20 +
 web/messages/id/nav.json                           |   10 +
 web/messages/id/properties.json                    |  123 +
 web/messages/id/schedules.json                     |   33 +
 web/messages/id/settings.json                      |   46 +
 web/messages/id/versions.json                      |   89 +
 web/messages/id/workflows.json                     |  222 +
 web/package.json                                   |    7 +-
 web/pnpm-lock.yaml                                 |  204 +
 web/project.inlang/settings.json                   |   25 +
 web/src/lib/api/generated/admin/admin.ts           |   94 +
 .../api/generated/datastore-rows/datastore-rows.ts |  110 +-
 web/src/lib/api/generated/models/aPIKeyResource.ts |    8 +
 web/src/lib/api/generated/models/binaryRemoval.ts  |   16 +
 .../api/generated/models/createAPIKeyInputBody.ts  |   10 +
 .../api/generated/models/createdAPIKeyResource.ts  |    7 +
 .../api/generated/models/deleteRowsInputBody.ts    |    2 +
 .../api/generated/models/incrementRowsInputBody.ts |   22 +
 .../generated/models/incrementRowsOutputBody.ts    |   16 +
 .../models/incrementRowsOutputBodyRowsItem.ts      |    9 +
 web/src/lib/api/generated/models/index.ts          |   11 +
 .../lib/api/generated/models/notReadyProblem.ts    |   31 +
 .../lib/api/generated/models/principalResource.ts  |    9 +
 .../lib/api/generated/models/readyDatastores.ts    |   19 +
 .../api/generated/models/readyDatastoresSpread.ts  |   12 +
 .../lib/api/generated/models/readyOutputBody.ts    |    3 +
 .../api/generated/models/tenantDeletionResource.ts |   24 +
 .../models/tenantDeletionResourceRemoved.ts        |   12 +
 .../api/generated/models/updateRowsInputBody.ts    |    2 +
 .../models/workflowPublishEventResource.ts         |    7 +
 .../workflowPublishEventResourceActorKind.ts       |   18 +
 .../models/workflowVersionSummaryResource.ts       |   12 +
 .../workflowVersionSummaryResourceActorKind.ts     |   18 +
 web/src/lib/api/generated/nodes/nodes.ts           |    8 +-
 web/src/lib/api/generated/system/system.ts         |   18 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/http.ts                            |    4 +-
 .../lib/components/dashboard/dashboard-nav.svelte  |   56 +-
 .../lib/components/dashboard/list-states.svelte    |   11 +-
 .../components/dashboard/locale-switcher.svelte    |   31 +
 .../lib/components/ui/dialog/dialog-content.svelte |    3 +-
 .../lib/components/ui/dialog/dialog-footer.svelte  |    3 +-
 .../lib/components/ui/sheet/sheet-content.svelte   |    3 +-
 .../workflow-editor/activation-notices.svelte      |   15 +-
 .../components/workflow-editor/canvas-edge.svelte  |    5 +-
 .../components/workflow-editor/canvas-node.svelte  |   27 +-
 .../workflow-editor/editor-controls.svelte         |    6 +-
 .../workflow-editor/execution-canvas-node.svelte   |    9 +-
 .../workflow-editor/execution-canvas.svelte        |   30 +-
 .../components/workflow-editor/node-picker.svelte  |   15 +-
 .../workflow-editor/properties-panel.svelte        |   47 +-
 .../workflow-editor/property-field.svelte          |  228 +-
 .../workflow-editor/property-field.test.ts         |   81 +
 .../workflow-editor/version-panel.svelte           |  107 +-
 .../workflow-editor/workflow-editor.svelte         |  157 +-
 web/src/lib/dashboard/cursor-page.test.ts          |  285 +-
 web/src/lib/dashboard/cursor-page.ts               |   96 +-
 web/src/lib/dashboard/execution-list.test.ts       |  148 +-
 web/src/lib/dashboard/execution-list.ts            |   98 +-
 web/src/lib/dashboard/nav-sections.test.ts         |   55 +-
 web/src/lib/dashboard/nav-sections.ts              |   59 +
 web/src/lib/dashboard/workflow-list.test.ts        |   20 +-
 web/src/lib/dashboard/workflow-list.ts             |   24 +-
 web/src/lib/datastore/columns.test.ts              |   41 +
 web/src/lib/datastore/columns.ts                   |   39 +-
 web/src/lib/datastore/transfer.ts                  |   15 +-
 web/src/lib/embed/embed-editor.svelte              |   37 +-
 web/src/lib/embed/session.svelte.ts                |   50 +-
 web/src/lib/embed/session.test.ts                  |   20 +-
 web/src/lib/i18n/catalog.test.ts                   |   75 +
 web/src/lib/i18n/copy.test.ts                      |  399 ++
 web/src/lib/i18n/locale.svelte.ts                  |  107 +
 web/src/lib/i18n/locale.test.ts                    |   90 +
 web/src/lib/workflow-editor/activation.ts          |    3 +-
 web/src/lib/workflow-editor/document.ts            |    3 +-
 web/src/lib/workflow-editor/execution.test.ts      |   48 +-
 web/src/lib/workflow-editor/execution.ts           |   55 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   13 +-
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 10556 -> 10731 bytes
 web/src/lib/workflow-editor/import-diagnostics.ts  |    3 +-
 web/src/lib/workflow-editor/ports.ts               |    9 +-
 web/src/lib/workflow-editor/shortcuts.ts           |   53 +-
 .../lib/workflow-editor/version-history.test.ts    |   65 +-
 web/src/lib/workflow-editor/version-history.ts     |   82 +-
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  162 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   53 +-
 .../app/workflows/[id]/export-dialog.svelte        |   31 +-
 .../app/workflows/diagnostics-section.svelte       |   50 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   39 +-
 .../app/workflows/import-report-drawer.svelte      |    9 +-
 .../(dashboard)/app/workflows/import-report.svelte |   30 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   71 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |   56 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  169 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  212 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  108 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   67 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  119 +-
 web/src/routes/+layout.svelte                      |   11 +
 web/src/routes/+page.svelte                        |   42 +-
 web/src/routes/approve/[token]/+page.svelte        |   51 +-
 web/src/routes/embed/[id]/+page.svelte             |   13 +-
 web/src/routes/login/+page.svelte                  |   21 +-
 web/vite.config.ts                                 |   21 +-
 667 files changed, 117270 insertions(+), 5281 deletions(-)
```
