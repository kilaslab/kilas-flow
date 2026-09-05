---
id: FEAT-ed6wdy
title: Convert declarative n8n community nodes into packs
status: todo
priority: medium
labels:
    - packs
    - interop
deps:
    - FEAT-cwz4ac
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:59:05Z"
updated: "2026-09-05T11:59:05Z"
---

## Scope

`cmd/nodepackgen` proves the shape of this work for one input format. It reads an OpenAPI document plus a hand-written manifest and emits a pack, reproducing `@devlikeapro/n8n-openapi-node`'s naming exactly — `internal/nodepack/startcase.go` is a faithful reimplementation of lodash's `startCase` word splitter, because resource and operation values in real workflow JSON are a pure function of those rules and "one character out means every imported workflow silently selects nothing".

The same argument applies one level up. A large share of n8n community nodes are **declarative**: a `description` object with `properties`, `displayOptions` and `routing` blocks, and no `execute()` at all. `@devlikeapro/n8n-nodes-waha` is the proof — its action node is 124 OpenAPI-derived operations of declarative routing metadata, which is why the whole WAHA strategy in p3 works without a JavaScript runtime. Those nodes are expressible as KilasFlow packs, and today each one needs the WAHA treatment by hand: find the upstream OpenAPI document, write a manifest, generate.

Where an upstream spec exists, that is the better path and stays the recommendation. Where it does not — which is most community nodes, whose `description` object *is* the specification — there is no path at all.

**The licence question is genuine here and must be settled before implementation, not during it.** `.pine/memory/licensing.md` draws the line as format versus code, with the test "could you have derived it from observing the wire format?" A node's declarative `description` object is closer to that line than anything else in p10: the resource and operation strings, the parameter shapes and the routing metadata are all interoperability format in the sense the memory means, and reading them is how any second implementation of the format would work. But the object arrives inside a compiled JavaScript package that declares `n8n-workflow` as a peer dependency, and reading it mechanically is not the same act as reading a JSON document that was published as a specification. The memory's own precedent is `FEAT-7cg0cd`, which makes recording the licence position an acceptance criterion ahead of any code. This ticket does the same and stops if the answer is no.

The other half of the design is refusal. A programmatic node with a real `execute()` cannot be converted, and a converter that emits a partial pack for one is worse than a converter that refuses: the pack registers, the node appears in the catalogue, and it silently does the wrong thing at run time.

## Acceptance criteria

- [ ] The licence position is recorded in `.pine/memory/licensing.md` before implementation begins, and states exactly what may be read, what may not, and whether any converted output may be distributed.
- [ ] A declarative n8n community node converts into a pack that loads through V2-p10-15's loader and executes against the real service.
- [ ] Resource and operation values in the output match the source node's byte for byte, verified against a real workflow JSON that references them, since a mismatch means every imported workflow silently selects nothing.
- [ ] A node carrying a real `execute()` is refused with a diagnostic naming why, and no partial pack is written.
- [ ] Every routing feature the interpreter does not implement — `preSend`, function-form `postReceive`, unsupported pagination — is reported rather than dropped, and the operation carrying it is excluded rather than emitted broken.
- [ ] A coverage report names every operation, property and feature the conversion left out, following the `packs/waha/REPORT-*.md` convention.
- [ ] The converter is build-time tooling and is never on the server's boot path, so a source package change is a build failure and not a startup failure.
- [ ] No converted third-party bytes are committed to this repository unless their licence independently permits it, matching the rule that governs `third_party/waha/`.

## Implementation Plan

Do the licence work first and stop if it fails. Nothing below is worth writing against an unresolved position, and this is the one ticket in p10 where the answer might be no.

Assuming it proceeds, build it beside `cmd/nodepackgen` as a second generator sharing the same emitter, not as a second implementation. The pack writer, the cascade generation and the `startCase` rules are all common; only the reader differs. Two generators with two pack writers would drift, and the naming rules are precisely where drift is fatal.

The reader is the hard part and its difficulty should be scoped honestly. A node's `description` is a JavaScript object literal, sometimes assembled across files, sometimes built by helper functions. Recommend narrowing the first cut to nodes whose description is statically analysable, and refusing the rest with a clear diagnostic — the same discipline as refusing `execute()`. A converter that guesses at a dynamically constructed description produces a pack nobody can trust.

Map only what the interpreter implements. `internal/routing` covers `requestDefaults`, `routing.request`, `routing.send`, post-receive `rootProperty`/`setKeyValue`/`limit`, and offset pagination. Property kinds are the closed set in `internal/property/property.go`. Anything outside either set is a report entry, never a best-effort translation — `Decode` uses `DisallowUnknownFields` specifically so that a pack using an unimplemented feature fails loudly, and a converter that papers over the gap defeats it.

Credentials need explicit handling rather than passthrough. An n8n credential reference is `{id, name}` and is instance-local, which the importer already knows — `FEAT-...` in p3 records that it must be rebound rather than trusted or dropped. The converter emits a credential *type* requirement, and the operator binds a real credential afterwards.

One decision to make in the ticket and state in the output: whether the converted pack keeps the source node's type string or takes a KilasFlow-namespaced one. Keeping it makes imported workflows match without an importer mapping; changing it requires a mapping entry but keeps the catalogue coherent. The WAHA precedent chose namespaced types — `pack.waha`, with an importer mapping from `@devlikeapro/n8n-nodes-waha.WAHA` — and this should follow it, because `RegisterFrom` refuses the `kilasflow.` prefix for external sources and an unnamespaced third-party type string in the catalogue is a collision waiting to happen.

## References

- Roadmap plan, p10 section, entry V2-p10-18: `.pine/roadmap.md`.
- `.pine/memory/licensing.md` — the format-versus-code test, the two third-party exceptions, and the precedent that a licence position is recorded before implementation.
- `.pine/tickets/FEAT-7cg0cd.md` — V2-p8-2, whose first acceptance criterion is the licence position, and which owns the programmatic nodes this converter refuses.
- `cmd/nodepackgen/` — `main.go`, `generate.go`, `openapi.go`, the emitter to share.
- `internal/nodepack/startcase.go` — the lodash `startCase` reimplementation and why exactness matters.
- `internal/routing/routing.go` — the implemented subset that bounds what may be converted.
- `internal/property/property.go` — the closed property-kind set.
- `internal/nodepack/nodepack.go` — `Decode`'s `DisallowUnknownFields` and the `Provenance` block a converted pack should carry.
- `internal/interop/n8n/n8n.go` — the `mappings` table a converted node needs an entry in, and the WAHA precedent for namespaced types.
- `packs/waha/REPORT-202409.md` — the report convention.
- `third_party/waha/PROVENANCE.md` — the rule governing third-party bytes in this repository.
