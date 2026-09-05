---
id: FEAT-68zzqs
title: Add the resource mapper property kind for column mapping
status: done
priority: high
labels:
    - registry
    - metadata
    - editor
deps:
    - FEAT-45tfmh
parent: EPIC-m42s3g
phase: p2
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T14:22:29Z"
---

## Scope

`node.PropertyKind` in `internal/node/registry.go` has no `resourceMapper`, and `knownPropertyKind` (`registry.go:263-270`) refuses any kind outside its `switch`, so the column-mapping control cannot be declared. V2-p2-2 deferred it: `.pine/tickets/FEAT-5s1w0t.md:48` reads "Leave `resourceLocator`, `resourceMapper`, `filter` and `assignmentCollection` out. They are large, they carry runtime behaviour rather than shape, and nothing before p4 needs them." An amendment now names this ticket the owner. V2-p2-10 delivers the locator that picks a table; nothing yet types the columns inside it.

The carrier is verifiable even though the node using it is not. `ResourceMapperValue` (`packages/workflow/src/interfaces.ts:4120-4127`) is `{mappingMode, value, matchingColumns, schema, attemptToConvertTypes, convertFieldsToString}`, and `ResourceMapperField` at 4046-4067 carries `id`, `displayName`, `defaultMatch`, `canBeUsedToMatch`, `required`, `display`, `type`, `removed`, `options`, `readOnly` and `defaultValue`. The roadmap's further detail — one `columns` mapper on the Postgres node from typeVersion 2.2, `mappingMode` of `autoMapInputData` or `defineBelow` — stands on the roadmap, since `packages/nodes-base/nodes/Postgres` is absent from the narrowed checkout.

What insert, update and upsert degrade to is `PropertyKeyValue`, used today for the Set node's `assignments` (`nodes/core.go:66-69`). That control is worse than it looks: `addKeyValue` writes `onChange({ ...objectValue, '': '' })` (`property-field.svelte:35-37`), so pressing Add field twice still yields one blank row, and `updateKeyValue` runs every keystroke through `parseValue`, a `JSON.parse` with a raw-string fallback (lines 78-84), so a cell typed `null` is stored as JSON null and one typed `true` as a boolean. No column type, no required flag, no matching column.

The schema cannot arrive over V2-p2-4 as written. `.pine/tickets/FEAT-whn5vb.md:29` pins that endpoint to returning "a list of `{label, value}`", which has nowhere to put a column's type, its nullability, or whether it may be used to match. A mapper fed from it renders every column as text and offers no matching-column selection.

This is the difference between a form a customer prefers and one they route around. Without the kind, V2-p4-9's Postgres operation set and V2-p9-10's Datastore write both present an untyped bag with no matching column — strictly worse than the raw SQL box they replace, and reason enough to keep writing SQL in a product bought to stop that.

## Acceptance criteria

- [x] `PropertyKind` accepts `resourceMapper`, and a definition declaring one with no schema source is refused at registration, proven by a registry test asserting the named error.
- [x] A stored mapper round-trips its mapping mode, mapped values and matching columns through save, reload and n8n export unchanged, proven by a document round-trip test.
- [x] Under automatic mapping, a column missing from an incoming item is omitted from the write rather than sent as null, proven by a test over the shared builder every node will call.
- [x] More than one matching column may be selected, and an operation requiring a match with none selected fails validation naming the operation, proven by a validator test.
- [x] A column the loaded schema marks required and the mapping leaves unset fails validation with that column named, proven by a test over a schema carrying one required and one optional column.
- [x] The schema response carries type, required and match-eligibility per column, and a column of an unrecognised type renders as text with a named warning rather than disappearing from the form.
- [x] `web/pnpm generate:api:check` and `sdk/pnpm generate:types:check` pass against regenerated clients, run by hand and the output recorded below.

## Outcome

### The schema response, settled before the kind

