---
id: FEAT-xqqjqv
title: Add the assignment collection property kind for Set v3
status: todo
priority: high
labels:
    - nodes
    - parity
    - sql
deps:
    - FEAT-5s1w0t
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T08:28:30Z"
---

## Scope

`node.PropertyKind` at `internal/node/registry.go:16-21` has six values — `string`, `number`, `boolean`, `select`, `keyValue`, `conditions` — and `assignmentCollection` is not among them. `knownPropertyKind` at line 263 rejects anything else, so the kind cannot be registered at all until it is added there, and `PropertyDefinition` at line 38 carries nothing that could hold an assignment's declared type: `Options` is `[]PropertyOption{Label, Value}`, a flat list of selectable strings.

The node that needs it is already in the tree. `setNode()` at `nodes/core.go:57` declares one parameter, `assignments`, with `Kind: node.PropertyKeyValue`, and both `executeSet` at `nodes/executors.go:58` and `validateSetConfiguration` at line 111 read it as `map[string]any`. A Go map has no order and no per-entry type, so two assignments to the same name are impossible and the order the user typed is lost the first time the document is saved.

The importer already pays for that. `setToKilas` at `internal/interop/n8n/parameters.go:80` reads n8n's ordered `assignments.assignments` array and collapses each `{id, name, type, value}` entry to `assignments[name] = value`, discarding the type. `setToN8N` at line 135 rebuilds the array from `sortedKeys` and hardcodes `"type":  "string"` at line 143, so a boolean or number assignment returns to n8n as a string and the field order comes back alphabetical rather than as authored.

The deferral is resolved on paper but owned by nobody in code. FEAT-5s1w0t (V2-p2-2) explicitly leaves `assignmentCollection` out and its Implementation Plan now names this entry as the owner — the roadmap still describes it as deferred to nobody, which the p9 planning amendment has since fixed. FEAT-jwhdsy, the data-shaping parity ticket, requires typed ordered assignments and lists p2-2's `fixedCollection` as its dependency, which is a different control: `fixedCollection` is a repeatable group of arbitrary properties, while an assignment collection is a fixed `{name, type, value}` row whose value editor depends on the sibling type.

This matters because Set is the single most common node in the corpus after the trigger. Every imported workflow that shapes data at all lands on a control that cannot express what its author wrote, and the loss is invisible until the exported file is opened somewhere else.

## Acceptance criteria

- [ ] `PropertyKind` gains `assignmentCollection` and `knownPropertyKind` accepts it while still rejecting an unknown kind, proven by a registry test covering both directions.
- [ ] An `assignmentCollection` property carries an ordered list of `{id, name, type, value}` entries in its own typed field rather than overloaded onto `Options`, and registration rejects an entry with an empty name.
- [ ] The accepted `type` values are exactly those n8n's Set v3 emits, and an entry carrying a type outside that set is refused at registration rather than silently treated as a string.
- [ ] `cloneProperties` deep-copies an assignment collection's default; a test mutates the entries of a returned definition and re-reads the registry unchanged.
- [ ] `property-field.svelte` renders the kind as repeatable typed rows and never falls through to the text input, proven by a component test asserting the value reaching `onChange` is still an array.
- [ ] Importing an n8n Set v3 node and exporting it again preserves assignment order and each entry's declared type, proven by a fixture comparing the arrays element by element.
- [ ] `/api/v1/node-types` carries the new kind and the regenerated clients match, proven by `pnpm generate:api:check` in `web/` and `pnpm generate:types:check` in `sdk/` run by hand and recorded in the ticket's work evidence.
- [ ] A Set node stored before this ticket, whose `assignments` is a plain object, still loads, still validates and still produces the same items, proven by a fixture using the old shape.

## Implementation Plan

Settle the Go shape before anything renders it. `node.Definition` is serialised directly as the `/api/v1/node-types` payload by `internal/api/handlers/nodes.go`, so the carrier's field names and JSON tags are an API contract from the moment they exist; every later adjustment is a client regeneration in two packages. Add the kind, the typed carrier and its validation as one change, then move outward.

