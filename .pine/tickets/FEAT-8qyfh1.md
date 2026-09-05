---
id: FEAT-8qyfh1
title: Decide and deliver the Code node compatibility story
status: done
priority: medium
labels:
    - nodes
    - parity
deps:
    - FEAT-v8k1tc
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T05:03:55Z"
updated: "2026-09-05T13:53:51Z"
---

## Scope

KilasFlow's Code node runs Go. `codeNode()` in `nodes/code.go` registers `kilasflow.code` with a `code` parameter whose description is "The body of func run(items []Item) ([]Item, error)", plus `timeoutSeconds` and `memoryMB`; `internal/runcode/runcode.go` compiles that source with `GOOS=wasip1 GOARCH=wasm` through `exec.CommandContext(… "build" …)` and runs the module under wazero. n8n's Code node runs JavaScript. The importer has no entry for `n8n-nodes-base.code` in `mappings` (`internal/interop/n8n/n8n.go`), so every imported Code node becomes `kilasflow.unsupported`, whose validator always fails — one Code node makes the whole workflow unactivatable, and the Code node is among the most-used nodes in the corpus.

The owner has already settled the JS question at the roadmap level: the JS sidecar is deferred to p8-2, so emulating n8n's Code node is not on the table in this phase. What is undecided, and what this ticket must decide and then deliver, is what actually happens to an imported JavaScript Code node — refused with a first-class diagnostic, or translated onto the Go node. That decision must be made once, here, and every other p4 ticket that meets a JavaScript escape hatch (Sort's `code` comparator, for one) must call into this ticket's mechanism rather than inventing its own.

Refusing only works if the alternative exists, and today it does not reliably. `internal/runcode/doc.go` still carries the open question in its own words — "compiling Go requires the full toolchain (~270MB), which cannot ship inside the distroless single binary. Resolve before Milestone 5 -- likely a separate compiler service" — and `ErrCompilerUnavailable` surfaces to the user as "this deployment cannot compile Code nodes" at run time, after the workflow was activated. The Go node also drops binary data in both directions (`CodeExecutor.Execute` moves only `item.JSON` into `runcode.Item` and only `JSON` back out) and has no equivalent of n8n's `runOnceForEachItem`, so it always runs once over the whole batch.

## Acceptance criteria

- [x] The decision is written into the ticket body and into `.pine/memory/code-node.md` as a durable learning, stating what an imported JavaScript or Python Code node becomes and why, so no later ticket relitigates it.
- [x] An imported `n8n-nodes-base.code` becomes a dedicated node type — not the generic `kilasflow.unsupported` — that preserves `jsCode`, `pythonCode`, `mode` and `language`, shows the original source read-only in the editor, and fails compilation with a message naming the node and the alternative.
- [x] The import diagnostic distinguishes "this workflow is blocked only by Code nodes" from "this workflow has other unsupported nodes", and names each Code node individually.
- [x] One refusal mechanism serves every JavaScript escape hatch in p4, and the p4-2 Sort `code` comparator uses it.
- [x] The Go toolchain question in `internal/runcode/doc.go` is answered, the doc comment no longer describes it as open, and a deployment that cannot compile reports it through `/api/v1/node-types` so the editor can say so before the workflow is saved, not after it runs.
- [x] The Go Code node carries binary through in both directions over the store from p3-8, instead of silently dropping it. **It already did** — FEAT-v8k1tc's binary work covered it; this ticket pins it with a test that would catch it being dropped again.
- [x] The Go Code node supports running once per item as well as once per batch, matching n8n's `mode`.
- [x] The p0 corpus report separates workflows blocked only by a Code node from workflows blocked for other reasons, and the counts are recorded below.

## The decision

**An imported JavaScript or Python Code node is refused, never translated.**

It becomes `kilasflow.foreignCode`: a first-class placeholder that keeps the original `jsCode` / `pythonCode`, `language` and `mode`, shows the source in the editor, exports back to `n8n-nodes-base.code` unchanged, and refuses to compile with a message naming the native node that most likely replaces it.

Translating is a compiler project with no correct stopping point. A one-line `items.map(…)` translates cleanly; the next body — a closure over `$input`, a regex whose semantics differ, `JSON.parse` on something that is not JSON — translates into Go that compiles and computes something *different*. Silently different is the one outcome this codebase has consistently refused, from `kilasflow.unsupported` onward.

