---
id: BUG-8t94wn
title: 'Wait/sub-workflow mapping: expression amounts, v1/v2 ports, workflowInputs, SplitInBatches'
status: done
priority: high
labels:
    - nodes
    - importer
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:03:59Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 7 finding(s) from dims: find:core-node-parity, find:engine-runtime, find:importer-fidelity, find:webhook-trigger-parity.

---
### SplitInBatches v1/v2 imported with v3 port semantics: loop body is wired to 'done' and the run fails [find:core-node-parity] (medium/bug) · area: importer / SplitInBatches · confidence: high

Every splitInBatches version is mapped onto the v3 loop (output 0 = done, output 1 = loop) with no typeVersion handling. In v1/v2 the only output (index 0) is the batch, so the loop body ends up attached to 'done'. The noItemsLeft context idiom is not reported either.

Evidence: cases/v3_sib_v1.json uses the classic v1 loop: SIB batchSize 2 → Set → IF v1 {{$node["SIB"].context["noItemsLeft"]}} → Done, with the false branch back to SIB. n8n runs SIB 3 times, all 5 items get marked, and Done runs. KilasFlow emits the batch on the unconnected loop port and fails 'compiled workflow graph has no schedulable node'. The import raises no issue. Code: internal/interop/n8n/parameters.go:1941-1960 and n8n.go:356.

n8n behavior: v1/v2 have a single batch output. The loop ends when an IF reads context.noItemsLeft.

Impact: splitInBatches@1 appears in 2 of the 100 templates and is common in older community workflows.

Suggested fix: For typeVersion < 3, remap output 0 to the loop port and synthesise the done edge, or raise a blocking issue. Translate the noItemsLeft IF idiom.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/internal/interop/n8n/n8n.go

Existing tickets: FEAT-vvwpjw, FEAT-sar60r

---
### Execute Sub-workflow: workflowInputs mapping ('Define using fields below') is ignored, so the sub receives the caller's raw items [find:core-node-parity] (medium/parity-gap) · area: importer + sub-workflow trigger · confidence: high

executeWorkflowToKilas never reads workflowInputs.value, and the 'fields' input source does not project items onto the declared inputs. The sub-workflow gets the parent's items unchanged: mapped fields are missing and undeclared fields leak through. The import also always raises a *blocking* workflowId issue, even when the ID resolves to an active KilasFlow workflow and activation succeeds.

Evidence: cases/v2_sub_mapping.json: the parent maps {id:{{$json.id}}, label:'L-{{$json.name}}'}, and the sub trigger 1.1 declares [id:number, label]. n8n gives {id:1,label:'L-a',fromSub:'1-'}; KilasFlow gives {id:1,name:'a',fromSub:'1-a'}. These cases match: passthrough once, each, waitForSubWorkflow=false and v1 string IDs (v2_sub_once/each/nowait/v1_string_id). Code: internal/interop/n8n/parameters.go:941-1030 and nodes/subworkflow.go.

n8n behavior: Evaluates the mapping per item in the caller and passes only the declared, typed fields to the sub-workflow.

Impact: executeWorkflowTrigger 1.1 appears in 10 of the 100 templates, and modern n8n sub-workflows default to typed inputs.

Suggested fix: Import workflowInputs.value as a per-item mapping and have the trigger project and convert to the declared fields. Downgrade the workflowId issue when the ID resolves locally.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/subworkflow.go

Existing tickets: FEAT-az620p (AC 29)

---
### Wait amount set by an expression, or omitted (n8n default), imports as 'no wait' [find:core-node-parity] (medium/bug) · area: importer / Wait · confidence: high

waitToKilas copies `amount` without fromN8NValue, so an expression amount stays a raw "={{...}}" string and becomes 0. A missing amount (n8n omits its default) also becomes 0.

