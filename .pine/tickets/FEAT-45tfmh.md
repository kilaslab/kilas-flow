---
id: FEAT-45tfmh
title: Add the resource locator property kind and an internal list-search seam
status: done
priority: high
labels:
    - registry
    - metadata
    - editor
deps:
    - FEAT-5s1w0t
    - FEAT-whn5vb
parent: EPIC-m42s3g
phase: p2
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T14:09:54Z"
---

## Scope

`node.PropertyKind` in `internal/node/registry.go` has six values — `string`, `number`, `boolean`, `select`, `keyValue`, `conditions` — and `knownPropertyKind` (`registry.go:263-270`) refuses anything else at registration, so `resourceLocator` cannot be declared at all. V2-p2-2 deferred it to nobody: `.pine/tickets/FEAT-5s1w0t.md:48` reads "Leave `resourceLocator`, `resourceMapper`, `filter` and `assignmentCollection` out. They are large, they carry runtime behaviour rather than shape, and nothing before p4 needs them." An amendment now names this ticket the owner and folds `filter` in with it.

Every picker that should search a live list is free text instead. n8n gives a Postgres node's Schema and Table locators with From list, By Name and By ID modes, and V2-p9-10's Datastore node needs the same control — taken from the roadmap, since `packages/nodes-base/nodes/Postgres` is absent from the narrowed checkout until V2-p0-1 widens it. The carrier is verifiable: `INodeParameterResourceLocator` (`packages/workflow/src/interfaces.ts:1745-1752`) is `{__rl: true, mode, value, cachedResultName?, cachedResultUrl?, __regex?}`, and `ResourceLocatorModes` on line 1738 is `'id' | 'url' | 'list' | string`.

`PropertyConditions` is the nearest control that exists and it holds exactly one condition: `property-field.svelte` reads `conditions[0]` and writes `onChange([next])` (lines 39-51, 136-148), so the stored array never gains a second entry, over four operators — `equals`, `notEquals`, `exists`, `notExists`. A list mode and a condition row need the same plumbing, which is why the amendment folds them together.

V2-p2-4 serves options over an outbound HTTP descriptor through `internal/safehttp` and the credential store, and most of its criteria exist to defend that path; n8n's list mode is the same shape, `INodePropertyMode.search` being an `INodePropertyRouting` (`interfaces.ts:2075-2100`). Neither serves a datastore list or an `information_schema` lookup, which construct no request. And `permits` at `internal/api/middleware/embed.go:93-96` — the roadmap cites 94-97 — allows the whole `/node-types/` subtree on read scope without comparing `session.WorkflowID`, as the `/workflows/` case does at line 111.

The posture at stake is the embedded, white-label one. A host embeds an editor confined to one workflow, and an internal loader under a subtree blanket-allowed on read scope turns that editor into an enumeration surface for every datastore in the tenant and every table any credential in it reaches. The kind is a form control; the seam beneath it is a tenancy boundary.

## Acceptance criteria

- [x] `PropertyKind` accepts `resourceLocator`, and a definition declaring one with no modes is refused at registration, proven by a registry test asserting the named error.
- [x] A stored locator survives save, reload and n8n export with its mode, value and cached display name intact, proven by a document round-trip test over every declared mode.
- [x] `expression.Resolve` returns a locator object unchanged and resolves only the expression held inside its value slot, proven by a resolver test covering each mode.
- [x] The panel renders a locator as a mode switch plus that mode's control, and an unrecognised mode degrades to a read-only view naming it rather than rendering nothing.
- [x] A property may declare an internal option source that constructs no outbound request, proven by a test asserting the `safehttp` client is never invoked for it.
- [x] An embed session loading options for any workflow other than its own `WorkflowID` is refused, proven by a handler test that names both workflow identifiers in its assertions.
- [x] A `conditions` property holds more than one condition row and round-trips them in order, proven by a component test that adds, reorders and removes a row.
- [x] `web/pnpm generate:api:check` and `sdk/pnpm generate:types:check` pass against regenerated clients, run by hand and the output recorded below.