Recommend a dedicated `Assignments` carrier on `PropertyDefinition` holding ordered entries, each with a name, a declared type and a default value. Reject overloading `Options` to hold them — the same overload FEAT-5s1w0t rejects for `collection` and `fixedCollection`, and for the same reason: a field whose meaning depends on the sibling `kind` produces a JSON schema the generated TypeScript client cannot express as anything better than `unknown`.

Recommend implementing it as its own kind rather than as a preset over `fixedCollection`. The two look alike from a distance and differ where it counts: an assignment's value control is chosen by its own `type` field, row by row, which a generic repeatable group cannot express without the panel special-casing it anyway. Building the special case into the kind keeps it in one place and lets `validateProperties` say something specific about a malformed entry.

The trap is the Svelte fallthrough. `web/src/lib/components/workflow-editor/property-field.svelte` ends its kind chain with a bare `{:else}` at line 149 that renders any unrecognised kind as a plain text input bound to `stringValue`, which is `String(value)`. An array of assignment objects therefore displays as `[object Object]`, and the first keystroke writes that literal string back through `onChange`. Nothing throws, nothing warns, and the node's parameters are destroyed on first edit. Adding the kind in Go without adding the branch is worse than not adding it at all.

Keep expression evaluation out of scope, but record what is found. `executeSet` at `nodes/executors.go:58` never calls `expression.Resolve`, unlike `nodes/database.go:204`, `nodes/http.go:181`, `nodes/webhook.go:235` and `nodes/ai.go:243` — so a Set assignment holding a template is written into the item literally. That is a runtime defect belonging to FEAT-jwhdsy, which owns Set's semantics; this ticket delivers the shape the panel and the document need, and must not quietly fix the executor on the way past.

**Whether `setNode()` switches in this ticket.** Recommend adding the kind and leaving `setNode()` on `PropertyKeyValue` until FEAT-jwhdsy changes `executeSet` with it. A definition advertising typed ordered assignments while the executor still reads `map[string]any` is a node whose editor and runtime disagree, and the disagreement is silent — the panel accepts a typed entry the runner then drops. What would reopen it is the two tickets being taken together or FEAT-jwhdsy landing first, in which case the kind and the switch belong in one change and the interim shape is never built.

## References

- Roadmap plan, p4 section, entry V2-p4-13: `.pine/roadmap.md`.
- `internal/node/registry.go` — `PropertyKind` at 16 to 21, `PropertyDefinition` at 38, `knownPropertyKind` at 263, `cloneProperties` at 296 and `requiredParameters` at 278.
- `nodes/core.go` — `setNode` at line 57 and its single `PropertyKeyValue` assignments parameter.
- `nodes/executors.go` — `executeSet` at 58 and `validateSetConfiguration` at 111, both reading `map[string]any`, and the absent `expression.Resolve` call.
- `internal/interop/n8n/parameters.go` — `setToKilas` at 80, `setToN8N` at 135 and the hardcoded `"type":  "string"` at 143.
- `web/src/lib/components/workflow-editor/property-field.svelte` — the kind chain, the `expressionCapable` guard at line 14, and the `{:else}` text-input fallthrough at 149.
- `internal/api/handlers/nodes.go` — `NodeTypesOutput`, which serialises `node.Definition` straight onto the wire.
- `.pine/tickets/FEAT-5s1w0t.md` — V2-p2-2, whose deferral paragraph names this entry as the owner.
- `.pine/tickets/FEAT-jwhdsy.md` — the data-shaping parity ticket that consumes the kind and owns Set's runtime semantics.
- n8n 2.34.0 reference checkout (read-only, outside this repo): `packages/nodes-base/nodes/Set/v2/SetV2.node.ts`, present in the sparse checkout, for the assignment entry shape and its type list.
