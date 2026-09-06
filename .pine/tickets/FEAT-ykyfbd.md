---
id: FEAT-ykyfbd
title: Prove community node pack installation end to end
status: doing
priority: high
labels:
    - e2e
    - testing
    - packs
deps:
    - FEAT-cx3hq1
    - FEAT-cwz4ac
    - FEAT-ed6wdy
parent: EPIC-m42s3g
phase: p11
created: "2026-09-05T12:04:38Z"
updated: "2026-09-06T05:16:56Z"
---

## Scope

V2-p10-15 makes a pack installable from a directory, V2-p10-16 makes one authorable, V2-p10-17 documents the path, and V2-p10-18 converts a declarative n8n community node into one. Four tickets deliver a community node ecosystem, and nothing proves the sequence works from an author's file to a running workflow.

Each of those tickets verifies its own piece: the loader loads, the validator validates, the converter converts. The failure this suite catches is the one that lives between them — a pack that validates and loads but whose node cannot be configured in the editor, or whose credential requirement resolves to a type the credential registry does not have, or whose dynamic option loader errors in a way that leaves an empty picker.

The registry already tags the source and serializes it, so the editor can distinguish a pack node from a built-in one, and no test asserts that it does. That matters more than it sounds: the operator-facing question "where did this node come from" is a supply-chain question, and `RegisterFrom` refusing the `kilasflow.` prefix for external sources is the enforcement. A test that installs a pack claiming that prefix and asserts the server refuses to start is the only check that the enforcement is wired, rather than merely written.

The specific steps that need proving, in order:

- An author's directory becomes an installed pack without a rebuild.
- Its nodes appear in the catalogue, tagged `pack`, and in the editor's node picker.
- Its parameters render, including the generated `resource`/`operation` cascade with its internal options loader narrowing operations to the selected resource.
- Its credential type is offered by the credential picker and a credential can be created for it.
- A workflow using it executes against a real endpoint, going through `internal/safehttp` like any other outbound call.
- A trigger pack binds a webhook, fans out by event, and registers itself with a remote service on activation.
- A converted n8n community node does all of the above, with resource and operation strings matching the source byte for byte.

## Acceptance criteria

- [ ] A pack placed in the configured directory is loaded on restart and its nodes appear in `/api/v1/node-types` tagged `pack`, with no rebuild.
- [ ] The pack's nodes are usable through the editor: picked, configured through the resource and operation cascade, saved, and executed against a stub endpoint.
- [ ] The pack's credential type appears in the credential picker and a credential created for it authenticates the outbound call.
- [ ] A trigger pack binds a webhook, routes a delivery to the correct per-event output, and performs its lifecycle registration on activation and its removal on deactivation.
- [ ] A pack claiming the `kilasflow.` namespace, a malformed pack, and a pack failing its checksum each prevent startup with a message naming the pack, and never leave a partially registered catalogue.
- [ ] Outbound calls from a pack node are subject to the same SSRF policy and credential domain scoping as a built-in node, proven by a test that a disallowed host is refused.
- [ ] A pack produced by V2-p10-18's converter from a real declarative n8n community node completes the same path, with its resource and operation values matching the source exactly.
- [ ] A workflow exported from n8n that references the converted node imports and binds to it, closing the loop between the converter and the importer.

## Implementation Plan

Use a purpose-built fixture pack for most of the suite rather than a real third-party one. It can exercise every feature deliberately — a cascade, a credential, an option loader, binary upload, a trigger with lifecycle — against the local stub, and it will not break when somebody else's API changes. Keep one real converted node as a separate case, because the converter's fidelity is a claim about the real world and a synthetic fixture cannot test it.

The negative cases deserve as much attention as the positive ones and are cheaper to write. A refused namespace, a bad checksum and a malformed manifest are three assertions about startup behaviour, and startup behaviour is exactly where "skip it and carry on" creeps in under pressure. Writing the tests early makes the loader's fail-loud decision durable.