## Outcome

### The kind, and the trap it is built around

`resourceLocator` stores a self-describing object carrying n8n's `__rl` sentinel — `{__rl, mode, value, cachedResultName}` — rather than a bare string with a sibling `…Mode` parameter. A locator is imported and exported far more often than it is authored, and the sibling form loses the pairing the moment a visibility rule hides one half of it. That form is exactly what n8n used *before* resource locators existed, and adopting it would have meant writing a lossy converter for every node that takes one.

**A mode may never be named `expression`, and registration refuses it.** The locator's stored value has `mode` and `value` keys, and so does the expression marker: a mode with that name would make the two indistinguishable, so `Resolve` would replace the whole locator with the evaluated string, the executor would receive a bare string where it expects an object, and nothing would report anything — the node would simply read an empty table name. An expression goes *inside* the value slot, where the existing recursion resolves it in place and the sentinel survives. Both halves are tested: the locator survives Resolve for every mode, and a marker in its value slot evaluates while the mode is kept.

`ReadLocator` accepts a bare string as a locator with no mode yet, because that is what a document written before this kind existed carries and refusing it would break every such node on load rather than where it matters.

### A real user, not a shape with no node

The Execute Sub-workflow node's `workflowId` is now a locator with **From list** and **By ID** modes, which is what it is in n8n — so the importer carries the shape across instead of flattening it, and the kind is exercised end to end rather than being metadata waiting for p4-9.

It arrives from n8n in **By ID** mode whatever mode it left in. A list selection there holds an ID from *that* instance, which this server's list will never contain, so a locator imported in list mode would render as an empty picker with an invisible value behind it — which reads as "no workflow chosen" rather than "the wrong workflow is chosen".

### The seam beneath it

The `internal` loader source already existed from FEAT-whn5vb; what it lacked was a user and a test. `loadoptions.Workflows` is the first, and a test now asserts the `safehttp` client is **never reached** — carrying an endpoint on the loader deliberately, so a fallback to the HTTP path would be caught rather than assumed absent.

A locator carries a loader **per mode**, not per property, because "from list" searches and "by ID" does not. The request says which mode is asking; the server takes the loader from the declared mode, never from the request. A mode that offers no list is a 422 rather than an empty answer, because "there is no list here" and "the list is empty" are different things.

**The tenancy boundary is the point of this ticket.** An internal loader constructs no request, so nothing the egress policy or the credential scoping defends applies to it, and `permits` allows the whole `/node-types/` subtree on read scope without reading a body. This handler is the only thing between an embedded editor and every workflow in the installation. A request naming another workflow is now **refused with both identifiers in the message**, rather than silently narrowed — answering it with the session's own list would tell the caller nothing about which one it got, which is the reading that turns a bug into a slow leak. The loader itself narrows again, so the two do not depend on each other being right.

### Conditions, which had a second row nobody could reach

The stored value was always an array and the control only ever wrote `[next]`, so a rule needing two conditions could be imported and could run but could not be edited without hand-writing JSON. Rows now add, reorder and remove, in a module with its own tests rather than inside the component.

Moving is **clamped, not wrapped**: order is meaningful here — the rules read top to bottom — and wrapping would send the last row to the top on a click meant to nudge it down.

### The panel

Taken from `design-refs/n8n-v2/28` and `35`: a narrow mode select beside the mode's own control, in one row, because the mode is a property of the value rather than a field above it. Switching modes keeps the value only when both sides take free text — carrying a typed name into a list mode would leave the control showing a selection that does not exist, while carrying an ID from By Name to By ID is exactly what the user wants.

**An unrecognised mode degrades to a read-only line naming it**, alongside a select that still shows it. A workflow saved against a newer node can carry a mode this build has never heard of, and rendering nothing reads as "this field is empty" rather than "this editor is older than this node".

### Clients

```
$ cd web && pnpm generate:api:check   # clean
$ cd sdk && pnpm generate:types:check # clean
```

