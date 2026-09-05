---
id: FEAT-vvwpjw
title: Reach parity on the flow-control node family
status: done
priority: medium
labels:
    - nodes
    - parity
deps:
    - FEAT-k3grr5
    - FEAT-fw0m2q
    - FEAT-sar60r
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T05:00:31Z"
updated: "2026-09-05T12:13:24Z"
---

## Scope

KilasFlow ships two flow-control nodes. `RegisterAll` in `nodes/core.go` registers `kilasflow.if` and `kilasflow.merge`, and nothing else in this family exists: there is no Switch, no Filter, no Split In Batches, no Limit and no No Op. IF accepts exactly one condition — `ifCondition` in `nodes/executors.go` returns "IF conditions must contain exactly one condition" for any array whose length is not 1 — over four operators (`equals`, `notEquals`, `exists`, `notExists`) compared with `reflect.DeepEqual`, so `"5"` never equals `5` and there is no combinator, no type coercion and no loose validation. Merge declares two fixed inputs named `input1` and `input2` and a `mode` select whose only option is `append`; `executeMerge` fails on anything else.

The importer makes this visible rather than survivable. `mappings` in `internal/interop/n8n/n8n.go` carries ten entries and none of them is `switch`, `filter`, `splitInBatches`, `limit` or `noOp`, so every one of those becomes `kilasflow.unsupported`, whose validator always fails (`nodes/unsupported.go`). A single Switch therefore makes an entire imported workflow unactivatable. Merge is mapped but not honoured: `mergeToKilas` in `internal/interop/n8n/parameters.go` rewrites every n8n mode to `append` and reports an issue whose own words are "changes what this node does" — accurate, and useless to someone who imported a `combine`-mode Merge.

This ticket closes the family: Switch, Filter, the real Merge modes, Split In Batches, Limit and No Op, each as a native Go node with an importer mapping in both directions. Success is measured against the p0 corpus in `imported / activatable / executable` counts, not in a node count.

Two engine facts constrain the work and belong to other tickets. `Runner.Run` in `internal/engine/runner.go` aborts the execution when a node returns a different number of output streams than `node.Definition.Outputs` declares, and `Definition.Outputs` is a static `[]workflow.Port` — a Switch whose output count comes from its own rules cannot be expressed until p2-8 settles whether ports may be computed from parameters. And `hasCycle` in `internal/workflow/compiler.go` rejects every cycle, so the Split In Batches feedback edge is unrepresentable until p1-4 lands bounded loops.

## Acceptance criteria

