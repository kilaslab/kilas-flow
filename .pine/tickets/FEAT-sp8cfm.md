---
id: FEAT-sp8cfm
title: Map WAHA workflows through the n8n importer
status: done
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-qe6wb8
    - FEAT-bp0ytb
    - FEAT-t5q318
    - FEAT-k3fmj1
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:02:19Z"
updated: "2026-09-05T11:13:56Z"
---

## Scope

The WAHA pack and the WAHA trigger are useless to the stated business goal until the importer knows how to reach them. `internal/interop/n8n` advertises exactly ten mappings today — the `mappings` table in `n8n.go`, matched by exact string in `byN8NType` — and anything outside it becomes `kilasflow.unsupported`, a placeholder whose `Validate` always fails, so a real WAHA template imports as a canvas full of nodes that cannot activate. This ticket adds `@devlikeapro/n8n-nodes-waha.WAHA` and `@devlikeapro/n8n-nodes-waha.wahaTrigger`, plus the legacy unscoped `n8n-nodes-waha.*` forms the older published package used.

Note the capitalisation: one package ships `WAHA` in caps for the action node and `wahaTrigger` in camel case for the trigger. Type strings are matched byte for byte here — the existing table already relies on that, carrying `n8n-nodes-base.mySql` verbatim — and any attempt to be helpful by lower-casing or normalising them will break both mappings at once.

Three things in the current importer stand in the way. Versions: `Import` sets `converted.TypeVersion = entry.kilasVersion`, a single `int` fixed per mapping, so both WAHA versions would collapse onto one definition; WAHA's typeVersion is a YYYYMM integer (`202409`, `202502`) and has to dispatch to the matching registered version. Ports: `outputPortName` resolves an n8n output index through `outputPortsFor`, which returns `{"true","false"}` for `kilasflow.if` and `{"main"}` for everything else — so all twenty of a WAHA trigger's outputs would collapse onto `main` and every branch of an imported template would land on the same wire. Credentials: `Node.Credentials` is decoded off the wire and then never read by `Import`, so today every credential reference is dropped in silence.

Dropping the credential reference is the right instinct and the wrong outcome. An n8n credential is `{id, name}` scoped to the instance it came from; the id means nothing here. But saying nothing leaves the user with a node that looks configured and fails at run time. The import has to name the credential the workflow expects and leave the node visibly unconfigured until it is bound to a local one.

## Acceptance criteria

- [x] `@devlikeapro/n8n-nodes-waha.WAHA` and `@devlikeapro/n8n-nodes-waha.wahaTrigger` both import onto the native WAHA nodes, and both legacy unscoped `n8n-nodes-waha.*` types map to the same targets.
- [x] A YYYYMM typeVersion selects the matching registered node version; an unknown version reports which versions exist instead of silently importing at another one.
- [x] `resource` and `operation` parameter values survive import unchanged and select a real operation in the imported node, proven against the WAHA templates in the import corpus.
- [x] A WAHA trigger's output indexes map onto that version's event port names, so a template wiring two events to two branches keeps both wires.
- [x] An imported node that referenced a WAHA credential in n8n reports the credential name it expected and imports unbound; no foreign credential id is ever stored or trusted.
- [x] Export of a native WAHA node reproduces the original n8n type string, including its capitalisation, and its typeVersion.
- [x] `SupportedMappings()` lists the new pairs, so the advertised subset stays a written-down claim rather than an inferred one.
- [x] The corpus measurement is recorded: how many WAHA templates import, how many activate, how many execute, before and after.

## Outcome

### The measurement

| | Before | After |
| --- | ---: | ---: |
| WAHA templates in the corpus | 13 | 13 |
| Imported | 13 | 13 |
| **Blocked by a WAHA node** | **7** | **0** |
| Activatable | 0 | 1 |
| Runnable | 0 | 0 (1 blocked by the offline policy) |

Seven of the thirteen used to fail compilation on a WAHA node itself — three on the action node, four on the trigger. None does now, and `TestNoWAHATemplateIsBlockedByAWAHANode` reads the real templates and asserts it stays that way.