Both regenerated: `PropertyMode` is a new model, `PropertyDefinition` gained `modes`, and the load-options body gained `mode`. `pnpm check` 1319 files 0 errors; web vitest 161 passing, sdk vitest 23 passing. The corpus is unchanged at 13/39 activatable and 3/39 runnable.

### What is left for the ticket that reopens this

`filter` is still a deferred kind, and the registry test that pins the closed set now lists `resourceLocator` and keeps `filter` in the deferred column. The database nodes' Schema and Table locators are p4-9's, and are unblocked by this.

## Implementation Plan

`internal/node/registry.go` comes first, before any endpoint or control, because three consumers read the shape and each will encode it independently if it lands late: the panel renders it, `internal/interop/n8n` maps it on import and export, and the executor reads the resolved value. Add the kind constant, a typed `Modes []PropertyMode` carrier on `PropertyDefinition` — never overloaded onto `Options`, for the reason V2-p2-2 already gives — and recurse `cloneProperties` and `validateProperties` into it.

**Named decision.** The stored value is either a self-describing object or a bare string with a sibling `…Mode` parameter. Recommend the object, carrying an `__rl` sentinel exactly as n8n does, because a locator is imported and exported far more often than it is authored, and a sibling parameter loses the pairing the moment `displayOptions` hides one half. Reject the sibling-parameter form explicitly: it is what n8n used before resource locators existed, and adopting it would mean writing a lossy converter in V2-p3-5 and V2-p4-9 that a sentinel-carrying object makes unnecessary.

The trap is the expression marker. `expression.IsExpression` matches any map whose `mode` is the string `"expression"` and whose `value` is a string, and `resolveValue` recurses through every map in the parameter tree. A locator that offers an expression mode using those two key names is therefore indistinguishable from the marker: `Resolve` replaces the whole locator object with the evaluated string, the executor receives a bare string where it expects `{mode, value}`, and nothing anywhere reports an error — the node simply reads an empty table name. `ResourceLocatorModes` being open (`… | string`) means n8n's own types would not stop it either. Never name a mode `expression`; put the expression marker inside the locator's `value` slot, where the existing recursion resolves it in place and the sentinel survives.

Add the option source as a discriminant on V2-p2-4's loader — `http` for today's descriptor, `internal` naming a handler registered in Go — rather than a second endpoint. A second endpoint reads cleaner and is the wrong choice: the panel would carry two fetch paths, the TTL cache would be duplicated, and the embed gate would have to be written twice, which is how two gates drift apart.

`permits` cannot do the workflow check. It is a switch over `strings.TrimPrefix(r.URL.Path, "/api/v1")` and the method, and it never reads a body, so a `POST` whose body names a workflow is invisible to it. Split the check the way the codebase already splits it for a single execution: the comment at `embed.go:141-145` states plainly that `permits` covers the scope while `handlers.RequireEmbedWorkflow` covers the identity. Narrow the `/node-types/` case to the catalogue read, and enforce `session.WorkflowID` inside the loader handler through `middleware.EmbedSessionFrom`.

One decision to settle now. Recommend folding the repeatable condition group into this ticket as V2-p2-2's amendment directs, since the single-condition control is already a defect and the two share their option-source plumbing. What would reopen it is V2-p4-9's WHERE builder needing per-operator value typing that the Datastore row filter does not — at that point the condition group becomes its own kind and this ticket keeps only the locator.

## References

