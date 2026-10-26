---
title: Build nodes for your API
description: Choose between the five extension paths, write the smallest pack that really runs, pick a credential type that exists, and publish it to an operator-configured directory.
---

"Add nodes for my product's API" is the advertised extension path, and it is
real: a pack is JSON an operator drops into a directory, and your customers
get your API as workflow nodes. This page is the front door — how to choose
between the paths, the smallest pack that actually runs end to end, which
credential type to name, and how a pack reaches a server. The format reference
with every field is [Node pack format](/reference/node-packs/); the
build-one-by-hand tutorial is
[Authoring a community node pack](/guides/node-authoring/).

## Choosing a path

There are five ways a node reaches a workflow, and they answer different
problems. The first question is the one that decides everything else: **is
your API regular?** Resources, operations, request shapes derivable from
parameters — or does a call need computation between steps?

| Path | You write | Covers | Does not cover |
| --- | --- | --- | --- |
| Hand-written declarative pack | JSON (`pack.json`) | Regular APIs: resources, operations, routing as data | Computation between calls, conditional pagination, bespoke signing |
| OpenAPI-generated pack | A small generation manifest | The same, when your API already has an OpenAPI 3 document — the pack is generated, committed and reviewed | Anything the document does not say (see its report) |
| Tenant-scoped node | The same pack, plus `visibleTo` | Shipping one pack to one customer's tenant only | Anything new — it is a visibility rule, not a node kind |
| WASM module pack | Go (or any wasip1 language), compiled | Real computation, with capabilities, memory and call limits enforced | Unbounded access — the module gets only what its manifest grants |
| JavaScript sidecar | Nothing — an operator installs an npm package | Existing n8n community packages with a real `execute()` | Anything you would author: it exists to run other people's code |

The decision, as a walk:

1. **Your API is regular and you can write JSON** — a hand-written
   [declarative pack](/guides/node-authoring/). Start from
   `nodepackgen scaffold` and grow it operation by operation.
2. **Your API is regular and already has an OpenAPI document** —
   [generate the pack](/guides/openapi-pack/) and read its report. The
   document names the operations; a small manifest supplies the decisions the
   document cannot make (node type, credential type, defaults).
3. **The pack is for one customer, not the whole deployment** — the same pack
   plus a visibility scope: [Tenant-scoped nodes](/guides/tenant-scoped-nodes/).