Note a real constraint on the harness: packs load at composition, so every one of these tests needs a server instance started with a particular pack directory. That is a per-test instance rather than a shared one, and the fixture from V2-p11-1 must support parameterising the data and pack directories. Flag it to that ticket rather than working around it here.

For the trigger lifecycle, the remote registration is an outbound `PUT` to a service; the stub receives it and the test asserts the minted URL was sent. That proves the half that is actually fragile — that the URL the server minted is the URL the remote service was told about — without needing the remote service to exist.

One decision to record: whether this suite installs the pack by writing files directly or through the `pack` subcommand from V2-p10-16. Recommend the subcommand for the primary path, since that is what an operator does and it exercises the checksum generation too, with direct file writes reserved for the malformed and tampered cases, which the tooling would refuse to produce.

## References

- Roadmap plan, p11 section, entry V2-p11-5: `.pine/roadmap.md`.
- `.pine/tickets/FEAT-czbzs6.md` — V2-p10-15, the loader this proves.
- `.pine/tickets/FEAT-cwz4ac.md` — V2-p10-16, the tooling that installs and checksums.
- `.pine/tickets/FEAT-ed6wdy.md` — V2-p10-18, the converter whose output this validates end to end.
- `internal/node/registry.go` — `RegisterFrom`, `SourcePack`, `BuiltinPrefix` and the reserved-namespace refusal.
- `internal/nodepack/nodepack.go` — `Decode`, `Load`, `Register`, and the generated resource/operation cascade.
- `internal/nodepack/trigger.go` — per-event outputs, HMAC verification and the declarative lifecycle block.
- `internal/routing/routing.go` — the request path every pack node's outbound call takes.
- `internal/safehttp/safehttp.go` — the policy a pack node must not escape.
- `packs/telegram/pack.json` — the hand-written pack to model the fixture on.

## Notes (PackE2E, 2026-09-06)

- Scope kept to the carried remainder: author-to-running vocabulary + failure
  modes. Trigger lifecycle, SSRF refusal, converter fidelity, and n8n import
  binding stay for follow-ups; the ticket's wider boxes are not claimed here.
- New files only, harness untouched: `e2e/tests/pack-install.spec.ts` (4 tests)
  + `e2e/fixtures/pack-install.ts` (toolchain driver + per-test pack server).
  No edits to `e2e/fixtures.ts`, `e2e/helpers/*`, loader, or registry.
- Harness note from the plan, resolved without a harness change: per-test
  pack servers boot through `startServer` with `KILASFLOW_PACKS_DIR` set
  around the call (it spreads `process.env` into the child) and restored in
  `finally`. Safe under `fullyParallel`: workers are separate processes, one
  test at a time each. No fixture parameterisation was needed.
- Author path is the Go tooling per the plan's recorded decision: `nodepackgen
  scaffold -type pack.e2ehello -credential-type wahaApi` -> `validate` ->
  `pack`, binary built once per worker (`go build ./cmd/nodepackgen`). No
  Node.js process serves anything; the stub is the outbound call target only.
- `wahaApi` (not the scaffold default `exampleApi`) because the server
  refuses credentials of unregistered types (422) and `telegramApi`'s path
  placement needs a `{credential.…}` marker the scaffold URL has no room for;
  header placement + non-secret `baseUrl` fit the scaffold shape exactly.
- Failure needles, all naming the pack dir: checksum (`"tampered"` +
  `recorded digest`), malformed (`"broken"` + `pack.json`, after `validate`
  refuses it first naming file+field), reserved namespace (`"evil"` +
  `kilasflow.`).
- Verification: `cd e2e && pnpm test pack-install` — 4 passed via the harness
  (catalogue `source: pack`, cascade `load-options` narrows to `sendMessage`,
  workflow `succeeded`, stub got `POST /sendMessage` with the chat/text body).