What blocks the remaining twelve is a different backlog, and the report names it per template: `n8n-nodes-base.switch` (two templates), `editImage`, `emailSend`, `convertToFile`, `wait`, plus Set/IF/postgres parameter gaps that belong to the p4 parity families. The corpus score moved from 11/39 activatable to 12/39; the WAHA half of that number is now a question about other nodes.

### Four mappings, one pair of nodes

The scoped and unscoped package forms both map to the same targets, listed explicitly rather than prefix-matched — an explicit entry is greppable and cannot accidentally capture a package that merely begins with the same characters. The capitalisation is preserved byte for byte: one package ships `WAHA` in caps and `wahaTrigger` in camel case, and a test pins `n8n.WAHANodeType == waha.NodeType` so the mapping table and the pack cannot drift apart silently.

### Three things this had to fix first

**`sharedVersion`.** The mapping's target version widened to `workflow.TypeVersion`, and a new flag says the two sides use the *same* numbering. That flag is what makes the unknown-version diagnostic possible without ruining every other import: KilasFlow's core nodes are at 1 while the n8n nodes they mirror are at 3.4 and 1.2, so resolving down is right there — and a WAHA workflow authored at 202409 that quietly landed on 202502 would be wired against a different event order. Export writes the node's own version for a shared-version mapping, so a round trip does not claim a version the workflow was not authored at.

**Export learned to ask the catalogue.** `outputIndexesFor` was a hardcoded switch that could only describe the nodes somebody wrote it for; the WAHA trigger has 26 outputs nobody hardcoded, and every branch of an exported workflow would have collapsed onto slot zero. `Export` now takes a catalogue, the same way `Import` already did, which also made `inputIndexFor` correct for free.

**A route label for an imported trigger.** n8n mints its own webhook route and stores it as an opaque `webhookId`, so an imported trigger carries no path — and a KilasFlow trigger with no path binds no route, which means the workflow activates and receives nothing. That was four of the seven blocked templates. The importer now invents a label from the node's name for *any* node whose definition declares a webhook path parameter, reads that from the catalogue rather than from a list of trigger type names, and reports the invented value: a value the user did not write should never appear in their workflow silently.

### Credentials

An n8n credential reference is `{id, name}` scoped to the instance it came from. The id names a row in somebody else's database, so it is never carried and never repeated back — but the *name* is reported, so the node arrives visibly unbound instead of looking configured and failing at run time.

### Not fixed here, and worth naming

`node.Definition.Credentials[].Required` is not enforced by the compiler. `restart-server-at-midnight` compiles with no WAHA credential attached and fails at the outbound call with `scheme "" is not supported`, because the base URL template resolved to nothing. A required credential ought to be a compile error naming the credential; the database nodes get that from their own `Validate`, and a pack has none.

## Implementation Plan

Extend the `mapping` struct rather than adding a parallel path. It needs a version-aware target — the current `kilasVersion int` becomes whatever the widened version type ends up being — and a way to say "this n8n type maps to this KilasFlow type at the same version number", since WAHA's versions are shared between the two sides. Add the legacy types as additional entries pointing at the same targets rather than by prefix-matching; an explicit list is greppable and cannot accidentally capture a package that merely starts with the same characters.

Fix `outputPortsFor` before the trigger mapping, not after. Its hardcoded switch is the reason a fan-out trigger cannot round-trip, and the fix is to ask the node registry for the definition's declared output ports instead of guessing from the type string — which also makes `outputIndexesFor`, its inverse used on export, correct for free. The importer currently has no registry reference, so this is a signature change through `Import` and `Export` and their API handlers; do it once, deliberately, rather than threading a WAHA special case through.

For credentials, add a per-node import diagnostic carrying the n8n credential type and the display name from `{id, name}`, and leave `Node.Credentials` empty on the canonical node. The diagnostic is the same channel the unsupported report already uses, so the import screen gets it with no new plumbing.

The trap is testing against invented fixtures. Every claim in this ticket is about matching strings a real template contains, so the tests must read the WAHA templates collected into the import corpus. A fixture written by the same person who wrote the mapping proves only that they were self-consistent.

## References

