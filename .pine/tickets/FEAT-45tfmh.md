---
id: FEAT-45tfmh
title: Add the resource locator property kind and an internal list-search seam
status: todo
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
updated: "2026-09-05T08:28:30Z"
---

## Scope

`node.PropertyKind` in `internal/node/registry.go` has six values — `string`, `number`, `boolean`, `select`, `keyValue`, `conditions` — and `knownPropertyKind` (`registry.go:263-270`) refuses anything else at registration, so `resourceLocator` cannot be declared at all. V2-p2-2 deferred it to nobody: `.pine/tickets/FEAT-5s1w0t.md:48` reads "Leave `resourceLocator`, `resourceMapper`, `filter` and `assignmentCollection` out. They are large, they carry runtime behaviour rather than shape, and nothing before p4 needs them." An amendment now names this ticket the owner and folds `filter` in with it.

Every picker that should search a live list is free text instead. n8n gives a Postgres node's Schema and Table locators with From list, By Name and By ID modes, and V2-p9-10's Datastore node needs the same control — taken from the roadmap, since `packages/nodes-base/nodes/Postgres` is absent from the narrowed checkout until V2-p0-1 widens it. The carrier is verifiable: `INodeParameterResourceLocator` (`packages/workflow/src/interfaces.ts:1745-1752`) is `{__rl: true, mode, value, cachedResultName?, cachedResultUrl?, __regex?}`, and `ResourceLocatorModes` on line 1738 is `'id' | 'url' | 'list' | string`.

`PropertyConditions` is the nearest control that exists and it holds exactly one condition: `property-field.svelte` reads `conditions[0]` and writes `onChange([next])` (lines 39-51, 136-148), so the stored array never gains a second entry, over four operators — `equals`, `notEquals`, `exists`, `notExists`. A list mode and a condition row need the same plumbing, which is why the amendment folds them together.

V2-p2-4 serves options over an outbound HTTP descriptor through `internal/safehttp` and the credential store, and most of its criteria exist to defend that path; n8n's list mode is the same shape, `INodePropertyMode.search` being an `INodePropertyRouting` (`interfaces.ts:2075-2100`). Neither serves a datastore list or an `information_schema` lookup, which construct no request. And `permits` at `internal/api/middleware/embed.go:93-96` — the roadmap cites 94-97 — allows the whole `/node-types/` subtree on read scope without comparing `session.WorkflowID`, as the `/workflows/` case does at line 111.

The posture at stake is the embedded, white-label one. A host embeds an editor confined to one workflow, and an internal loader under a subtree blanket-allowed on read scope turns that editor into an enumeration surface for every datastore in the tenant and every table any credential in it reaches. The kind is a form control; the seam beneath it is a tenancy boundary.

## Acceptance criteria

- [ ] `PropertyKind` accepts `resourceLocator`, and a definition declaring one with no modes is refused at registration, proven by a registry test asserting the named error.
- [ ] A stored locator survives save, reload and n8n export with its mode, value and cached display name intact, proven by a document round-trip test over every declared mode.
- [ ] `expression.Resolve` returns a locator object unchanged and resolves only the expression held inside its value slot, proven by a resolver test covering each mode.
- [ ] The panel renders a locator as a mode switch plus that mode's control, and an unrecognised mode degrades to a read-only view naming it rather than rendering nothing.
- [ ] A property may declare an internal option source that constructs no outbound request, proven by a test asserting the `safehttp` client is never invoked for it.
- [ ] An embed session loading options for any workflow other than its own `WorkflowID` is refused, proven by a handler test that names both workflow identifiers in its assertions.
- [ ] A `conditions` property holds more than one condition row and round-trips them in order, proven by a component test that adds, reorders and removes a row.
- [ ] `web/pnpm generate:api:check` and `sdk/pnpm generate:types:check` pass against regenerated clients, run by hand and the output recorded on this ticket.

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
