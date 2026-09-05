---
id: FEAT-k3fmj1
title: Represent fractional and date-style node type versions
status: done
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:05:37Z"
updated: "2026-09-05T05:05:37Z"
---

## Scope

Node versions are integers everywhere in KilasFlow. `node.Definition.Version` is `int` (internal/node/registry.go:54), the registry is keyed on `definitionKey{nodeType string, version int}` (registry.go:72-75), `Registry.Get` and `Registry.Lookup` take `version int` (registry.go:104, 131), `workflow.Catalog.Lookup` declares the same signature (internal/workflow/compiler.go:11), `workflow.NodeDefinition.Version` is `int` (compiler.go:24), `workflow.Node.TypeVersion` is `int` (internal/workflow/document.go:42), and the persisted contract pins it: `schemas/workflow-v1.schema.json:49` declares `"typeVersion": {"type": "integer", "minimum": 1}`.

n8n does not use integers. Core nodes are on `4.2`, `3.4`, `1.1`, `2.4` — the export side of the importer already writes exactly those values (`exportTypeVersion` in internal/interop/n8n/n8n.go:117-158), so the fractional form is already a fact this codebase records. WAHA goes further and uses YYYYMM integers, `202409` and `202502`, which fit in an `int` but not in a scheme where version 1 is the only registered version.

The import side throws all of it away. `n8n.Node.TypeVersion` is a `float64` (n8n.go:48), and every mapping entry sets `kilasVersion: 1`, so `converted.TypeVersion = entry.kilasVersion` (n8n.go:260) collapses every imported node to version 1 regardless of what the source said. The only surviving trace is `originalTypeVersion` on an unsupported placeholder, and the diagnostic truncates with `TypeVersion: int(node.TypeVersion)` (n8n.go:253), so an n8n node on version 4.2 is reported as version 4.

That is tolerable while exactly one version of each node exists. It stops being tolerable the moment a node has two versions with different parameter shapes, which is the entire premise of the node-pack work: Set v2 and Set v3.4 take different parameters, and WAHA's `202409` and `202502` have different event lists. Without a version that survives import, an imported node is silently configured against the wrong schema.

## Acceptance criteria

- [x] A node type version is representable as a fractional value end to end — registry key, definition, catalog lookup, compiled IR, persisted document, JSON schema and API contract — with no truncation at any boundary.
- [x] A YYYYMM version such as `202502` registers, saves, compiles and round-trips unchanged.
- [x] Importing an n8n node preserves its source `typeVersion` rather than replacing it with 1, and the diagnostic reports the exact version, `4.2` not `4`.
- [x] A node type can declare a default version, and a document that names a version the registry does not have resolves per a documented rule rather than failing with "version 1 is not registered".
- [x] Two versions of one node type can be registered simultaneously and a document selects between them by version.
- [x] Every workflow saved before this change loads and compiles unchanged, and the existing `ErrorUnknownVersion` diagnostic still distinguishes an unknown version from an unknown type.

## Implementation Plan

Choose the representation first, and do not choose `float64`. A float map key invites `4.2 != 4.2000000000000002` and makes registry lookups nondeterministic in a way that will surface once and be miserable to find. Two safe options: a normalized decimal string (`"4.2"`, `"202502"`) or a struct of major and minor integers. Recommendation: a small named type wrapping major and minor `int`, with `MarshalJSON`/`UnmarshalJSON` that read and write JSON numbers so the wire format stays `4.2` and stays compatible with both n8n and today's documents. It is comparable, it is a valid map key, and it makes "is this version at least 3.0?" a real question rather than a float comparison.