- Roadmap plan, p2 section, entry V2-p2-10: `.pine/roadmap.md`.
- `internal/node/registry.go` — `PropertyKind`, `PropertyDefinition`, `knownPropertyKind`, `validateProperties`, `cloneProperties`.
- `.pine/tickets/FEAT-5s1w0t.md` — V2-p2-2, whose closing plan paragraph defers this kind and now names this ticket its owner.
- `.pine/tickets/FEAT-whn5vb.md` — V2-p2-4, the declarative loader this ticket gives an internal source kind and a workflow bound.
- `internal/expression/expression.go` — `IsExpression`, `resolveValue`, and the `mode`/`value` key constants the locator must not collide with.
- `web/src/lib/components/workflow-editor/property-field.svelte` — the kind chain, `expressionCapable`, `toggleExpression`, and the single-condition control.
- `web/src/lib/components/workflow-editor/properties-panel.svelte` — `isVisible`, the strict-equality filter a locator's mode gating passes through.
- `internal/api/middleware/embed.go` — `permits`, the blanket `/node-types/` case, and the `/workflows/` and `/executions/` cases that do bound by `WorkflowID`.
- `nodes/database.go` — the SQL node's parameters, which offer no schema or table field for a locator to replace.
- n8n 2.34.0 reference (read-only, outside this repo): `packages/workflow/src/interfaces.ts` — `INodeParameterResourceLocator`, `ResourceLocatorModes`, `INodePropertyMode`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 27-28 and 35 — the resource locator modes verbatim (**From list**, **By Name**, **By ID**), and the Postgres node's Schema and Table locators, whose dependent parameters stay hidden until the locator holds a value. Captured from a live local n8n 2.x instance; gitignored, never vendored.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `4816f2ed` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `cf72f8d0` — chore(pine): open phase p9 for the Datastore and route database parity to p4
- Files changed (base → working tree):

