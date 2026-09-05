---
id: FEAT-68zzqs
title: Add the resource mapper property kind for column mapping
status: todo
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
updated: "2026-09-05T08:28:30Z"
---

## Scope

`node.PropertyKind` in `internal/node/registry.go` has no `resourceMapper`, and `knownPropertyKind` (`registry.go:263-270`) refuses any kind outside its `switch`, so the column-mapping control cannot be declared. V2-p2-2 deferred it: `.pine/tickets/FEAT-5s1w0t.md:48` reads "Leave `resourceLocator`, `resourceMapper`, `filter` and `assignmentCollection` out. They are large, they carry runtime behaviour rather than shape, and nothing before p4 needs them." An amendment now names this ticket the owner. V2-p2-10 delivers the locator that picks a table; nothing yet types the columns inside it.

The carrier is verifiable even though the node using it is not. `ResourceMapperValue` (`packages/workflow/src/interfaces.ts:4120-4127`) is `{mappingMode, value, matchingColumns, schema, attemptToConvertTypes, convertFieldsToString}`, and `ResourceMapperField` at 4046-4067 carries `id`, `displayName`, `defaultMatch`, `canBeUsedToMatch`, `required`, `display`, `type`, `removed`, `options`, `readOnly` and `defaultValue`. The roadmap's further detail — one `columns` mapper on the Postgres node from typeVersion 2.2, `mappingMode` of `autoMapInputData` or `defineBelow` — stands on the roadmap, since `packages/nodes-base/nodes/Postgres` is absent from the narrowed checkout.

What insert, update and upsert degrade to is `PropertyKeyValue`, used today for the Set node's `assignments` (`nodes/core.go:66-69`). That control is worse than it looks: `addKeyValue` writes `onChange({ ...objectValue, '': '' })` (`property-field.svelte:35-37`), so pressing Add field twice still yields one blank row, and `updateKeyValue` runs every keystroke through `parseValue`, a `JSON.parse` with a raw-string fallback (lines 78-84), so a cell typed `null` is stored as JSON null and one typed `true` as a boolean. No column type, no required flag, no matching column.

The schema cannot arrive over V2-p2-4 as written. `.pine/tickets/FEAT-whn5vb.md:29` pins that endpoint to returning "a list of `{label, value}`", which has nowhere to put a column's type, its nullability, or whether it may be used to match. A mapper fed from it renders every column as text and offers no matching-column selection.

This is the difference between a form a customer prefers and one they route around. Without the kind, V2-p4-9's Postgres operation set and V2-p9-10's Datastore write both present an untyped bag with no matching column — strictly worse than the raw SQL box they replace, and reason enough to keep writing SQL in a product bought to stop that.

## Acceptance criteria

- [ ] `PropertyKind` accepts `resourceMapper`, and a definition declaring one with no schema source is refused at registration, proven by a registry test asserting the named error.
- [ ] A stored mapper round-trips its mapping mode, mapped values and matching columns through save, reload and n8n export unchanged, proven by a document round-trip test.
- [ ] Under automatic mapping, a column missing from an incoming item is omitted from the write rather than sent as null, proven by an executor test over a two-item input with differing keys.
- [ ] More than one matching column may be selected, and an operation requiring a match with none selected fails validation naming the operation, proven by a validator test.
- [ ] A column the loaded schema marks required and the mapping leaves unset fails validation with that column named, proven by a test over a schema carrying one required and one optional column.
- [ ] The schema response carries type, required and match-eligibility per column, and a column of an unrecognised type renders as text with a named warning rather than disappearing from the form.
- [ ] `web/pnpm generate:api:check` and `sdk/pnpm generate:types:check` pass against regenerated clients, run by hand and the output recorded on this ticket.

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
