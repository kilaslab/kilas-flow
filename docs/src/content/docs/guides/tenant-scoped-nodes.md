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
- **A node's agent-tool form follows the node.** `pack.telegramTool` runs the
  Telegram node for an AI Agent, so scoping `pack.telegram` scopes its tool form
  with it, and an entry naming the tool form itself refuses the boot with a
  message naming the node to scope instead.
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
- an entry naming a built-in type: `packs.visible_to` scopes packs only, and a
  built-in cannot be given a tenant list because the engine names some of them
  (the sub-workflow trigger, the error trigger). To keep a built-in away from a
  tenant, use `nodes.deny` or `nodes.allow`, described
  [below](#allowing-or-denying-built-in-nodes);
- an entry naming a node's agent-tool form, which always shares the node's own
  scope;
- a `visibleTo` list present but empty in a manifest.

A typo in a *tenant ID* is not checked against the identity store, because
composition runs before identity bootstrap and tenant rows can legitimately
predate the tenants table. The boot log prints each scoped type with its tenant
list, in sorted type order, so a mistake is visible to the operator:

```text
level=info msg="node type is scoped to tenants" type=pack.acme.crm tenants=[acme]
```

## Allowing or denying built-in nodes

`packs.visible_to` cannot scope a built-in node, but a SaaS has good reasons to
keep some of them away from its customers: **SQLite** opens a file path on the
server, **Code** runs a script, **PostgreSQL** and **MySQL** dial out, and **HTTP
Request** reaches any URL the outbound policy lets through. Two lists decide
which built-ins a tenant may use: `nodes.deny` hides types, and `nodes.allow`
limits a tenant to the types it names.

A hidden built-in behaves exactly like a pack node scoped to other tenants. It is
absent from `GET /api/v1/node-types` and answers the unknown-type 404 on the icon,
load-options and load-schema routes, and a workflow that holds it is refused with
`node.not_available` when it is activated, when a version is published, when a
manual run is queued, and at the worker's claim (so webhook, schedule,
sub-workflow, error-workflow and resumed runs are refused too). The same
[narrowing caveat](#narrowing-a-scope-later) applies: deactivate affected
workflows before denying a type they use.

### In the deployment configuration

Each entry is written `"<node type>"` for every tenant, or `"<node type>=<tenant
id>"` for one tenant, the same `type=tenant` grammar as `packs.visible_to` (and
`KILASFLOW_NODES_DENY` / `KILASFLOW_NODES_ALLOW` take it comma-separated):

```yaml
nodes:
  deny:
    - kilasflow.sqlite            # nobody may use SQLite
    - kilasflow.code=acme         # acme may not use Code
  allow:
    - kilasflow.httpRequest=trial # trial tenants may use HTTP Request only
    - kilasflow.set=trial
    - kilasflow.manual=trial
```

The rules, for any one tenant:

- **Allow lists add up.** The allow list that applies to a tenant is every entry
  without a tenant, every entry naming it, and its own `nodes.allow` setting (see
  below). If that list is **not empty**, only the built-ins on it are visible to
  the tenant. If it is empty, `allow` limits nothing, which is the default.
- **Deny lists add up too**, and a built-in on one is hidden.
- **Deny wins over allow.** A type on both lists is hidden.
- **Engine-required types are always visible.** The error trigger
  (`kilasflow.errorTrigger`) and the sub-workflow trigger
  (`kilasflow.executeWorkflowTrigger`) are named by the engine, so an error
  workflow or a sub-workflow call would break from the inside without them. An
  allow list does not need to list them, and a deny entry naming one is refused.
- **Built-ins only.** A pack's nodes keep `packs.visible_to`; an entry naming a
  pack node is refused with a pointer to it.
- **A tool form follows its base.** `kilasflow.gmailTool` is the Gmail node
  offered to an AI Agent, so denying or allowing `kilasflow.gmail` does the same
  for its tool form, and an entry naming the tool form itself is refused with the
  node to name instead.

The boot refuses, naming the key and the entry's index (`nodes.deny[1]`), a
malformed entry (an empty type, an empty tenant after `=`, a tenant ID containing
a second `=`), a type that is not a registered built-in (a typo fails closed
rather than hiding nothing), a deny entry for an engine-required type, a tool
form, and a malformed tenant ID. Give every process the same value: the API hides
an unavailable node, but the worker is the authority, so a worker started without
the key accepts and runs what the API would have refused. The boot log lists the
entries it applied:

```text
level=info msg="built-in node types are denied" entries="[kilasflow.code=acme kilasflow.sqlite]"
```

### In a tenant's settings

The operator can also set the lists per tenant, live, through the
[tenant settings](/operate/tenants/#tenant-settings) section `nodes`:

```bash
curl -X PATCH "$KILASFLOW_URL/api/v1/tenants/acme" \
  -H "Authorization: Bearer $OPERATOR_KEY" \
  -H "Content-Type: application/json" \
  -d '{"settings":{"nodes":{"deny":["kilasflow.sqlite","kilasflow.code"]}}}'
```

Both fields are lists of node types (no `=tenant`: the tenant is the one in the
path), and a list is replaced whole by a patch that sets it. They are **added to
the deployment's lists**, so a tenant's `deny` adds to `nodes.deny`, its `allow`
adds to the deployment's allow entries, and deny still wins: a tenant setting can
narrow what the deployment offers and can never lift a `nodes.deny` entry.
Setting `nodes` to `null` removes the tenant's lists.

The patch is refused with `422` and `tenant_settings_invalid`, one error per
entry located at `body.settings.nodes.deny[1]`, when an entry is an
engine-required type on the deny list (`... cannot be denied`), is not a
registered built-in, or names a tool form. Nothing is written. Entries are
validated when written, not when read back, so a node type removed by a later
upgrade hides nothing and does not switch off the tenant's other entries.

A change applies at once on the process that served the `PATCH` and within five
seconds on every other process, the tenant-settings cache window. If a tenant's
lists cannot be read at all (the settings store fails and nothing is cached), the
tenant is shown none of the built-ins that can be hidden, only the
engine-required types and the packs' nodes, and an error is logged: a deny list
that failed open would be switched off by a settings outage.

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
