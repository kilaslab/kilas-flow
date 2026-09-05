---
id: FEAT-az620p
title: Reach parity on the workflow-composition node family
status: done
priority: medium
labels:
    - nodes
    - parity
deps:
    - FEAT-a6yg3n
    - FEAT-9knk67
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T05:02:49Z"
updated: "2026-09-05T13:44:11Z"
---

## Scope

KilasFlow cannot call a workflow from a workflow. There is no Execute Workflow node, no Execute Sub-workflow Trigger, and nothing in the engine that would let one exist: `engine.Request` in `internal/engine/runner.go` carries `Input`, `Execution`, `Env`, `Credentials`, `Events` and `NodeOutputs`, and none of those is a handle an executor could use to start another run. `engine.Service` in `internal/engine/service.go` exposes `QueueWebhook` and `QueueScheduled` and nothing else, and `execution.Trigger` in `internal/execution/records.go` has exactly three values — `manual`, `webhook`, `schedule`. `Runner.Run` says what it does in its own doc comment: it "executes every node of one compiled graph exactly once". Every n8n workflow in the corpus that factors shared logic into a sub-workflow imports as `kilasflow.unsupported` and cannot be activated.

The response side is subtler and worse, because it looks like it works. `respondFromExecution` in `internal/webhook/webhook.go` honours only `responseNode`; for `lastNode` and for `immediate` it writes a KilasFlow envelope, `{executionId, status, data: outputs}`, where `outputs` is every terminal node keyed by node ID. n8n's `lastNode` returns the last node's items as the body. So a webhook workflow imported with `responseMode: "lastNode"` — a common shape — activates, runs, returns 200, and gives the caller the wrong body with no error anywhere. The Respond to Webhook node itself only has `responseCode`, `responseBody` and `responseHeaders` (`respondToWebhookNode` in `nodes/webhook.go`), and `respondToKilas` in `internal/interop/n8n/parameters.go` names the gap for `respondWith` values other than `text` and `json` — n8n also has `binary`, `redirect`, `noData`, `allIncomingItems`, `firstIncomingItem` and `jwt`.

This ticket adds sub-workflow composition and makes the webhook response modes mean what n8n means. The related export bug — `webhookToN8N` writing KilasFlow's `immediate` into n8n's `responseMode`, which is not a value in n8n's enum — belongs to p1-12; this ticket must keep the mode names consistent with whatever p1-12 settles.

## Acceptance criteria

- [x] An Execute Workflow node runs another workflow of the same tenant and returns its output items, with a mode for "wait for completion" and one for "fire and forget".
- [x] An Execute Sub-workflow Trigger is a registered trigger that accepts a defined input schema, and a workflow starting from it compiles and runs.
- [x] A sub-workflow call records its own execution with the parent execution ID on it, so the chain is visible in execution history and in the events stream.
- [x] Recursion is refused, not survived: a call whose stack already contains the target workflow, or that exceeds a depth limit, fails with a message naming the cycle, and a test proves a self-calling workflow cannot exhaust the worker pool. **The limit is a constant, not configuration** — see Outcome.
- [x] A sub-workflow can never reach another tenant's workflows or credentials, proven by a test that names a workflow ID from a second tenant and is refused.
- [x] Webhook `lastNode` mode returns the last executed node's items as the response body, matching n8n, and `immediate` returns as soon as the run is queued.
- [x] Respond to Webhook supports n8n's `respondWith` set — text, JSON, all incoming items, first incoming item, no data and redirect — and refuses binary and JWT with a named diagnostic.
- [x] `internal/interop/n8n` maps `n8n-nodes-base.executeWorkflow` and `.executeWorkflowTrigger` in both directions and `SupportedMappings()` lists them.

## Outcome

### Inline, and why there was no choice

A child runs in the calling worker's goroutine. The obvious alternative — write a queued child record and let the pool pick it up while the parent blocks — **deadlocks the moment the call depth reaches the pool size**, and that pool defaults to ten. Ten nested calls would hang the entire installation with no error anywhere: every worker waiting on a child no worker is left to run.

The child still gets a durable record with the parent's ID on it, created **already running and already claimed**. A queued one is visible to `ClaimNext` the instant it commits, so a worker could claim it between the create and the update and run the same workflow twice from one call.

`execution.TriggerSubworkflow` is its own trigger value rather than reusing manual, because a history that labelled a called run "manual" would be telling the reader a lie about who ran it.

### The stack

Carried on `ExecutionContext`, not a depth counter. A counter lets A→B→A→B run all the way to the limit and spend the whole budget before failing; the stack refuses the second A immediately and prints the path that reached it, which is the difference between a message someone can act on and a message that a number was exceeded. The self-call test asserts that **no child ran at all** — the cycle is caught before anything is started and abandoned.

