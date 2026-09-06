---
id: FEAT-ykyfbd
title: Prove community node pack installation end to end
status: done
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
updated: "2026-09-06T06:08:49Z"
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

- [x] A pack placed in the configured directory is loaded on restart and its nodes appear in `/api/v1/node-types` tagged `pack`, with no rebuild. (PackE2E: pack-install `an authored pack installs…` — catalogue `source: pack`.)
- [x] The pack's nodes are usable through the editor: picked, configured through the resource and operation cascade, saved, and executed against a stub endpoint. (PackE2E: cascade `load-options` narrows to `sendMessage` + workflow `succeeded`. PackE2E2: pack-editor picker add + `Save` disables + `Run` reaches stub `POST /sendMessage` with chat/text; converted-pack cascade narrows message→send/get/list, mailbox→list in the panel.)
- [x] The pack's credential type appears in the credential picker and a credential created for it authenticates the outbound call. (PackE2E: `credential-types` lists `wahaApi`. PackE2E2: panel `#credential-wahaApi` offers `E2E Pack WAHA`, warning clears on bind; header observer sees `x-api-key: e2e-key` on the outbound call.)
- [x] A trigger pack binds a webhook, routes a delivery to the correct per-event output, and performs its lifecycle registration on activation and its removal on deactivation. (PackE2E2: pack-trigger — activation `PUT /api/subscriptions/default` body `{"url":"/webhook/<32hex>"}` told==delivered route; `message`→branch-a only; unknown event→catch-all branch-b; deactivation `DELETE` + delivery 404s with no new execution.)
- [x] A pack claiming the `kilasflow.` namespace, a malformed pack, and a pack failing its checksum each prevent startup with a message naming the pack, and never leave a partially registered catalogue. (PackE2E: `"tampered"`+`recorded digest`, `"broken"`+`pack.json`, `"evil"`+`kilasflow.`.)
- [x] Outbound calls from a pack node are subject to the same SSRF policy and credential domain scoping as a built-in node, proven by a test that a disallowed host is refused. (PackE2E2: pack-safety — `127.0.0.2` same-port run `failed` with no stub hit; `allowedDomains: [example.com]` run `failed` with no stub hit. `allow_private_networks` never set.)
- [x] A pack produced by V2-p10-18's converter from a real declarative n8n community node completes the same path, with its resource and operation values matching the source exactly. (PackE2E2: pack-converted — `pack-convert` driver runs `nodepack.ConvertDocument` on the Acme transcription → `pack.e2eacme`, 4 converted / 5 excluded / report names watch+purge+stream+raw+draft; manifest resources/operations byte-equal the transcription; `send` runs `POST /v1/messages` with to/subject; `mailbox/watch` run fails `no request is declared for resource "mailbox" operation "watch"`.)
- [x] A workflow exported from n8n that references the converted node imports and binds to it, closing the loop between the converter and the importer. (PackE2E2: pack-converted import loop — `n8n-nodes-acme.AcmeMail` imports as `blocking` placeholder preserving `resource: message`/`operation: send` in the capsule; rebound to `pack.e2eacme` with those values + real credential → `succeeded`, stub got the same body.)

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

## Notes (PackE2E2, 2026-09-06)

- Remainder boxes closed: editor cascade use, credential picker+auth,
  trigger lifecycle, SSRF/domain refusal, converter-produced pack, n8n-import
  loop. Boxes 1, 2-partial and 5 stay as PackE2E proved them (untouched).
- New/extend e2e only, harness untouched: `e2e/tests/pack-editor.spec.ts`
  (4 tests), `pack-trigger.spec.ts` (1), `pack-safety.spec.ts` (2),
  `pack-converted.spec.ts` (2); extended `e2e/fixtures/pack-install.ts`
  (endpoint override, header observer, trigger scaffold, converter driver
  runner); new `e2e/fixtures/pack-acme-transcription.json` (operator-copied
  format facts, never third-party bytes) + `pack-convert-driver.go`
  (`//go:build ignore`, `go run`, never on the server boot path). No edits
  to `e2e/fixtures.ts`, `e2e/helpers/*`, loader, toolchain, or converter.
- Converter pack keeps the proven acme operation shapes but transcribes
  credential `wahaApi` + baseURL `{{ $credentials.baseUrl }}` so the output
  runs against the stub with a real credential, like the scaffold pack.
- Trigger pack is purpose-built (`pack.e2ealert`: message/session.status +
  catch-all other, declarative set+remove lifecycle), sealed with the
  operator `pack` subcommand like the action pack. (The validator's
  throwaway registry briefly refused every trigger pack — reproduced
  against the repo's own WAHA trigger — and the toolchain owner has since
  fixed it; the suite seals via the subcommand again.)
- Two product bugs found proving box 4 and fixed by Main during the run:
  (1) `WebhookRoutes` dropped trigger Parameters so `GatedLifecycle` never
  enabled on activation (+ regression `TestWebhookRoutesCarriesTriggerParameters`);
  (2) the lifecycle coordinator was built with `safehttp.DefaultPolicy()`
  instead of `outboundPolicy(cfg.Outbound)`. Before the fix, activation
  succeeded with no PUT; after, the PUT carries the minted route exactly.
- PublicURL is path-only here (no public base URL configured), so the told
  URL is asserted byte-equal to the delivered route, not prefixed.
- Verification: `cd e2e && pnpm test pack-install pack-safety
  pack-converted pack-trigger pack-editor` — 13 passed via the harness
  (4 PackE2E + 9 PackE2E2). 8/8 boxes ticked with proof above; no remainder.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-06.

- Base: `ddfc94c3` (last commit at or before ticket created 2026-09-05)
- Commits (2):
  - `1fb88a72` — merge: pack installation path end to end, author to running (FEAT-ykyfbd)
  - `c94583a2` — feat(nodes): reach parity on the flow-control node family
- Files changed (base → working tree):