Then widen in dependency order: `internal/node/registry.go` (`Definition.Version`, `definitionKey`, `Register`, `Get`, `List`, `Lookup`, `HasType`, `validateDefinition`'s `Version < 1` check), then `internal/workflow/compiler.go` (`Catalog`, `TypeCatalog`, `NodeDefinition`, the lookup and its two error messages), then `internal/workflow/document.go` (`Node.TypeVersion`, the `TypeVersion < 1` validation), then `schemas/workflow-v1.schema.json`, then every `Version: 1` literal in `nodes/`. Compiling after each step keeps the change reviewable; doing it in one sweep produces a diff nobody can check.

Default version and dispatch are the new behaviour, not just a widening. Add `DefaultVersion` to the definition set so a document that omits `typeVersion`, or names one that does not exist, resolves to a declared default. The rule needs stating: recommend resolving to the highest registered version that is less than or equal to the requested one, and failing when none is, because it matches how n8n treats an older workflow against a newer node and it never silently upgrades a workflow into a parameter shape it was not written for.

Two traps. `Definition` is serialized directly as the `/api/v1/node-types` payload, so changing `Version`'s JSON representation is an API change even though the wire value looks the same — run `pnpm generate:api` in `web/` and `pnpm generate:types` in `sdk/`, and check the generated TypeScript still types it as a number. And `schemas/workflow-v1.schema.json` says `"type": "integer"`; every existing persisted document satisfies both the old and new schema, but a document written after this change will not satisfy the old one, so the schema version and any host validating against it need attention.

Finally the importer: replace `kilasVersion: 1` with the real target version per mapping, stop discarding `node.TypeVersion`, and fix the `int(node.TypeVersion)` truncation in the unsupported diagnostic.

## References

- Roadmap plan, p1 section, entry V2-p1-11: `.pine/roadmap.md`.
- `.pine/roadmap.md` — entry V2-p3-3, the WAHA YYYYMM versions that depend on it.
- `internal/node/registry.go` — `Definition.Version`, `definitionKey`, `Register`, `Get`, `List`, `Lookup`, `validateDefinition`.
- `internal/workflow/compiler.go` — `Catalog`, `TypeCatalog`, `NodeDefinition`, the version lookup and `ErrorUnknownVersion`.
- `internal/workflow/document.go`, `schemas/workflow-v1.schema.json` — the persisted contract.
- `internal/interop/n8n/n8n.go` — `mapping.kilasVersion`, `exportTypeVersion`, `int(node.TypeVersion)`.
- `internal/api/handlers/nodes.go` — the handler that serves `Definition` as the node-types payload.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 04 — the NDV footer states the resolved version in words: "AI Agent node version 3.1 (Latest)". Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

## Outcome

### The representation

`workflow.TypeVersion` is a fixed-point decimal held as a scaled `int64`
(six decimal places), not a float and not two integers.

Not a float, for the reason the ticket gives: `4.2 != 4.2000000000000002` makes
registry lookups nondeterministic once in a very long while, which is miserable
to find. `TestTypeVersionIsASafeMapKey` pins that the same decimal is always the
same key.

Not two integers either, which the ticket's recommendation would have been. A
major/minor pair orders `4.15` above `4.2`, because it compares minors as
integers when the fraction is a decimal. `TestTypeVersionTreatsTheFractionAsADecimal`
pins the right ordering and that `4.20` and `4.2` are the same version.

The field is unexported deliberately. A named integer type would let
`Version: 1` keep compiling while meaning `0.000001`; a struct makes the
compiler name all 104 sites, which is what made a sweep this wide safe.

### Resolution rule

`Registry.Resolve` picks the **highest registered version less than or equal to
the requested one**; an unset request takes the newest registered version; and
it fails when every registered version is higher.

Resolving upward would silently run a workflow written for version 2 against
version 3's parameter shape — a behaviour change disguised as a lookup.
Downward can only ever give a workflow the shape it was written for or an older
one, and refusing outright when even the oldest registered version is newer says
plainly that this installation cannot run this workflow.

This is also what makes the import correct. An imported node keeps n8n's own
`typeVersion` — Set at `3.4`, WAHA at `202502` — and resolves to whatever
KilasFlow has today, landing on the right shape the moment that shape is
registered, with no document rewrite.

### Boundaries widened

Registry key, definition, catalog lookup, compiled IR, persisted document, JSON
schema and the API contract. The IR now records the version that **resolved**
rather than the one requested, so a reader of an execution can tell which
parameter shape actually ran.

The importer no longer discards `node.TypeVersion`, and the
`int(node.TypeVersion)` truncation in the unsupported diagnostic is gone — a
node on `4.2` was reported as version `4`, which is a different node with a
different parameter shape, so the diagnostic pointed at the wrong one.

### Three things the ticket did not anticipate

1. **The OpenAPI generator publishes a Go struct as an object.** Every workflow
   POST began failing with `expected object at body.nodes[0].typeVersion`.
   `TypeVersion` implements `huma.SchemaProvider` and declares itself a JSON
   number, which is the one place `internal/workflow` knows about the HTTP
   layer — a real cost, taken deliberately, because registering the mapping
   where the API is assembled would put it away from the type and let the next
   exposure silently publish an object again. Regeneration confirmed the
   TypeScript stays `number` in both `web/` and `sdk/`; the only diff is the
   added description.

2. **`TestCompileDistinguishesUnknownNodeVersion` stopped testing what it
   claimed.** It uses a map test double whose `Lookup` demands an exact match,
   so it still passes — but the real registry now resolves downward, and that
   scenario succeeds in production. The test is kept and its comment corrected
   to say it exercises the compiler's error branching, with two new tests
   covering the registry's actual policy: a request for an unregistered newer
   version compiles, and a request below everything registered still raises
   `ErrorUnknownVersion`.

3. **The licence guardrail false-positived on this ticket's own tests.** Its
   `n8n-nodes-waha` marker matched `@devlikeapro/n8n-nodes-waha.WAHA`, which is a
   node **type** string — format fact that appears in real workflow JSON and must
   be nameable. The marker is removed and the reason recorded in place; a path to
   the clone is still caught, because reaching it from a build input needs an
   absolute path and those are matched.

`typeVersion` may now be omitted, which resolves to the registered default, so
`ValidateDraft` no longer rejects it as non-positive. A negative or malformed
version cannot reach that check, because `TypeVersion` refuses to decode from
one.

The corpus baseline is unchanged, which is the compatibility result: every
document that compiled before still compiles, and nothing silently improved.