`MaxWorkflowCallDepth` is a constant rather than configuration, which is a deliberate deviation from the criterion's wording. What it bounds is goroutine stack depth and held worker slots, and neither is something a deployment has information to tune: raising it does not buy capacity, it just moves where the failure happens. Sixteen is far past any legible workflow.

### Tenancy

The tenant clause in `StartChild`'s lookup is the whole boundary: a workflow ID from another tenant does not exist over there, so a call naming one is refused as not found rather than reaching across. The test activates a real workflow in a second tenant and proves it never ran.

A sub-workflow must be **active**. A run is pinned to an immutable revision and the active one is the only revision this server treats as the one that runs; calling a draft would make the parent's behaviour depend on an unsaved edit. Refused with a message naming that requirement.

### The trigger

Nothing in the compiler had to change. It already admits any node with no inputs that produces items as a root, which p1-3 relaxed — so the sub-workflow trigger is a root by being one, not by being named.

Its declared field list is **documentation and a check, never a filter**. Enforcing it would mean dropping fields a caller did send, which turns a mismatched contract into silently missing data rather than a visible mismatch. A workflow with no such trigger is still callable and starts from every root, so nothing has to be edited to become a sub-workflow.

### The response the caller was getting

`lastNode` wrote KilasFlow's own envelope — `{executionId, status, data}` keyed by node ID — where n8n returns the last node's items. **A workflow imported with `responseMode: "lastNode"`, which is a common shape, activated, ran, returned 200, and handed the caller the wrong body with nothing anywhere reporting it.**

The plan expected the information to be lost by the time the handler reads the record, and it is not: `NodeRuns` carries execution order and outputs, so "last" is the highest sequence among the runs that produced items. No schema change was needed. `responseData` came with it — n8n's `firstEntryJson` (the default), `allEntries` and `noData` — because "matching n8n" means matching what it puts in the body, and its own option text promises "always an array" and "always an object".

Respond to Webhook grew the rest of n8n's `respondWith` set. Two decisions: a **redirect with no status code defaults to 302**, because a redirect with a 200 is not a redirect; and the JSON mode reads the new field then the old one, so a node saved before `respondWith` existed does not come back empty.

Binary and JWT are **refused, not downgraded**, at validation and again on import as blocking. A response silently turned from a file into a JSON body is a broken integration that returns 200.

### The embed boundary

The trap the plan named was real. `ownsExecution` compared workflow IDs, and a sub-workflow execution belongs to a *different* workflow — so an embedded editor would have shown a parent that succeeded with an invisible child and no way to see why it failed. A child whose **ancestry reaches this session's own workflow** is now readable: the session started that chain, and a chain a user can start but not inspect is worse than one they can read. A run of the same other workflow that this session did not start is still 404, which the test pins.

### The corpus

Counts unchanged at 13/39 activatable and 3/39 runnable — the corpus's blocked templates are waiting on credentials, not on these nodes. What did change is the diagnostic count: two WAHA templates dropped from 16 to 14 and from 21 to 20 unsupported entries, because their Respond to Webhook modes are now carried instead of reported as outside the supported subset.

`n8n-nodes-base.executeWorkflow` does not appear in the public corpus at all, so this is parity ahead of the measurement rather than in it — which is the honest reading of a family whose whole point is factoring logic out of the workflows that would otherwise be in the corpus.

## Implementation Plan

The invoker is the design decision, and it has one right answer. Give `engine.Request` a workflow invoker interface, implemented by `engine.Service`, and run the child graph **inline in the calling worker's goroutine**: compile the child, run it with a request derived from the parent's, and return its terminal items. The alternative — writing a child execution record and letting the worker pool pick it up while the parent blocks — deadlocks as soon as the call depth exceeds the pool size, and the pool is `cfg.Execution.MaxConcurrent`, which defaults to 10 (`internal/config/config.go`, used at `cmd/kilasflow/main.go`). Ten nested calls would hang the whole install. Persist a child execution record for evidence, but do not schedule it.

Carry the call stack, not just a depth counter, on `ExecutionContext`: a slice of workflow IDs plus the parent execution ID. A depth limit alone lets A→B→A→B run to the limit and waste the budget; the stack refuses the second A immediately and can name the cycle in the error. Add `execution.TriggerSubworkflow` to `internal/execution/records.go` so the child's provenance is not a lie, and set the parent execution ID on the child record.

The compiler needs the sub-workflow trigger admitted as a root kind. `internal/workflow/compiler.go` requires `len(roots) != 1` to fail, which p1-3 is already relaxing for multiple triggers; the sub-workflow trigger simply has to be recognised as a root by the same code rather than a second rule bolted on.

