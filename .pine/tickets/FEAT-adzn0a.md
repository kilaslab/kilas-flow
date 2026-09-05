---
id: FEAT-adzn0a
title: Tag registry entries by source and prove executor bindings
status: done
priority: high
labels:
    - registry
    - metadata
    - editor
deps:
    - FEAT-qcm5ec
parent: EPIC-m42s3g
phase: p2
created: "2026-09-05T05:03:52Z"
updated: "2026-09-05T05:03:52Z"
---

## Scope

KilasFlow has two registries and neither knows where its entries came from. `internal/node.Registry` keys definitions by `definitionKey{nodeType string, version int}` and refuses to replace an existing key; `internal/engine.Registry` in `internal/engine/runner.go` maps opaque executor ID strings to `Executor` implementations and likewise refuses a duplicate. Both are assembled at composition in `cmd/kilasflow/main.go` — `nodes.RegisterAll(nodeRegistry)` and `nodes.RegisterExecutors(executorRegistry, …)` — and both are read-only for the life of the process.

That immutability is right and should stay. What is missing is provenance. Once p3 loads a generated WAHA pack and p8 optionally adds a sidecar, three kinds of definition coexist and nothing distinguishes them: an API client cannot tell a first-party node from a generated one, an operator cannot tell what a pack added, and a pack that registers `kilasflow.httpRequest` would either shadow a built-in or fail with a bare "already registered" error depending purely on load order. Because both registries are already string-keyed and source-agnostic, this is mostly a loading-order and validation change rather than a redesign.

The second half of this ticket closes a hole that is live today. A `node.Definition` binds to an implementation through `ExecutorID`, an opaque string checked only for non-emptiness by `validateDefinition`. Nothing ever asserts that the string resolves. `nodes.RegisterAll` and `nodes.RegisterExecutors` are called together in exactly one place — `cmd/kilasflow/main.go` — and never in a test: every `nodes/*_test.go` calls `RegisterAll` alone, and only `nodes/ai_test.go` also builds an executor registry, without cross-checking it. So a definition whose `ExecutorID` names an executor that was never registered compiles, passes `go vet`, passes the whole suite, activates a workflow, and fails at the moment a customer's webhook arrives. `engine.Registry.Lookup` is already exported with a doc comment saying it exists so a test can run the exact binding the engine would — that test was never written.

## Acceptance criteria

- [x] `node.Definition` carries a source tag of `builtin`, `pack` or `sidecar`, set by the registration path rather than by the definition's author, and returned by `/api/v1/node-types`.
- [x] Namespacing is enforced at registration: only built-in registration may claim the `kilasflow.` prefix, and a pack registering into it fails with the offending type named.
- [x] Conflict precedence is explicit and tested: a built-in always wins over a pack for the same type and version, the loser is refused rather than silently dropped, and the refusal names both sources.
- [x] A test builds both registries exactly as `cmd/kilasflow/main.go` does and asserts that every registered definition's `ExecutorID` resolves through `engine.Registry.Lookup`.
- [x] The same test asserts the reverse for built-ins: every registered executor ID is referenced by at least one definition, so a renamed node cannot leave dead executor code behind.
- [x] Registration order is deterministic and documented — built-ins first, then packs in a stable order — so two runs of the same binary produce the same catalogue.
- [x] `web/pnpm generate:api:check` and `sdk/pnpm generate:types:check` pass against regenerated clients carrying the new field.

## Implementation Plan

Do the binding test first. It is the smallest change here and the only one that fixes a defect that already exists: a new test file in `nodes/` that calls `nodes.RegisterAll` and `nodes.RegisterExecutors` against fresh registries with the same stub dependencies `nodes/ai_test.go` already constructs (`localPolicy()`, `sqlGuard()`, `ai.NewLoopRuntime()`), then walks `registry.List()` and asserts `executors.Lookup(definition.ExecutorID)` for each. Land it before touching anything else, so the rest of the ticket is refactoring under a net rather than over one.

