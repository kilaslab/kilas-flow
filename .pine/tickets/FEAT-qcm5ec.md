---
id: FEAT-qcm5ec
title: Add presentation metadata to the node definition
status: done
priority: high
labels:
    - registry
    - metadata
    - editor
deps:
    - FEAT-k3fmj1
parent: EPIC-m42s3g
phase: p2
created: "2026-09-05T05:00:10Z"
updated: "2026-09-05T05:00:10Z"
---

## Scope

`internal/node.Definition` describes what a node *is* — type, version, display name, description, category, ports, parameters, shared settings — and nothing about how it looks. Everything visual therefore lives in the SPA, hardcoded against KilasFlow's own seventeen node types. A node the server adds without a matching frontend entry arrives on the canvas as a grey `Box` glyph, with the muted-foreground accent and no subtitle. That is tolerable while the catalogue is a closed list written in this repo. It stops being tolerable the moment p3 generates a WAHA pack of 124 operations that nobody will hand-write frontend entries for.

The category field is also doing work it was never designed for. `Category` is a panel-grouping string — "Triggers", "Core", "AI", "Database", "Imported" — and `web/src/lib/components/workflow-editor/node-picker.svelte` decides whether a node may start a workflow by comparing `definition.category !== 'Triggers'`. Behaviour is being inferred from a display label. n8n keeps the two apart: `group` is a behavioural array (`input`, `output`, `organization`, `schedule`, `transform`, `trigger`) and `codex.categories`/`codex.subcategories` drive the panel. KilasFlow needs the same split, because an imported node carries n8n's `group` and nothing that maps onto our category strings.

This ticket adds the presentation half of the definition: `Icon` with light and dark variants, an accent colour, `Group` as a behavioural multi-value field, a `Subtitle` template, `DocumentationURL`, and a codex-style block carrying categories, subcategories and search aliases. It does not build the icon route or delete the frontend maps — that is the ticket that follows. It establishes the fields those changes consume.

The trap is the API contract. `internal/api/handlers/nodes.go` returns `NodeTypesOutput{Body []node.Definition}`, so `node.Definition` *is* the `/api/v1/node-types` response schema — there is no DTO in between. Every field added here is an OpenAPI change, and both generated clients are checked in: `web/pnpm generate:api` (orval, from a dumped spec via `scripts/openapi-spec.mjs`) and `sdk/pnpm generate:types`. Skip either and `pnpm generate:api:check` or `pnpm generate:types:check` fails in CI.

## Acceptance criteria

- [x] `node.Definition` carries `Icon` (light and dark variants), `IconColor`, `Group`, `Subtitle`, `DocumentationURL` and a codex block with categories, subcategories and aliases, all serialized in `/api/v1/node-types`.
- [x] `Group` is a behavioural, multi-value field independent of `Category`; `validateDefinition` rejects a definition that declares no group and rejects any group value outside the documented set.
- [x] Every built-in definition in `nodes/*.go` declares a group, an icon and an accent; the node picker's trigger filter reads the group, not the category string.
- [x] `cloneDefinition` deep-copies every new slice and map field, proven by a test that mutates a returned definition and re-reads it from the registry unchanged.
- [x] `web/pnpm generate:api:check` and `sdk/pnpm generate:types:check` both pass against the regenerated clients, and the generated `Definition` model carries the new fields.
- [x] A definition may omit every presentation field and still register; the API then returns the field absent rather than an empty string, so a client can tell "unset" from "set to nothing".
- [x] The subtitle template dialect is documented on the field and covered by a test that renders one against a node's stored parameters.

## Implementation Plan

Start in `internal/node/registry.go`. Add the fields to `Definition`, extend `validateDefinition` to check `Group`, and extend `cloneDefinition`/`cloneProperties` for anything newly added — the existing clone machinery exists precisely because the registry hands callers a copy, and a new `[]string` or nested struct that skips it silently reintroduces aliasing. Then walk `nodes/core.go`, `nodes/webhook.go`, `nodes/http.go`, `nodes/database.go`, `nodes/ai.go`, `nodes/code.go` and `nodes/unsupported.go` and fill the fields in. Regenerate both clients last, in one commit, so the spec and the checked-in clients never disagree.

Two design decisions remain open.

