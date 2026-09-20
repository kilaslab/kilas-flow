---
title: Tenant-scoped nodes
description: Ship a node pack to one customer — declare the tenants it is visible to in the manifest or scope it from the operator's configuration, and know exactly what every other tenant sees.
---

A SaaS host that ships its own nodes — its CRM nodes, its WhatsApp nodes —
publishes them to every tenant of the deployment by default. Node visibility is
how you keep such a pack for one customer: the node type is registered once, but
a tenant that is not on its list does not see it and cannot run it, as if the
type did not exist.

The unit is the node **type**, not one version of it. Every registered version
of a scoped type shares one scope, so a workflow written against version 1 of a
scoped type cannot resolve downward to a version the tenant was never given.

## Declaring it in a manifest

A pack declares who may see it with the `visibleTo` manifest field, an array of
tenant IDs. The relevant fields of `pack.json` look like this (the full field
list is in the [node pack reference](/reference/node-packs/) — `parameters` and
`generator` are required there and elided here):

```json
{
  "type": "pack.acme.crm",
  "version": 1,
  "displayName": "Acme CRM",
  "category": "Acme",
  "visibleTo": ["acme"],
  "requestDefaults": { "baseURL": "https://api.acme.example" },
  "resources": []
}
```

An absent `visibleTo` keeps the type visible to every tenant, which is what every
pack shipped before this field did. An empty list is refused, at both the loader
and `nodepackgen validate`, because it would read either as "nobody" or as
"everybody" depending on who implemented it:

```text
pack.json: $.visibleTo: an empty visibleTo says neither nobody nor everybody: omit the field to keep the node visible to every tenant
```

`pack.json` is covered by the `pack.sha256` sidecar, so editing the manifest
means regenerating the sidecar the loader verifies (`nodepackgen pack -dir
./packs/acme`) before restarting — otherwise the boot refuses the pack for a
digest mismatch. The manifest field is in the [node pack
reference](/reference/node-packs/).

## The operator override

An operator can scope or re-scope an installed pack without touching its pinned
manifest, through the config key `packs.visible_to` (environment
`KILASFLOW_PACKS_VISIBLE_TO`, comma-separated). Each entry is written
`"<node type>=<tenant id>"`:

```yaml
packs:
  visible_to:
    - 'pack.acme.crm=acme'
```

A few rules make it predictable:

- **One entry per node type.** A WAHA install needs two entries, `pack.waha=acme`
  and `pack.wahaTrigger=acme`, because the trigger is a node type of its own.
- **It replaces the manifest's set** for that type. A single entry narrows what
  the manifest declared, and listing a different tenant widens it; the two are
  not merged.
- **There is no wildcard.** Every tenant is named explicitly.
- **It applies to any registered pack**, embedded in the binary or loaded from
  `packs.dir`.
- **Give every process the same value.** The API role hides an invisible node,
  but the worker is the authoritative gate, so a worker started without this key
  accepts and runs what the API would have refused.

The key is generated from the config schema; see the [configuration
reference](/operate/configuration-reference/).

## Where tenant IDs come from

A tenant ID is the ID of a tenant row. An operator creates one with
`POST /api/v1/tenants`, and the response's `id` is what a grant names. With
authentication off, which is the default, the deployment has a single tenant
named `default` and every caller resolves to it.

## What a tenant sees

A tenant that is not on the list sees the type exactly as if it were not
registered: it is absent from `GET /api/v1/node-types`, and the icon,
load-options and load-schema routes answer the same 404 they answer for an
unknown type, with the same body. No response names the tenants a type is
reserved for. An embed session is no exception — it resolves to its own tenant
before anything reads the catalogue (see [Embedding and
multi-tenancy](/guides/embedding/)).

An operator whose own caller is `default` sees the node, which is why the
verification recipe below narrows a type to a tenant other than `default`.

## What is refused

The catalogue is one gate; compilation and execution are the others. A document
that references a type the tenant may not see is refused with its own code,
distinct from a type that does not exist:

| Code | When |
| --- | --- |
| `node.unknown_type` | no such node type is registered anywhere |
| `node.unknown_version` | the type exists, but not at a version this install can resolve |
| `node.not_available` | the type exists and is registered, but is scoped to other tenants |

`node.not_available` is returned when a workflow is activated, when a version is
published, and when a manual run is queued. The worker re-checks at claim, so a
webhook, a schedule, a sub-workflow call, an error workflow and a resumed run are
all refused even if the record reached the queue another way: the execution fails,
its message carries the not-available text, and no node runs. Drafts are not
compiled, so a draft can still hold the type until someone activates it.

## Narrowing a scope later

A workflow that was active when a type was visible keeps its webhook and schedule
bindings after the operator narrows the scope — bindings are not re-evaluated at
configuration time. An inbound delivery is still accepted (`202`), and that run
then fails at claim with the not-available message; a run waiting for approval
fails the same way when it resumes. Deactivate such workflows before narrowing
the scope.

## Boot-time refusals

An override that cannot be honoured refuses the whole boot rather than starting
with a scope that matches nothing:

- a malformed entry, reported as `packs.visible_to[N]` — a missing `=`, an empty
  node type, an empty tenant ID, or a tenant ID containing `=`;
- an entry naming a node type that is not registered, so a typo in the type fails
  closed rather than silently scoping nobody;
- an entry naming a built-in type, which can never be scoped because the engine
  names some of them (the sub-workflow trigger, the error trigger);
- a `visibleTo` list present but empty in a manifest.

A typo in a *tenant ID* is not checked against the identity store, because
composition runs before identity bootstrap and tenant rows can legitimately
predate the tenants table. The boot log prints each scoped type with its tenant
list, in sorted type order, so a mistake is visible to the operator:

```text
level=info msg="node type is scoped to tenants" type=pack.acme.crm tenants=[acme]
```

## What this is not

- **Visibility, not a sandbox.** A scoped node still runs in the same process
  with the same privileges; the scope only decides who may use it. Isolation is
  the tenant's storage boundary, not this.
- **Credential types are deployment-wide.** `GET /credential-types` is not
  scoped to a tenant. A pack names credential types that exist; it does not carry
  one.
- **No runtime install and no per-tenant pack upload.** Packs are placed on disk
  or compiled in the binary, and the registry is read-only once serving begins.

## Verification recipe

Scope an installed pack to a tenant other than the caller's and confirm the
catalogue and a run both refuse it. With a scratch install (auth off), the caller
resolves to `default`:

```sh
# Baseline: the type is visible to the default tenant.
curl -s http://127.0.0.1:8080/api/v1/node-types | jq -r '.[].type' | grep '^pack\.acme\.crm$'

# Re-scope it away from the caller and restart with:
#   packs:
#     visible_to: ['pack.acme.crm=acme']
# The type is now absent, and the catalogue is not shared-cacheable.
curl -sD - -o /dev/null http://127.0.0.1:8080/api/v1/node-types | grep -i cache-control
curl -s http://127.0.0.1:8080/api/v1/node-types | jq -r '.[].type' | grep -c '^pack\.acme\.crm$'   # 0

# Activating a workflow that references it is refused with the new code.
curl -s -X POST http://127.0.0.1:8080/api/v1/workflows/<id>/activate | jq '.errors[].value.code'   # "node.not_available"
```

Switch the entry back to the caller's tenant (`pack.acme.crm=default`), restart,
and the type is listed again.
