---
id: FEAT-5z37xh
title: Cover every registered node type end to end
status: todo
priority: high
labels:
    - e2e
    - testing
deps:
    - FEAT-cx3hq1
parent: EPIC-m42s3g
phase: p11
created: "2026-09-05T12:03:20Z"
updated: "2026-09-05T12:03:20Z"
---

## Scope

`nodes/core.go`'s `RegisterAll` registers nineteen built-in definitions plus the unsupported capsule at four arities, and the two packs add `pack.telegram`, `pack.waha` and `pack.wahaTrigger` at two versions each. By the time p3 through p9 land, that catalogue grows by the flow-control, data-shaping, time, composition and database families, the AI cluster, and the Datastore node.

Not one of them is exercised through the editor by any automated test.

Go tests cover executors, and that is real coverage of behaviour. What it cannot cover is the path a user actually takes, which runs through metadata the server generates and the editor renders without knowing anything specific about the node. `node.Definition` is serialized directly as the `/api/v1/node-types` payload; the editor builds its parameter panel from `PropertyDefinition` kinds, `TypeOptions`, `displayOptions` visibility and dynamic option loaders. A node whose executor is perfect and whose property metadata is malformed is a node that passes every Go test and cannot be configured by a human.

The registry already proves part of this class of defect is real and undetectable. `nodes/bindings_test.go` exists because "forgetting an executor compiles, passes every test, and fails only at run time" — and it is deliberately scoped to built-ins, because a pack may expose one executor under several definitions. The metadata equivalent has no test at all.

The specific failures a per-node UI pass catches, none of which a Go test can:

- A property kind the editor has no renderer for, so the field is invisible and the node is unconfigurable.
- A `displayOptions` condition referencing a key that does not exist, so a field never appears.
- A dynamic options loader that errors, leaving an empty picker with no explanation.
- A required parameter with no default and no validation message, so the node fails at run time rather than at save.
- A node with no icon or credential mapping, which before p2-5's server-driven maps renders as a grey box with no credential picker.
- A subtitle template that reads something other than `$parameter`, which registration refuses — but only for nodes anyone thought to register in a test.

This is a coverage ticket, so its most important deliverable is not the tests but the mechanism that makes a missing test visible.

## Acceptance criteria

- [ ] Every node type in `/api/v1/node-types` is exercised at least once through the editor: placed on the canvas, configured, saved, and run or validated.
- [ ] A report lists node types with no end-to-end coverage, and the suite fails when a registered node type has neither a test nor an explicit, justified exclusion.
- [ ] Coverage is driven from the live `/api/v1/node-types` response rather than a hand-maintained list, so a node added by a later ticket appears as uncovered without anyone updating the suite.
- [ ] Every property kind in the closed `PropertyKind` set is rendered and edited by at least one test, independently of node coverage, since a kind may be added before any node uses it heavily.
- [ ] Each node's dynamic option loaders are exercised where it declares one, against the local stub rather than a third-party service.
- [ ] Trigger nodes are covered on their own terms — binding a webhook, receiving a delivery, and for self-registering triggers, the activation and deactivation lifecycle.
- [ ] A node that requires a credential is tested with the credential picker, and its absence produces the validation message a user would see rather than a run-time failure.
- [ ] Pack-sourced nodes are covered by the same mechanism as built-ins, and the report distinguishes `builtin`, `pack` and `sidecar` sources.

## Implementation Plan

Build the coverage report before writing tests. It is what turns this from a task that is done once into a property the project keeps: fetch `/api/v1/node-types`, compare against the set of node types the suite touched, and fail on the difference. Written afterwards it is documentation of a moment; written first it is a ratchet.

Do not write nineteen bespoke tests. Most of a node's editor behaviour is generic — place, open the parameter panel, fill each declared property according to its kind, save, assert no validation error — and that is a table-driven test parameterised by the definition the server returns. Reserve hand-written tests for the nodes with genuine behaviour: the triggers, the agent cluster, the database family, the Datastore node, and the unsupported capsule, whose correct behaviour is that it saves, opens and refuses to activate.

Sequence the work to match the phases rather than waiting for all of them. The mechanism plus coverage of what exists today can land immediately; each later phase's nodes are covered by the report the moment they register, which is the point of driving it from the live catalogue.

One thing to decide explicitly: what "exercised" means for a node that needs a third party. WAHA needs a WAHA server, Telegram needs a bot token, the database nodes need a database. Recommend three tiers, stated in the report so a green result is not read as more than it is — configured and validated in the editor, executed against a local stub, and executed against the real service, with the last reserved for the capstone suite in V2-p11-8.

The other decision worth stating: exclusions must carry a reason and be reviewed, not just listed. An exclusion list that accepts a bare node type will accumulate entries and the report will stop meaning anything.

## References

- Roadmap plan, p11 section, entry V2-p11-3: `.pine/roadmap.md`.
- `nodes/core.go` — `RegisterAll` and the nineteen built-in definitions.
- `nodes/bindings_test.go` — the existing invariant test and the reasoning about defects that compile and pass.
- `internal/node/registry.go` — `Definition`, `Source`, and `validateDefinition`'s rules.
- `internal/property/property.go` — the closed `PropertyKind` set every kind-coverage test enumerates.
- `internal/property/loader.go` — the option loaders to exercise against a stub.
- `internal/api/handlers/nodes.go` — `/api/v1/node-types`, the catalogue the report reads.
- `web/src/lib/components/workflow-editor/property-field.svelte` — the renderer whose gaps this suite finds.
- `nodes/unsupported.go` — the capsule whose correct behaviour is a refusal.
- `packs/telegram/pack.json`, `packs/waha/` — the pack-sourced nodes the report must treat identically.