As the plan directed, because the kind is inert without a typed column list and two later tickets have to fill that shape. It is a **sibling endpoint**, `POST /node-types/{type}/load-schema`, not a widening of load-options: that endpoint's committed criterion fixes its result at `{label, value}`, orval has already generated that type, and a discriminated union in a response body is exactly the shape this codebase rejected for a property's `Options` on the grounds that the generated TypeScript cannot express it as better than `unknown`.

The sibling **reuses** the request body, the registry lookup, the credential path and the embed bound — through `declaredProperty` and `scopeFor`, extracted from load-options rather than copied. Two endpoints answering for the same node with two gates is how two gates drift apart.

Each column carries id, display name, type, required, match eligibility, default-match and read-only. The response is **uncached** where an option list is cached for thirty seconds: a mapping validated against a stale column set would refuse a column that exists or accept one that no longer does.

### The trap, and what it decides

Automatic mapping builds the write from the **intersection of the item's keys and the schema**, never from the schema alone. Building it from the schema turns a column the item does not carry into an explicit null, and on an update that silently blanks a column the user never touched — no error, no diagnostic, and the damage visible only in the customer's data. The mirror mistake, sending every key the item carries, fails loudly at the database on the first unknown column, and loud is the one this chooses.

The **dropped keys are returned rather than discarded**, so an unmapped field is a diagnostic instead of merely absent.

### Where required-ness lives

In the loaded schema, not in the property — so `ValidateMapping` is called by an executor after parameters resolve, never at registration, which has nothing to check against. Three rules, each tested: an operation that identifies rows needs at least one matching column; a matching column must exist and be eligible; a required, writable column left unset names itself. **A required column used as the match satisfies its own required-ness**, because it identifies the row rather than supplying it. Automatic mapping is not checked here at all, since required-ness is only knowable per item and is checked as the write is built.

More than one matching column is allowed: a composite key is a key.

### The stored copy

n8n persists a copy of the schema inside the value, and this matches it — an imported mapper carries one and dropping it would make export lossy. It is treated **strictly as display data**: the executor re-reads the live schema, because a copy taken when the node was last opened has no authority over a table that has changed since. The editor keeps the stored copy when the live one has not loaded, so a saved node's form is not empty on first render and a keystroke does not blank the copy.

### The control

From `design-refs/n8n-v2/29`: a Mapping Column Mode select above the columns. Automatic mapping renders a **read-only summary of what will be sent** rather than a form nobody fills in; manual mapping renders one row per writable column with the widget its type implies. Matching columns are a multi-select drawn from the eligible ones, and a column used as a match drops out of the value list, because it identifies the row rather than supplying it.

**A column of an unrecognised type renders as text with a warning beside it.** Dropping it from the form would read as "this table has no such column", which is a worse lie than "we are not sure what this one is" — and the type survives the API response unblanked so the editor can say which one it was.

### Clients

```
$ cd web && pnpm generate:api:check   # clean
$ cd sdk && pnpm generate:types:check # clean
```

New models: `MapperColumn`, `LoadSchemaResource`, `ResourceMapperDeclaration`; `PropertyDefinition` gained `mapper`. `pnpm check` 1324 files 0 errors; web vitest 180 passing, sdk vitest 23 passing.

### What this leaves

No shipped node declares a mapper yet — the database nodes get theirs in p4-9 and the Datastore in p9-10 — so the kind ships with its runtime rules implemented and tested as shared functions those tickets call, rather than as metadata with the behaviour still to be written twice. `requiredParameters` is untouched: it treats a non-nil default as satisfied, and a mapper declares no default, so it is already reported as unconfigured.

## Implementation Plan

Settle the schema response shape before the property kind, because the kind is inert without a typed column list and two later tickets have to fill that shape: V2-p4-8's `information_schema` loaders and V2-p9-2's datastore catalogue. Getting the response wrong means rewriting both. Model it on `ResourceMapperFields` — a `fields` array plus an optional empty-fields notice — and carry per column the identifier, display name, type, required flag, match eligibility and default-match flag.