```
 .env.example                                       |  186 +
 .github/actions/js-toolchain/action.yml            |   49 +
 .github/workflows/ci.yml                           |  429 +++
 .github/workflows/release.yml                      |  157 +
 .gitignore                                         |    3 +
 .pine/CHECKPOINT.md                                |  148 +
 .pine/MEMORY.md                                    |    7 +
 .pine/learnings/LRN-jrxe9h.md                      |    9 +
 .pine/learnings/LRN-t016v0.md                      |    9 +
 .pine/memory/code-node.md                          |   74 +
 .pine/memory/docker.md                             |    9 +
 .pine/memory/licensing.md                          |    4 +-
 .pine/memory/live-databases.md                     |   40 +
 .pine/memory/persistence.md                        |   14 +
 .pine/memory/web-editor.md                         |   10 +
 .pine/roadmap.md                                   |  273 +-
 .pine/tickets/BUG-9s3htg.md                        |  170 +
 .pine/tickets/BUG-br7ggc.md                        |  189 +
 .pine/tickets/BUG-v6tdjr.md                        |  349 ++
 .pine/tickets/BUG-xmcm8x.md                        |  152 +
 .pine/tickets/EPIC-m42s3g.md                       |   12 +-
 .pine/tickets/FEAT-0556ck.md                       |  729 ++++
 .pine/tickets/FEAT-096vs9.md                       |  784 +++-
 .pine/tickets/FEAT-0f87fn.md                       |    2 +-
 .pine/tickets/FEAT-12s0e5.md                       |  494 ++-
 .pine/tickets/FEAT-1500sp.md                       |  168 +-
 .pine/tickets/FEAT-1axhdn.md                       |   83 +-
 .pine/tickets/FEAT-1br8at.md                       |    2 +-
 .pine/tickets/FEAT-1c70nt.md                       |    5 +
 .pine/tickets/FEAT-27km39.md                       |  666 +++-
 .pine/tickets/FEAT-2f68r8.md                       |    2 +-
 .pine/tickets/FEAT-2phs15.md                       |  413 ++-
 .pine/tickets/FEAT-347egc.md                       |  806 ++++-
 .pine/tickets/FEAT-3taswf.md                       |  723 +++-
 .pine/tickets/FEAT-3xqky1.md                       |  850 ++++-
 .pine/tickets/FEAT-45tfmh.md                       |  390 +-
 .pine/tickets/FEAT-48hreg.md                       |  841 ++++-
 .pine/tickets/FEAT-4d0bje.md                       |  862 ++++-
 .pine/tickets/FEAT-53fht8.md                       |   80 +-
 .pine/tickets/FEAT-55v09k.md                       |    2 +-
 .pine/tickets/FEAT-5fhj6p.md                       |   69 +
 .pine/tickets/FEAT-5fv8gf.md                       |  189 +-
 .pine/tickets/FEAT-5kfctc.md                       |  162 +-
 .pine/tickets/FEAT-5kv1jq.md                       |    3 +-
 .pine/tickets/FEAT-5mvech.md                       |  280 +-
 .pine/tickets/FEAT-5rvtzc.md                       |    2 +-
 .pine/tickets/FEAT-5s1w0t.md                       |    2 +-
 .pine/tickets/FEAT-5z37xh.md                       |  767 ++++
 .pine/tickets/FEAT-68zzqs.md                       |  391 +-
 .pine/tickets/FEAT-6vfn3s.md                       |    2 +-
 .pine/tickets/FEAT-7cg0cd.md                       |  832 ++++-
 .pine/tickets/FEAT-7tgasa.md                       |  211 +-
 .pine/tickets/FEAT-8qyfh1.md                       |  455 ++-
 .pine/tickets/FEAT-8r9n21.md                       |    2 +-
 .pine/tickets/FEAT-91as16.md                       |    3 +-
 .pine/tickets/FEAT-9555xz.md                       |   54 +-
 .pine/tickets/FEAT-96p7m3.md                       |  784 +++-
 .pine/tickets/FEAT-9dqn7d.md                       |  374 +-
 .pine/tickets/FEAT-9knk67.md                       |    2 +-
 .pine/tickets/FEAT-a6yg3n.md                       |    2 +-
 .pine/tickets/FEAT-a7p1b2.md                       |  136 +
 .pine/tickets/FEAT-a94c8y.md                       |  877 ++++-
 .pine/tickets/FEAT-adzn0a.md                       |    2 +-
 .pine/tickets/FEAT-afs850.md                       |    3 +-
 .pine/tickets/FEAT-agj52c.md                       |  919 ++++-
 .pine/tickets/FEAT-ajw7wt.md                       |  122 +-
 .pine/tickets/FEAT-az620p.md                       |  450 ++-
 .pine/tickets/FEAT-bp0ytb.md                       |    2 +-
 .pine/tickets/FEAT-bscygc.md                       |  655 +++-
 .pine/tickets/FEAT-c2a081.md                       |  842 ++++-
 .pine/tickets/FEAT-cgm1y3.md                       |  786 +++-
 .pine/tickets/FEAT-cjpbe6.md                       | 1000 ++++-
 .pine/tickets/FEAT-cpdp8y.md                       |   70 +
 .pine/tickets/FEAT-csqgg5.md                       |    5 +-
 .pine/tickets/FEAT-cwz4ac.md                       |  763 ++++
 .pine/tickets/FEAT-cx3hq1.md                       |  712 ++++
 .pine/tickets/FEAT-czbzs6.md                       |  656 +++-
 .pine/tickets/FEAT-ddzk2k.md                       |   75 +-
 .pine/tickets/FEAT-de8d4c.md                       |  818 +++++
 .pine/tickets/FEAT-ed6wdy.md                       |  804 +++++
 .pine/tickets/FEAT-ej0468.md                       |  826 ++++-
 .pine/tickets/FEAT-frvez8.md                       |  775 +++-
 .pine/tickets/FEAT-fw0m2q.md                       |    2 +-
 .pine/tickets/FEAT-g6wrxm.md                       |  152 +-
 .pine/tickets/FEAT-gg85se.md                       |  702 ++++
 .pine/tickets/FEAT-gjzgkd.md                       | 1036 +++++-
 .pine/tickets/FEAT-gvn62x.md                       |  146 +-
 .pine/tickets/FEAT-gxppx1.md                       |  835 ++++-
 .pine/tickets/FEAT-hv4q8e.md                       |    2 +-
 .pine/tickets/FEAT-je4f4t.md                       |  788 +++-
 .pine/tickets/FEAT-jq84xk.md                       |  727 +++-
 .pine/tickets/FEAT-jwhdsy.md                       |  414 ++-
 .pine/tickets/FEAT-k3fmj1.md                       |    3 +-
 .pine/tickets/FEAT-k3grr5.md                       |    2 +-
 .pine/tickets/FEAT-k65hqv.md                       |  846 ++++-
 .pine/tickets/FEAT-k9dwgn.md                       |  989 ++++-
 .pine/tickets/FEAT-knpfqf.md                       | 1014 +++++-
 .pine/tickets/FEAT-kwxxd0.md                       |  141 +
 .pine/tickets/FEAT-m94hhx.md                       |  209 +-
 .pine/tickets/FEAT-mvegj5.md                       |   76 +-
 .pine/tickets/FEAT-n19dch.md                       |  919 ++++-
 .pine/tickets/FEAT-n5fdz3.md                       |  409 ++-
 .pine/tickets/FEAT-nbqye0.md                       |    2 +-
 .pine/tickets/FEAT-nch9dg.md                       |  949 ++++-
 .pine/tickets/FEAT-nrfg6e.md                       |  856 ++++-
 .pine/tickets/FEAT-nrfz6m.md                       |  151 +-
 .pine/tickets/FEAT-nxxbs5.md                       |  140 +-
 .pine/tickets/FEAT-pd3p6x.md                       |    2 +-
 .pine/tickets/FEAT-ptyh9w.md                       |  789 +++-
 .pine/tickets/FEAT-q81bq4.md                       |  447 ++-
 .pine/tickets/FEAT-qcm5ec.md                       |    2 +-
 .pine/tickets/FEAT-qe6wb8.md                       |    2 +-
 .pine/tickets/FEAT-r6xhnp.md                       |  808 ++++-
 .pine/tickets/FEAT-rj17xj.md                       | 1045 +++++-
 .pine/tickets/FEAT-sar60r.md                       |    3 +-
 .pine/tickets/FEAT-sbnejr.md                       |  789 +++-
 .pine/tickets/FEAT-sdjdh2.md                       |   65 +-
 .pine/tickets/FEAT-sfy1tq.md                       |   96 +-
 .pine/tickets/FEAT-snxxny.md                       |  361 +-
 .pine/tickets/FEAT-sp8cfm.md                       |    2 +-
 .pine/tickets/FEAT-ss44d9.md                       |  812 ++++-
 .pine/tickets/FEAT-t26rt7.md                       | 1005 +++++-
 .pine/tickets/FEAT-t5q318.md                       |    2 +-
 .pine/tickets/FEAT-v8k1tc.md                       |    2 +-
 .pine/tickets/FEAT-vvwpjw.md                       |  398 +-
 .pine/tickets/FEAT-w9kqeg.md                       |    2 +-
 .pine/tickets/FEAT-whn5vb.md                       |    2 +-
 .pine/tickets/FEAT-wkmv5e.md                       |  823 ++++-
 .pine/tickets/FEAT-xeq6st.md                       |  850 ++++-
 .pine/tickets/FEAT-xr7ga9.md                       |  841 +++++
 .pine/tickets/FEAT-xx6p22.md                       |   93 +-
 .pine/tickets/FEAT-ybm2pd.md                       |   68 +-
 .pine/tickets/FEAT-ykyfbd.md                       |  135 +
 .pine/tickets/FEAT-yx0qt6.md                       |  682 +++-
 .pine/tickets/FEAT-yyjfjq.md                       |    3 +-
 .pine/tickets/FEAT-za118x.md                       |  654 +++-
 .pine/tickets/FEAT-zmfsjd.md                       |  146 +
 .pine/tickets/FEAT-znm60y.md                       |    2 +-
 .pine/tickets/FEAT-ztxs5p.md                       |    2 +-
 Dockerfile                                         |   54 +-
 Makefile                                           |  232 +-
 README.md                                          |  243 +-
 cmd/kilasflow/main.go                              |  562 ++-
 cmd/kilasflow/main_test.go                         |  135 +-
 cmd/kilasflow/secrets_boot_test.go                 |  102 +
 cmd/nodepackgen/authorcmd.go                       |  134 +
 cmd/nodepackgen/main.go                            |   12 +-
 compose.build.yaml                                 |   35 +
 compose.postgres.yaml                              |   76 +
 compose.yaml                                       |  103 +
 config.example.yaml                                |  333 +-
 docker-compose.yml                                 |   41 -
 docs/.gitignore                                    |    6 +
 docs/astro.config.mjs                              |  130 +
 docs/package.json                                  |   20 +
 docs/plugins/base-links.mjs                        |   57 +
 docs/pnpm-lock.yaml                                | 3807 ++++++++++++++++++++
 docs/src/components/ThemeProvider.astro            |   59 +
 docs/src/components/ThemeSelect.astro              |   79 +
 docs/src/content.config.ts                         |   11 +
 docs/src/content/docs/404.md                       |   21 +
 docs/src/content/docs/concepts/architecture.md     |  163 +
 docs/src/content/docs/concepts/credentials.md      |  223 ++
 docs/src/content/docs/concepts/execution-model.md  |  335 ++
 docs/src/content/docs/concepts/expressions.md      |  210 ++
 .../src/content/docs/concepts/items-and-lineage.md |  174 +
 docs/src/content/docs/concepts/node-registry.md    |  319 ++
 .../src/content/docs/concepts/safety-boundaries.md |  283 ++
 .../content/docs/concepts/tenancy-and-embedding.md |  305 ++
 docs/src/content/docs/concepts/webhooks.md         |  203 ++
 docs/src/content/docs/contributing.md              |   89 +
 docs/src/content/docs/guides/community-nodes.md    |   94 +
 docs/src/content/docs/guides/embedding.md          |  337 ++
 docs/src/content/docs/guides/n8n-migration.md      |  674 ++++
 docs/src/content/docs/guides/node-authoring.md     |  499 +++
 docs/src/content/docs/index.mdx                    |   59 +
 .../docs/operate/configuration-reference.md        |  770 ++++
 docs/src/content/docs/operate/configuration.md     |   71 +
 docs/src/content/docs/operate/deployment.md        |  144 +
 docs/src/content/docs/operate/security.md          |  112 +
 docs/src/content/docs/operate/upgrades.md          |   69 +
 docs/src/content/docs/reference/api-contract.md    |  306 ++
 docs/src/content/docs/reference/api.md             |   41 +
 docs/src/content/docs/reference/api/auth.md        |  129 +
 docs/src/content/docs/reference/api/credentials.md |  168 +
 docs/src/content/docs/reference/api/embed.md       |   29 +
 docs/src/content/docs/reference/api/errors.md      |   36 +
 docs/src/content/docs/reference/api/events.md      |   40 +
 docs/src/content/docs/reference/api/executions.md  |  102 +
 docs/src/content/docs/reference/api/interop.md     |   51 +
 docs/src/content/docs/reference/api/nodes.md       |  111 +
 docs/src/content/docs/reference/api/schedules.md   |   88 +
 docs/src/content/docs/reference/api/system.md      |   42 +
 docs/src/content/docs/reference/api/webhooks.md    |   36 +
 docs/src/content/docs/reference/api/workflows.md   |  288 ++
 .../content/docs/reference/expression-grammar.md   |  183 +
 docs/src/content/docs/reference/node-packs.md      |  153 +
 docs/src/content/docs/start/first-workflow.md      |   40 +
 docs/src/content/docs/start/install.md             |  271 ++
 docs/src/content/docs/start/what-kilasflow-is.md   |   84 +
 docs/src/styles/kilasflow.css                      |  138 +
 docs/tsconfig.json                                 |    5 +
 e2e/.gitignore                                     |    2 +
 e2e/fixtures.ts                                    |   40 +
 e2e/fixtures/ai-gateway.ts                         |  126 +
 e2e/fixtures/pack-install.ts                       |  269 ++
 e2e/fixtures/waha-migration.ts                     |  210 ++
 e2e/global-setup.ts                                |   27 +
 e2e/helpers/seed.ts                                |  174 +
 e2e/helpers/server.ts                              |  142 +
 e2e/helpers/stub.ts                                |   98 +
 e2e/package.json                                   |   14 +
 e2e/playwright.config.ts                           |   32 +
 e2e/pnpm-lock.yaml                                 |   57 +
 e2e/tests/ai-agent-ollama.spec.ts                  |  564 +++
 e2e/tests/node-coverage.spec.ts                    |  868 +++++
 e2e/tests/pack-install.spec.ts                     |  173 +
 e2e/tests/smoke.spec.ts                            |  108 +
 e2e/tests/waha-migration.spec.ts                   |  279 ++
 executions-narrow.png                              |  Bin 0 -> 37917 bytes
 gflow-prd-v1.md                                    |   32 +
 go.mod                                             |    9 +-
 go.sum                                             |   37 +-
 internal/ai/agent.go                               |  146 +-
 internal/ai/agent_output_test.go                   |  185 +
 internal/ai/ai.go                                  |   50 +-
 internal/ai/ai_test.go                             |  166 +
 internal/ai/fromai.go                              |  548 +++
 internal/ai/fromai_test.go                         |  139 +
 internal/ai/maf/doc.go                             |   15 +-
 internal/ai/maf/runtime.go                         |  144 +
 internal/ai/maf/runtime_test.go                    |  131 +
 internal/ai/memory.go                              |  164 +-
 internal/ai/openai.go                              |  100 +-
 internal/ai/openai_test.go                         |  156 +
 internal/ai/outputschema.go                        |  414 +++
 internal/api/auth_test.go                          |  668 ++++
 internal/api/credentials_test.go                   |  401 +++
 internal/api/datastores_test.go                    |  355 ++
 internal/api/embed_test.go                         |   61 +-
 internal/api/handlers/auth.go                      |  380 ++
 internal/api/handlers/credentials.go               |  284 +-
 internal/api/handlers/datastores.go                |  598 +++
 internal/api/handlers/executions.go                |   84 +-
 internal/api/handlers/nodes.go                     |  288 +-
 internal/api/handlers/resume.go                    |  216 ++
 internal/api/handlers/resume_test.go               |  232 ++
 internal/api/handlers/tenants.go                   |   46 +
 internal/api/handlers/workflows.go                 |  217 +-
 internal/api/handlers/workflows_delete_test.go     |  111 +
 internal/api/middleware/auth.go                    |  173 +
 internal/api/middleware/embed.go                   |    7 +
 internal/api/node_types_test.go                    |  424 +++
 internal/api/routes.go                             |   61 +-
 internal/api/server.go                             |   57 +-
 internal/api/workflow_history_test.go              |  188 +
 internal/api/workflows_test.go                     |  138 +-
 internal/auth/auth.go                              |   73 +
 internal/auth/auth_test.go                         |  311 ++
 internal/auth/keys.go                              |  197 +
 internal/auth/session.go                           |  274 ++
 internal/conditions/conditions.go                  |  542 +++
 internal/conditions/conditions_test.go             |  231 ++
 internal/conditions/doc.go                         |   26 +
 internal/config/config.go                          |  596 ++-
 internal/config/config_test.go                     |  387 ++
 internal/config/secrets_test.go                    |   35 +
 internal/credentials/builtin.go                    |   36 +
 internal/credentials/credentials.go                |   15 +-
 internal/credentials/credentials_test.go           |    2 +
 internal/credentials/external.go                   |  437 +++
 internal/credentials/external_test.go              |  359 ++
 internal/credentials/keysource.go                  |   82 +
 internal/credentials/registry.go                   |   14 +-
 internal/credentials/vault.go                      |  155 +
 internal/database/database.go                      |   71 +-
 internal/database/database_test.go                 |   91 +-
 internal/database/migrate.go                       |  648 ++++
 internal/database/migrate_test.go                  |  898 +++++
 internal/database/prefix_test.go                   |  371 ++
 internal/datastore/catalogue.go                    |  113 +
 internal/datastore/catalogue_test.go               |   74 +
 internal/datastore/concurrency.go                  |  284 ++
 internal/datastore/concurrency_test.go             |  289 ++
 internal/datastore/config_bind_test.go             |   28 +
 internal/datastore/doc.go                          |   39 +
 internal/datastore/engine.go                       |  438 +++
 internal/datastore/engine_test.go                  |  628 ++++
 internal/datastore/evolve_test.go                  |  156 +
 internal/datastore/filter.go                       |  370 ++
 internal/datastore/fleet.go                        |  163 +
 internal/datastore/fleet_test.go                   |  117 +
 internal/datastore/idents.go                       |  206 ++
 internal/datastore/idents_test.go                  |  215 ++
 internal/datastore/isolation.go                    |   74 +
 internal/datastore/isolation_test.go               |  207 ++
 internal/datastore/limits.go                       |  192 +
 internal/datastore/limits_test.go                  |  295 ++
 internal/datastore/migrate_test.go                 |  244 ++
 internal/datastore/model.go                        |   51 +
 internal/datastore/rows.go                         |  832 +++++
 internal/datastore/rows_test.go                    |  651 ++++
 internal/datastore/trace.go                        |  118 +
 internal/datastore/trace_test.go                   |  183 +
 internal/datetime/datetime_test.go                 |  134 +
 internal/datetime/doc.go                           |   15 +
 internal/datetime/format.go                        |  195 +
 internal/datetime/parse.go                         |  108 +
 internal/embed/embed.go                            |    2 +
 internal/engine/approval.go                        |  334 ++
 internal/engine/approval_test.go                   |  213 ++
 internal/engine/authenticate.go                    |   13 +-
 internal/engine/checkpoint.go                      |   82 +
 internal/engine/runner.go                          |  266 +-
 internal/engine/service.go                         |  468 ++-
 internal/engine/service_test.go                    |    8 +-
 internal/engine/subworkflow_test.go                |  329 ++
 internal/engine/trace.go                           |   28 +
 internal/engine/trace_test.go                      |  263 ++
 internal/engine/wait_service.go                    |  585 +++
 internal/engine/wait_service_test.go               |  713 ++++
 internal/engine/worker_test.go                     |   33 +
 internal/execution/records.go                      |   25 +-
 internal/execution/redact.go                       |    8 +
 internal/execution/redact_datastore_test.go        |   75 +
 internal/expression/doc.go                         |   19 +-
 internal/expression/expression.go                  |   31 +-
 internal/expression/expression_test.go             |   65 +
 internal/guardrails/shell_injection_test.go        |   70 +
 internal/interop/n8n/corpus/BASELINE.md            |   29 +-
 internal/interop/n8n/corpus/baseline.json          |   90 +-
 .../n8n/corpus/fixtures/control-datatable.json     |   95 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   73 +-
 internal/interop/n8n/export_test.go                |   15 +
 internal/interop/n8n/n8n.go                        |  505 ++-
 internal/interop/n8n/n8n_test.go                   | 2347 +++++++++++-
 internal/interop/n8n/parameters.go                 | 3129 +++++++++++++++-
 internal/interop/n8n/sqlfidelity_test.go           |  442 +++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |  110 +
 internal/loadoptions/datastores.go                 |  124 +
 internal/loadoptions/datastores_test.go            |   74 +
 internal/loadoptions/loadoptions.go                |   61 +-
 internal/loadoptions/loadoptions_test.go           |   99 +
 internal/loadoptions/schema.go                     |   68 +
 internal/loadoptions/sql.go                        |  287 ++
 internal/loadoptions/sql_test.go                   |  350 ++
 internal/loadoptions/workflows.go                  |   64 +
 internal/node/registry.go                          |  109 +-
 internal/node/registry_test.go                     |  247 +-
 internal/nodepack/author.go                        |  159 +
 internal/nodepack/author_test.go                   |  292 ++
 internal/nodepack/convert.go                       |  799 ++++
 internal/nodepack/convert_test.go                  |  413 +++
 internal/nodepack/loaddir.go                       |  155 +
 internal/nodepack/loaddir_test.go                  |  336 ++
 internal/nodepack/nodepack.go                      |   16 +-
 internal/nodepack/validate.go                      |  417 +++
 internal/property/locator_test.go                  |  116 +
 internal/property/mapper.go                        |  322 ++
 internal/property/mapper_test.go                   |  196 +
 internal/property/property.go                      |  189 +-
 internal/repository/auth.go                        |  330 ++
 internal/repository/auth_test.go                   |  322 ++
 internal/repository/claim_wake_test.go             |  395 ++
 internal/repository/credentials.go                 |  196 +-
 internal/repository/credentials_external_test.go   |  246 ++
 internal/repository/execution_retention.go         |  180 +
 internal/repository/execution_retention_test.go    |  396 ++
 internal/repository/executions.go                  |  243 +-
 internal/repository/models.go                      |  238 +-
 internal/repository/models_test.go                 |   12 +-
 internal/repository/postgres_execution_test.go     |  213 ++
 internal/repository/prefix_test.go                 |   80 +
 internal/repository/schedules.go                   |  145 +-
 internal/repository/table_names_test.go            |   51 +
 internal/repository/tenant_purge.go                |   57 +
 internal/repository/tenant_purge_test.go           |  118 +
 internal/repository/waits.go                       |  521 +++
 internal/repository/waits_test.go                  |  313 ++
 internal/repository/wake.go                        |  199 +
 internal/repository/wake_internal_test.go          |   93 +
 internal/repository/webhooks.go                    |   11 +
 internal/repository/workflow_history.go            |  446 +++
 internal/repository/workflow_history_test.go       |  613 ++++
 internal/repository/workflows.go                   |  182 +-
 internal/routing/request.go                        |    6 +-
 internal/runcode/doc.go                            |   37 +-
 internal/runcode/runcode.go                        |   96 +-
 internal/runcode/runcode_test.go                   |  184 +-
 internal/safehttp/safehttp.go                      |  139 +-
 internal/safehttp/safehttp_test.go                 |  193 +
 internal/scheduler/extract.go                      |   81 +
 internal/scheduler/item.go                         |   71 +
 internal/scheduler/rule.go                         |  321 ++
 internal/scheduler/rule_test.go                    |  278 ++
 internal/scheduler/scheduler.go                    |  139 +-
 internal/scheduler/scheduler_test.go               |  155 +-
 internal/sqlbuild/dialect.go                       |  256 ++
 internal/sqlbuild/sqlbuild.go                      |  392 ++
 internal/sqlbuild/sqlbuild_test.go                 |  650 ++++
 .../testdata/fuzz/FuzzIdentifier/074b649c870c7bf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/0ef06ad2d24324a6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/10abc61b4419477c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1138f8b07d3718ed  |    2 +
 .../testdata/fuzz/FuzzIdentifier/11956af7e5f573cf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/143ed5c16fb0545a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/18f080b7181cbda6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a1bc12d893c9520  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1a4b8093f72994e4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1bb2624596902fcc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/1d590be519b71d94  |    2 +
 .../testdata/fuzz/FuzzIdentifier/202cac18931087b8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/25f9d5a362559265  |    2 +
 .../testdata/fuzz/FuzzIdentifier/28464299ac9ceaf3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e18a0fadf8f8d1e  |    2 +
 .../testdata/fuzz/FuzzIdentifier/2e6e50da573e69b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/33e9c7c26e121287  |    2 +
 .../testdata/fuzz/FuzzIdentifier/348f655cbe635cf8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34b4da777d929731  |    2 +
 .../testdata/fuzz/FuzzIdentifier/34e7a76efc318e82  |    2 +
 .../testdata/fuzz/FuzzIdentifier/35351672b96f8693  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3695e57e46ce93c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/37ee41c42fb1c3c8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3903663d1e91caeb  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3c4eb8315991acf7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3d501ac59ad587c9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/3db44c7ba19fdd16  |    2 +
 .../testdata/fuzz/FuzzIdentifier/436a22ea064a03b1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4415ff0d1b9299f1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/445fca83849180f2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/45a37a7d87af8888  |    2 +
 .../testdata/fuzz/FuzzIdentifier/46fa4fad84220ea4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/471c839522bc6d06  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4935e94ff365ef93  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ddf220b34d80659  |    2 +
 .../testdata/fuzz/FuzzIdentifier/4ee3ead78b5e68a2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5288346b2319478f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5438070243513251  |    2 +
 .../testdata/fuzz/FuzzIdentifier/54811be07baf792d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/56065070b35266a8  |    2 +
 .../testdata/fuzz/FuzzIdentifier/58540c400d7a154c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5ac14d4f3feba852  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b47e5ee0596ab19  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5b4f050cad7d6971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5d03424fa48149c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/5df9ba84197819df  |    2 +
 .../testdata/fuzz/FuzzIdentifier/60aff40e01913abe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66542488bb28dac3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/66889c986f27ea1d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/677dfe3de201697f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6a16db612d198990  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6ad80d0a2c06ac54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6b16cbabe8e4a2e7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/6c6dfd24f8c73c0b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/76b6d045b42add72  |    2 +
 .../testdata/fuzz/FuzzIdentifier/77fa2102462675e0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7ad7e6cfe18eb7fe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7b4af4ca74813068  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7daed2d2a835f0b7  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e151b9450d2b761  |    2 +
 .../testdata/fuzz/FuzzIdentifier/7e2babaa1ef35360  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8059934d90bcf246  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8378d0fed0b8996b  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8ac7c327fd5b5cc3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8dd1afcdc5104588  |    2 +
 .../testdata/fuzz/FuzzIdentifier/8fff7f2dd3835c42  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9006884cc2246a54  |    2 +
 .../testdata/fuzz/FuzzIdentifier/90c2eff4ee6601b9  |    2 +
 .../testdata/fuzz/FuzzIdentifier/92bbc2ba64e693c5  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9512693f2cee29c1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9633d0ce6f89b127  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9789b6bb44075878  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9a89fb10b388482a  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c3612f616e75c30  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9c83e3607e077307  |    2 +
 .../testdata/fuzz/FuzzIdentifier/9f72fab7a7931bc4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a4ce8510de4da361  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a5833806503a0d20  |    2 +
 .../testdata/fuzz/FuzzIdentifier/a595c2930c69a903  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ab73d083e8e43971  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ad4c4ad6237bb964  |    2 +
 .../testdata/fuzz/FuzzIdentifier/af9e1ebfb8d2795d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b128ad1323911a0d  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b3feba13964d5945  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b41d082073a2bebc  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b48b96f658bda199  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b6073b717a32efea  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b70f41cf6885f569  |    2 +
 .../testdata/fuzz/FuzzIdentifier/b8dadd1180c17fc2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bc4962e0586168dd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/bee82b5732ad61c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c59644d3f3881a96  |    2 +
 .../testdata/fuzz/FuzzIdentifier/c61c2d3db1f59301  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ca57023a06a80b6f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cb2b19dce8ab4792  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cbb1fd2d98956b9f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cea400674b589d15  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cef254100bb1d3ec  |    2 +
 .../testdata/fuzz/FuzzIdentifier/cf37f09b95c8bad1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d10d0ab0376ef2c6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d15249b25edd04e1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d1a9e9faf54d8eb4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/d43cc2dfd7b882e3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dc8d3d87a20bf6bd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dcad439aaec6c2f6  |    2 +
 .../testdata/fuzz/FuzzIdentifier/dd271580db144444  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e257a0a06a6d0ddd  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2927b2f113531c2  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e2b8af39f88d630c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e43666abfc98f368  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e55caa61264144d4  |    2 +
 .../testdata/fuzz/FuzzIdentifier/e758baca4af7c7b3  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ec1cb26f1a43547c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/ee4745c7324edeaf  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f080234fd9c3be57  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f34f2012d2974cb0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f3a15bf1ad31abb1  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f5b1055fe63bbb4f  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f6c3d89cb56eb2f0  |    2 +
 .../testdata/fuzz/FuzzIdentifier/f85456d0b9f26fbe  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd22c094ae02673c  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fd474a797a00ff71  |    2 +
 .../testdata/fuzz/FuzzIdentifier/fe2500ec53808c61  |    2 +
 internal/sqlbuild/testdata/mysql/delete_drop.sql   |    1 +
 .../testdata/mysql/delete_drop_cascade.sql         |    1 +
 internal/sqlbuild/testdata/mysql/delete_rows.sql   |    2 +
 .../sqlbuild/testdata/mysql/delete_truncate.sql    |    1 +
 .../testdata/mysql/delete_truncate_restart.sql     |    1 +
 internal/sqlbuild/testdata/mysql/insert.sql        |    2 +
 .../testdata/mysql/insert_skip_conflict.sql        |    2 +
 internal/sqlbuild/testdata/mysql/select.sql        |    3 +
 internal/sqlbuild/testdata/mysql/select_all.sql    |    2 +
 internal/sqlbuild/testdata/mysql/select_ilike.sql  |    3 +
 internal/sqlbuild/testdata/mysql/select_null.sql   |    3 +
 .../sqlbuild/testdata/mysql/select_ordered.sql     |    2 +
 internal/sqlbuild/testdata/mysql/update.sql        |    2 +
 internal/sqlbuild/testdata/mysql/upsert.sql        |    2 +
 .../sqlbuild/testdata/mysql/upsert_nothing.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_drop.sql     |    1 +
 .../testdata/postgres/delete_drop_cascade.sql      |    1 +
 .../sqlbuild/testdata/postgres/delete_rows.sql     |    2 +
 .../sqlbuild/testdata/postgres/delete_truncate.sql |    1 +
 .../testdata/postgres/delete_truncate_restart.sql  |    1 +
 internal/sqlbuild/testdata/postgres/insert.sql     |    3 +
 .../testdata/postgres/insert_skip_conflict.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select.sql     |    3 +
 internal/sqlbuild/testdata/postgres/select_all.sql |    2 +
 .../sqlbuild/testdata/postgres/select_ilike.sql    |    3 +
 .../sqlbuild/testdata/postgres/select_null.sql     |    3 +
 .../sqlbuild/testdata/postgres/select_ordered.sql  |    2 +
 internal/sqlbuild/testdata/postgres/update.sql     |    3 +
 internal/sqlbuild/testdata/postgres/upsert.sql     |    3 +
 .../sqlbuild/testdata/postgres/upsert_nothing.sql  |    3 +
 internal/sqlguard/admit.go                         |  245 ++
 internal/sqlguard/attack_test.go                   |  344 ++
 internal/sqlguard/dialect.go                       |  260 ++
 internal/sqlguard/doc.go                           |   53 +
 internal/sqlguard/sqlguard.go                      |  443 +++
 internal/sqlguard/sqlguard_test.go                 |  338 ++
 internal/sqlnode/export_test.go                    |   11 +
 internal/sqlnode/guard_test.go                     |  126 +
 internal/sqlnode/internal_test.go                  |  251 ++
 internal/sqlnode/introspect.go                     |  240 ++
 internal/sqlnode/policy_test.go                    |  243 ++
 internal/sqlnode/sqlnode.go                        |  905 ++++-
 internal/sqlnode/sqlnode_test.go                   |  106 +
 internal/web/dist/index.html                       |   38 +-
 internal/webhook/webhook.go                        |   74 +
 internal/webhook/webhook_test.go                   |  179 +-
 internal/workflow/compiler.go                      |  145 +-
 internal/workflow/compiler_test.go                 |  118 +
 internal/workflow/lifecycle.go                     |   51 +
 migrations/.gitkeep                                |    0
 migrations/embed.go                                |   27 +
 migrations/postgres/000001_baseline.down.sql       |   23 +
 migrations/postgres/000001_baseline.up.sql         |  192 +
 .../postgres/000002_workflow_history.down.sql      |   11 +
 migrations/postgres/000002_workflow_history.up.sql |   30 +
 migrations/postgres/000003_identity.down.sql       |   15 +
 migrations/postgres/000003_identity.up.sql         |   75 +
 .../postgres/000004_execution_indexes.down.sql     |    5 +
 .../postgres/000004_execution_indexes.up.sql       |   26 +
 migrations/postgres/000005_datastores.down.sql     |   10 +
 migrations/postgres/000005_datastores.up.sql       |   48 +
 migrations/postgres/000006_vector_store.down.sql   |   19 +
 migrations/postgres/000006_vector_store.up.sql     |  155 +
 .../postgres/000008_secret_bindings.down.sql       |    6 +
 migrations/postgres/000008_secret_bindings.up.sql  |   29 +
 .../postgres/000009_execution_waits.down.sql       |    6 +
 migrations/postgres/000009_execution_waits.up.sql  |   40 +
 migrations/sqlite/000001_baseline.down.sql         |   22 +
 migrations/sqlite/000001_baseline.up.sql           |  185 +
 migrations/sqlite/000002_workflow_history.down.sql |   11 +
 migrations/sqlite/000002_workflow_history.up.sql   |   29 +
 migrations/sqlite/000003_identity.down.sql         |   14 +
 migrations/sqlite/000003_identity.up.sql           |   73 +
 .../sqlite/000004_execution_indexes.down.sql       |    5 +
 migrations/sqlite/000004_execution_indexes.up.sql  |   21 +
 migrations/sqlite/000005_datastores.down.sql       |   10 +
 migrations/sqlite/000005_datastores.up.sql         |   47 +
 migrations/sqlite/000006_vector_store.down.sql     |    6 +
 migrations/sqlite/000006_vector_store.up.sql       |   29 +
 migrations/sqlite/000008_secret_bindings.down.sql  |    6 +
 migrations/sqlite/000008_secret_bindings.up.sql    |   29 +
 migrations/sqlite/000009_execution_waits.down.sql  |    6 +
 migrations/sqlite/000009_execution_waits.up.sql    |   39 +
 nodes/ai.go                                        | 2782 +++++++++++++-
 nodes/ai_mcp_test.go                               |  502 +++
 nodes/ai_ollama_test.go                            |  413 +++
 nodes/ai_test.go                                   | 1435 +++++++-
 nodes/ai_tools_test.go                             |  519 +++
 nodes/apostrophe_live_test.go                      |   43 +
 nodes/bindings_test.go                             |    7 +
 nodes/code.go                                      |  114 +-
 nodes/code_test.go                                 |   91 +-
 nodes/conditions.go                                |  139 +
 nodes/core.go                                      |  177 +-
 nodes/database.go                                  |  307 +-
 nodes/database_test.go                             |  706 +++-
 nodes/datastore.go                                 | 1214 +++++++
 nodes/datastore_test.go                            |  456 +++
 nodes/datastore_tool_test.go                       |  758 ++++
 nodes/datetime.go                                  |  408 +++
 nodes/datetime_test.go                             |  299 ++
 nodes/executors.go                                 |  620 +++-
 nodes/executors_test.go                            |  151 +
 nodes/flow.go                                      |  457 +++
 nodes/flow_test.go                                 |  464 +++
 nodes/http.go                                      |    4 +-
 nodes/jscode.go                                    |  172 +
 nodes/jscode_test.go                               |  100 +
 nodes/mysql_v2.go                                  |  199 +
 nodes/mysql_v2_test.go                             |  181 +
 nodes/pgvector.go                                  | 1236 +++++++
 nodes/pgvector_test.go                             |  493 +++
 nodes/postgres_v2.go                               |  633 ++++
 nodes/postgres_v2_test.go                          |  231 ++
 nodes/sql_options.go                               |  569 +++
 nodes/sql_options_live_test.go                     |  594 +++
 nodes/sql_options_test.go                          |  257 ++
 nodes/sqlite_attach_test.go                        |  161 +
 nodes/subworkflow.go                               |  275 ++
 nodes/testdata/n8n_chat_model_options.json         |   38 +
 nodes/testdata/n8n_sql_options.json                |   17 +
 nodes/transform.go                                 |  745 ++++
 nodes/transform_test.go                            |  315 ++
 nodes/wait.go                                      |  228 ++
 nodes/webhook.go                                   |  257 +-
 pkg/sdk/.gitkeep                                   |    0
 pkg/sdk/doc.go                                     |   49 +
 pkg/sdk/example/echo/main.go                       |   35 +
 pkg/sdk/sdk.go                                     |   75 +
 pkg/sdk/sdk_test.go                                |   80 +
 pkg/sdk/wasm_exec_test.go                          |   86 +
 scripts/config-reference.go                        |  402 +++
 scripts/config-reference_test.go                   |   87 +
 scripts/docker-tags.sh                             |   84 +
 scripts/e2e-stub.mjs                               |   50 +
 scripts/generate-api-reference.mjs                 |  394 ++
 scripts/smoke-docker.sh                            |   13 +-
 scripts/smoke-postgres.sh                          |   43 +-
 sdk/CHANGELOG.md                                   |   23 +
 sdk/README.md                                      |  150 +-
 sdk/examples/host-page/README.md                   |   26 +-
 sdk/examples/host-page/index.html                  |   31 +-
 sdk/examples/host-page/package.json                |   13 +
 sdk/examples/host-page/server.mjs                  |   31 +-
 sdk/examples/reference-host/README.md              |  100 +
 sdk/examples/reference-host/package.json           |   13 +
 sdk/examples/reference-host/server.mjs             |  387 ++
 sdk/examples/reference-host/tenant.html            |  101 +
 sdk/package.json                                   |   22 +-
 sdk/scripts/dump-openapi.mjs                       |   13 +
 sdk/src/browser.ts                                 |  120 +-
 sdk/src/generated/models.ts                        | 2401 +++++++++++-
 sdk/src/http.ts                                    |   31 +-
 sdk/src/server.ts                                  |  304 +-
 sdk/src/version.ts                                 |   15 +-
 sdk/test/browser.test.ts                           |  121 +
 sdk/test/operation-coverage.test.mjs               |  138 +
 sdk/test/operations.test.ts                        |  220 ++
 sdk/test/server.test.ts                            |   90 +-
 sdk/test/version.test.mjs                          |   40 +
 sidecar/doc.go                                     |   49 +
 sidecar/fixture/echo.js                            |   74 +
 sidecar/fixture_test.go                            |   98 +
 sidecar/protocol.go                                |   98 +
 sidecar/sidecar.go                                 |  494 +++
 sidecar/sidecar_test.go                            |  433 +++
 web/src/lib/api/generated/auth/auth.ts             |  752 ++++
 .../lib/api/generated/credentials/credentials.ts   |  105 +-
 .../datastore-columns/datastore-columns.ts         |  363 ++
 .../api/generated/datastore-rows/datastore-rows.ts |  902 +++++
 web/src/lib/api/generated/datastores/datastores.ts |  651 ++++
 web/src/lib/api/generated/models/aPIKeyResource.ts |   20 +
 .../generated/models/clearedDatastoreOutputBody.ts |   13 +
 .../api/generated/models/createAPIKeyInputBody.ts  |   17 +
 .../generated/models/createDatastoreInputBody.ts   |   23 +
 .../models/createStreamTicketInputBody.ts          |   17 +
 .../api/generated/models/createdAPIKeyResource.ts  |   18 +
 .../api/generated/models/datastoreColumnInput.ts   |   22 +
 .../generated/models/datastoreColumnResource.ts    |   14 +
 .../generated/models/datastoreListOutputBody.ts    |   15 +
 .../lib/api/generated/models/datastoreResource.ts  |   17 +
 web/src/lib/api/generated/models/definition.ts     |    1 +
 .../api/generated/models/deleteRowsInputBody.ts    |   15 +
 .../api/generated/models/deleteRowsOutputBody.ts   |   16 +
 .../models/deleteRowsOutputBodyRowsItem.ts         |    9 +
 .../lib/api/generated/models/executionResource.ts  |    4 +
 .../lib/api/generated/models/executionSummary.ts   |    2 +
 .../api/generated/models/executionWaitingEvent.ts  |   24 +
 web/src/lib/api/generated/models/filter.ts         |   14 +
 .../lib/api/generated/models/filterCondition.ts    |   13 +
 .../lib/api/generated/models/getDatastoreRow200.ts |    9 +
 web/src/lib/api/generated/models/index.ts          |   52 +
 .../api/generated/models/insertDatastoreRow201.ts  |    9 +
 .../lib/api/generated/models/insertRowInputBody.ts |   15 +
 .../generated/models/insertRowInputBodyValues.ts   |   12 +
 .../api/generated/models/listAPIKeysOutputBody.ts  |   15 +
 .../generated/models/listDatastoreRowsParams.ts    |   37 +
 .../generated/models/listWorkflowVersionsParams.ts |   20 +
 .../api/generated/models/loadOptionsInputBody.ts   |    2 +
 .../lib/api/generated/models/loadSchemaResource.ts |   17 +
 web/src/lib/api/generated/models/loginInputBody.ts |   24 +
 web/src/lib/api/generated/models/mapperColumn.ts   |   21 +
 .../lib/api/generated/models/principalResource.ts  |   24 +
 .../lib/api/generated/models/propertyDefinition.ts |    5 +
 web/src/lib/api/generated/models/propertyMode.ts   |   19 +
 .../generated/models/publishVersionInputBody.ts    |   17 +
 .../models/renameDatastoreColumnInputBody.ts       |   17 +
 .../generated/models/renameDatastoreInputBody.ts   |   17 +
 .../generated/models/resourceMapperDeclaration.ts  |   15 +
 .../lib/api/generated/models/rowListOutputBody.ts  |   16 +
 .../generated/models/rowListOutputBodyItemsItem.ts |    9 +
 .../models/streamExecutionEvents200Item.ts         |    9 +
 .../api/generated/models/streamTicketResource.ts   |   16 +
 .../api/generated/models/testCredentialResource.ts |    3 +
 .../lib/api/generated/models/testPayloadBody.ts    |   22 +
 .../api/generated/models/testPayloadBodyFields.ts  |   12 +
 .../api/generated/models/updateRowsInputBody.ts    |   18 +
 .../generated/models/updateRowsInputBodyValues.ts  |   12 +
 .../api/generated/models/updateRowsOutputBody.ts   |   16 +
 .../models/updateRowsOutputBodyRowsItem.ts         |    9 +
 .../lib/api/generated/models/upsertRowInputBody.ts |   18 +
 .../generated/models/upsertRowInputBodyValues.ts   |   12 +
 .../api/generated/models/upsertRowOutputBody.ts    |   17 +
 .../models/upsertRowOutputBodyRowsItem.ts          |    9 +
 .../models/workflowPublishEventResource.ts         |   19 +
 .../models/workflowPublishEventResourceAction.ts   |   16 +
 .../models/workflowVersionListResource.ts          |   17 +
 .../models/workflowVersionSummaryResource.ts       |   23 +
 web/src/lib/api/generated/nodes/nodes.ts           |  103 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |  314 +-
 web/src/lib/api/generated/workflows/workflows.ts   |  113 +-
 web/src/lib/api/http.test.ts                       |   79 +-
 web/src/lib/api/http.ts                            |   56 +
 .../lib/components/dashboard/dashboard-nav.svelte  |   12 +-
 .../lib/components/dashboard/list-states.svelte    |  136 +
 .../components/dashboard/synced-checkbox.svelte    |   44 +
 web/src/lib/components/ui/table/index.ts           |   28 +
 web/src/lib/components/ui/table/table-body.svelte  |   15 +
 .../lib/components/ui/table/table-caption.svelte   |   20 +
 web/src/lib/components/ui/table/table-cell.svelte  |   15 +
 .../lib/components/ui/table/table-footer.svelte    |   20 +
 web/src/lib/components/ui/table/table-head.svelte  |   15 +
 .../lib/components/ui/table/table-header.svelte    |   20 +
 web/src/lib/components/ui/table/table-row.svelte   |   15 +
 web/src/lib/components/ui/table/table.svelte       |   17 +
 .../workflow-editor/activation-notices.svelte      |  120 +
 .../components/workflow-editor/canvas-node.svelte  |   12 +
 .../workflow-editor/properties-panel.svelte        |   27 +-
 .../workflow-editor/property-field.svelte          |  407 ++-
 .../workflow-editor/version-panel.svelte           |  509 +++
 .../workflow-editor/workflow-editor.svelte         |  286 +-
 web/src/lib/dashboard/cursor-page.test.ts          |   71 +
 web/src/lib/dashboard/cursor-page.ts               |   64 +
 web/src/lib/dashboard/list-state.test.ts           |   65 +
 web/src/lib/dashboard/list-state.ts                |   69 +
 web/src/lib/dashboard/nav-sections.test.ts         |   39 +
 web/src/lib/dashboard/request-guard.test.ts        |   55 +
 web/src/lib/dashboard/request-guard.ts             |   44 +
 web/src/lib/datastore/columns.test.ts              |   97 +
 web/src/lib/datastore/columns.ts                   |   98 +
 web/src/lib/embed/embed-editor.svelte              |   50 +-
 web/src/lib/embed/session.svelte.ts                |   22 +-
 web/src/lib/embed/session.test.ts                  |   35 +-
 web/src/lib/workflow-editor/activation.test.ts     |  130 +
 web/src/lib/workflow-editor/activation.ts          |  108 +
 web/src/lib/workflow-editor/collection.test.ts     |  131 +
 web/src/lib/workflow-editor/collection.ts          |  148 +
 web/src/lib/workflow-editor/conditions.test.ts     |   82 +
 web/src/lib/workflow-editor/conditions.ts          |   80 +
 web/src/lib/workflow-editor/document.ts            |   10 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |    1 +
 web/src/lib/workflow-editor/execution.ts           |    6 +-
 .../lib/workflow-editor/fixed-collection.test.ts   |  131 +
 web/src/lib/workflow-editor/fixed-collection.ts    |   75 +
 web/src/lib/workflow-editor/history-diff.test.ts   |  273 ++
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 0 -> 10556 bytes
 web/src/lib/workflow-editor/node-visual.test.ts    |   11 +
 web/src/lib/workflow-editor/node-visual.ts         |   20 +
 .../lib/workflow-editor/resource-locator.test.ts   |  112 +
 web/src/lib/workflow-editor/resource-locator.ts    |  100 +
 .../lib/workflow-editor/resource-mapper.test.ts    |  121 +
 web/src/lib/workflow-editor/resource-mapper.ts     |  111 +
 .../lib/workflow-editor/version-history.test.ts    |  191 +
 web/src/lib/workflow-editor/version-history.ts     |  156 +
 web/src/lib/workflow-editor/visibility.ts          |   20 +
 web/src/routes/(dashboard)/+layout.svelte          |    1 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |  141 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  113 +-
 .../app/workflows/[id]/export-dialog.svelte        |  118 +
 .../app/workflows/diagnostics-section.svelte       |  120 +
 .../(dashboard)/app/workflows/import-dialog.svelte |  204 ++
 .../(dashboard)/app/workflows/import-report.svelte |  126 +
 .../routes/(dashboard)/credentials/+page.svelte    |   54 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |  209 ++
 .../(dashboard)/datastores/[id]/+page.svelte       |  809 +++++
 web/src/routes/(dashboard)/executions/+page.svelte |  192 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   17 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   54 +-
 web/src/routes/+page.svelte                        |    8 +-
 web/src/routes/approve/[token]/+page.svelte        |  173 +
 823 files changed, 149367 insertions(+), 2359 deletions(-)
```