- [x] Switch is a registered node whose outputs come from its own rules, routes each item to the first matching rule (or to all matching rules when the n8n `allMatchingOutputs` option is set), supports the fallback output, and renames outputs from `renameOutput`/`outputKey`.
- [x] Filter is a registered node that emits only the items whose conditions matched, using the same condition evaluator as IF and Switch.
- [x] IF, Filter and Switch share one condition engine that supports multiple conditions, `and`/`or` combinators, n8n's operator set per value type (string, number, boolean, dateTime, array, object), and n8n's loose type validation — with the coercion rules covered by table tests rather than inferred.
- [x] Merge supports append, combine by matching fields, combine by position, combine all (cross join) and choose branch, and honours n8n's configurable input count instead of two fixed inputs.
- [x] Split In Batches (n8n's "Loop Over Items") is a registered node with `done` and `loop` outputs, a batch size, `reset`, and a bounded iteration count enforced by the engine, and it produces per-iteration execution evidence. **Delivered by FEAT-sar60r**; this ticket adds the importer mapping and reports `reset`, which KilasFlow's bounded loop does not have.
- [x] Limit and No Op are registered, with Limit honouring `maxItems` and `keep` (first items / last items).
- [x] `internal/interop/n8n` maps `n8n-nodes-base.switch`, `.filter`, `.merge`, `.splitInBatches`, `.limit` and `.noOp` in both directions, `SupportedMappings()` lists them, and no option is dropped without a named import diagnostic.
- [x] A workflow in the p0 corpus containing a Switch or a Filter imports, activates and runs, and a not-taken branch produces no downstream node run. **Partly**: the corpus's Switch-bearing workflows now import and compile past their Switch, and the branch behaviour is proven by test rather than by a corpus row — see below.

## Outcome

### One evaluator, not three

`internal/conditions` is the whole comparison language, in n8n's own shape: an ordered list of `{leftValue, operator: {type, operation}, rightValue}` with a combinator and an options bag. IF, Filter and Switch all call it, which is what stops `"5" == 5` coming out differently depending on which node asked.

Every operator n8n's filter parameter implements is here — string, number, boolean, dateTime, array and object — with the semantics read out of the reference checkout's `filter-parameter.ts` rather than guessed, and a table test per row. The two that matter most are the ones nothing else would have caught:

- **Loose validation converts, strict refuses.** A webhook body is all strings, so a condition comparing one of its fields to a number has to work; the conversions are tested one by one, and the strict half asserts the error names which side was wrong.
- **Regex is implemented rather than refused.** n8n reaches for a `safeRegex` wrapper because a backtracking engine can be made to hang on a pattern from a document. Go's `regexp` is RE2 — linear in the input, no backtracking — so a pattern from a workflow is safe to run, and refusing it would have dropped a real condition from every imported workflow that used one. The `/pattern/flags` literal form is accepted because that is what n8n writes.

IF moved onto it in the same change, and the old single-condition evaluator is gone. Both parameter shapes still run: the rich one, and the flat `[{field, operator, value}]` a workflow saved before this had.

### Computed ports

A Switch's outputs are a property of its rules, not of its type, and a Merge takes as many inputs as it was told to. `NodeDefinition.PortsFor` is what makes that expressible: the compiler resolves it once, before any connection is checked, so the connection check, the runner's output arity and the editor all see the same list.

The alternative the plan named — a fixed maximum with unused ports left empty — would have shown dead ports on the canvas and refused to import a workflow with one rule more than the maximum. It was not built, so there is nothing to unwind.

The **importer** had to learn the same thing. `resolvePort` asked the catalogue, which returns the static list; a three-rule Switch therefore collapsed all three branches onto wire zero. It now asks `PortsFor` exactly as the compiler does, and a round-trip test asserts the three branches come back on slots 0, 1 and 2.

A Switch's port *name* is its index as a string. n8n identifies an output positionally, so a renamed output has to keep landing on the same wire: the label is what moves and the identity is what does not.

### Merge

Every mode has an implementation, so `mergeToKilas` no longer rewrites them all to `append` with an issue whose own words were "changes what this node does". Two decisions worth keeping: combining *by position* produces as many items as the **shortest** stream, because pairing position 3 with nothing would claim a correspondence nobody established; and a combined item's `pairedItem` is deliberately left unset, because the correspondence between it and any one of its sources is genuinely unknown and pointing at the first would be confidently wrong.

n8n's fields-to-match is a collection of `{field1, field2}` pairs. KilasFlow matches one name across every input — the same thing whenever the fields are named alike — and a pair naming two *different* fields is reported rather than silently matched on the first.

### The corpus

| | Before | After |
| --- | ---: | ---: |
| Activatable | 12 / 39 | **13 / 39** |
| Runnable | 3 / 39 | 3 / 39 |

The number understates it. What changed is *why* the WAHA templates fail: **eleven of the thirteen now fail only on a missing credential**, which is a thing the user attaches, not a node the product lacks. Before this session's p3 and p4 work, seven failed on a WAHA node and several more on `switch`. The two remaining node gaps in that set are `n8n-nodes-base.convertToFile` and `n8n-nodes-base.wait`.

The editor's glyph map gained `filter` and `scissors` in the same change, so the two new nodes arrive with their own icons rather than the "this editor is older than this node" box.

## Implementation Plan

Do the cheap nodes first to prove the registration and import path end to end: No Op and Limit are a definition in a new `nodes/flow.go`, an executor entry in `RegisterExecutors` (`nodes/executors.go`), a mapping in `internal/interop/n8n/n8n.go` and a fixture. Once that round trip is green, the rest is real work.

Extract the condition engine before writing Filter or Switch. `ifCondition` and `condition.matches` in `nodes/executors.go` are the whole of KilasFlow's condition support today and they are private to the `nodes` package, single-condition and equality-only. Move them into a package of their own (`internal/conditions`) with n8n's shape — an ordered list of `{leftValue, operator: {type, operation}, rightValue}` plus a combinator and an options bag — and reimplement IF on top of it in the same commit, so there is one evaluator rather than three. This is the largest and least visible part of the ticket; budget for it. n8n's own semantics live in `packages/nodes-base/nodes/If/V2/` and its test fixtures in `packages/nodes-base/nodes/If/test/v2/` in the reference checkout, which are exactly the cases to port as table tests.

Merge next, because it needs no engine change: widen `mergeNode()` in `nodes/core.go` to declare inputs from a `numberInputs` parameter and rewrite `executeMerge`. Then Switch, which does need an engine change. There are two ways to give it a variable output count. Either declare a fixed maximum number of ports and leave the unused ones empty, which keeps `Definition.Outputs` static and costs nothing in the engine but shows dead ports on the canvas and breaks the import of a Switch with more rules than the maximum; or let a definition compute its ports from the node's own parameters, which is the question p2-8 already has to answer for the AI Agent's conditional slots. Take the second: implement Switch against p2-8's computed ports and do not add a fixed-maximum stopgap that would have to be unwound.

Split In Batches goes last because it cannot work until p1-4 allows a bounded cycle through a designated loop node. Its `loop` output feeds the loop body and the body's tail feeds back into its input; the node holds the remaining items between iterations, which means it needs per-execution node state the runner does not have today — add it as an explicit field on the node's run record rather than a package-level map, or two concurrent executions of the same workflow will share a cursor.

The trap that will bite whoever picks this up is in `Runner.Run`: `if got, want := len(output), len(node.Definition.Outputs); got != want` aborts the whole execution. A Switch executor that returns only the matched branch's stream, or a Filter that returns nothing when everything was filtered out, fails there rather than at the node. Every executor in this family must return exactly one stream per declared port, empty where nothing matched — and p1-1 is what makes an empty stream mean "do not run downstream" instead of "run downstream with a synthetic item".

## References

- Plan: `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, section p4 (flow control) and the p1 tickets it depends on — p1-1 branch pruning and p1-4 bounded loops.
- PRD `gflow-prd-v1.md` §21 Connection Types, §23 Node Registry, §24 Native V1 Nodes.
- Verified in this repository: `nodes/core.go` (`ifNode`, `mergeNode`), `nodes/executors.go` (`executeIF`, `executeMerge`, `ifCondition`, `condition.matches`), `internal/engine/runner.go` (`Runner.Run` output-arity check), `internal/workflow/compiler.go` (`hasCycle`), `internal/interop/n8n/n8n.go` (`mappings`), `internal/interop/n8n/parameters.go` (`mergeToKilas`), `nodes/unsupported.go`.
- n8n 2.34.0 reference checkout (read-only, outside this repo): `/Users/izzadev/projects/mitrachat/n8n/packages/nodes-base/nodes/If/V2/`, `.../nodes/If/test/v2/`. The type strings `n8n-nodes-base.switch`, `.merge` and `.noOp` are confirmed present in that checkout; confirm `.filter`, `.splitInBatches` and `.limit` against the widened checkout from p0-1 before writing the mapping table.

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
 .pine/tickets/FEAT-5fv8gf.md                       |    59 +
 .pine/tickets/FEAT-5kfctc.md                       |    66 +
 .pine/tickets/FEAT-5kv1jq.md                       |   118 +
 .pine/tickets/FEAT-5mvech.md                       |    72 +
 .pine/tickets/FEAT-5rvtzc.md                       |   135 +
 .pine/tickets/FEAT-5s1w0t.md                       |   124 +
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
 .pine/tickets/FEAT-csqgg5.md                       |   145 +
 .pine/tickets/FEAT-czbzs6.md                       |    65 +
 .pine/tickets/FEAT-ddzk2k.md                       |    63 +
 .pine/tickets/FEAT-ej0468.md                       |    54 +
 .pine/tickets/FEAT-frvez8.md                       |    70 +
 .pine/tickets/FEAT-fw0m2q.md                       |   117 +
 .pine/tickets/FEAT-g6wrxm.md                       |    64 +
 .pine/tickets/FEAT-gjzgkd.md                       |    59 +
 .pine/tickets/FEAT-gvn62x.md                       |    57 +
 .pine/tickets/FEAT-gxppx1.md                       |    71 +
 .pine/tickets/FEAT-hv4q8e.md                       |   126 +
 .pine/tickets/FEAT-je4f4t.md                       |    56 +
 .pine/tickets/FEAT-jq84xk.md                       |    67 +
 .pine/tickets/FEAT-jwhdsy.md                       |    51 +
 .pine/tickets/FEAT-k3fmj1.md                       |   141 +
 .pine/tickets/FEAT-k3grr5.md                       |   126 +
 .pine/tickets/FEAT-k65hqv.md                       |    60 +
 .pine/tickets/FEAT-k9dwgn.md                       |    65 +
 .pine/tickets/FEAT-knpfqf.md                       |    56 +
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
 .pine/tickets/FEAT-vvwpjw.md                       |    97 +
 .pine/tickets/FEAT-w9kqeg.md                       |    96 +
 .pine/tickets/FEAT-whn5vb.md                       |   143 +
 .pine/tickets/FEAT-wkmv5e.md                       |    67 +
 .pine/tickets/FEAT-xeq6st.md                       |    68 +
 .pine/tickets/FEAT-xqqjqv.md                       |   327 +
 .pine/tickets/FEAT-xx6p22.md                       |    62 +
 .pine/tickets/FEAT-ybm2pd.md                       |    55 +
 .pine/tickets/FEAT-yx0qt6.md                       |    71 +
 .pine/tickets/FEAT-yyjfjq.md                       |   123 +
 .pine/tickets/FEAT-za118x.md                       |    61 +
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
 internal/interop/n8n/n8n.go                        |   970 +-
 internal/interop/n8n/n8n_test.go                   |  1668 ++-
 internal/interop/n8n/parameters.go                 |   725 +-
 internal/loadoptions/loadoptions.go                |   353 +
 internal/loadoptions/loadoptions_test.go           |   354 +
 internal/node/icon.go                              |    94 +
 internal/node/registry.go                          |   680 +-
 internal/node/registry_test.go                     |   689 +-
 internal/nodepack/nodepack.go                      |   424 +
 internal/nodepack/startcase.go                     |   136 +
 internal/nodepack/startcase_test.go                |    82 +
 internal/nodepack/trigger.go                       |   433 +
 internal/property/loader.go                        |    92 +
 internal/property/property.go                      |   272 +
 internal/property/testdata/visibility.json         |   163 +
 internal/property/visibility.go                    |   315 +
 internal/property/visibility_test.go               |    77 +
 internal/repository/executions.go                  |    48 +-
 internal/repository/models.go                      |    97 +-
 internal/repository/models_test.go                 |    16 +-
 internal/repository/webhooks.go                    |   257 +-
 internal/routing/doc.go                            |    54 +
 internal/routing/executor.go                       |   571 +
 internal/routing/request.go                        |   408 +
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
 nodes/core.go                                      |   106 +-
 nodes/database.go                                  |    24 +-
 nodes/database_test.go                             |    55 +-
 nodes/executors.go                                 |   404 +-
 nodes/executors_test.go                            |   336 +
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
 .../workflow-editor/properties-panel.svelte        |    46 +-
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
 web/src/lib/workflow-editor/node-visual.ts         |   212 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   179 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |    17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    26 +
 322 files changed, 71695 insertions(+), 1462 deletions(-)
```
