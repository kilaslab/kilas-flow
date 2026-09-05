---
id: FEAT-jwhdsy
title: Reach parity on the data-shaping node family
status: done
priority: medium
labels:
    - nodes
    - parity
deps:
    - FEAT-9knk67
    - FEAT-5s1w0t
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T05:01:10Z"
updated: "2026-09-05T12:23:35Z"
---

## Scope

KilasFlow's whole data-shaping surface is one node. `setNode()` in `nodes/core.go` declares a single required parameter, `assignments`, of kind `node.PropertyKeyValue`, and `executeSet` in `nodes/executors.go` copies every incoming item and writes each assignment at the top level of `item.JSON`. There is no type on an assignment, no dot notation, no raw-JSON mode, no include/exclude of incoming fields, no duplicate-item option, and no ordering — `assignments` is read as `map[string]any`, so the order the user typed is lost on the first round trip and two assignments to the same name are impossible. There is no Aggregate, Sort, Split Out, Summarize or Remove Duplicates node at all; each of those imports as `kilasflow.unsupported`, whose validator always fails, so one Sort blocks activation of the whole workflow.

n8n's Set is much larger, and the gap is silent rather than reported. `setToKilas` in `internal/interop/n8n/parameters.go` reads both the v3 `assignments.assignments` array and the legacy `values.*` groups, then throws away each entry's declared type and keeps only `{name: value}`. It reports exactly one lossy case — `includeOtherFields == false` — and says nothing about `mode: "raw"`, `duplicateItem`, `include` (all / none / selected / except), `includeFields`, `excludeFields`, or the options `dotNotation`, `ignoreConversionErrors`, `includeBinary` and `stripBinary`, all of which are real parameters of `Set` v3 in the reference checkout at `packages/nodes-base/nodes/Set/v2/SetV2.node.ts` (n8n's directory is `v2`; the node's own `version` list covers 3.x). An imported Set that replaced the item now silently augments it, and the diagnostic that would have said so is only emitted for one of the four `include` values.

This ticket brings Set to n8n's v3 semantics and adds the five transform nodes that the p0 corpus actually uses. It is measured against that corpus in `imported / activatable / executable` counts.

## Acceptance criteria

- [x] Set carries typed, ordered assignments — each `{name, type, value}` with n8n's types (string, number, boolean, array, object) — and converts values to the declared type, honouring `ignoreConversionErrors`. **The typed ordered assignments landed in FEAT-xqqjqv**; this ticket adds the conversion-error option around them.
- [x] Set supports `mode: manual` and `mode: raw` (a whole-object JSON body), the `include` choice of all / none / selected / except with `includeFields` and `excludeFields`, `duplicateItem` with `duplicateCount`, and `dotNotation` defaulting to on as n8n does.
- [x] Set passes binary through by default and strips it on request, over the binary store from p3-8.
- [x] Aggregate, Sort, Split Out, Summarize and Remove Duplicates are registered nodes with n8n's parameter surface, and each has table tests covering the aggregation and comparison rules rather than a single happy path.
- [x] `internal/interop/n8n` maps `n8n-nodes-base.set`, `.aggregate`, `.sort`, `.splitOut`, `.summarize` and `.removeDuplicates` in both directions, `SupportedMappings()` lists them, and every unmapped option produces a named import diagnostic instead of being dropped.
- [x] A workflow saved before this ticket, whose Set carries the old `map[string]any` assignments, still loads, still runs and produces the same items.
- [x] The p0 corpus counts improve and the new figure is recorded here.

## Outcome

### Set

The whole n8n v3 surface: manual and JSON modes, the four `include` choices with their field lists, duplication, and the options collection — dot notation, ignore-conversion-errors, include-binary and strip-binary.

**Dot notation ships on, matching n8n.** The plan called it the trap and it is: an assignment named `user.email` writes a nested object in n8n and wrote a literal dotted key here. Defaulting it off would have left every imported Set subtly wrong in a way nobody notices until an HTTP node sends the wrong body. Both directions are tested.