**Named decision.** That response either widens V2-p2-4's load-options result into a union or lands as a sibling operation. Recommend a sibling `POST /api/v1/node-types/{type}/load-schema` reusing that endpoint's request body, registry validation, credential resolution, TTL cache and embed bound. Reject widening load-options: its committed acceptance criterion fixes the result at `{label, value}`, orval has already generated that type, and a discriminated union in a response body is precisely the shape V2-p2-2 rejected for `Options` on the grounds that the generated TypeScript client cannot express it usefully.

Then the Go carrier in `internal/node`, and the control in `property-field.svelte` beside the kinds V2-p2-2 added. The mode switch drives the form: automatic mapping renders a read-only summary of what will be sent, manual mapping renders one row per column with the widget its type implies. Matching columns are a multi-select drawn from the fields whose match eligibility is set, and they are read-only as values, since a matching column identifies a row rather than supplying one.

The trap is what automatic mapping does with a column an item does not carry. Building the write from the schema rather than from the item's own keys means an absent column becomes an explicit null, and on an update that silently blanks a column the user never touched — no error, no diagnostic, and the damage is visible only in the customer's data. The mirror mistake, sending every key the item carries, fails loudly at the database on the first unknown column and is therefore the safe one. Build the column set from the intersection of the item's keys and the schema, and record the dropped keys as a node-run diagnostic so an unmapped field is visible rather than merely absent.

`requiredParameters` in `registry.go:278-286` needs the same correction V2-p2-2 makes for repeated values: it treats any property carrying a non-nil `Default` as satisfied, so a mapper defaulting to an empty manual mapping would never be reported as unconfigured. Required-ness for this kind lives per column in the loaded schema, not in the property, so the executor must re-check it after `expression.Resolve` rather than trusting registration-time validation.

One decision to settle. n8n persists a copy of the schema inside the value (`ResourceMapperValue.schema`), and the temptation is to diverge and store only the user's choices. Recommend matching n8n and persisting the copy — an imported mapper carries it, and dropping it makes export lossy — while treating it strictly as display data that the executor never trusts, re-reading the live schema at run time instead. What would reopen this is a datastore whose column catalogue is authoritative and cannot drift, where the stored copy is pure duplication and the export concern does not apply.

## References

