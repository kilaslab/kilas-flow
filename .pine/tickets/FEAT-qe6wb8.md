---
id: FEAT-qe6wb8
title: Ship the WAHA node pack for both published versions
status: todo
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-znm60y
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:01:00Z"
updated: "2026-09-05T05:01:00Z"
---

## Scope

Run the generator over WAHA's own MIT `openapi.json` and ship the result as a registered node pack, for both versions the n8n package publishes: `202409` and `202502`. This is the ticket where the whole native-first bet becomes visible — a customer's WAHA workflow finds a node with the same resources, the same operations and the same parameter names it was authored against, running in the Go binary with no npm package anywhere.

The pack needs a credential type it cannot have yet. WAHA authenticates with a base URL plus an `X-Api-Key` header, and `internal/credentials` is a closed package-level `map[string]Definition` holding exactly six types — `httpBasicAuth`, `httpHeaderAuth`, `httpBearerAuth`, `postgres`, `mysql`, `sqlite` — with an `Apply` switch that returns `credential type %q cannot authenticate an HTTP request` for anything else. `wahaApi` has to be registerable from outside that map, and the pack has to declare that it requires it. The editor side is closed too: `web/src/lib/workflow-editor/credentials.ts` maps node type to credential types in a hardcoded `BY_NODE_TYPE` table and `credentialTypesFor` returns `[]` for anything absent, so an unlisted node renders with no credential picker at all.

Two hidden defaults decide whether real templates work. The generator that produced the n8n package injects custom defaults that are not in the OpenAPI document: `session` defaults to `={{ $json.session }}` and `chatId` to `={{ $json.payload.from }}`. Official WAHA templates omit both parameters entirely and rely on those defaults, so a pack that treats absent as empty produces workflows that look correct, activate, and send nothing anywhere. Both defaults have to survive generation into KilasFlow's own explicit expression marker.

There is a live defect waiting for the `session` default in particular. `internal/execution/redact.go` lists `session`, `sessionid` and `sessiontoken` among its sensitive keys, and redaction runs at webhook ingest and on every executions write, with the runner rehydrating the trigger item from the redacted record — so `$json.session` resolves to `[redacted]` and every WAHA call goes to a session that does not exist. That is fixed elsewhere in the roadmap; this ticket owns the test that proves it stays fixed.

## Acceptance criteria

- [ ] Both `202409` and `202502` are registered as distinct versions of one node type, generated from the vendored spec, and the operation count for each is recorded in the ticket evidence from the spec itself rather than assumed.
- [ ] `resource` and `operation` option values are byte-identical to the values in the WAHA workflow JSON fixtures in the import corpus, checked by a test that reads the fixtures rather than a hand-copied list.
- [ ] A `wahaApi` credential type exists with a base URL and an API key field, the key is secret and never returned after storage, and it is applied as the `X-Api-Key` header on every request the pack makes.
- [ ] The credential's base URL supplies the routing base URL, and the credential's `AllowedDomains` scope is enforced on the resolved host exactly as it is for the HTTP Request node.
- [ ] The `session` and `chatId` defaults are present on every operation that takes them, expressed in KilasFlow's explicit expression marker, and an end-to-end test proves `session` reaches the outbound request as the real session name and not `[redacted]`.
- [ ] A send-text operation runs against a stub WAHA server through `safehttp` and the credential store, and the recorded execution shows the request and the response without the API key.
- [ ] Regenerating both packs from the unchanged spec produces no diff.
- [ ] The WAHA node renders on the canvas with a credential picker and its own icon rather than the fallback grey box.

## Implementation Plan

Vendor nothing into this repository: the spec lives in the reference checkout beside the n8n one, and only the generated pack is committed. Two spec files, two manifests, two pack files, one node type registered at two versions — the registry keys on `{type, version}`, so both coexist without special handling once versions are wider than the current `int`.

Take the credential work in the order that keeps the tree green: open credential registration first, register `wahaApi`, teach `Apply` a header-with-fixed-name form (it is `httpHeaderAuth` with the name pinned to `X-Api-Key`, so reuse rather than duplicate the header path), then generate. The base URL is the piece with no precedent — no existing credential type carries one, and the routing interpreter needs it before it can build a URL. Expose it to routing as a non-secret credential field, never as part of the applied auth.

Decide the node type string deliberately and write the decision down. It must be a KilasFlow type, not `@devlikeapro/n8n-nodes-waha.WAHA`; the importer maps the foreign string onto it. Use `kilasflow.waha`, keeping the namespace rule the rest of the catalogue follows, and leave the original type visible in the import diagnostics rather than in the node type itself.

The trap is the version pair. The two specs are not additive — operations move and parameters change between `202409` and `202502` — so generate both independently and never derive one from the other. A workflow imported at `202409` must keep resolving against the `202409` definition for the life of the process, which is exactly what the registry's immutability rule already guarantees.

## References

- Roadmap plan, p3 section, entry V2-p3-3: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- `github.com/devlikeapro/n8n-nodes-waha` (MIT), cloned outside this repository — `openapi.json` for both `202409` and `202502`.
- `internal/credentials/credentials.go` — the closed `definitions` map, `Field`, `Definition`, `Apply`'s switch, and `Record.AllowsHost`.
- `internal/execution/redact.go` — `session`, `sessionid` and `sessiontoken` on the sensitive-key list.
- `web/src/lib/workflow-editor/credentials.ts` — `BY_NODE_TYPE` and `credentialTypesFor`.