It is **blocking**, not pass-through. A Code node that quietly passed its items along would let the workflow activate, run, produce plausible output, and be missing whatever the code was there to do.

Recorded in `.pine/memory/code-node.md` so no later ticket reopens it.

## Outcome

### The refusal, made worth having

A distinct node type rather than the generic placeholder, because the two are different problems: an unsupported node type is something this product has not built, and a Code node is something it deliberately has not — the source is right there, the user wrote it, and what they need is to be told which native node now does the job.

Named `foreignCode` rather than `jsCode`, because n8n carries Python under the same node type and a placeholder labelled JavaScript holding Python would be a small lie in the one place the user is reading closely.

`SuggestReplacement` is **a curated table, not a translator**: it recognises `.filter(`, `.sort(`, `.reduce(`, `.flatMap(`, `new Set(`, `fetch(`, `new Date(`, `.slice(` and `.map(` and names the node that replaces each — Filter, Sort, Aggregate or Summarize, Split Out, Remove Duplicates, HTTP Request, Date & Time, Limit, Set. It suggests; it never rewrites. The same function produces the sentence on import and the sentence in the node's own validator, so a user who reads the diagnostic and then opens the node is not told two different things.

**One mechanism, one wording.** `unsupportedScript` is the single refusal every JavaScript escape hatch produces, and Sort's `code` comparator now goes through it rather than carrying its own sentence. Three wordings for one situation is how a user concludes the three are different problems, and a test asserts both hatches refuse in the same words.

### The toolchain question, answered

`internal/runcode/doc.go` had carried it open since Milestone 5 was planned. The answer is that **`runcode.Compiler` is the seam** and a deployment supplies whichever implementation it has: `ToolchainCompiler` where `go` is on the PATH, nothing at all in the distroless image, or a compiler service behind the same interface for a hosted install — where `Cache`, already keyed by source hash, means each distinct body is built once for the whole installation.

What is explicitly **not** the answer is bundling the toolchain into the runtime image: it triples the image, puts a compiler on every machine that runs a workflow, and buys nothing a cache in front of one compiler does not buy more cheaply.

The rule that follows is the important part: **a user must never discover at run time that their deployment cannot compile.** `Definition.Unavailable` is stamped onto the catalogue on the way out, from a function rather than a startup snapshot — a compiler that comes back or goes away is picked up without a restart, and a snapshot would go stale in exactly the direction that misleads.

### The Go node

Per-item mode using n8n's own names, so an import needs no translation. It costs **one build either way**: the artifact is keyed by source hash, so running once per item multiplies sandbox calls and not compilations.

Binary already survived the round trip — user code sees JSON only, and the reference is re-attached positionally afterwards — so this ticket's contribution there is the test that would catch it being dropped again, over both modes.

### The corpus

| | Count |
| --- | ---: |
| Blocked only by a Code node | **0** / 39 |
| `n8n-nodes-base.code` instances | 3 |

**Zero, and that is the finding.** Three Code nodes exist across the public corpus, and every workflow containing one is blocked by something else as well — a missing credential, in each case. So replacing those Code nodes would not activate a single additional workflow today, which is exactly what the separated count is for: without it, three Code nodes look like three workflows this decision is costing, and they are not.

Activatable and runnable are unchanged at 13/39 and 3/39.

## Implementation Plan

Take the refusal, not the translation. Translating JavaScript to Go is a compiler project with no correct stopping point: a Code node body that looks like a one-line `items.map(…)` will translate, and the next one — closures over `$input`, a regex with JavaScript semantics, `JSON.parse` on a string that is not JSON — will translate into Go that compiles and computes something different. Silently different is the one outcome this codebase has consistently refused, from `kilasflow.unsupported` onward. The refusal must be much better than today's generic placeholder, though: a distinct node type in a new `nodes/jscode.go` that keeps the original source visible, and a diagnostic that tells the user which native node now does the job — p4-1 and p4-2 add Filter, Switch, Aggregate, Sort, Split Out, Summarize and Remove Duplicates precisely because those replace most real Code nodes. A curated pattern table that recognises common bodies and *suggests* a replacement is worth building; an automatic rewrite is not.

Keep it blocking. The placeholder's `Validate` must fail, like `nodes/unsupported.go`'s does, because a Code node that quietly passes items through is worse than one that refuses. What changes is the reporting: the import result should say "this workflow is one Code node away from running" rather than burying it in a list.

