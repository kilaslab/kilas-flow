---
id: FEAT-znm60y
title: Generate node packs from an OpenAPI document
status: todo
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-8r9n21
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:00:24Z"
updated: "2026-09-05T05:00:24Z"
---

## Scope

WAHA's n8n package is not hand-written. It is produced by `@devlikeapro/n8n-openapi-node` from WAHA's own MIT `openapi.json`, which is why it carries 124 operations and no `execute()`. This ticket builds the equivalent for KilasFlow: a build-time generator that turns an OpenAPI 3 document into a node pack of definitions plus the routing metadata the interpreter from the previous ticket executes. WAHA is the first customer; any other OpenAPI-described service becomes a pack for free.

The hard requirement is not "generate something usable" — it is "generate the same strings". An imported n8n workflow carries the literal `resource` and `operation` values the original package chose, and KilasFlow matches an imported node against its own parameter options. Get the naming wrong by one character and every imported WAHA workflow selects nothing. The rules `@devlikeapro/n8n-openapi-node` applies are: `resource` is `lodash.startCase` of the OpenAPI tag with non-alphanumeric characters stripped, and `operation` is `startCase` of the `operationId` minus its first `_`-separated segment. Both come out as human-readable strings with spaces — `"Chatting"`, `"Send Text"` — not slugs, and that is what sits in real workflow JSON.

The generator's second rule is about body shape: only first-level request-body properties become structured parameters. A nested object arrives as a single JSON-string parameter that a user fills with an expression. Reproducing that is not laziness — it is what makes the generated parameter set match the one the imported workflow was authored against.

KilasFlow has no code generation of any kind today; `nodes/core.go`'s `RegisterAll` is a hand-written list of seventeen definitions. This ticket introduces the first generated artifact and therefore also has to settle how a pack is shipped, loaded and reviewed.

## Acceptance criteria

- [ ] A Go command under `cmd/` takes an OpenAPI 3 document plus a small pack manifest (node type, display name, credential type, version) and writes a node pack.
- [ ] `resource` and `operation` values reproduce `lodash.startCase` semantics exactly, proven by a golden test whose fixture includes tags with punctuation, `operationId`s with and without a leading `_` segment, and identifiers containing digit runs.
- [ ] Path, query and first-level request-body properties become parameters with the right kind, required flag and default; nested request-body objects become one JSON parameter each rather than being flattened or dropped.
- [ ] Every generated operation carries routing metadata the interpreter executes with no node-specific Go, and each operation's parameters are gated by `displayOptions` on the owning resource.
- [ ] Regenerating a pack from an unchanged document produces a byte-identical file: map iteration never reaches the output.
- [ ] Constructs the generator cannot express — `oneOf`/`anyOf` request bodies, multipart uploads, non-JSON media types, security schemes with no credential mapping — are listed in a generation report and are absent from the pack, never emitted as a guess.
- [ ] A generated pack registers into `internal/node.Registry` under its own type namespace and version, and the registry rejects a pack whose executor binding has no executor.

## Implementation Plan

Write the generator as `cmd/nodepackgen`, reading the OpenAPI document and a manifest, and emitting the pack. Settle the shipping format first, because everything else follows from it: emit a JSON pack file committed to the repository and embedded with `go:embed`, not generated Go source and not a document parsed at server start. A JSON pack diffs readably in review, keeps the single binary intact, and means a bad spec change can never turn into a compile error at deploy time. Generated Go would be reviewable too, but 124 operations of it would drown every future diff in this repository; parsing OpenAPI at startup would put a third-party document on the boot path.

Implement `startCase` before anything else and test it in isolation. Lodash's word splitter breaks on case boundaries, on non-alphanumeric separators, and between letter runs and digit runs — so `sendText`, `send_text` and `send-text` all become `Send Text`, while an identifier containing digits does not survive a naive `strings.Title`. This one function decides whether every imported WAHA workflow matches or silently selects nothing, so it gets its own table-driven test with the real WAHA `operationId` list once the spec is vendored.

Then walk the document: group operations by first tag into resources, sort resources and operations by their generated display strings so output is stable, and build one `options` property for `resource`, one per resource for `operation`, and the parameter set for each operation. Map OpenAPI types onto the property kinds the registry has — which today are only `string`, `number`, `boolean`, `select`, `keyValue` and `conditions` (`internal/node/registry.go`), so a generator run before the property-kinds work lands will have to degrade `enum` to `select` and everything structured to `string`. Note that limitation in the generation report rather than hiding it.

The trap is the security scheme. An OpenAPI document describes how to authenticate but not which KilasFlow credential type carries it; the manifest supplies that mapping, and a scheme with no mapping must fail generation loudly. A pack that generates cleanly and then cannot authenticate is the worst outcome available here.

## References

- Roadmap plan, p3 section, entry V2-p3-2: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- n8n 2.34.0 reference checkout, `packages/workflow/src/Interfaces.ts` — the routing and property metadata shapes a pack must fill in.
- `internal/node/registry.go` — `Definition`, `PropertyDefinition`, `PropertyKind` and `Register`'s immutability rule.
- `nodes/core.go` `RegisterAll` — the hand-written registration this generator's output has to join.