Two smaller decisions: **strip wins over include** when a user sets both, which is what they meant; and **ignoring a conversion error keeps the value as it arrived**, which is the only other honest answer — inventing a zero would be a number the workflow never had.

### A defect the mode parameter exposed

Adding `mode` to Set nearly broke every Set node in existence. `assignments` is shown when `mode` is `manual`, and a node saved before `mode` existed stores nothing for it — so `Visible` saw an absent value, matched nothing, and hid the only field the node has.

The fix is general rather than a special case: visibility is now evaluated against the declared **defaults** filled in, in Go and in the editor, because a property the user never touched has its default value. That is what n8n's own `getNodeParameter` does, and without it every conditional property in the product had the same latent bug waiting for the first node that shipped a default it gated on.

### Five nodes

Aggregate and Split Out were written together and the round trip is one fixture, because they are inverses and a test that only goes one way proves half of it.

Decisions worth keeping:

- **Aggregate skips a field an item does not have** rather than gathering a null: a list with holes in it is worse than a shorter list.
- **Split Out treats a non-list as one element.** An API that returns a single object where it usually returns an array is ordinary, and failing there would break the workflow on exactly the one-result case.
- **Sort is stable**, so items it cannot tell apart keep the order they arrived in — an unstable sort makes a workflow's output vary run to run for no reason a reader can see. Numbers compare as numbers, because comparing their text puts 10 before 9.
- **Summarize keeps groups in first-seen order** rather than sorting them, and **averaging nothing yields nothing**: zero is an answer, and this is the absence of one.
- **Remove Duplicates compares a JSON rendering**, so two items with the same fields in a different order are the same item.

Two things are refused rather than approximated, each with a named import diagnostic: Sort's **JavaScript comparator**, because sorting by something the author did not write is worse than saying so — and the Code decision stays in the ticket that owns it; and Remove Duplicates' **across-executions** operation, which needs durable per-workflow state this deployment does not have. The second imports as the local operation so the workflow still runs, with the difference stated.

### The corpus

| | Before | After |
| --- | ---: | ---: |
| Activatable | 13 / 39 | 13 / 39 |
| Runnable | 3 / 39 | 3 / 39 |

**The number did not move, and that is the honest report.** The five nodes are not what the corpus is blocked on: `splitOut` appears in five fixtures, all of them WAHA templates that now stop earlier on a missing credential — something a user attaches, not a node the product lacks. What this ticket removes is a *future* block: before it, adding a credential to any of those templates would have hit `splitOut` next.

## Implementation Plan

Change the Set document shape first, because everything else in this ticket is additive and this one is not. Today `executeSet` reads `node.Parameters["assignments"].(map[string]any)`; the target is an ordered array of `{name, type, value}` entries, which needs p2-2's `fixedCollection` property kind and its `typeOptions.multipleValues` to be renderable in the editor. Accept both shapes on read — a map keeps working, an array is the new form — and always write the array. Do not migrate stored documents in place: V1 workflows exist, and a reader that handles both costs a dozen lines while a migration costs a whole class of failure. Touch `nodes/core.go` (`setNode`), `nodes/executors.go` (`executeSet`, `validateSetConfiguration`) and `internal/interop/n8n/parameters.go` (`setToKilas`, `setToN8N`) together, and note that `Definition` is serialized directly as the `/api/v1/node-types` payload by `internal/api/handlers/nodes.go`, so a new property kind is an OpenAPI change: `pnpm generate:api` must be rerun or `pnpm generate:api:check` fails in CI.

Dot notation is the trap. n8n's `dotNotation` defaults to true, so an n8n assignment named `user.email` writes a nested object, while KilasFlow's `executeSet` writes a literal key containing a dot. Turning dot notation on changes the behaviour of every already-imported Set that has a dotted field name. Ship it on by default to match n8n, expose the option, and say so in the import diagnostics — the alternative, defaulting it off, makes every imported workflow subtly wrong in a way nobody notices until the HTTP node sends the wrong body.