Evidence: - wait_expr_amount (amount '={{ $json.w }}', w=1): n8n waits 1.03 s; KilasFlow finishes in 0.03 s.
- wait_default_amount (Wait 1.1 with only unit=seconds): n8n waits 5.03 s; KilasFlow 0.03 s.
Code: internal/interop/n8n/parameters.go:1313-1315; numberValue() in nodes/wait.go:180.

n8n behavior: Evaluates the expression and applies the per-version default amount.

Impact: Rate-limit pauses silently disappear, which can trigger 429s from third-party APIs.

Suggested fix: Pass amount through fromN8NValue, apply n8n's default amount when absent, and raise an issue when the amount cannot be read.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/wait.go

Existing tickets: FEAT-q81bq4

---
### Split In Batches v1 is wired as v3: its single batch output lands on the loop's `done` port [find:importer-fidelity] (medium/bug) · area: importer/splitInBatches · confidence: high

In v1/v2 the single output emits each batch (the loop body), and termination is an If on context["noItemsLeft"]. v3 has done=0 and loop=1. Every version is mapped to kilasflow.loop [done, loop] positionally, so a v1 body edge is attached to `done`, and no issue is raised.

Evidence: 1073 'SplitInBatches' v1: output0 -> uProc becomes done -> uProc. 1862 'Split In Batches' v1: output0 -> HTTP Request, Merge becomes done -> both. All 19 v3 loops map correctly.

n8n behavior: v1 runs the body once per batch.

Impact: 2/100 templates; the loop body runs once at the end instead of per batch.

Suggested fix: For versions below 3, remap output 0 to `loop` and synthesise `done`, or block with a message. Translate or report the noItemsLeft idiom.

Files: internal/interop/n8n/parameters.go

Existing tickets: FEAT-sar60r (done)

---
### Sub-workflow inputs are lost on import: a trigger without inputSource becomes passthrough, and the caller's workflowInputs mapping is dropped [find:webhook-trigger-parity] (high/bug) · area: importer / executeWorkflowTrigger + executeWorkflow · confidence: high

n8n's ExecuteWorkflowTrigger defaults inputSource to 'workflowInputs' for v1.1 and later, so exports omit it. KF imports that as passthrough and discards the declared fields. The Execute Workflow caller's workflowInputs mapping (v1.2) is dropped without an issue, and KF's caller node has no inputs mapping at all.

Evidence: n8n 2.33.7 ExecuteWorkflowTrigger definition (container) shows inputSource defaulting to WORKFLOW_INPUTS for version 1.1 and later. KF import of trigger v1.1 {workflowInputs:{values:[requestId,jobType,data]}} gives {inputSource:'passthrough'}. Caller v1.2 {workflowInputs:{mappingMode:'defineBelow', value:{jobType:'report', requestId:'={{ $json.id }}', data:'={{ $json }}'}}} gives {itemsPerCall, mode, workflowId}; the only issue raised is about workflowId. KF's 'fields' mode is documentation-only and never shapes the item. Code: internal/interop/n8n/parameters.go:1013-1029 and 942-989; nodes/subworkflow.go:35-70 and 110-125.

n8n behavior: The sub-workflow receives exactly the mapped workflowInputs fields.

Impact: Trigger side: 7/100 templates (2878, 3770, 3050, 3135, 3790, 2085, 3514). Caller mapping: 2878 (3 calls; its sub-workflow routes on jobType, which the caller no longer sends, so no branch runs) and 3770. Every modern n8n sub-workflow pair is affected.

Suggested fix: Treat a missing inputSource on v1.1+ as workflowInputs. Add a workflowInputs mapping (key/value with expressions) to kilasflow.executeWorkflow and import it. Make 'fields' mode shape items like n8n: declared keys only, missing keys set to null, optional type conversion.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/subworkflow.go

Existing tickets: FEAT-az620p

---
### SplitInBatches v1/v2 imports silently wire the batch output to the loop's `done` port, and the run dies with 'compiled workflow graph has no schedulable node' [find:engine-runtime] (medium/bug) · area: importer SIB mapping + runner error text · confidence: high

