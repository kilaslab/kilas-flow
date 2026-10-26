---
title: Tenants, provisioning and identity
description: The operator credential, provisioning a tenant and its first user and key over the API or the SDK, scoped keys and what each scope allows, and what the identity model does and does not include.
sidebar:
  order: 5.5
---

A deployment that serves one customer needs no provisioning: authentication
off, one tenant named `default`, every caller is it. The moment a second
customer arrives, three questions need answers — who may create tenants, how
a customer's first credential gets into their hands, and what a credential
may do once it has one. This page is those answers, and every command on it
has been run against a real server with authentication enabled.

## The operator credential

Tenants are operator-managed: there is no self-service signup endpoint. The
one credential that may provision customers is an API key **registered at
boot, not minted** — you generate it, put it in the environment, and the
server registers its hash on every start:

```sh
# A key in the shape the server refuses to boot without:
# kfa1_<hex prefix>_<secret>
export KILASFLOW_AUTH_OPERATOR_KEY="kfa1_$(openssl rand -hex 6)_$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')"
```

The shape is enforced: `kfa1`, then a non-empty hex prefix, then the secret
half, joined by underscores — `an API key token must be shaped like
kfa1_<hex prefix>_<secret>` is the refusal a malformed value produces, and it
refuses the whole boot, not just the registration. Minted keys carry a
12-hex-character prefix; the operator key only needs the middle segment to
be valid hex. Registration is idempotent — the row is keyed by the token's
own prefix, so a restart neither disables the key nor issues a second one —
and the boot log says it happened:

```text
level=INFO msg="registered the operator API key" tenant=operator
```

Three behaviours to know before you rely on it:

- **Operator routes need `auth.enabled`.** The gate is the handler's, not the
  middleware's: with authentication off there is no principal at all, and
  every operator route answers `403 this endpoint is reserved for the
  operator` to everyone. An operator key set on an auth-off deployment
  registers fine and opens nothing.
- **Only the key, never a session.** Even a signed-in account whose tenant is
  the operator tenant is refused; provisioning is a machine action and the
  credential for it is a credential you can put in a pipeline.
- **Rotation is restart + revoke.** A changed environment variable takes
  effect on the next boot; until then the old key keeps working, and a
  restarted server with a new key leaves the old row valid until you revoke
  it with `DELETE /api/v1/api-keys/{id}`. Emptying the variable does not
  unregister a key that is already stored.

The variable's own name is configurable (`auth.operator_key_env`, default
`KILASFLOW_AUTH_OPERATOR_KEY`) — see the
[configuration reference](/operate/configuration-reference/).

## Provisioning: tenant, user, key

The operator surface is ten operations under `/api/v1`, all requiring the
operator credential ([the generated reference](/reference/api/tenants/) has
every field). The provisioning sequence a host runs per customer is three
calls, and each one was verified against a live server for this page:

```sh
BASE=http://127.0.0.1:8080
OP="Authorization: Bearer $KILASFLOW_AUTH_OPERATOR_KEY"

# 1. The tenant. The ID is stable — it appears in URLs and on every row
#    scoped to this tenant — and must match ^[a-z0-9][a-z0-9_-]*$.
#    A taken ID answers 409, so a mistyped create is never mistaken
#    for a successful one.
curl -X POST "$BASE/api/v1/tenants" -H "$OP" -H 'content-type: application/json' \
  -d '{"id":"acme","name":"Acme"}'
# → 201 {"id":"acme","name":"Acme","createdAt":"…","userCount":0}

# 2. The tenant's first user: a dashboard account. The email is unique
#    across the deployment, because login presents an address and nothing
#    else; the password is hashed with PBKDF2 before it reaches the store.
curl -X POST "$BASE/api/v1/tenants/acme/users" -H "$OP" -H 'content-type: application/json' \
  -d '{"email":"dev@acme.example","name":"Dev","password":"…"}'
# → 201 {"id":"usr_…","tenantId":"acme","email":"…","createdAt":"…"}

# 3. The tenant's first API key — the credential your backend holds.
curl -X POST "$BASE/api/v1/tenants/acme/api-keys" -H "$OP" -H 'content-type: application/json' \
  -d '{"label":"acme backend"}'
# → 201 {"id":"…","prefix":"…","label":"acme backend","token":"kfa1_…","createdAt":"…"}
```

The token in step 3 is shown exactly once — this is the one surface with no
read-back path for another tenant's keys, because the key listing is scoped
to the caller's own tenant. Store it then. With the SDK the same three calls
are:

```ts
// import { KilasFlowClient } from "@kilasflow/sdk/server";
// Every method here requires the operator credential.
const tenant = await client.createTenant({ id: "acme", name: "Acme" });
const user = await client.createTenantUser(tenant.id, {
  email: "dev@acme.example",
  name: "Dev",
  password
});
const key = await client.createTenantApiKey(tenant.id, { label: "acme backend" });
// key.token — shown exactly once
```