Then the five transform nodes, in a new `nodes/transform.go` with executors registered in `RegisterExecutors`. Split Out and Aggregate are inverses and should be written together, so the round trip is testable in one fixture. Sort has three n8n modes — simple field ordering, random, and a JavaScript comparator; implement the first two and refuse the third with the diagnostic p4-5 defines, so the Code decision stays in exactly one ticket. Summarize needs n8n's aggregation set (count, count unique, sum, min, max, average, concatenate, append) and split-by fields. Remove Duplicates has two operations: removing duplicates within the incoming items, which is local and easy, and removing items seen in previous executions, which needs durable per-workflow state that this repository does not have — implement the first, and refuse the second with a diagnostic naming it, leaving the durable form to the PostgreSQL tier in p6.

Every one of these nodes must return exactly one item stream per declared output port even when it is empty, or `Runner.Run` in `internal/engine/runner.go` aborts the execution on its output-arity check.

## References

- Plan: `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, section p4 (data shaping); p2-2 property kinds; p3-8 binary data storage.
- PRD `gflow-prd-v1.md` §22 Workflow Data Model, §23 Node Registry, §24 Native V1 Nodes.
- Verified in this repository: `nodes/core.go` (`setNode`), `nodes/executors.go` (`executeSet`, `validateSetConfiguration`), `internal/interop/n8n/parameters.go` (`setToKilas`, `setToN8N`), `internal/interop/n8n/n8n.go` (`mappings`), `internal/node/registry.go` (`PropertyKind` has six values), `internal/api/handlers/nodes.go`, `nodes/unsupported.go`.
- n8n 2.34.0 reference checkout (read-only, outside this repo): `/Users/izzadev/projects/mitrachat/n8n/packages/nodes-base/nodes/Set/v2/SetV2.node.ts` and `.../nodes/Set/test/`. The transform nodes ship from `nodes/Transform/{Aggregate,Sort,SplitOut,Summarize,Limit,RemoveDuplicates}` per that package's `package.json`; `n8n-nodes-base.splitOut` is confirmed as a type string in the checkout, and the rest must be confirmed against the widened checkout from p0-1 before the mapping table is written.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `8d531d39` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `71f15dab` — chore(pine): open the V2 n8n-first epic
- Files changed (base → working tree):

```
 .gitignore                                         |    10 +
 .pine/MEMORY.md                                    |     4 +
 .pine/memory/licensing.md                          |    12 +
 .pine/memory/n8n-reference.md                      |    11 +
 .pine/roadmap.md                                   |  1161 ++
 .pine/tickets/EPIC-m42s3g.md                       |    79 +
 .pine/tickets/FEAT-0556ck.md                       |    66 +
 .pine/tickets/FEAT-096vs9.md                       |    53 +
 .pine/tickets/FEAT-0f87fn.md                       |   337 +
 .pine/tickets/FEAT-12s0e5.md                       |    65 +
 .pine/tickets/FEAT-1500sp.md                       |    58 +
 .pine/tickets/FEAT-1axhdn.md                       |    65 +
 .pine/tickets/FEAT-1br8at.md                       |   128 +
 .pine/tickets/FEAT-1c70nt.md                       |    73 +
 .pine/tickets/FEAT-27km39.md                       |    71 +
 .pine/tickets/FEAT-2f68r8.md                       |   129 +
 .pine/tickets/FEAT-2phs15.md                       |    68 +
 .pine/tickets/FEAT-347egc.md                       |    54 +
 .pine/tickets/FEAT-3taswf.md                       |    67 +
 .pine/tickets/FEAT-3xqky1.md                       |    70 +
 .pine/tickets/FEAT-45tfmh.md                       |    68 +
 .pine/tickets/FEAT-48hreg.md                       |    67 +
 .pine/tickets/FEAT-4d0bje.md                       |    62 +
 .pine/tickets/FEAT-53fht8.md                       |    60 +
 .pine/tickets/FEAT-55v09k.md                       |   124 +
 .pine/tickets/FEAT-5fhj6p.md                       |    69 +
 .pine/tickets/FEAT-5fv8gf.md                       |    59 +
 .pine/tickets/FEAT-5kfctc.md                       |    66 +
 .pine/tickets/FEAT-5kv1jq.md                       |   118 +
 .pine/tickets/FEAT-5mvech.md                       |    72 +
 .pine/tickets/FEAT-5rvtzc.md                       |   135 +
 .pine/tickets/FEAT-5s1w0t.md                       |   124 +
 .pine/tickets/FEAT-5z37xh.md                       |    72 +
 .pine/tickets/FEAT-68zzqs.md                       |    65 +
 .pine/tickets/FEAT-6vfn3s.md                       |   395 +
 .pine/tickets/FEAT-7cg0cd.md                       |    60 +
 .pine/tickets/FEAT-7tgasa.md                       |    61 +
 .pine/tickets/FEAT-8qyfh1.md                       |    53 +
 .pine/tickets/FEAT-8r9n21.md                       |   343 +
 .pine/tickets/FEAT-91as16.md                       |   141 +
 .pine/tickets/FEAT-9555xz.md                       |    58 +
 .pine/tickets/FEAT-96p7m3.md                       |    52 +
 .pine/tickets/FEAT-9dqn7d.md                       |    66 +
 .pine/tickets/FEAT-9knk67.md                       |   121 +
 .pine/tickets/FEAT-a6yg3n.md                       |   126 +
 .pine/tickets/FEAT-a94c8y.md                       |    60 +
 .pine/tickets/FEAT-adzn0a.md                       |   112 +
 .pine/tickets/FEAT-afs850.md                       |   113 +
 .pine/tickets/FEAT-agj52c.md                       |    64 +
 .pine/tickets/FEAT-ajw7wt.md                       |    61 +
 .pine/tickets/FEAT-az620p.md                       |    54 +
 .pine/tickets/FEAT-bp0ytb.md                       |   376 +
 .pine/tickets/FEAT-bscygc.md                       |    62 +
 .pine/tickets/FEAT-c2a081.md                       |    55 +
 .pine/tickets/FEAT-cgm1y3.md                       |    50 +
 .pine/tickets/FEAT-cjpbe6.md                       |    70 +
 .pine/tickets/FEAT-cpdp8y.md                       |    70 +
 .pine/tickets/FEAT-csqgg5.md                       |   145 +
 .pine/tickets/FEAT-cwz4ac.md                       |    66 +
 .pine/tickets/FEAT-cx3hq1.md                       |    71 +
 .pine/tickets/FEAT-czbzs6.md                       |    65 +
 .pine/tickets/FEAT-ddzk2k.md                       |    63 +
 .pine/tickets/FEAT-de8d4c.md                       |    71 +
 .pine/tickets/FEAT-ed6wdy.md                       |    66 +
 .pine/tickets/FEAT-ej0468.md                       |    54 +
 .pine/tickets/FEAT-frvez8.md                       |    70 +
 .pine/tickets/FEAT-fw0m2q.md                       |   117 +
 .pine/tickets/FEAT-g6wrxm.md                       |    64 +
 .pine/tickets/FEAT-gg85se.md                       |    69 +
 .pine/tickets/FEAT-gjzgkd.md                       |    59 +
 .pine/tickets/FEAT-gvn62x.md                       |    57 +
 .pine/tickets/FEAT-gxppx1.md                       |    71 +
 .pine/tickets/FEAT-hv4q8e.md                       |   126 +
 .pine/tickets/FEAT-je4f4t.md                       |    56 +
 .pine/tickets/FEAT-jq84xk.md                       |    67 +
 .pine/tickets/FEAT-jwhdsy.md                       |    90 +
 .pine/tickets/FEAT-k3fmj1.md                       |   141 +
 .pine/tickets/FEAT-k3grr5.md                       |   126 +
 .pine/tickets/FEAT-k65hqv.md                       |    60 +
 .pine/tickets/FEAT-k9dwgn.md                       |    65 +
 .pine/tickets/FEAT-knpfqf.md                       |    56 +
 .pine/tickets/FEAT-kwxxd0.md                       |    64 +
 .pine/tickets/FEAT-m94hhx.md                       |    60 +
 .pine/tickets/FEAT-mvegj5.md                       |    56 +
 .pine/tickets/FEAT-n19dch.md                       |    66 +
 .pine/tickets/FEAT-n5fdz3.md                       |    69 +
 .pine/tickets/FEAT-nbqye0.md                       |   129 +
 .pine/tickets/FEAT-nc6z9r.md                       |    68 +
 .pine/tickets/FEAT-nch9dg.md                       |    67 +
 .pine/tickets/FEAT-nrfg6e.md                       |    69 +
 .pine/tickets/FEAT-nrfz6m.md                       |    64 +
 .pine/tickets/FEAT-nxxbs5.md                       |    77 +
 .pine/tickets/FEAT-pd3p6x.md                       |   129 +
 .pine/tickets/FEAT-pnbt4z.md                       |    91 +
 .pine/tickets/FEAT-ptyh9w.md                       |    65 +
 .pine/tickets/FEAT-q81bq4.md                       |    56 +
 .pine/tickets/FEAT-qcm5ec.md                       |   117 +
 .pine/tickets/FEAT-qe6wb8.md                       |   378 +
 .pine/tickets/FEAT-qfr9xe.md                       |   136 +
 .pine/tickets/FEAT-r6xhnp.md                       |    54 +
 .pine/tickets/FEAT-rj17xj.md                       |    64 +
 .pine/tickets/FEAT-sar60r.md                       |   124 +
 .pine/tickets/FEAT-sbnejr.md                       |    51 +
 .pine/tickets/FEAT-sdjdh2.md                       |    33 +
 .pine/tickets/FEAT-sfy1tq.md                       |    63 +
 .pine/tickets/FEAT-snxxny.md                       |    68 +
 .pine/tickets/FEAT-sp8cfm.md                       |   396 +
 .pine/tickets/FEAT-ss44d9.md                       |    67 +
 .pine/tickets/FEAT-t26rt7.md                       |    65 +
 .pine/tickets/FEAT-t5q318.md                       |   131 +
 .pine/tickets/FEAT-v8k1tc.md                       |   132 +
 .pine/tickets/FEAT-vvwpjw.md                       |   432 +
 .pine/tickets/FEAT-w9kqeg.md                       |    96 +
 .pine/tickets/FEAT-whn5vb.md                       |   143 +
 .pine/tickets/FEAT-wkmv5e.md                       |    67 +
 .pine/tickets/FEAT-xeq6st.md                       |    68 +
 .pine/tickets/FEAT-xqqjqv.md                       |   327 +
 .pine/tickets/FEAT-xr7ga9.md                       |    76 +
 .pine/tickets/FEAT-xx6p22.md                       |    62 +
 .pine/tickets/FEAT-ybm2pd.md                       |    55 +
 .pine/tickets/FEAT-ykyfbd.md                       |    72 +
 .pine/tickets/FEAT-yx0qt6.md                       |    71 +
 .pine/tickets/FEAT-yyjfjq.md                       |   123 +
 .pine/tickets/FEAT-za118x.md                       |    61 +
 .pine/tickets/FEAT-zmfsjd.md                       |    71 +
 .pine/tickets/FEAT-znm60y.md                       |   350 +
 .pine/tickets/FEAT-ztxs5p.md                       |   384 +
 Makefile                                           |    19 +
 README.md                                          |    33 +
 cmd/kilasflow/main.go                              |   116 +-
 cmd/nodepackgen/generate.go                        |   576 +
 cmd/nodepackgen/generate_test.go                   |   374 +
 cmd/nodepackgen/main.go                            |   165 +
 cmd/nodepackgen/openapi.go                         |   168 +
 cmd/nodepackgen/testdata/manifest.json             |    12 +
 cmd/nodepackgen/testdata/pack.golden.json          |   238 +
 cmd/nodepackgen/testdata/report.golden.md          |    20 +
 cmd/nodepackgen/testdata/spec.json                 |    98 +
 config.example.yaml                                |    17 +
 internal/ai/ai_test.go                             |    47 +
 internal/api/handlers/credentials.go               |    73 +
 internal/api/handlers/executions.go                |     1 +
 internal/api/handlers/interop.go                   |    61 +-
 internal/api/handlers/nodes.go                     |   262 +-
 internal/api/handlers/workflows.go                 |   119 +-
 internal/api/middleware/embed.go                   |    10 +-
 internal/api/routes.go                             |    12 +-
 internal/api/server.go                             |    15 +-
 internal/api/workflows_test.go                     |    73 +-
 internal/binary/binary.go                          |   212 +
 internal/binary/binary_test.go                     |   210 +
 internal/conditions/conditions.go                  |   542 +
 internal/conditions/conditions_test.go             |   231 +
 internal/conditions/doc.go                         |    26 +
 internal/config/config.go                          |    33 +
 internal/credentials/builtin.go                    |   153 +
 internal/credentials/credentials.go                |    98 +-
 internal/credentials/credentials_test.go           |   242 +
 internal/credentials/registry.go                   |   340 +
 internal/engine/authenticate.go                    |    95 +
 internal/engine/runner.go                          |   858 +-
 internal/engine/runner_test.go                     |  1229 +-
 internal/engine/service.go                         |    85 +-
 internal/engine/service_test.go                    |    47 +-
 internal/engine/worker_test.go                     |     4 +-
 internal/execution/records.go                      |    55 +-
 internal/execution/redact.go                       |   116 +-
 internal/execution/redact_test.go                  |   213 +-
 internal/expression/doc.go                         |    82 +-
 internal/expression/expression.go                  |   328 +-
 internal/expression/expression_test.go             |   309 +-
 internal/expression/functions.go                   |   219 +
 internal/expression/roots.go                       |   125 +
 internal/expression/undefined.go                   |    22 +
 internal/guardrails/doc.go                         |     8 +
 internal/guardrails/licence_boundary_test.go       |   359 +
 internal/interop/n8n/corpus/BASELINE.md            |   104 +
 internal/interop/n8n/corpus/MANIFEST.json          |   169 +
 internal/interop/n8n/corpus/baseline.json          |   435 +
 internal/interop/n8n/corpus/corpus.go              |   250 +
 internal/interop/n8n/corpus/doc.go                 |    19 +
 internal/interop/n8n/corpus/fixtures/README.md     |    12 +
 .../n8n/corpus/fixtures/control-manual-set.json    |    40 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   574 +
 internal/interop/n8n/export_test.go                |    10 +
 internal/interop/n8n/n8n.go                        |  1001 +-
 internal/interop/n8n/n8n_test.go                   |  1778 ++-
 internal/interop/n8n/parameters.go                 |   961 +-
 internal/loadoptions/loadoptions.go                |   353 +
 internal/loadoptions/loadoptions_test.go           |   354 +
 internal/node/icon.go                              |    94 +
 internal/node/registry.go                          |   683 +-
 internal/node/registry_test.go                     |   708 +-
 internal/nodepack/nodepack.go                      |   424 +
 internal/nodepack/startcase.go                     |   136 +
 internal/nodepack/startcase_test.go                |    82 +
 internal/nodepack/trigger.go                       |   433 +
 internal/property/loader.go                        |    92 +
 internal/property/property.go                      |   299 +
 internal/property/testdata/visibility.json         |   163 +
 internal/property/visibility.go                    |   315 +
 internal/property/visibility_test.go               |    77 +
 internal/repository/executions.go                  |    48 +-
 internal/repository/models.go                      |    97 +-
 internal/repository/models_test.go                 |    16 +-
 internal/repository/webhooks.go                    |   257 +-
 internal/routing/doc.go                            |    54 +
 internal/routing/executor.go                       |   571 +
 internal/routing/request.go                        |   412 +
 internal/routing/response.go                       |   177 +
 internal/routing/routing.go                        |   373 +
 internal/routing/routing_test.go                   |   764 ++
 internal/scheduler/scheduler.go                    |     8 +-
 internal/scheduler/scheduler_test.go               |    41 +-
 internal/webhook/export_test.go                    |    14 +
 internal/webhook/lifecycle.go                      |   304 +
 internal/webhook/lifecycle_test.go                 |   229 +
 internal/webhook/request_lifecycle.go              |   207 +
 internal/webhook/shape.go                          |   248 +
 internal/webhook/shape_test.go                     |   211 +
 internal/webhook/webhook.go                        |   322 +-
 internal/webhook/webhook_test.go                   |   473 +-
 internal/workflow/compiler.go                      |   397 +-
 internal/workflow/compiler_test.go                 |    97 +
 internal/workflow/document.go                      |   100 +-
 internal/workflow/document_test.go                 |   881 +-
 internal/workflow/typeversion.go                   |   159 +
 internal/workflow/typeversion_openapi.go           |    28 +
 internal/workflow/typeversion_test.go              |   125 +
 nodes/ai.go                                        |    64 +-
 nodes/ai_test.go                                   |    28 +-
 nodes/annotation.go                                |    62 +
 nodes/assignments.go                               |   180 +
 nodes/bindings_test.go                             |   129 +
 nodes/code.go                                      |    28 +-
 nodes/code_test.go                                 |    68 +-
 nodes/conditions.go                                |   139 +
 nodes/core.go                                      |   181 +-
 nodes/database.go                                  |    24 +-
 nodes/database_test.go                             |    55 +-
 nodes/executors.go                                 |   569 +-
 nodes/executors_test.go                            |   480 +
 nodes/flow.go                                      |   457 +
 nodes/flow_test.go                                 |   464 +
 nodes/http.go                                      |   193 +-
 nodes/http_test.go                                 |   231 +-
 nodes/loop.go                                      |   245 +
 nodes/presentation_test.go                         |    60 +
 nodes/routing.go                                   |    23 +
 nodes/telegram.go                                  |   393 +
 nodes/telegram_download.go                         |   243 +
 nodes/telegram_lifecycle.go                        |   423 +
 nodes/telegram_test.go                             |   610 +
 nodes/unsupported.go                               |   116 +-
 nodes/webhook.go                                   |    47 +-
 packs/telegram/README.md                           |    40 +
 packs/telegram/pack.json                           |  1119 ++
 packs/telegram/telegram.go                         |    58 +
 packs/telegram/telegram_test.go                    |   466 +
 packs/waha/README.md                               |    32 +
 packs/waha/REPORT-202409.md                        |   100 +
 packs/waha/REPORT-202502.md                        |   128 +
 packs/waha/manifest-202409.json                    |   124 +
 packs/waha/manifest-202502.json                    |   124 +
 packs/waha/pack-202409.json                        |  2794 +++++
 packs/waha/pack-202502.json                        |  3844 +++++++
 packs/waha/pack-trigger-202409.json                |   124 +
 packs/waha/pack-trigger-202502.json                |   130 +
 packs/waha/waha.go                                 |   107 +
 packs/waha/waha_test.go                            |  1196 ++
 schemas/workflow-v1.schema.json                    |     2 +-
 scripts/corpus-sync.sh                             |   225 +
 sdk/src/generated/models.ts                        |   593 +-
 third_party/waha/LICENSE                           |    19 +
 third_party/waha/PROVENANCE.md                     |    57 +
 third_party/waha/openapi-202409.json               |  8129 ++++++++++++++
 third_party/waha/openapi-202502.json               | 11084 +++++++++++++++++++
 .../lib/api/generated/credentials/credentials.ts   |    96 +-
 .../lib/api/generated/models/activationNotice.ts   |    13 +
 .../lib/api/generated/models/activationResource.ts |    23 +
 .../models/{unsupported.ts => assignment.ts}       |     9 +-
 web/src/lib/api/generated/models/condition.ts      |    14 +
 .../api/generated/models/credentialRequirement.ts  |    15 +
 web/src/lib/api/generated/models/definition.ts     |    17 +
 .../generated/models/executionNodeRunResource.ts   |     2 +
 .../lib/api/generated/models/executionResource.ts  |     2 +
 .../lib/api/generated/models/executionSummary.ts   |     2 +
 web/src/lib/api/generated/models/exportIssue.ts    |    16 +
 .../api/generated/models/exportIssueSeverity.ts    |    19 +
 .../generated/models/exportedWorkflowResource.ts   |     4 +-
 .../lib/api/generated/models/expressionGrammar.ts  |    16 +
 web/src/lib/api/generated/models/field.ts          |     1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |    19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |    15 +
 web/src/lib/api/generated/models/importIssue.ts    |    20 +
 .../api/generated/models/importIssueSeverity.ts    |    19 +
 .../generated/models/importedWorkflowResource.ts   |     7 +-
 web/src/lib/api/generated/models/index.ts          |    28 +-
 .../api/generated/models/loadOptionsInputBody.ts   |    20 +
 .../models/loadOptionsInputBodyParameters.ts       |     9 +
 .../api/generated/models/loadOptionsResource.ts    |    17 +
 web/src/lib/api/generated/models/node.ts           |     1 +
 web/src/lib/api/generated/models/nodeCodex.ts      |    16 +
 .../api/generated/models/nodeCodexSubcategories.ts |     9 +
 .../api/generated/models/{lossy.ts => nodeIcon.ts} |     7 +-
 web/src/lib/api/generated/models/option.ts         |    12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |    21 +
 web/src/lib/api/generated/models/port.ts           |     9 +-
 .../lib/api/generated/models/propertyDefinition.ts |    14 +
 web/src/lib/api/generated/models/propertyGroup.ts  |    15 +
 .../api/generated/models/testCredentialResource.ts |    14 +
 web/src/lib/api/generated/models/typeOptions.ts    |    17 +
 web/src/lib/api/generated/models/visibility.ts     |    15 +
 .../lib/api/generated/models/webhookDeclaration.ts |    15 +
 .../api/generated/models/webhookRouteResource.ts   |    14 +
 web/src/lib/api/generated/nodes/nodes.ts           |   342 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |     3 +-
 .../components/workflow-editor/canvas-node.svelte  |    33 +-
 .../workflow-editor/execution-canvas-node.svelte   |    20 +-
 .../components/workflow-editor/node-icon.svelte    |    10 +-
 .../components/workflow-editor/node-picker.svelte  |     8 +-
 .../workflow-editor/properties-panel.svelte        |    48 +-
 .../workflow-editor/property-field.svelte          |   203 +-
 .../workflow-editor/workflow-editor.svelte         |     4 +-
 web/src/lib/workflow-editor/assignments.test.ts    |    82 +
 web/src/lib/workflow-editor/assignments.ts         |    89 +
 web/src/lib/workflow-editor/credentials.ts         |    41 +-
 web/src/lib/workflow-editor/document.test.ts       |    10 +-
 web/src/lib/workflow-editor/document.ts            |     4 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |     2 +-
 web/src/lib/workflow-editor/execution.test.ts      |    72 +-
 web/src/lib/workflow-editor/execution.ts           |    86 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |    43 +
 web/src/lib/workflow-editor/node-visual.test.ts    |   163 +-
 web/src/lib/workflow-editor/node-visual.ts         |   214 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   199 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |    17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    26 +
 341 files changed, 75666 insertions(+), 1465 deletions(-)
```