```
 .pine/MEMORY.md                                    |    2 +
 .pine/memory/code-node.md                          |   41 +
 .pine/memory/live-databases.md                     |   30 +
 .pine/roadmap.md                                   |  252 ++
 .pine/tickets/EPIC-m42s3g.md                       |   10 +-
 .pine/tickets/FEAT-0556ck.md                       |   66 +
 .pine/tickets/FEAT-0f87fn.md                       |  300 +-
 .pine/tickets/FEAT-12s0e5.md                       |   65 +
 .pine/tickets/FEAT-1axhdn.md                       |   65 +
 .pine/tickets/FEAT-1c70nt.md                       |   73 +
 .pine/tickets/FEAT-27km39.md                       |   71 +
 .pine/tickets/FEAT-2f68r8.md                       |   81 +-
 .pine/tickets/FEAT-2phs15.md                       |   68 +
 .pine/tickets/FEAT-3taswf.md                       |   67 +
 .pine/tickets/FEAT-3xqky1.md                       |   70 +
 .pine/tickets/FEAT-45tfmh.md                       |  117 +
 .pine/tickets/FEAT-48hreg.md                       |    6 +
 .pine/tickets/FEAT-4d0bje.md                       |   62 +
 .pine/tickets/FEAT-53fht8.md                       |   60 +
 .pine/tickets/FEAT-55v09k.md                       |   82 +-
 .pine/tickets/FEAT-5fhj6p.md                       |   69 +
 .pine/tickets/FEAT-5fv8gf.md                       |    3 +
 .pine/tickets/FEAT-5kfctc.md                       |   66 +
 .pine/tickets/FEAT-5kv1jq.md                       |   81 +-
 .pine/tickets/FEAT-5mvech.md                       |   72 +
 .pine/tickets/FEAT-5rvtzc.md                       |   91 +-
 .pine/tickets/FEAT-5s1w0t.md                       |   83 +-
 .pine/tickets/FEAT-5z37xh.md                       |   72 +
 .pine/tickets/FEAT-68zzqs.md                       |   65 +
 .pine/tickets/FEAT-6vfn3s.md                       |  354 +-
 .pine/tickets/FEAT-7tgasa.md                       |   61 +
 .pine/tickets/FEAT-8qyfh1.md                       |  452 ++-
 .pine/tickets/FEAT-8r9n21.md                       |  304 +-
 .pine/tickets/FEAT-91as16.md                       |   93 +-
 .pine/tickets/FEAT-9dqn7d.md                       |  422 +++
 .pine/tickets/FEAT-9knk67.md                       |   84 +-
 .pine/tickets/FEAT-adzn0a.md                       |   74 +-
 .pine/tickets/FEAT-agj52c.md                       |   64 +
 .pine/tickets/FEAT-az620p.md                       |  447 ++-
 .pine/tickets/FEAT-bp0ytb.md                       |  338 +-
 .pine/tickets/FEAT-bscygc.md                       |   62 +
 .pine/tickets/FEAT-cjpbe6.md                       |   70 +
 .pine/tickets/FEAT-cpdp8y.md                       |   70 +
 .pine/tickets/FEAT-cwz4ac.md                       |   66 +
 .pine/tickets/FEAT-cx3hq1.md                       |   71 +
 .pine/tickets/FEAT-czbzs6.md                       |   65 +
 .pine/tickets/FEAT-ddzk2k.md                       |    6 +-
 .pine/tickets/FEAT-de8d4c.md                       |   71 +
 .pine/tickets/FEAT-ed6wdy.md                       |   66 +
 .pine/tickets/FEAT-frvez8.md                       |   70 +
 .pine/tickets/FEAT-g6wrxm.md                       |   64 +
 .pine/tickets/FEAT-gg85se.md                       |   69 +
 .pine/tickets/FEAT-gxppx1.md                       |   71 +
 .pine/tickets/FEAT-jq84xk.md                       |   67 +
 .pine/tickets/FEAT-jwhdsy.md                       |  411 ++-
 .pine/tickets/FEAT-k9dwgn.md                       |   65 +
 .pine/tickets/FEAT-kwxxd0.md                       |   64 +
 .pine/tickets/FEAT-m94hhx.md                       |   60 +
 .pine/tickets/FEAT-n19dch.md                       |   66 +
 .pine/tickets/FEAT-n5fdz3.md                       |   69 +
 .pine/tickets/FEAT-nc6z9r.md                       |   68 +
 .pine/tickets/FEAT-nch9dg.md                       |   67 +
 .pine/tickets/FEAT-nrfg6e.md                       |   69 +
 .pine/tickets/FEAT-nrfz6m.md                       |   64 +
 .pine/tickets/FEAT-nxxbs5.md                       |   77 +
 .pine/tickets/FEAT-pd3p6x.md                       |   85 +-
 .pine/tickets/FEAT-pnbt4z.md                       |   91 +
 .pine/tickets/FEAT-ptyh9w.md                       |   65 +
 .pine/tickets/FEAT-q81bq4.md                       |  444 ++-
 .pine/tickets/FEAT-qcm5ec.md                       |   74 +-
 .pine/tickets/FEAT-qe6wb8.md                       |  342 +-
 .pine/tickets/FEAT-qfr9xe.md                       |  136 +
 .pine/tickets/FEAT-sar60r.md                       |   87 +-
 .pine/tickets/FEAT-sdjdh2.md                       |   33 +
 .pine/tickets/FEAT-sfy1tq.md                       |   63 +
 .pine/tickets/FEAT-snxxny.md                       |  409 +++
 .pine/tickets/FEAT-sp8cfm.md                       |  359 +-
 .pine/tickets/FEAT-ss44d9.md                       |   67 +
 .pine/tickets/FEAT-t26rt7.md                       |   65 +
 .pine/tickets/FEAT-v8k1tc.md                       |   88 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  395 +-
 .pine/tickets/FEAT-whn5vb.md                       |  101 +-
 .pine/tickets/FEAT-wkmv5e.md                       |   67 +
 .pine/tickets/FEAT-xeq6st.md                       |   68 +
 .pine/tickets/FEAT-xqqjqv.md                       |  327 ++
 .pine/tickets/FEAT-xr7ga9.md                       |   76 +
 .pine/tickets/FEAT-xx6p22.md                       |   62 +
 .pine/tickets/FEAT-ykyfbd.md                       |   72 +
 .pine/tickets/FEAT-yx0qt6.md                       |   71 +
 .pine/tickets/FEAT-za118x.md                       |   61 +
 .pine/tickets/FEAT-zmfsjd.md                       |   71 +
 .pine/tickets/FEAT-znm60y.md                       |  314 +-
 .pine/tickets/FEAT-ztxs5p.md                       |  345 +-
 Makefile                                           |   11 +
 README.md                                          |   33 +
 cmd/kilasflow/main.go                              |  186 +-
 cmd/nodepackgen/generate.go                        |  576 +++
 cmd/nodepackgen/generate_test.go                   |  374 ++
 cmd/nodepackgen/main.go                            |  165 +
 cmd/nodepackgen/openapi.go                         |  168 +
 cmd/nodepackgen/testdata/manifest.json             |   12 +
 cmd/nodepackgen/testdata/pack.golden.json          |  238 ++
 cmd/nodepackgen/testdata/report.golden.md          |   20 +
 cmd/nodepackgen/testdata/spec.json                 |   98 +
 config.example.yaml                                |   17 +
 internal/api/credentials_test.go                   |  357 ++
 internal/api/embed_test.go                         |   61 +-
 internal/api/handlers/credentials.go               |  298 ++
 internal/api/handlers/executions.go                |   40 +-
 internal/api/handlers/interop.go                   |    5 +-
 internal/api/handlers/nodes.go                     |  333 +-
 internal/api/handlers/workflows.go                 |  118 +-
 internal/api/middleware/embed.go                   |   10 +-
 internal/api/node_types_test.go                    |  156 +
 internal/api/routes.go                             |   27 +-
 internal/api/server.go                             |   30 +-
 internal/api/workflows_test.go                     |   75 +-
 internal/binary/binary.go                          |  212 ++
 internal/binary/binary_test.go                     |  210 ++
 internal/conditions/conditions.go                  |  542 +++
 internal/conditions/conditions_test.go             |  231 ++
 internal/conditions/doc.go                         |   26 +
 internal/config/config.go                          |  101 +-
 internal/config/config_test.go                     |   35 +
 internal/credentials/builtin.go                    |  153 +
 internal/credentials/credentials.go                |   98 +-
 internal/credentials/credentials_test.go           |  242 ++
 internal/credentials/registry.go                   |  340 ++
 internal/datetime/datetime_test.go                 |  134 +
 internal/datetime/doc.go                           |   15 +
 internal/datetime/format.go                        |  195 +
 internal/datetime/parse.go                         |  108 +
 internal/engine/authenticate.go                    |   95 +
 internal/engine/runner.go                          |  465 ++-
 internal/engine/runner_test.go                     |  579 ++-
 internal/engine/service.go                         |  318 +-
 internal/engine/service_test.go                    |   33 +-
 internal/engine/subworkflow_test.go                |  329 ++
 internal/engine/worker_test.go                     |    5 +
 internal/execution/records.go                      |   41 +-
 internal/expression/doc.go                         |   82 +-
 internal/expression/expression.go                  |  350 +-
 internal/expression/expression_test.go             |  374 +-
 internal/expression/functions.go                   |  219 ++
 internal/expression/roots.go                       |  125 +
 internal/expression/undefined.go                   |   22 +
 internal/interop/n8n/corpus/BASELINE.md            |   43 +-
 internal/interop/n8n/corpus/baseline.json          |  115 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |  107 +-
 internal/interop/n8n/n8n.go                        |  422 ++-
 internal/interop/n8n/n8n_test.go                   | 1160 +++++-
 internal/interop/n8n/parameters.go                 | 1437 +++++++-
 internal/loadoptions/loadoptions.go                |  353 ++
 internal/loadoptions/loadoptions_test.go           |  453 +++
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  698 +++-
 internal/node/registry_test.go                     |  742 +++-
 internal/nodepack/nodepack.go                      |  424 +++
 internal/nodepack/startcase.go                     |  136 +
 internal/nodepack/startcase_test.go                |   82 +
 internal/nodepack/trigger.go                       |  433 +++
 internal/property/loader.go                        |   92 +
 internal/property/property.go                      |  454 +++
 internal/property/testdata/visibility.json         |  163 +
 internal/property/visibility.go                    |  315 ++
 internal/property/visibility_test.go               |   77 +
 internal/repository/executions.go                  |  135 +
 internal/repository/models.go                      |   71 +-
 internal/repository/schedules.go                   |  140 +-
 internal/repository/webhooks.go                    |   17 +-
 internal/repository/workflows.go                   |   66 +-
 internal/routing/doc.go                            |   54 +
 internal/routing/executor.go                       |  571 +++
 internal/routing/request.go                        |  412 +++
 internal/routing/response.go                       |  177 +
 internal/routing/routing.go                        |  373 ++
 internal/routing/routing_test.go                   |  764 ++++
 internal/runcode/doc.go                            |   37 +-
 internal/scheduler/extract.go                      |   81 +
 internal/scheduler/item.go                         |   71 +
 internal/scheduler/rule.go                         |  321 ++
 internal/scheduler/rule_test.go                    |  278 ++
 internal/scheduler/scheduler.go                    |  139 +-
 internal/scheduler/scheduler_test.go               |  153 +
 internal/sqlnode/sqlnode.go                        |  305 +-
 internal/sqlnode/sqlnode_test.go                   |  106 +
 internal/webhook/export_test.go                    |   14 +
 internal/webhook/lifecycle.go                      |  304 ++
 internal/webhook/lifecycle_test.go                 |  229 ++
 internal/webhook/request_lifecycle.go              |  207 ++
 internal/webhook/shape.go                          |  248 ++
 internal/webhook/shape_test.go                     |  211 ++
 internal/webhook/webhook.go                        |  261 +-
 internal/webhook/webhook_test.go                   |  225 +-
 internal/workflow/compiler.go                      |  310 +-
 internal/workflow/compiler_test.go                 |   97 +
 internal/workflow/document.go                      |   81 +-
 internal/workflow/document_test.go                 |  360 +-
 internal/workflow/typeversion.go                   |   10 +
 nodes/ai.go                                        |   54 +-
 nodes/annotation.go                                |    3 +
 nodes/assignments.go                               |  180 +
 nodes/bindings_test.go                             |  129 +
 nodes/code.go                                      |   93 +-
 nodes/code_test.go                                 |  126 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  182 +-
 nodes/database.go                                  |  255 +-
 nodes/database_test.go                             |  534 ++-
 nodes/datetime.go                                  |  408 +++
 nodes/datetime_test.go                             |  274 ++
 nodes/executors.go                                 |  602 ++-
 nodes/executors_test.go                            |  480 +++
 nodes/flow.go                                      |  457 +++
 nodes/flow_test.go                                 |  464 +++
 nodes/http.go                                      |  195 +-
 nodes/http_test.go                                 |  225 ++
 nodes/jscode.go                                    |  172 +
 nodes/jscode_test.go                               |  100 +
 nodes/loop.go                                      |  245 ++
 nodes/presentation_test.go                         |   60 +
 nodes/routing.go                                   |   23 +
 nodes/subworkflow.go                               |  275 ++
 nodes/telegram.go                                  |  393 ++
 nodes/telegram_download.go                         |  243 ++
 nodes/telegram_lifecycle.go                        |  423 +++
 nodes/telegram_test.go                             |  610 ++++
 nodes/transform.go                                 |  745 ++++
 nodes/transform_test.go                            |  315 ++
 nodes/unsupported.go                               |    4 +
 nodes/wait.go                                      |  227 ++
 nodes/webhook.go                                   |  290 +-
 packs/telegram/README.md                           |   40 +
 packs/telegram/pack.json                           | 1119 ++++++
 packs/telegram/telegram.go                         |   58 +
 packs/telegram/telegram_test.go                    |  466 +++
 packs/waha/README.md                               |   32 +
 packs/waha/REPORT-202409.md                        |  100 +
 packs/waha/REPORT-202502.md                        |  128 +
 packs/waha/manifest-202409.json                    |  124 +
 packs/waha/manifest-202502.json                    |  124 +
 packs/waha/pack-202409.json                        | 2794 ++++++++++++++
 packs/waha/pack-202502.json                        | 3844 ++++++++++++++++++++
 packs/waha/pack-trigger-202409.json                |  124 +
 packs/waha/pack-trigger-202502.json                |  130 +
 packs/waha/waha.go                                 |  107 +
 packs/waha/waha_test.go                            | 1196 ++++++
 sdk/src/generated/models.ts                        |  632 +++-
 .../lib/api/generated/credentials/credentials.ts   |  199 +-
 .../lib/api/generated/models/activationNotice.ts   |   13 +
 .../lib/api/generated/models/activationResource.ts |   23 +
 web/src/lib/api/generated/models/assignment.ts     |   14 +
 web/src/lib/api/generated/models/condition.ts      |   14 +
 .../api/generated/models/credentialRequirement.ts  |   15 +
 web/src/lib/api/generated/models/definition.ts     |   17 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/executionResource.ts  |    2 +
 .../lib/api/generated/models/executionSummary.ts   |    2 +
 .../lib/api/generated/models/expressionGrammar.ts  |   16 +
 web/src/lib/api/generated/models/field.ts          |    1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |   19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |   15 +
 web/src/lib/api/generated/models/index.ts          |   24 +
 .../api/generated/models/loadOptionsInputBody.ts   |   22 +
 .../models/loadOptionsInputBodyParameters.ts       |    9 +
 .../api/generated/models/loadOptionsResource.ts    |   17 +
 web/src/lib/api/generated/models/nodeCodex.ts      |   16 +
 .../api/generated/models/nodeCodexSubcategories.ts |    9 +
 web/src/lib/api/generated/models/nodeIcon.ts       |   12 +
 web/src/lib/api/generated/models/option.ts         |   12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |   21 +
 web/src/lib/api/generated/models/port.ts           |    9 +-
 .../lib/api/generated/models/propertyDefinition.ts |   17 +
 web/src/lib/api/generated/models/propertyGroup.ts  |   15 +
 .../api/generated/models/testCredentialResource.ts |   17 +
 .../lib/api/generated/models/testPayloadBody.ts    |   22 +
 .../api/generated/models/testPayloadBodyFields.ts  |   12 +
 web/src/lib/api/generated/models/typeOptions.ts    |   17 +
 web/src/lib/api/generated/models/visibility.ts     |   15 +
 .../lib/api/generated/models/webhookDeclaration.ts |   15 +
 web/src/lib/api/generated/nodes/nodes.ts           |  342 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    3 +-
 .../components/workflow-editor/canvas-node.svelte  |   33 +-
 .../workflow-editor/execution-canvas-node.svelte   |   20 +-
 .../components/workflow-editor/node-icon.svelte    |   10 +-
 .../components/workflow-editor/node-picker.svelte  |    8 +-
 .../workflow-editor/properties-panel.svelte        |   51 +-
 .../workflow-editor/property-field.svelte          |  381 +-
 .../workflow-editor/workflow-editor.svelte         |    4 +-
 web/src/lib/workflow-editor/assignments.test.ts    |   82 +
 web/src/lib/workflow-editor/assignments.ts         |   89 +
 web/src/lib/workflow-editor/credentials.ts         |   41 +-
 web/src/lib/workflow-editor/document.test.ts       |   10 +-
 web/src/lib/workflow-editor/document.ts            |    4 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |    2 +-
 web/src/lib/workflow-editor/execution.test.ts      |   72 +-
 web/src/lib/workflow-editor/execution.ts           |   75 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   43 +
 .../lib/workflow-editor/fixed-collection.test.ts   |  131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |   75 +
 web/src/lib/workflow-editor/node-visual.test.ts    |  163 +-
 web/src/lib/workflow-editor/node-visual.ts         |  220 +-
 web/src/lib/workflow-editor/ports.test.ts          |   98 +-
 web/src/lib/workflow-editor/ports.ts               |   34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |   35 +
 web/src/lib/workflow-editor/visibility.ts          |  199 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   26 +
 308 files changed, 56257 insertions(+), 1591 deletions(-)
```