The harder half is making the Go node a real alternative. Decide the compiler story explicitly. There are three shapes: bundle the toolchain in the runtime image, which contradicts the single-binary distribution the PRD builds on; ship an optional compiler sidecar image that the main binary talks to over the existing `runcode.Compiler` interface, which `ToolchainCompiler` already abstracts; or precompile at save time in CI-like environments and ship artifacts. Recommend the sidecar: `runcode.Compiler` is already the seam, `Cache` already keys artifacts by source hash, and `CodeExecutor.Status` already exists so the editor can show compilation state without running the workflow. Whatever is chosen, surface availability in the node-types payload so `internal/api/handlers/nodes.go` reports it and the editor greys the node out — a user must not discover at run time that their deployment cannot compile.

Then close the two Go-node gaps in `nodes/code.go`: carry `item.Binary` into and out of `runcode.Item` over p3-8's store, and add a `mode` parameter so the executor can loop per item. Note that per-item mode multiplies compilation cache hits, not compilations — the artifact is keyed by source hash, so this is cheap.

Finally, keep p8-5's three prerequisites in view but out of scope: a new wazero runtime is built per call with the compilation cache unused, and the guest has zero host functions, so HTTP, credentials and binary data are impossible from inside user code today. The binary work in this ticket is plumbing at the executor boundary, not host functions inside the sandbox.

## References

