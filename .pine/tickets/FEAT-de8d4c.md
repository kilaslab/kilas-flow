---
id: FEAT-de8d4c
title: Write the community node authoring guide
status: todo
priority: medium
labels:
    - docs
    - packs
deps:
    - FEAT-cwz4ac
    - FEAT-nxxbs5
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:58:23Z"
updated: "2026-09-05T11:58:23Z"
---

## Scope

With V2-p10-15 making a pack installable and V2-p10-16 making one authorable, this ticket writes the guide that turns those two capabilities into an ecosystem.

The format is unusually teachable because it is entirely data — an author writes JSON, not code, and the server interprets it. That is also why the guide has to be explicit about the boundary: an author arriving from n8n will expect to write `execute()`, `preSend` and function-form `postReceive`, and none of those exist here. `internal/routing` refuses `preSend` and function `postReceive` **at registration**, deliberately, rather than ignoring them — they are JavaScript closures with no data representation. An author who is not told this up front will discover it as a rejected pack.

What a guide has to cover, because every one of these is load-bearing and none is discoverable:

- **Resources and operations**, and that the `resource`/`operation` cascade is generated automatically with an internal options loader narrowing operations to the selected resource.
- **Declarative routing** — `requestDefaults`, `routing.request` with method, URL, headers, query and body, `routing.send` with its `body`/`query`/`path`/`binary` placements, and post-receive `rootProperty`, `setKeyValue` and `limit`.
- **Credentials** — that a pack names a credential *type* by string and never a credential, that authentication is declarative through `Authentication{Placement, Name, Value}` with `{{ field }}` templates, and that `$credentials` in a routing template resolves through `credentials.Split` to **non-secret fields only**.
- **Dynamic options** — `LoaderHTTP` and `LoaderInternal`, and that a loader is data rather than a function because anything that runs arbitrary code cannot keep the egress policy.
- **Triggers** — one output per event, HMAC verification, media download to the binary store, and declarative lifecycle registration that PUTs the minted webhook URL to a remote service on activation.
- **Icons** — `builtin:` lucide names cost no bytes; anything else ships bytes through `IconAsset` and is validated at registration, which refuses `<script>`, `<foreignObject>`, `on*=` handlers and external references, because the editor renders inside a customer's page and a hostile icon is cross-tenant stored XSS.
- **Versioning** — that the registry resolves downward, so registering `202409` and `202502` and requesting `202410` gets `202409`, which is what lets an imported workflow keep its original version.

The pedagogical asset already exists and is not labelled: `packs/telegram/pack.json` is a hand-written declarative pack with 23 operations, a credential type and an options cascade — the shape an author should imitate — while `packs/waha/*` is generated and carries a provenance block that would be wrong to copy.

## Acceptance criteria

- [ ] A reader with no Go knowledge and no prior exposure to this repository ships a working single-operation pack against a real API by following the guide start to finish.
- [ ] The guide is structured as a build-up — one operation, then a second, then a credential, then dynamic options, then a trigger — with the pack file shown complete at each stage rather than as fragments.
- [ ] What the routing interpreter refuses is stated early and with its reason, so an author does not design around `preSend` or a function `postReceive` before discovering they are rejected.
- [ ] The credential section states that only non-secret fields reach a routing template, and shows the correct way to place a secret through declarative authentication instead.
- [ ] The trigger section covers webhook binding, per-event fan-out, HMAC verification and lifecycle registration, with the activation-notice case where a URL must still be pasted into the remote service by hand.
- [ ] The icon section states the XSS rules as rules an author's pack will be refused for breaking, not as advice.
- [ ] Every code sample in the guide is a real file that `pack validate` accepts, verified rather than asserted.
- [ ] The guide ends with installation and distribution: where the pack directory is, how the checksum record is produced, and what an operator must do to trust a pack.

## Implementation Plan

Write it against a real third-party API that is small, publicly documented and needs a credential, so the credential and dynamic-options sections have something honest to demonstrate. Do not invent a fictional API — a guide whose examples cannot be run is a guide whose examples are never tested.

Build the running example up rather than presenting a finished pack and annotating it. The finished-artifact style reads well and teaches badly for a format like this, where the interesting knowledge is which field goes where and what happens when it is wrong.

Include a failure section, and give it real weight. The most valuable page in this guide will be the one listing what registration refuses and what each refusal means: an unknown property kind, a subtitle template reading anything but `$parameter`, a duplicate key at one level, an icon with a script element, a type claiming the `kilasflow.` prefix, a routing block with `preSend`. Those are the errors an author will actually hit, and `internal/node/registry.go` already produces good messages for them — the guide's job is to make the messages findable.

Sequence after V2-p10-16 for a concrete reason: `pack validate` is what makes the "every sample is real" criterion cheap to hold. Without it, verifying the samples means booting a server per example.

One judgement to state in the guide rather than leave implicit. An author will ask when a declarative pack is the wrong tool. The honest answer is that a pack covers an API that is regular — resources, operations, request shapes derivable from parameters — and does not cover an integration needing computation between calls, conditional pagination or a bespoke signing scheme. Say that plainly and point at `FEAT-48hreg`, so an author whose integration does not fit is not left concluding the format is broken.

## References

- Roadmap plan, p10 section, entry V2-p10-17: `.pine/roadmap.md`.
- `packs/telegram/pack.json` — the reference hand-written pack the guide builds toward.
- `packs/waha/README.md` — the existing prose on the pack contract, node type naming, and why versions are not additive.
- `internal/nodepack/nodepack.go` — `Pack`, `Resource`, `Operation`, `Parameter`, `Trigger`, `Provenance` and the generated cascade.
- `internal/routing/doc.go` and `internal/routing/routing.go` — the implemented subset, and the registration-time refusal of `preSend` and function `postReceive`.
- `internal/nodepack/trigger.go` — per-event outputs, HMAC, media download and the lifecycle block.
- `internal/credentials/registry.go`, `internal/credentials/builtin.go` — declarative `Authentication` and `TestRequest`.
- `internal/property/loader.go` — `LoaderHTTP`, `LoaderInternal`, and the comment on why a loader cannot execute.
- `internal/node/icon.go` — `BuiltinIconPrefix`, `IconAsset` and `ValidateIcon`.
- `internal/node/registry.go` — `Resolve`'s downward version rule and the refusal messages the failure section documents.
- `.pine/tickets/FEAT-48hreg.md` — V2-p8-5, where an integration that a declarative pack cannot express belongs.