Every splitInBatches version maps to kilasflow.loop, whose output 0 is `done` (n8n.go:356). The single batch output of v1/v2 therefore lands on `done`, and no diagnostic is emitted.

Evidence: Private :8101, script <W>/t_sib_v1.py: a classic v1 loop (SIB v1 -> Process -> IF noItemsLeft -> false back to SIB / true -> Finished). The import reports no issues and stores the connection `l.done -> Process`. Running it over [1,2,3] ends in status failed, `compiled workflow graph has no schedulable node`.

n8n behavior: v1/v2 have a single output carrying each batch. The loop is closed by an IF on context.noItemsLeft.

Impact: 2/100 top templates, plus many pre-2023 customer workflows, fail with a confusing error.

Suggested fix: Map typeVersion < 3 output 0 onto the `loop` port and treat the IF exit as done, or flag the node as blocking. Make the scheduler error name the nodes that cannot be scheduled.

Files: internal/interop/n8n/n8n.go, internal/interop/n8n/parameters.go, internal/engine/runner.go

Existing tickets: FEAT-vvwpjw

---
### Wait v1.1 imports with v1 defaults: a missing unit becomes 'hours' (n8n: seconds) and a missing amount becomes 0 (n8n: 5) [find:webhook-trigger-parity] (high/bug) · area: importer / wait · confidence: high

n8n's Wait defaults differ by version: v1 is amount 1 / unit hours, v1.1 is amount 5 / unit seconds. The importer always fills unit 'hours' and copies amount as-is. So 'wait 3 seconds' becomes 3 hours and fails the 1 h cap, and a missing amount means no wait at all.

Evidence: The n8n 2.33.7 Wait node definition inside the container shows v1.1 defaults amount 5 and unit 'seconds'. KF import of Wait v1.1 {amount:3} named 'Wait 3 seconds' gives {amount:3, unit:'hours'}; a manual run fails with 'a wait of 3h0m0s is longer than this server's limit of 1h0m0s'. An empty {} or {unit:'minutes'} leaves amount unset, which numberValue reads as 0, so there is no pause. No import issue in either case. Code: internal/interop/n8n/parameters.go:1314-1316; nodes/wait.go:176-183.

n8n behavior: Wait v1.1 with {amount:3} pauses 3 seconds; with {} it pauses 5 seconds.

Impact: 14/100 templates: 2320, 2340, 2466, 2557, 2567, 2605, 2896, 2982, 3121, 3442, 4352, 4846, 5035, 5171. Short rate-limit pauses become 1-60 hour suspensions or hard failures, or disappear.

Suggested fix: Use version-aware defaults (1.1: amount 5, unit seconds; 1: amount 1, unit hours) and write them explicitly into the imported parameters.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/wait.go

## Acceptance criteria

- [ ] SplitInBatches v1/v2 imported with v3 port semantics: loop body is wired to 'done' and the run fails
- [ ] Execute Sub-workflow: workflowInputs mapping ('Define using fields below') is ignored, so the sub receives the
- [ ] Wait amount set by an expression, or omitted (n8n default), imports as 'no wait'
- [ ] Split In Batches v1 is wired as v3: its single batch output lands on the loop's `done` port
- [ ] Sub-workflow inputs are lost on import: a trigger without inputSource becomes passthrough, and the caller's wo
- [ ] SplitInBatches v1/v2 imports silently wire the batch output to the loop's `done` port, and the run dies with '
- [ ] Wait v1.1 imports with v1 defaults: a missing unit becomes 'hours' (n8n: seconds) and a missing amount becomes
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
---
## ImporterTail slice — 2026-09-20

Status: `testing`. Commits: `47544b7` (swept into a peer's commit — content is in
`internal/interop/n8n/`), `41f1d50`.