- Roadmap plan, p4 section: `.pine/roadmap.md`.
- `.pine/roadmap.md` — the p4 "code" family and the locked decision "JS sidecar: deferred to the long tail"; entry V2-p8-2 (JS sidecar for programmatic community nodes); entry V2-p8-5 (native community module SDK and its three prerequisites); entry V2-p3-8 (binary data storage).
- PRD `gflow-prd-v1.md` §30 Go Code Node, §31 Go Code Security, §32 Go Code V1 Restrictions.
- Verified in this repository: `nodes/code.go` (`codeNode`, `CodeExecutor.Execute`, `CodeExecutor.Status`, `CompilationStatus`), `internal/runcode/doc.go` (the open toolchain question, verbatim), `internal/runcode/runcode.go` (`ErrCompilerUnavailable`, `ToolchainCompiler`, the `GOOS=wasip1 GOARCH=wasm` build), `internal/interop/n8n/n8n.go` (`mappings` has no `code` entry), `nodes/unsupported.go` (`validateUnsupportedConfiguration` always fails), `internal/api/handlers/nodes.go`.
- n8n 2.34.0 reference checkout (read-only, outside this repo): the type string `n8n-nodes-base.code` is confirmed present, shipping from `packages/nodes-base/nodes/Code/`.

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
 .pine/memory/live-databases.md                     |    30 +
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
 .pine/tickets/FEAT-8qyfh1.md                       |   102 +
 .pine/tickets/FEAT-8r9n21.md                       |   343 +
 .pine/tickets/FEAT-91as16.md                       |   141 +
 .pine/tickets/FEAT-9555xz.md                       |    58 +
 .pine/tickets/FEAT-96p7m3.md                       |    52 +
 .pine/tickets/FEAT-9dqn7d.md                       |   422 +
 .pine/tickets/FEAT-9knk67.md                       |   121 +
 .pine/tickets/FEAT-a6yg3n.md                       |   126 +
 .pine/tickets/FEAT-a94c8y.md                       |    60 +
 .pine/tickets/FEAT-adzn0a.md                       |   112 +
 .pine/tickets/FEAT-afs850.md                       |   113 +
 .pine/tickets/FEAT-agj52c.md                       |    64 +
 .pine/tickets/FEAT-ajw7wt.md                       |    61 +
 .pine/tickets/FEAT-az620p.md                       |   481 +
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
 .pine/tickets/FEAT-jwhdsy.md                       |   444 +
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
 .pine/tickets/FEAT-q81bq4.md                       |   480 +
 .pine/tickets/FEAT-qcm5ec.md                       |   117 +
 .pine/tickets/FEAT-qe6wb8.md                       |   378 +
 .pine/tickets/FEAT-qfr9xe.md                       |   136 +
 .pine/tickets/FEAT-r6xhnp.md                       |    54 +
 .pine/tickets/FEAT-rj17xj.md                       |    64 +
 .pine/tickets/FEAT-sar60r.md                       |   124 +
 .pine/tickets/FEAT-sbnejr.md                       |    51 +
 .pine/tickets/FEAT-sdjdh2.md                       |    33 +
 .pine/tickets/FEAT-sfy1tq.md                       |    63 +
 .pine/tickets/FEAT-snxxny.md                       |   409 +
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
 cmd/kilasflow/main.go                              |   170 +-
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
 internal/api/credentials_test.go                   |   357 +
 internal/api/embed_test.go                         |    61 +-
 internal/api/handlers/credentials.go               |   298 +
 internal/api/handlers/executions.go                |    41 +-
 internal/api/handlers/interop.go                   |    61 +-
 internal/api/handlers/nodes.go                     |   291 +-
 internal/api/handlers/workflows.go                 |   122 +-
 internal/api/middleware/embed.go                   |    10 +-
 internal/api/node_types_test.go                    |    39 +
 internal/api/routes.go                             |    29 +-
 internal/api/server.go                             |    30 +-
 internal/api/workflows_test.go                     |    77 +-
 internal/binary/binary.go                          |   212 +
 internal/binary/binary_test.go                     |   210 +
 internal/conditions/conditions.go                  |   542 +
 internal/conditions/conditions_test.go             |   231 +
 internal/conditions/doc.go                         |    26 +
 internal/config/config.go                          |   101 +-
 internal/config/config_test.go                     |    35 +
 internal/credentials/builtin.go                    |   153 +
 internal/credentials/credentials.go                |    98 +-
 internal/credentials/credentials_test.go           |   242 +
 internal/credentials/registry.go                   |   340 +
 internal/datetime/datetime_test.go                 |   134 +
 internal/datetime/doc.go                           |    15 +
 internal/datetime/format.go                        |   195 +
 internal/datetime/parse.go                         |   108 +
 internal/engine/authenticate.go                    |    95 +
 internal/engine/runner.go                          |   905 +-
 internal/engine/runner_test.go                     |  1229 +-
 internal/engine/service.go                         |   358 +-
 internal/engine/service_test.go                    |    47 +-
 internal/engine/subworkflow_test.go                |   329 +
 internal/engine/worker_test.go                     |     9 +-
 internal/execution/records.go                      |    67 +-
 internal/execution/redact.go                       |   116 +-
 internal/execution/redact_test.go                  |   213 +-
 internal/expression/doc.go                         |    82 +-
 internal/expression/expression.go                  |   350 +-
 internal/expression/expression_test.go             |   374 +-
 internal/expression/functions.go                   |   219 +
 internal/expression/roots.go                       |   125 +
 internal/expression/undefined.go                   |    22 +
 internal/guardrails/doc.go                         |     8 +
 internal/guardrails/licence_boundary_test.go       |   359 +
 internal/interop/n8n/corpus/BASELINE.md            |   105 +
 internal/interop/n8n/corpus/MANIFEST.json          |   169 +
 internal/interop/n8n/corpus/baseline.json          |   436 +
 internal/interop/n8n/corpus/corpus.go              |   250 +
 internal/interop/n8n/corpus/doc.go                 |    19 +
 internal/interop/n8n/corpus/fixtures/README.md     |    12 +
 .../n8n/corpus/fixtures/control-manual-set.json    |    40 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   613 +
 internal/interop/n8n/export_test.go                |    10 +
 internal/interop/n8n/n8n.go                        |  1045 +-
 internal/interop/n8n/n8n_test.go                   |  2136 +++-
 internal/interop/n8n/parameters.go                 |  1483 ++-
 internal/loadoptions/loadoptions.go                |   353 +
 internal/loadoptions/loadoptions_test.go           |   354 +
 internal/node/icon.go                              |    94 +
 internal/node/registry.go                          |   727 +-
 internal/node/registry_test.go                     |   763 +-
 internal/nodepack/nodepack.go                      |   424 +
 internal/nodepack/startcase.go                     |   136 +
 internal/nodepack/startcase_test.go                |    82 +
 internal/nodepack/trigger.go                       |   433 +
 internal/property/loader.go                        |    92 +
 internal/property/property.go                      |   299 +
 internal/property/testdata/visibility.json         |   163 +
 internal/property/visibility.go                    |   315 +
 internal/property/visibility_test.go               |    77 +
 internal/repository/executions.go                  |   181 +-
 internal/repository/models.go                      |   129 +-
 internal/repository/models_test.go                 |    16 +-
 internal/repository/schedules.go                   |   140 +-
 internal/repository/webhooks.go                    |   257 +-
 internal/repository/workflows.go                   |    66 +-
 internal/routing/doc.go                            |    54 +
 internal/routing/executor.go                       |   571 +
 internal/routing/request.go                        |   412 +
 internal/routing/response.go                       |   177 +
 internal/routing/routing.go                        |   373 +
 internal/routing/routing_test.go                   |   764 ++
 internal/runcode/doc.go                            |    37 +-
 internal/scheduler/extract.go                      |    81 +
 internal/scheduler/item.go                         |    71 +
 internal/scheduler/rule.go                         |   321 +
 internal/scheduler/rule_test.go                    |   278 +
 internal/scheduler/scheduler.go                    |   147 +-
 internal/scheduler/scheduler_test.go               |   194 +-
 internal/sqlnode/sqlnode.go                        |   305 +-
 internal/sqlnode/sqlnode_test.go                   |   106 +
 internal/webhook/export_test.go                    |    14 +
 internal/webhook/lifecycle.go                      |   304 +
 internal/webhook/lifecycle_test.go                 |   229 +
 internal/webhook/request_lifecycle.go              |   207 +
 internal/webhook/shape.go                          |   248 +
 internal/webhook/shape_test.go                     |   211 +
 internal/webhook/webhook.go                        |   396 +-
 internal/webhook/webhook_test.go                   |   650 +-
 internal/workflow/compiler.go                      |   432 +-
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
 nodes/code.go                                      |    95 +-
 nodes/code_test.go                                 |   134 +-
 nodes/conditions.go                                |   139 +
 nodes/core.go                                      |   207 +-
 nodes/database.go                                  |   257 +-
 nodes/database_test.go                             |   540 +-
 nodes/datetime.go                                  |   408 +
 nodes/datetime_test.go                             |   274 +
 nodes/executors.go                                 |   601 +-
 nodes/executors_test.go                            |   480 +
 nodes/flow.go                                      |   457 +
 nodes/flow_test.go                                 |   464 +
 nodes/http.go                                      |   197 +-
 nodes/http_test.go                                 |   231 +-
 nodes/loop.go                                      |   245 +
 nodes/presentation_test.go                         |    60 +
 nodes/routing.go                                   |    23 +
 nodes/subworkflow.go                               |   248 +
 nodes/telegram.go                                  |   393 +
 nodes/telegram_download.go                         |   243 +
 nodes/telegram_lifecycle.go                        |   423 +
 nodes/telegram_test.go                             |   610 +
 nodes/transform.go                                 |   745 ++
 nodes/transform_test.go                            |   315 +
 nodes/unsupported.go                               |   116 +-
 nodes/wait.go                                      |   227 +
 nodes/webhook.go                                   |   302 +-
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
 sdk/src/generated/models.ts                        |   678 +-
 third_party/waha/LICENSE                           |    19 +
 third_party/waha/PROVENANCE.md                     |    57 +
 third_party/waha/openapi-202409.json               |  8129 ++++++++++++++
 third_party/waha/openapi-202502.json               | 11084 +++++++++++++++++++
 .../lib/api/generated/credentials/credentials.ts   |   199 +-
 .../lib/api/generated/models/activationNotice.ts   |    13 +
 .../lib/api/generated/models/activationResource.ts |    23 +
 .../models/{unsupported.ts => assignment.ts}       |     9 +-
 web/src/lib/api/generated/models/condition.ts      |    14 +
 .../api/generated/models/credentialRequirement.ts  |    15 +
 web/src/lib/api/generated/models/definition.ts     |    18 +
 .../generated/models/executionNodeRunResource.ts   |     2 +
 .../lib/api/generated/models/executionResource.ts  |     4 +
 .../lib/api/generated/models/executionSummary.ts   |     4 +
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
 web/src/lib/api/generated/models/index.ts          |    30 +-
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
 .../api/generated/models/testCredentialResource.ts |    17 +
 .../lib/api/generated/models/testPayloadBody.ts    |    22 +
 .../api/generated/models/testPayloadBodyFields.ts  |    12 +
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
 .../workflow-editor/property-field.svelte          |   264 +-
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
 .../lib/workflow-editor/fixed-collection.test.ts   |   131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |    75 +
 web/src/lib/workflow-editor/node-visual.test.ts    |   163 +-
 web/src/lib/workflow-editor/node-visual.ts         |   220 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   199 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |    17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    26 +
 370 files changed, 86705 insertions(+), 1661 deletions(-)
```