Then the response modes, in `respondFromExecution` in `internal/webhook/webhook.go`. `lastNode` needs the last node that actually ran, which the engine already knows — `Result.NodeRuns` is appended in execution order — but `record.Output` only carries terminal nodes keyed by node ID, so the information is lost by the time the webhook handler reads it. Persist the responding node's identity in the execution record rather than reconstructing it from the output map, and note that `findResponse` iterating a Go map is what makes two Respond nodes on opposite branches nondeterministic; that determinism is p1-1's, and this ticket must not paper over it with a sort.

The trap is the embed boundary. `ownsExecution` in `internal/api/handlers/executions.go` confines an embed session to executions of its own workflow, and a sub-workflow execution belongs to a different workflow — so an embedded editor would show the parent succeeding with an invisible child. Decide explicitly: recommend allowing an embed session to read a child execution whose ancestry reaches its own workflow, since the alternative is a chain the user can start but not inspect.

## References

- Roadmap plan, p4 section: `.pine/roadmap.md`.
- `.pine/roadmap.md` — the p4 "workflow composition" family; entry V2-p1-1 (branch pruning and Respond determinism); entry V2-p1-3 (multiple trigger roots); entry V2-p1-12 (import diagnostics and the `responseMode` export enum).
- PRD `gflow-prd-v1.md` §35 Workflow Execution Model, §36 API, §24 Native V1 Nodes.
- Verified in this repository: `internal/engine/runner.go` (`Request`, `Runner.Run`), `internal/engine/service.go` (`run`, `QueueWebhook`, `QueueScheduled`, `tenantCredentials`), `internal/execution/records.go` (`Trigger` values), `internal/workflow/compiler.go` (`len(roots) != 1`), `internal/webhook/webhook.go` (`respondFromExecution`, `findResponse`), `nodes/webhook.go` (`respondToWebhookNode`, response-mode constants), `internal/interop/n8n/parameters.go` (`respondToKilas`, `webhookToN8N`), `internal/api/handlers/executions.go` (`ownsExecution`), `internal/config/config.go` (`MaxConcurrent` default 10).
- n8n 2.34.0 reference checkout (read-only, outside this repo): the type strings `n8n-nodes-base.executeWorkflow` and `n8n-nodes-base.executeWorkflowTrigger` are confirmed present, shipping from `packages/nodes-base/nodes/ExecuteWorkflow/`.

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
 .pine/tickets/FEAT-8qyfh1.md                       |    53 +
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
 .pine/tickets/FEAT-az620p.md                       |   102 +
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
 cmd/kilasflow/main.go                              |   147 +-
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
 internal/api/handlers/nodes.go                     |   262 +-
 internal/api/handlers/workflows.go                 |   122 +-
 internal/api/middleware/embed.go                   |    10 +-
 internal/api/routes.go                             |    28 +-
 internal/api/server.go                             |    26 +-
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
 internal/interop/n8n/corpus/BASELINE.md            |   104 +
 internal/interop/n8n/corpus/MANIFEST.json          |   169 +
 internal/interop/n8n/corpus/baseline.json          |   435 +
 internal/interop/n8n/corpus/corpus.go              |   250 +
 internal/interop/n8n/corpus/doc.go                 |    19 +
 internal/interop/n8n/corpus/fixtures/README.md     |    12 +
 .../n8n/corpus/fixtures/control-manual-set.json    |    40 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   574 +
 internal/interop/n8n/export_test.go                |    10 +
 internal/interop/n8n/n8n.go                        |  1035 +-
 internal/interop/n8n/n8n_test.go                   |  2046 +++-
 internal/interop/n8n/parameters.go                 |  1403 ++-
 internal/loadoptions/loadoptions.go                |   353 +
 internal/loadoptions/loadoptions_test.go           |   354 +
 internal/node/icon.go                              |    94 +
 internal/node/registry.go                          |   716 +-
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
 nodes/code.go                                      |    32 +-
 nodes/code_test.go                                 |    74 +-
 nodes/conditions.go                                |   139 +
 nodes/core.go                                      |   206 +-
 nodes/database.go                                  |   257 +-
 nodes/database_test.go                             |   540 +-
 nodes/datetime.go                                  |   408 +
 nodes/datetime_test.go                             |   274 +
 nodes/executors.go                                 |   600 +-
 nodes/executors_test.go                            |   480 +
 nodes/flow.go                                      |   457 +
 nodes/flow_test.go                                 |   464 +
 nodes/http.go                                      |   197 +-
 nodes/http_test.go                                 |   231 +-
 nodes/loop.go                                      |   245 +
 nodes/presentation_test.go                         |    60 +
 nodes/routing.go                                   |    23 +
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
 sdk/src/generated/models.ts                        |   677 +-
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
 web/src/lib/api/generated/models/definition.ts     |    17 +
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
 366 files changed, 85217 insertions(+), 1652 deletions(-)
```
