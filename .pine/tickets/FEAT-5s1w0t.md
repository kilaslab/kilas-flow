---
id: FEAT-5s1w0t
title: Extend the node property model to the kinds real nodes need
status: todo
priority: high
labels:
    - registry
    - metadata
    - editor
deps:
    - FEAT-qcm5ec
parent: EPIC-m42s3g
phase: p2
created: "2026-09-05T05:00:48Z"
updated: "2026-09-05T05:00:48Z"
---

## Scope

`node.PropertyKind` in `internal/node/registry.go` has six values: `string`, `number`, `boolean`, `select`, `keyValue`, `conditions`. They were enough for seventeen hand-written nodes. They cannot express the parameter surface of a generated WAHA pack, a Telegram node, or an AI chat model. There is no repeated field, no nested group, no free-form JSON editor, no date picker, no explanatory notice, no password masking, no multi-line text, no numeric bounds. A node that needs any of those has to either fake it with a `keyValue` bag — which throws away every label, type and validation — or not exist.

The gap is structural, not cosmetic. `PropertyDefinition.Options` is `[]PropertyOption{Label, Value}`, a flat list of selectable strings. n8n's `INodeProperties.options` is overloaded to hold three different things at once: selectable options, nested properties (for `collection`), and named collections of properties (for `fixedCollection`). Copying that overload into Go would produce a field whose meaning depends on the sibling `kind` and whose JSON schema is a union the generated TypeScript client cannot express usefully. The kinds must be added with distinct, typed carriers instead.

Add the kinds the next two phases actually consume: `options` and `multiOptions` for single and multi select, `collection` for an optional group of fields, `fixedCollection` for a repeatable named group, `notice` for read-only guidance in the panel, `json` for a raw JSON editor, `dateTime`, and a `typeOptions` bag carrying `password`, `multiline`/`rows`, `minValue`/`maxValue`, `numberPrecision`, `multipleValues` and `multipleValueButtonText`.

One semantic must be carried across exactly, because getting it wrong silently corrupts every imported node: under `typeOptions.multipleValues`, n8n's `default` describes **one element**, not the collection. A property with `multipleValues: true` and `default: {}` defaults to an empty list whose *elements* look like `{}` — it does not default to `{}`. This also breaks `requiredParameters` in `internal/node/registry.go`, which treats any property carrying a non-nil `Default` as satisfiable and therefore not required. With a per-element default that reasoning is wrong: the element default says nothing about whether the list has any elements.

## Acceptance criteria

- [ ] `PropertyKind` gains `options`, `multiOptions`, `collection`, `fixedCollection`, `notice`, `json` and `dateTime`, and `knownPropertyKind` accepts exactly the documented set and nothing else.
- [ ] `PropertyDefinition` carries a `TypeOptions` bag supporting at least `password`, `rows`, `minValue`, `maxValue`, `numberPrecision`, `multipleValues` and `multipleValueButtonText`, with unknown keys rejected at registration rather than passed through.
- [ ] `collection` and `fixedCollection` carry nested `PropertyDefinition`s in their own typed fields — never overloaded onto `Options` — and `validateProperties` recurses into them, rejecting a duplicate key or an invalid kind at any depth.
- [ ] `cloneProperties` deep-copies nested definitions and type options; a test mutates a returned definition at depth and re-reads it from the registry unchanged.
- [ ] A property with `multipleValues: true` is treated as required-and-unsatisfied by `requiredParameters` even when it declares a default, and the field's documentation states that the default describes one element.
- [ ] `web/src/lib/components/workflow-editor/property-field.svelte` renders every new kind, and a kind it does not recognise degrades to a read-only JSON view with a named warning instead of rendering nothing.
- [ ] `web/pnpm generate:api:check` and `sdk/pnpm generate:types:check` pass against regenerated clients.

## Implementation Plan

`internal/node/registry.go` first: the kind constants, the `TypeOptions` type, the nested carriers, then `validateProperties` and `cloneProperties` made recursive. `validateProperties` currently checks key, label and kind at one level and de-duplicates keys with a flat `seen` map; recursion needs a per-level `seen`, not a shared one, or a `collection` whose inner field is named `value` will collide with an outer `value`.

Then `web/src/lib/components/workflow-editor/property-field.svelte`, which today is an `{:else if property.kind === …}` chain over the six existing kinds. Note the guard at the top: `expressionCapable` is `kind === 'string' || kind === 'number'`. Decide deliberately which new kinds may hold an expression — recommend adding `json` and `dateTime` and nothing else, since a checkbox, a select and a nested collection have no free-text surface to show a template in, and n8n reaches the same conclusion through `noDataExpression`.

**The `select` versus `options` decision.** KilasFlow calls it `select`; n8n calls it `options`. Keeping both is the worst outcome — two names for one control, and every generated pack has to remember which one this server speaks. Recommend renaming to `options` outright, in this ticket, and dropping `select`. The blast radius today is entirely inside this repo: five call sites in `nodes/webhook.go`, `nodes/database.go`, `nodes/http.go` and `nodes/core.go`, one branch in `property-field.svelte`, and the generated clients. After p3 ships a pack the rename becomes a compatibility break for someone else's node, so it is now or never. If the owner would rather not touch working node definitions, the fallback is to accept `options` as the canonical kind and keep `select` as a deprecated synonym that `validateDefinition` warns on — but do not let both survive past p3.

`notice` needs one non-obvious rule: it holds no value. It must never appear in `requiredParameters`, must never be written into a node's stored parameters, and the panel must not send an `onChange` for it. A `notice` that round-trips into the document will fail validation on the next save.

Leave `resourceLocator`, `resourceMapper`, `filter` and `assignmentCollection` out. They are large, they carry runtime behaviour rather than shape, and nothing before p4 needs them.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, section "p2 — Node metadata foundation", entry V2-p2-2.
- `internal/node/registry.go` — `PropertyKind`, `PropertyDefinition`, `validateProperties`, `cloneProperties`, `requiredParameters`.
- `web/src/lib/components/workflow-editor/property-field.svelte` — the kind chain and the `expressionCapable` guard.
- `nodes/webhook.go`, `nodes/database.go`, `nodes/http.go`, `nodes/core.go` — every current `PropertySelect` use.
- n8n 2.34.0 reference (read-only, outside this repo): `packages/workflow/src/interfaces.ts` — `NodePropertyTypes`, `INodePropertyTypeOptions`, `INodePropertyOptions`, `INodePropertyCollection`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 05, 11, 13 — the property kinds this phase must add, in use: `notice` blocks, multi-select chips, `Options`/`Additional Fields` collections with "+ Add Field". Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