4. **A call needs computation, or your signing scheme is bespoke** — the pack
   format is deliberately data and refuses it. Write a
   [WASM module pack](/reference/node-packs/#module-packs).
5. **You need an existing n8n community package, not your own node** — the
   [JavaScript sidecar](/operate/javascript-sidecar/) runs it, opt-in, at a
   cost the linked page states plainly.

## The smallest pack that runs

`nodepackgen scaffold` writes a pack directory, and the two flags that matter
for "my API" are the credential type and the base URL. Left at their defaults,
the scaffold names a credential type called `exampleApi` — which is not in the
catalogue, and never can be: a pack cannot add a credential type, so no
credential of that type can ever be bound, and a workflow that uses the node
is refused at validation with `node "p" requires a exampleApi credential`.
Name a type that exists and the scaffold's output runs end to end:

```sh
$ nodepackgen scaffold -dir ./packs/acme \
    -type pack.acme -display-name Acme \
    -credential-type httpHeaderAuth \
    -base-url https://api.acme.example
nodepackgen: scaffolded ./packs/acme/pack.json
$ nodepackgen validate ./packs/acme
nodepackgen: packs are valid
```

The manifest this produces is the worked example the
[authoring guide](/guides/node-authoring/) grows step by step: one resource
(`message`), one operation (`sendMessage`), two parameters, and
`requestDefaults` holding the base URL you passed. The credential the
operator binds — an [HTTP Header Auth](/concepts/credentials/) credential
naming your header — signs every request; the pack never sees its value.

## Credentials for your API

A pack names a credential *type* by string; it never carries a credential.
The type says how a secret is attached to an outbound request, as data
(`Authentication` in `internal/credentials/registry.go`), so most APIs need no
new type at all. For "my product's API", choose from the generic HTTP types:

| Your API authenticates with | Type | The operator's credential holds |
| --- | --- | --- |
| A fixed header (`X-Api-Key: …`) | `httpHeaderAuth` | the header name and the value |
| `Authorization: Bearer …` | `httpBearerAuth` | the token |
| HTTP basic auth | `httpBasicAuth` | user and password |
| A fixed query parameter (`?api_key=…`) | `httpQueryAuth` | the parameter name and the value |
| Several headers and parameters at once | `httpCustomAuth` | a JSON template naming all of them |

Two rules shape what the rest of the pack can do with the credential:

- **`$credentials` in a routing template resolves to non-secret fields only.**
  A secret never passes through a template; the type's declarative
  authentication attaches it to the request instead.
- **Where the base URL lives depends on the type.** The generic types carry no
  address, so the pack's `requestDefaults.baseURL` is either a literal
  (`"https://api.acme.example"`) or a template over a non-secret field of a
  type that has one (`wahaApi` names `baseUrl`, which is why the WAHA pack
  templates `{{ $credentials.baseUrl }}` and works).

And one answer to the question the audit found the docs disagreeing on:
**a pack cannot add a credential type.** A new *named* type — one that shows
up in the credential picker as its own form — is a Go change in
`internal/credentials/builtin.go`: a `Type` value declaring fields, which are
secret, how it authenticates, and where an empty `allowedDomains` may send it.
The registry is built in a package initialiser and nothing in startup lets a
pack contribute one. Until your type lands, the generic types above cover the
APIs a pack can call; see [Credentials](/concepts/credentials/) for the whole
catalogue and the storage and scoping rules.

## Calling your API over a private network

A host embedding KilasFlow usually wants the pack to call the host's own API —
the same product, on an internal address. The deployment's egress policy
refuses private and loopback addresses by default, exactly so a workflow
editor cannot point a credentialed request at the network interior. The
operator admits one endpoint at a time with `outbound.allowed_private_endpoints`
(environment `KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS`):

```yaml
outbound:
  allowed_private_endpoints:
    - 'hostapi.internal:8443'
```

An entry is an exact `host:port`, and an entry that is not both is refused at
startup rather than ignored. It does not open a subnet — each dependency is
named — and the grant does not follow redirects to other private addresses:
every hop is re-checked, and only the named endpoint passes. The wider
`outbound.allow_private_networks` switch exists too, and the two are not
alternatives of the same size: this one names an endpoint, that one hands
every outbound request the whole internal network. The egress policy is
narrowing on top of this, never replaced by it — a credential's
`allowedDomains` still applies to the request that carries it. See
[safety boundaries](/concepts/safety-boundaries/) and the
[configuration reference](/operate/configuration-reference/).

## Events from your app

If your product *delivers* events rather than only receiving calls, a trigger
pack declares them: one output per event, an inbound webhook route minted on
activation, and an HMAC scheme that decides which deliveries are yours. When
your app is the sender, you are also the signer — the exact recipe, in Node
and Go, is [App events as triggers](/guides/app-events/). Two decisions
belong here because the pack declares them:

- **`deliveryIdHeader`** names the header your app fills with its own delivery
  identifier. A retried delivery with the same identifier is answered without
  running the workflow again — the dedup window is five minutes, and the
  repeated answer mirrors the original outcome. Without it, a retry runs the
  workflow twice, because two genuinely identical bodies are not a duplicate.
- **`hmac`** names the signature scheme. `algorithm` (`sha256`/`sha512`),
  `encoding` (`hex`/`base64`), `prefix`, a `signingString` template and a
  timestamp tolerance are all declarable, and the
  [Standard Webhooks](https://standardwebhooks.com) scheme is one word
  (`"preset": "standardWebhooks"`). A delivery that fails verification is
  refused before an execution exists.

## Publishing and installation

A pack you write is your JSON to distribute; what runs on a server is what
that server's operator placed there and approved with a checksum. There is no
registry, no upload endpoint and no hot reload — by design, the same line the
project draws for executable sidecars.

For the operator, installation is four steps:

```sh
$ cp -r acme/ /etc/kilasflow/packs/acme
$ nodepackgen pack -dir /etc/kilasflow/packs/acme
nodepackgen: wrote /etc/kilasflow/packs/acme/pack.sha256
$ systemctl restart kilasflow
```

1. Place the pack directory under the directory named by `packs.dir`
   (`KILASFLOW_PACKS_DIR`). Each immediate subdirectory is one pack.
2. Write the checksum sidecar the loader verifies, in `sha256sum`-compatible
   shape. The loader hashes `pack.json` and refuses a pack whose bytes no
   longer match, or that has no sidecar at all.
3. Restart. Packs load at composition, before the registry is shared; an
   absent or empty directory is a normal, silent condition. A pack that fails
   to load refuses the whole boot, naming the pack and the reason.
4. The operator binds a credential of the type the pack names, per tenant —
   the pack never carries one.

Upgrading follows the same steps, with two behaviours that make it boring:

- **Versions resolve downward, never upward.** Registering version 2 while a
  customer's workflow declares version 1 keeps that workflow on version 1
  until its document changes; an imported workflow keeps its original version
  the same way. Ship a new version alongside, not instead.
- **The checksum is the upgrade gate.** New `pack.json` bytes, new
  `pack.sha256` — the loader refuses the old record against new bytes, so an
  operator cannot silently drift from what they approved.

To ship to one customer rather than every tenant, add `visibleTo` (or have the
operator set `packs.visible_to`) — see
[Tenant-scoped nodes](/guides/tenant-scoped-nodes/) for both, and for what a
tenant that is not on the list sees.