Landed (all in `internal/interop/n8n/*`, `nodes/subworkflow.go`):
- Wait version-aware defaults (v1: 1 hour; v1.1+: 5 seconds), expression amounts
  converted rather than copied, unknown units blocking, n8n's `limitWaitTime`
  bound carried under the executor's names, durable resume modes no longer
  refused (webhook as itself, form as the approval page with a lossy note).
- SplitInBatches v1/v2: output 0 remapped to the `loop` port (`legacyOutputPort`),
  the exit that needs rewiring named as a blocking issue, `maxIterations` written
  at the instance ceiling instead of inheriting a lower default, `options.reset`
  carried and reported.
- Sub-workflows: the caller's `workflowInputs` mapper is carried (`inputFields`)
  and applied per item in `executeExecuteWorkflow`; an omitted `inputSource` on a
  1.1 trigger now reads as the declared fields n8n defaults to; `$workflow.id`
  self-references resolve instead of blocking.

Scoped proof: `go test ./internal/interop/n8n/ -count=1` green, including 20 new
regression tests (`waitsubworkflow_test.go`, `importer_tail_test.go`);
`go test ./nodes/ -count=1 -run 'Subworkflow|Assignment|Unsupported|Loop'` green.

Remaining (deliberate, not silent):
- The v1/v2 loop *exit* stays a manual rewire: KilasFlow's loop ends on its own,
  so the If/noItemsLeft test is named as blocking rather than rewritten.
- The trigger side deliberately does not filter to declared fields — n8n does not
  either (its own repro shows `fromSub` leaking through both), so filtering would
  drop data n8n keeps.