- Roadmap plan, p2 section, entry V2-p2-11: `.pine/roadmap.md`.
- `.pine/tickets/FEAT-5s1w0t.md` — V2-p2-2, which defers this kind and now names this ticket its owner, and the `multipleValues` criterion this ticket mirrors.
- `.pine/tickets/FEAT-whn5vb.md` — V2-p2-4, whose `{label, value}` result shape this ticket cannot reuse but whose validation and cache it should.
- `internal/node/registry.go` — `PropertyKind`, `knownPropertyKind`, `validateProperties`, `requiredParameters`.
- `web/src/lib/components/workflow-editor/property-field.svelte` — the `keyValue` control this kind replaces: `addKeyValue`, `updateKeyValue`, `parseValue`.
- `nodes/core.go` — the Set node's `assignments`, the untyped key/value bag in production use today.
- `nodes/database.go` — the SQL node's parameters, which V2-p4-9 replaces with mapped columns.
- `internal/sqlnode/sqlnode.go` — `Connection.Query` and `Result`, the seam an `information_schema` loader reads through.
- n8n 2.34.0 reference (read-only, outside this repo): `packages/workflow/src/interfaces.ts` — `ResourceMapperValue`, `ResourceMapperField`, `ResourceMapperFields`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 27 and 29 — **Mapping Column Mode** with its two options and their help text: *Map Each Column Manually — Set the value for each column*, and *Map Automatically — Look for incoming data that matches the columns in Data table*. Captured from a live local n8n 2.x instance; gitignored, never vendored.

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
 .pine/tickets/FEAT-45tfmh.md                       |  438 +++
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
 .pine/tickets/FEAT-68zzqs.md                       |  110 +
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
 internal/api/handlers/nodes.go                     |  427 ++-
 internal/api/handlers/workflows.go                 |  118 +-
 internal/api/middleware/embed.go                   |   10 +-
 internal/api/node_types_test.go                    |  228 ++
 internal/api/routes.go                             |   27 +-
 internal/api/server.go                             |   30 +-
 internal/api/workflows_test.go                     |  136 +-
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
 internal/loadoptions/loadoptions.go                |  357 ++
 internal/loadoptions/loadoptions_test.go           |  453 +++
 internal/loadoptions/workflows.go                  |   64 +
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  711 +++-
 internal/node/registry_test.go                     |  816 ++++-
 internal/nodepack/nodepack.go                      |  424 +++
 internal/nodepack/startcase.go                     |  136 +
 internal/nodepack/startcase_test.go                |   82 +
 internal/nodepack/trigger.go                       |  433 +++
 internal/property/loader.go                        |   92 +
 internal/property/locator_test.go                  |  116 +
 internal/property/property.go                      |  459 +++
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
 sdk/src/generated/models.ts                        |  720 +++-
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
 web/src/lib/api/generated/models/index.ts          |   27 +
 .../api/generated/models/loadOptionsInputBody.ts   |   22 +
 .../models/loadOptionsInputBodyParameters.ts       |    9 +
 .../api/generated/models/loadOptionsResource.ts    |   17 +
 web/src/lib/api/generated/models/nodeCodex.ts      |   16 +
 .../api/generated/models/nodeCodexSubcategories.ts |    9 +
 web/src/lib/api/generated/models/nodeIcon.ts       |   12 +
 web/src/lib/api/generated/models/option.ts         |   12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |   21 +
 web/src/lib/api/generated/models/port.ts           |    9 +-
 .../lib/api/generated/models/propertyDefinition.ts |   19 +
 web/src/lib/api/generated/models/propertyGroup.ts  |   15 +
 web/src/lib/api/generated/models/propertyMode.ts   |   19 +
 .../api/generated/models/testCredentialResource.ts |   17 +
 .../lib/api/generated/models/testPayloadBody.ts    |   22 +
 .../api/generated/models/testPayloadBodyFields.ts  |   12 +
 web/src/lib/api/generated/models/typeOptions.ts    |   17 +
 web/src/lib/api/generated/models/visibility.ts     |   15 +
 .../lib/api/generated/models/webhookDeclaration.ts |   15 +
 web/src/lib/api/generated/nodes/nodes.ts           |  443 ++-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    3 +-
 .../components/workflow-editor/canvas-node.svelte  |   33 +-
 .../workflow-editor/execution-canvas-node.svelte   |   20 +-
 .../components/workflow-editor/node-icon.svelte    |   10 +-
 .../components/workflow-editor/node-picker.svelte  |    8 +-
 .../workflow-editor/properties-panel.svelte        |   61 +-
 .../workflow-editor/property-field.svelte          |  493 ++-
 .../workflow-editor/workflow-editor.svelte         |    4 +-
 web/src/lib/workflow-editor/assignments.test.ts    |   82 +
 web/src/lib/workflow-editor/assignments.ts         |   89 +
 web/src/lib/workflow-editor/conditions.test.ts     |   82 +
 web/src/lib/workflow-editor/conditions.ts          |   80 +
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
 .../lib/workflow-editor/resource-locator.test.ts   |  112 +
 web/src/lib/workflow-editor/resource-locator.ts    |  100 +
 web/src/lib/workflow-editor/visibility.test.ts     |   35 +
 web/src/lib/workflow-editor/visibility.ts          |  199 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   26 +
 315 files changed, 57835 insertions(+), 1591 deletions(-)
```