The rest of the surface: `GET /tenants` and `GET /tenants/{id}` read back
(`userCount` included), `disable`/`enable` on a user stop and resume the
account signing in (a disabled account's sessions are cut too), a password
set replaces the old one immediately, and `DELETE /tenants/{id}` purges the
tenant — everything that purge removes, and its idempotence, is
[Tenant deletion](/operate/tenant-deletion/).

## Isolation: what a tenant's key can reach

Every row the server stores carries a tenant identifier, and every repository
call takes a tenant scope resolved from the caller's credential. The
consequence has a precise wording worth internalizing: **a row belonging to
another tenant does not fail a permission check — it does not exist.** The
`acme` key above lists one workflow after `birch` is created, because it was
never shown anything else. Verified live:

```sh
$ curl "$BASE/api/v1/workflows" -H "Authorization: Bearer $ACME_KEY"
# → only acme's workflows, always
$ curl "$BASE/api/v1/tenants" -H "Authorization: Bearer $ACME_KEY"
# → 403 this endpoint is reserved for the operator
```

Tenants share one database and one process; the boundary is the storage
scope, not a sandbox. Two deliberate exceptions live outside it: the
pre-authentication lookups login needs (finding an account by email,
authenticating a key by prefix), and the operator surface itself.

## Scoped API keys

A tenant-wide key — what step 3 above mints — can do everything its tenant
can. For a backend that only needs some of it, `POST /api/v1/api-keys` mints
a **scoped key**: a list of scopes, optionally bound to one workflow, with
optional expiry:

```sh
curl -X POST "$BASE/api/v1/api-keys" -H "Authorization: Bearer $ACME_KEY" \
  -H 'content-type: application/json' \
  -d '{"label":"etl runner","scopes":["workflow:run"],"workflowId":"wf_…"}'
```

An API key may hold five scopes (two more exist but are embed-session-only —
an API key cannot publish a revision or change activation, those stay with
the tenant-wide key):

| Scope | Allows |
| --- | --- |
| `workflow:read` | Read the tenant's (or the bound) workflow: document, versions, diagnostics, webhooks; validate and convert documents; read executions; read the node catalogue and credential types and names. |
| `workflow:write` | Save and edit workflows (`create`/`update`/`duplicate`), implied to include `workflow:read`. |
| `workflow:run` | Run the bound workflow and retry its executions; implies `workflow:read`. |
| `datastore:read` | Read a datastore's rows and definition. |
| `datastore:write` | Insert, update, upsert and delete rows, and import CSV; implies `datastore:read`. |

Two implications are built in (`workflow:write` and `workflow:run` each imply
`workflow:read`; `datastore:write` implies `datastore:read`) and nothing
crosses families — a datastore-only key cannot so much as list workflows.
With a `workflowId`, the binding is absolute: other workflows answer `404`
`Workflow not found.`, not `403`, because a token confined to one workflow
must not be able to probe which sibling ids are real. Refused operations
carry `403` with a sentence naming what the key cannot do.

`expiresAt` (RFC 3339) stops the key working from that moment — no cron, the
check is per request. A past value is refused at mint (`the key would never
work`). Rotation is the same everywhere: mint the successor, move new work
to it, revoke the old (`DELETE /api/v1/api-keys/{id}` — immediate and
permanent, the row kept so an audit has something to name). A scoped key
cannot mint another key; that is a tenant-wide action by design.

## 401 and 403

The two statuses mean different things and every client should branch on
them:

- **401** is *who are you*: no credential, an unknown, revoked or expired
  key, an invalid session. The answer is identical for all of them on
  purpose — an unknown key and a wrong secret are indistinguishable, and
  both mean "get a new credential". Retrying with the same credential will
  not help; rotating will.
- **403** is *not you*: authenticated, and either not the operator, or a
  scope the credential does not hold. Retrying will never help; minting a
  differently-scoped credential might.

## Authentication for humans: sessions, and no SSO

Dashboard login is an email and a password: `POST /api/v1/auth/login` sets an
`HttpOnly`, `SameSite=Lax` session cookie (12 hours by default,
`auth.session_ttl`), stateless and signed with the auth signing key. There is
**no SSO today** — no OIDC, SAML or LDAP connector exists in the server, and
an integrator evaluating "auth/SSO" should read that as a hard no for this
release. The recommended pattern for a host product is the one the embed
model already implies: **your product authenticates its users however it
authenticates them, and it mints KilasFlow credentials — tenant keys, scoped
keys, embed sessions — on their behalf.** KilasFlow never sees your users;
it sees your backend's credentials. See
[Embedding and multi-tenancy](/concepts/tenancy-and-embedding/) and
[Security posture](/operate/security/).

## A note on the first boot

Turning `auth.enabled` on for the first time needs three things on that same
first start, or the lock closes before the key is cut: the signing key
(`KILASFLOW_AUTH_SIGNING_KEY`, 32 bytes), a bootstrap account
(`auth.bootstrap_email` + `KILASFLOW_BOOTSTRAP_PASSWORD` — created once, and
only on an installation that has no users), and — if provisioning is part of
the plan — the operator key above. [Deployment](/operate/deployment/) and
[Configuration](/operate/configuration/) cover the rest of the boot.