**Icon representation.** The choice is between an opaque icon identifier the client resolves, and a URL the server serves bytes from. Recommend both, discriminated by prefix: `builtin:<lucide-name>` for first-party nodes, which keeps today's tree-shaken lucide imports and ships no new bytes, and a server-served path for pack and generated nodes that bring their own SVG. The `Icon` field then holds `{light, dark string}` where each value is one of those two forms, and the next ticket implements the route that answers the second form. Picking URL-only would force an asset pipeline for seventeen glyphs that already exist as components; picking identifier-only leaves a generated WAHA pack with no way to ship its own artwork.

**Subtitle dialect.** n8n's `subtitle` is an `=`-prefixed expression evaluated client-side against the node's parameters. KilasFlow already has an explicit expression marker and a restricted grammar in `internal/expression`, and the server cannot evaluate a subtitle anyway because the editor's parameters are unsaved. Recommend a `{{ }}` template restricted to a single new root, `$parameter.<key>`, rendered by the SPA with the same soft-undefined behaviour p1-10 introduces, and rejected by `validateDefinition` if it references any other root. That keeps the subtitle a template rather than a second expression dialect, and keeps an unconfigured node showing nothing instead of a placeholder — which is what the current hand-written switch already does deliberately.

Do not touch `Version`; widening it to a float is p1-11's job and doing it here will collide.

## References

- Roadmap plan, p2 section, entry V2-p2-1: `.pine/roadmap.md`.
- `internal/node/registry.go` — `Definition`, `validateDefinition`, `cloneDefinition`.
- `internal/api/handlers/nodes.go` — `NodeTypesOutput` serializes `node.Definition` directly.
- `web/src/lib/components/workflow-editor/node-picker.svelte` — the `definition.category !== 'Triggers'` filter.
- `web/src/lib/workflow-editor/node-visual.ts` — the `ICONS` and `ACCENTS` maps this ticket makes redundant.
- `web/package.json` / `sdk/package.json` — `generate:api`, `generate:api:check`, `generate:types`, `generate:types:check`.
- n8n 2.34.0 reference (read-only, outside this repo): `packages/workflow/src/interfaces.ts` — `INodeTypeBaseDescription` (icon, iconColor, iconUrl, group, documentationUrl, subtitle, codex), `NodeGroupType`, `CodexData`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 02, 04, 09, 10, 12 — the node creator root categories with their description lines, the derived Triggers/Actions counts, and the same node appearing under a second subcategory when used as a tool. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

## Outcome

### The split that mattered most

`Group` is behavioural and multi-valued; `Category` stays a panel caption. The
picker decided whether a node could start a workflow by comparing
`definition.category !== 'Triggers'` — behaviour inferred from a display string,
so a node filed anywhere else could never be a trigger however it behaved. It
reads the group now.

The set is closed and enforced at registration, so a typo cannot quietly produce
a node the picker files nowhere.
`TestTriggerGroupIsBehaviouralNotACategoryLabel` asserts both directions: the
three real triggers are in the group, and a transform is not — however it is
captioned.

One correction while filling the definitions in: the schedule trigger is
`{trigger, schedule}`, not `schedule` alone. It is genuinely both, and the
multi-value field exists precisely so it does not have to choose.

### Icons

Both forms, discriminated by prefix, as recommended. `builtin:<name>` names a
glyph the SPA already imports, so seventeen first-party nodes ship no new bytes;
anything else is a path the server answers with a pack's own artwork. Choosing
one form only would either force an asset pipeline for glyphs that already exist
as components, or leave a generated pack unable to ship artwork at all.

### Subtitle

A `{{ $parameter.key }}` template and nothing else, refused at registration if
it names any other root. The editor renders it against parameters the user has
not saved, so it cannot reach run-time data even in principle — enforcing that
here stops a definition shipping a subtitle the editor will silently fail to
render, and stops the dialect growing into a second expression language.

### Deep copy

`cloneDefinition` copies the group slice, the icon pointer and the whole codex
block including its nested map. `TestRegistryDeepCopiesPresentationFields`
mutates every reference type a caller is handed and re-reads from the registry —
aliasing here would be invisible until something mutated what it was given.

### The API contract

`node.Definition` *is* the `/api/v1/node-types` response schema, so every field
added is an OpenAPI change. Both clients were regenerated and both drift checks
pass.

Nine frontend fixtures needed the new required field, which is the contract
change being visible rather than a problem: `group` is required, so a client
constructing a `Definition` must say what the node does.

### Held to scope

The icon route is not built and the frontend's hardcoded visual maps are not
deleted — that is the ticket that follows. `Version` was not touched.