- Roadmap plan, p3 section, entry V2-p3-5: `.pine/roadmap.md`.
- `internal/interop/n8n/n8n.go` — the `mapping` struct, the `mappings` table, `byN8NType` / `byKilasType`, `Import`'s unsupported branch and its `converted.TypeVersion = entry.kilasVersion`, `outputPortName` / `outputPortsFor` / `inputPortName` / `inputPortsFor`, and `Node.Credentials`, decoded but unused on import.
- `.pine/tickets/FEAT-chxkvq.md` — the V1 interop ticket that set the advertised-subset rule and the unsupported-placeholder contract this extends.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `8d531d39` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `71f15dab` — chore(pine): open the V2 n8n-first epic
- Files changed (base → working tree):

```
 .gitignore                                         |    10 +
 .pine/MEMORY.md                                    |     2 +
 .pine/memory/licensing.md                          |    12 +
 .pine/memory/n8n-reference.md                      |    11 +
 .pine/roadmap.md                                   |   909 ++
 .pine/tickets/EPIC-m42s3g.md                       |    73 +
 .pine/tickets/FEAT-096vs9.md                       |    53 +
 .pine/tickets/FEAT-0f87fn.md                       |   337 +
 .pine/tickets/FEAT-12s0e5.md                       |    65 +
 .pine/tickets/FEAT-1500sp.md                       |    58 +
 .pine/tickets/FEAT-1axhdn.md                       |    65 +
 .pine/tickets/FEAT-1br8at.md                       |   128 +
 .pine/tickets/FEAT-1c70nt.md                       |    68 +
 .pine/tickets/FEAT-2f68r8.md                       |   129 +
 .pine/tickets/FEAT-2phs15.md                       |    68 +
 .pine/tickets/FEAT-347egc.md                       |    54 +
 .pine/tickets/FEAT-3xqky1.md                       |    70 +
 .pine/tickets/FEAT-45tfmh.md                       |    68 +
 .pine/tickets/FEAT-48hreg.md                       |    61 +
 .pine/tickets/FEAT-4d0bje.md                       |    62 +
 .pine/tickets/FEAT-55v09k.md                       |   124 +
 .pine/tickets/FEAT-5fv8gf.md                       |    59 +
 .pine/tickets/FEAT-5kfctc.md                       |    66 +
 .pine/tickets/FEAT-5kv1jq.md                       |   118 +
 .pine/tickets/FEAT-5rvtzc.md                       |   135 +
 .pine/tickets/FEAT-5s1w0t.md                       |   124 +
 .pine/tickets/FEAT-68zzqs.md                       |    65 +
 .pine/tickets/FEAT-6vfn3s.md                       |    61 +
 .pine/tickets/FEAT-7cg0cd.md                       |    60 +
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
 .pine/tickets/FEAT-c2a081.md                       |    55 +
 .pine/tickets/FEAT-cgm1y3.md                       |    50 +
 .pine/tickets/FEAT-cjpbe6.md                       |    70 +
 .pine/tickets/FEAT-csqgg5.md                       |   145 +
 .pine/tickets/FEAT-ddzk2k.md                       |    59 +
 .pine/tickets/FEAT-ej0468.md                       |    54 +
 .pine/tickets/FEAT-fw0m2q.md                       |   117 +
 .pine/tickets/FEAT-g6wrxm.md                       |    64 +
 .pine/tickets/FEAT-gjzgkd.md                       |    59 +
 .pine/tickets/FEAT-gvn62x.md                       |    57 +
 .pine/tickets/FEAT-gxppx1.md                       |    71 +
 .pine/tickets/FEAT-hv4q8e.md                       |   126 +
 .pine/tickets/FEAT-je4f4t.md                       |    56 +
 .pine/tickets/FEAT-jwhdsy.md                       |    51 +
 .pine/tickets/FEAT-k3fmj1.md                       |   141 +
 .pine/tickets/FEAT-k3grr5.md                       |   126 +
 .pine/tickets/FEAT-k65hqv.md                       |    60 +
 .pine/tickets/FEAT-k9dwgn.md                       |    65 +
 .pine/tickets/FEAT-knpfqf.md                       |    56 +
 .pine/tickets/FEAT-mvegj5.md                       |    56 +
 .pine/tickets/FEAT-n19dch.md                       |    66 +
 .pine/tickets/FEAT-n5fdz3.md                       |    69 +
 .pine/tickets/FEAT-nbqye0.md                       |   129 +
 .pine/tickets/FEAT-nch9dg.md                       |    67 +
 .pine/tickets/FEAT-nrfg6e.md                       |    69 +
 .pine/tickets/FEAT-nrfz6m.md                       |    64 +
 .pine/tickets/FEAT-pd3p6x.md                       |   129 +
 .pine/tickets/FEAT-ptyh9w.md                       |    65 +
 .pine/tickets/FEAT-q81bq4.md                       |    56 +
 .pine/tickets/FEAT-qcm5ec.md                       |   117 +
 .pine/tickets/FEAT-qe6wb8.md                       |   378 +
 .pine/tickets/FEAT-r6xhnp.md                       |    54 +
 .pine/tickets/FEAT-rj17xj.md                       |    64 +
 .pine/tickets/FEAT-sar60r.md                       |   124 +
 .pine/tickets/FEAT-sbnejr.md                       |    51 +
 .pine/tickets/FEAT-sdjdh2.md                       |    33 +
 .pine/tickets/FEAT-snxxny.md                       |    68 +
 .pine/tickets/FEAT-sp8cfm.md                       |    93 +
 .pine/tickets/FEAT-ss44d9.md                       |    67 +
 .pine/tickets/FEAT-t26rt7.md                       |    65 +
 .pine/tickets/FEAT-t5q318.md                       |   131 +
 .pine/tickets/FEAT-v8k1tc.md                       |   132 +
 .pine/tickets/FEAT-vvwpjw.md                       |    57 +
 .pine/tickets/FEAT-w9kqeg.md                       |    96 +
 .pine/tickets/FEAT-whn5vb.md                       |   143 +
 .pine/tickets/FEAT-wkmv5e.md                       |    67 +
 .pine/tickets/FEAT-xeq6st.md                       |    68 +
 .pine/tickets/FEAT-xqqjqv.md                       |    66 +
 .pine/tickets/FEAT-xx6p22.md                       |    62 +
 .pine/tickets/FEAT-ybm2pd.md                       |    55 +
 .pine/tickets/FEAT-yyjfjq.md                       |   123 +
 .pine/tickets/FEAT-znm60y.md                       |   350 +
 .pine/tickets/FEAT-ztxs5p.md                       |    59 +
 Makefile                                           |    19 +
 cmd/kilasflow/main.go                              |    96 +-
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
 internal/credentials/builtin.go                    |   130 +
 internal/credentials/credentials.go                |    91 +-
 internal/credentials/credentials_test.go           |   241 +
 internal/credentials/registry.go                   |   312 +
 internal/engine/authenticate.go                    |    95 +
 internal/engine/runner.go                          |   830 +-
 internal/engine/runner_test.go                     |  1142 +-
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
 internal/interop/n8n/corpus/baseline.json          |   436 +
 internal/interop/n8n/corpus/corpus.go              |   250 +
 internal/interop/n8n/corpus/doc.go                 |    19 +
 internal/interop/n8n/corpus/fixtures/README.md     |    12 +
 .../n8n/corpus/fixtures/control-manual-set.json    |    40 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   570 +
 internal/interop/n8n/export_test.go                |    10 +
 internal/interop/n8n/n8n.go                        |   907 +-
 internal/interop/n8n/n8n_test.go                   |  1263 ++-
 internal/interop/n8n/parameters.go                 |   101 +-
 internal/loadoptions/loadoptions.go                |   353 +
 internal/loadoptions/loadoptions_test.go           |   354 +
 internal/node/icon.go                              |    94 +
 internal/node/registry.go                          |   634 +-
 internal/node/registry_test.go                     |   610 +-
 internal/nodepack/nodepack.go                      |   424 +
 internal/nodepack/startcase.go                     |   136 +
 internal/nodepack/startcase_test.go                |    82 +
 internal/nodepack/trigger.go                       |   433 +
 internal/property/loader.go                        |    92 +
 internal/property/property.go                      |   180 +
 internal/property/testdata/visibility.json         |   163 +
 internal/property/visibility.go                    |   315 +
 internal/property/visibility_test.go               |    77 +
 internal/repository/executions.go                  |    48 +-
 internal/repository/models.go                      |    97 +-
 internal/repository/models_test.go                 |    16 +-
 internal/repository/webhooks.go                    |   257 +-
 internal/routing/doc.go                            |    54 +
 internal/routing/executor.go                       |   371 +
 internal/routing/request.go                        |   388 +
 internal/routing/response.go                       |   172 +
 internal/routing/routing.go                        |   354 +
 internal/routing/routing_test.go                   |   764 ++
 internal/scheduler/scheduler.go                    |     8 +-
 internal/scheduler/scheduler_test.go               |    41 +-
 internal/webhook/export_test.go                    |    14 +
 internal/webhook/lifecycle.go                      |   304 +
 internal/webhook/lifecycle_test.go                 |   229 +
 internal/webhook/request_lifecycle.go              |   207 +
 internal/webhook/shape.go                          |   219 +
 internal/webhook/shape_test.go                     |   211 +
 internal/webhook/webhook.go                        |   258 +-
 internal/webhook/webhook_test.go                   |   473 +-
 internal/workflow/compiler.go                      |   347 +-
 internal/workflow/document.go                      |   100 +-
 internal/workflow/document_test.go                 |   881 +-
 internal/workflow/typeversion.go                   |   159 +
 internal/workflow/typeversion_openapi.go           |    28 +
 internal/workflow/typeversion_test.go              |   125 +
 nodes/ai.go                                        |    64 +-
 nodes/ai_test.go                                   |    28 +-
 nodes/annotation.go                                |    62 +
 nodes/bindings_test.go                             |   126 +
 nodes/code.go                                      |    28 +-
 nodes/code_test.go                                 |    68 +-
 nodes/core.go                                      |    49 +-
 nodes/database.go                                  |    14 +-
 nodes/database_test.go                             |     8 +-
 nodes/executors.go                                 |    29 +-
 nodes/executors_test.go                            |   113 +
 nodes/http.go                                      |   193 +-
 nodes/http_test.go                                 |   231 +-
 nodes/loop.go                                      |   245 +
 nodes/presentation_test.go                         |    60 +
 nodes/routing.go                                   |    23 +
 nodes/unsupported.go                               |   116 +-
 nodes/webhook.go                                   |    47 +-
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
 packs/waha/waha_test.go                            |  1193 ++
 schemas/workflow-v1.schema.json                    |     2 +-
 scripts/corpus-sync.sh                             |   225 +
 sdk/src/generated/models.ts                        |   457 +-
 third_party/waha/LICENSE                           |    19 +
 third_party/waha/PROVENANCE.md                     |    57 +
 third_party/waha/openapi-202409.json               |  8129 ++++++++++++++
 third_party/waha/openapi-202502.json               | 11084 +++++++++++++++++++
 .../lib/api/generated/credentials/credentials.ts   |    96 +-
 .../models/{unsupported.ts => activationNotice.ts} |    10 +-
 .../lib/api/generated/models/activationResource.ts |    23 +
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
 .../lib/api/generated/models/getNodeIconParams.ts  |    19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |    15 +
 web/src/lib/api/generated/models/importIssue.ts    |    20 +
 .../api/generated/models/importIssueSeverity.ts    |    19 +
 .../generated/models/importedWorkflowResource.ts   |     7 +-
 web/src/lib/api/generated/models/index.ts          |    27 +-
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
 .../lib/api/generated/models/propertyDefinition.ts |    11 +
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
 .../workflow-editor/property-field.svelte          |   138 +-
 .../workflow-editor/workflow-editor.svelte         |     4 +-
 web/src/lib/workflow-editor/credentials.ts         |    41 +-
 web/src/lib/workflow-editor/document.test.ts       |    10 +-
 web/src/lib/workflow-editor/document.ts            |     4 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |     2 +-
 web/src/lib/workflow-editor/execution.test.ts      |    72 +-
 web/src/lib/workflow-editor/execution.ts           |    86 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |    43 +
 web/src/lib/workflow-editor/node-visual.test.ts    |   163 +-
 web/src/lib/workflow-editor/node-visual.ts         |   206 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   179 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |    17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    26 +
 290 files changed, 62577 insertions(+), 1152 deletions(-)
```