Then the source tag. Keep `Register` as the built-in path and add a distinct registration entry point for packs, so the source cannot be spoofed by a definition that sets its own field — the tag is a property of *how* something was registered, not of what it claims. Precedence is easiest to get right by loading in order and refusing on collision: built-ins register first, a pack colliding with an existing key is refused with both sources named, and two packs colliding are refused the same way. Resist "last wins" — a catalogue that depends on load order is one that changes when a directory listing changes.

The reverse binding check needs a decision. An executor registered with no definition pointing at it is dead code today, but a pack may legitimately register executors it exposes under several definitions, and a future sidecar may register a generic executor before its definitions arrive. Recommend asserting the reverse direction for built-ins only, where the invariant genuinely holds, and leaving it unasserted for packs rather than weakening it to a warning nobody reads.

Note for whoever picks this up: `validateDefinition` already refuses an empty `ExecutorID` and a duplicate type/version, and `cloneDefinition` copies on both `Get` and `List`. Extend those rather than adding a parallel validation path — the registry's guarantee is that a caller can never mutate what another caller sees, and a new field that skips `cloneDefinition` quietly breaks it.

## References

- Roadmap plan, p2 section, entry V2-p2-7: `.pine/roadmap.md`.
- `internal/node/registry.go` — `Registry`, `definitionKey`, `Register`, `validateDefinition`, `cloneDefinition`.
- `internal/engine/runner.go` — `Registry`, `Register`, `Lookup` and its doc comment about testing the exact binding.
- `nodes/core.go` — `RegisterAll` and the seventeen built-in definitions.
- `nodes/executors.go` — `RegisterExecutors` and the executor ID map.
- `cmd/kilasflow/main.go` — the only place both registries are built together today.
- `nodes/ai_test.go` — the existing stub dependencies the new test can reuse.

## Outcome

### The binding test first

Landed before anything else, as the plan instructed, so the rest was refactoring
under a net rather than over one. It builds both registries exactly as
`cmd/kilasflow/main.go` does — a test assembling a different catalogue would
prove something about the test rather than about what the server runs.

It fixes a defect that already existed: a definition naming an executor nobody
registered failed at **run** time, on the first item to reach that node, in
whatever workflow a customer happened to be running. Nothing checked it.

A third binding turned out to need the same treatment and was not in the
acceptance criteria: `LifecycleID`, added by FEAT-91as16. A trigger declaring a
hook nobody registered activates and silently never registers with its remote
service, which is worse than failing because the workflow looks live.
`TestEveryDeclaredLifecycleIsBound` covers it.

### The reverse direction

Asserted for built-ins only, as recommended. An executor nobody points at is
dead code left by a rename — but a pack may expose one executor under several
definitions and a sidecar may register a generic executor before its definitions
arrive, so the invariant genuinely does not hold there. Weakening it to a
warning nobody reads would be worse than scoping it to where it is true.

### The source tag

Set by the **registration path**, never read from the definition, and a test
registers a definition that lies about its own source to prove the claim is
ignored. A pack able to declare itself built-in would inherit the built-in
namespace and win every precedence contest, so the tag has to be a property of
how something was registered rather than of what it claims.

`Register` stays the built-in path; `RegisterFrom` is the pack and sidecar path
and refuses `builtin` outright.

### Namespace and precedence

Only built-in registration may claim `kilasflow.`. A pack shadowing — or being
mistaken for — a node this project ships is a supply-chain problem rather than a
naming one, and the refusal names the offending type.

Collisions are **refused**, never resolved last-wins, and the error names both
sources. Last-wins would make the catalogue depend on load order, so it would
change when a directory listing did. Two cases are tested: a pack cannot
displace a built-in (and the built-in is checked to be untouched afterwards),
and two non-built-in sources colliding are refused with both named.

### Determinism

`RegisterAll`'s order is the literal order of its list, documented on the
function, and `TestRegistrationOrderIsDeterministic` snapshots the catalogue six
times and compares. A catalogue that depended on map iteration would change
between runs for no visible reason, and a diff of the node-types response would
be noise rather than signal.