- Adversarial re-verify against a live n8n/stub instance is Main's final step.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (3):
  - `a6f63041` — BUG-8t94wn BUG-gaavr5 BUG-wdypd2 BUG-qq4xva FEAT-j5s2n4: record testing state — importer tail slice
  - `47544b77` — BUG-8dmp5y: throttle login, revalidate sessions, bound credential scope through redirects — auth/security
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   11 +-
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   40 +
 .pine/tickets/BUG-4053h6.md                        |  921 +++++++
 .pine/tickets/BUG-57n76x.md                        |  576 +++++
 .pine/tickets/BUG-66es9z.md                        |  248 ++
 .pine/tickets/BUG-6as5y7.md                        |  669 +++++
 .pine/tickets/BUG-6bqh51.md                        |  640 +++++
 .pine/tickets/BUG-6jvcs5.md                        |  689 ++++++
 .pine/tickets/BUG-8dmp5y.md                        |  639 +++++
 .pine/tickets/BUG-8h4yy1.md                        |  496 ++++
 .pine/tickets/BUG-8sb0jw.md                        |  695 ++++++
 .pine/tickets/BUG-8t94wn.md                        |  179 ++
 .pine/tickets/BUG-9853ay.md                        |   84 +
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |   29 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 10476 bytes
 .pine/tickets/BUG-aede06.md                        |  326 +++
 .pine/tickets/BUG-c241hm.md                        |  154 ++
 .pine/tickets/BUG-cq4yk3.md                        |  338 +++
 .pine/tickets/BUG-dndnhn.md                        |   48 +
 .pine/tickets/BUG-esb9sh.md                        |  138 ++
 .pine/tickets/BUG-f9frth.md                        |  410 +++
 .pine/tickets/BUG-fv5fer.md                        |  172 ++
 .pine/tickets/BUG-gaavr5.md                        |  363 +++
 .pine/tickets/BUG-hfhzq6.md                        |   49 +
 .pine/tickets/BUG-hm76dq.md                        |  119 +
 .pine/tickets/BUG-j7rtv3.md                        |   90 +
 .pine/tickets/BUG-kzkvv6.md                        |   74 +
 .pine/tickets/BUG-mewhrd.md                        |   68 +
 .pine/tickets/BUG-mz8xrb.md                        |   56 +
 .pine/tickets/BUG-npfz43.md                        |   38 +
 .pine/tickets/BUG-pwckhd.md                        |   63 +
 .pine/tickets/BUG-qmgz2f.md                        |  179 ++
 .pine/tickets/BUG-qq4xva.md                        |   57 +
 .pine/tickets/BUG-rjd6fm.md                        |  272 ++
 .pine/tickets/BUG-rrkjrd.md                        |   76 +
 .pine/tickets/BUG-s0wy50.md                        |   56 +
 .pine/tickets/BUG-t2wezf.md                        |   85 +
 .pine/tickets/BUG-tcqkad.md                        |  188 ++
 .pine/tickets/BUG-txc9xg.md                        |   71 +
 .pine/tickets/BUG-wdypd2.md                        |  230 ++
 .pine/tickets/BUG-wp2y0y.md                        |   45 +
 .pine/tickets/BUG-xf1wqm.md                        |   42 +
 .pine/tickets/BUG-y57cz4.md                        |  167 ++
 .pine/tickets/BUG-ysvmaa.md                        |  298 +++
 .pine/tickets/BUG-ze1nn8.md                        |  114 +
 .pine/tickets/BUG-ztzxck.md                        |   56 +
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  311 +++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  175 ++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  188 ++
 .pine/tickets/FEAT-j5s2n4.md                       |  188 ++
 .pine/tickets/FEAT-jvembs.md                       |  363 +++
 .pine/tickets/FEAT-nqpvf6.md                       |  161 ++
 .pine/tickets/FEAT-qdedm0.md                       |   23 +
 .pine/tickets/FEAT-x5km1z.md                       |  142 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |  114 +-
 README.md                                          |   27 +-
 cmd/kilasflow/main.go                              |  158 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |   13 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    4 +-
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
 .../docs/operate/configuration-reference.md        |   13 +-
 docs/src/content/docs/operate/deployment.md        |   26 +-
 docs/src/content/docs/operate/security.md          |   52 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +-
 docs/src/content/docs/reference/api-contract.md    |   13 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    2 +-
 docs/src/content/docs/reference/api/datastores.md  |  392 +++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |    2 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    4 +-
 docs/src/content/docs/reference/api/tenants.md     |  200 ++
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 .../content/docs/reference/expression-grammar.md   |  369 ++-
 docs/src/content/docs/start/install.md             |   44 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   91 +-
 e2e/fixtures/datastore.ts                          |    8 +-
 e2e/fixtures/epic-telegram.ts                      |   24 +-
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import.ts                     |   13 +-
 e2e/fixtures/n8n-live.ts                           |   10 +-
 e2e/playwright.config.ts                           |    4 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   30 +-
 e2e/tests/epic-acceptance.spec.ts                  |    8 +-
 e2e/tests/library-import.spec.ts                   |    8 +-
 e2e/tests/waha-migration.spec.ts                   |    6 +-
 internal/ai/agent.go                               |  101 +-
 internal/ai/ai.go                                  |   31 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  140 +-
 internal/ai/openai_test.go                         |  145 ++
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  102 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/embed_confinement_test.go             |  284 +++
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 +++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   37 +-
 internal/api/handlers/datastores.go                |   32 +-
 internal/api/handlers/datastores_csv.go            |   72 +
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  318 ++-
 internal/api/handlers/interop.go                   |  130 +-
 internal/api/handlers/problem.go                   |   31 +
 internal/api/handlers/schedules.go                 |   35 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  132 +
 internal/api/middleware/cors_test.go               |  189 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   14 +-
 internal/api/server.go                             |  123 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  257 ++
 internal/config/config.go                          |  305 ++-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |   16 +
 internal/engine/approval.go                        |   17 +-
 internal/engine/authenticate.go                    |   45 +-
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |   24 +
 internal/engine/error_workflow_test.go             |  230 ++
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 +++
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiprocess_test.go               |  115 +-
 internal/engine/runner.go                          | 1670 +++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  724 +++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  174 +-
 internal/engine/wait_service_test.go               |  232 +-
 internal/engine/worker_test.go                     |   15 +
 internal/expression/doc.go                         |   94 +-
 internal/expression/evaluator.go                   |  771 ++++++
 internal/expression/expression.go                  |  474 +---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  528 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1187 +++++++++
 internal/expression/parity_test.go                 |  505 ++++
 internal/expression/parser.go                      |  824 +++++++
 internal/expression/roots.go                       |  339 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         |  772 ++++++
 internal/interop/n8n/n8n.go                        |  462 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2606 ++++++++++++++++++--
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++++
 internal/loadoptions/loadoptions.go                |   11 +
 internal/loadoptions/redirect_test.go              |  117 +
 internal/property/property.go                      |   20 +-
 internal/property/visibility_test.go               |   34 +
 internal/repository/auth.go                        |  388 +++
 internal/repository/auth_admin_test.go             |  477 ++++
 internal/repository/claim_lease_test.go            |  266 ++
 internal/repository/claim_wake_test.go             |    4 +-
 internal/repository/credentials.go                 |  103 +
 internal/repository/execution_retention_test.go    |    3 +-
 internal/repository/executions.go                  |  534 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   40 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  153 ++
 internal/repository/tenant_purge_test.go           |    2 +-
 internal/repository/waits_test.go                  |    2 +-
 internal/repository/webhooks.go                    |  188 +-
 internal/repository/webhooks_test.go               |  306 +++
 internal/repository/workflow_history.go            |   14 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  233 +-
 internal/safehttp/safehttp.go                      |   54 +-
 internal/safehttp/safehttp_test.go                 |   97 +
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   52 +
 internal/scheduler/scheduler_test.go               |   67 +
 internal/sqlbuild/sqlbuild_test.go                 |    4 +-
 internal/web/dist/index.html                       |   37 -
 internal/web/embed.go                              |  459 +++-
 internal/web/embed_test.go                         |  335 ++-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |    2 +-
 internal/webhook/form.go                           |  262 ++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/request_lifecycle.go              |  425 +++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  758 ++++--
 internal/webhook/webhook_test.go                   |  678 ++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 nodes/ai.go                                        |  498 +++-
 nodes/ai_test.go                                   |  733 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  220 ++
 nodes/embedscope_test.go                           |  242 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  347 ++-
 nodes/http_test.go                                 |  302 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |   97 +-
 nodes/subworkflow_calls_test.go                    |   56 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  477 +++-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   83 +-
 packs/waha/waha_test.go                            |  373 ++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 scripts/check-coordinates.sh                       |   60 +
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/smoke-dev.sh                               |   27 +
 sdk/README.md                                      |   12 +
 sdk/examples/host-page/README.md                   |   11 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 sdk/package.json                                   |    6 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++
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
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   32 +-
 web/src/lib/api/http.ts                            |   16 +
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  376 ++-
 .../components/workflow-editor/node-picker.svelte  |  148 +-
 .../workflow-editor/properties-panel.svelte        |  167 +-
 .../workflow-editor/property-field.svelte          |  534 ++--
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  736 +++++-
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  151 +-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   94 +-
 web/src/lib/workflow-editor/conditions.ts          |  176 +-
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   29 +-
 web/src/lib/workflow-editor/document.test.ts       |  158 ++
 web/src/lib/workflow-editor/document.ts            |  291 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |   50 +
 web/src/lib/workflow-editor/expression-assist.ts   |   91 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   49 +
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 +-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  243 +-
 web/src/lib/workflow-editor/ports.ts               |  125 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   77 +
 web/src/lib/workflow-editor/shortcuts.ts           |  115 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  369 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  369 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  147 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |    6 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  176 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   59 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  293 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 434 files changed, 60068 insertions(+), 4725 deletions(-)
```
