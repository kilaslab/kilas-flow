---
title: SaaS integration playbook
description: The whole integration in order, from provisioning a customer's tenant in your control plane to production, plus a recipe for using a workflow as a synchronous tool for your own AI agent.
---

This page walks one integration from start to finish. It is for the team that
will put KilasFlow inside a multi-tenant product: each of your customers gets
their own workflows, credentials, runs and data tables, and your users edit
workflows in an editor mounted in your own pages.

It links to the deep pages instead of repeating them. Read it once in order,
then keep the sections you need open.

What you build, in the order you build it:

1. [Provision a tenant](#1-provision-a-tenant-from-your-control-plane)
2. [Store and rotate the tenant key](#2-store-and-rotate-the-tenant-key)
3. [Map your roles to scopes](#3-map-your-roles-to-scopes)
4. [Build the list and create UI](#4-build-the-list-and-create-ui-with-the-tenant-key)
5. [Mint embed sessions and seed confinement](#5-mint-embed-sessions-and-seed-confinement)
6. [Proxy activation](#6-proxy-activation)
7. [Provision credentials](#7-provision-credentials)
8. [Dispatch host events into workflows](#8-dispatch-host-events-into-workflows)
9. [Make every write idempotent](#9-make-every-write-idempotent)
10. [Delete a tenant](#10-delete-a-tenant)
11. [Watch it in production](#11-watch-it-in-production)
12. [Go-live checklist](#12-go-live-checklist)

Then a recipe: [a workflow as a synchronous tool for your AI agent](#recipe-a-workflow-as-a-synchronous-tool-for-your-ai-agent).

## How the pieces fit

Four credentials exist. Each lives in exactly one place:

| Credential | Looks like | Lives in | Can do |
| --- | --- | --- | --- |
| Operator key | `kfa1_<hex>_<secret>` | your control plane only | create and delete tenants, mint a tenant's first key |
| Tenant key | `kfa1_<hex>_<secret>` | your backend only, one per customer | everything inside that one tenant |
| Embed session token | `kfe1.…` | the browser, for 15 minutes | one workflow (or one data table), the scopes you chose, one origin |
| Webhook secret | whatever the trigger checks | the system that sends the delivery | start one workflow |

Your users never hold a key. They hold a token that your backend minted after
your own authorization check. KilasFlow never sees your users. It sees your
backend's keys. There is no single sign-on.

Everything below was either taken from `sdk/examples/reference-host`, the
two-tenant host in the repository, or run against a real server with
authentication on while this page was written. The generated
[API reference](/reference/api/) shows every field of every operation, and the
[configuration reference](/operate/configuration-reference/) lists every key.

The SDK is not on npm yet, so install it from a checkout
([Using the host SDK](/guides/sdk/#install-from-a-checkout-until-the-first-release)).
Every call below is also a plain HTTP request, and the curl form is shown where
the SDK form is not obvious.

### Run the server for this

A host deployment needs authentication, a credential encryption key and the
embed settings. These are the variables the reference host's setup uses:

| Variable | What it is for |
| --- | --- |
| `KILASFLOW_AUTH_ENABLED=true` | Without it every request is the shared `default` tenant and none of the keys below do anything. |
| `KILASFLOW_AUTH_SIGNING_KEY` | 32 bytes. Signs dashboard sessions. |
| `KILASFLOW_AUTH_OPERATOR_KEY` | The operator key, in the shape `kfa1_<hex>_<secret>`. |
| `KILASFLOW_AUTH_BOOTSTRAP_EMAIL`, `KILASFLOW_BOOTSTRAP_PASSWORD` | The first dashboard account. Needed on the first start with authentication on. |
| `KILASFLOW_ENCRYPTION_KEY` | Seals stored credentials. Without it you cannot store one. |
| `KILASFLOW_EMBED_SIGNING_KEY` | Exactly 32 bytes. Without it embedding answers `503`. |
| `KILASFLOW_EMBED_ALLOWED_ORIGINS` | Every origin that frames the editor, exactly. An empty list disables embedding. |
| `KILASFLOW_SERVER_PUBLIC_URL` | The address browsers use to reach KilasFlow. Set it behind a proxy. |

The full list, with the reasoning for each, is the
[operator checklist](/guides/embedding/#operator-checklist). The production
topology is [Deploy for SaaS](/operate/deploy-for-saas/).

## 1. Provision a tenant from your control plane

A tenant is one customer's slice: its own workflows, credentials, runs, data
tables and keys. Another tenant's rows do not fail a permission check for you.
They do not exist. Map **one KilasFlow tenant to one customer**. A shared tenant
with host-side partitioning gives up every isolation guarantee
([the mapping decision](/guides/embedding/#the-mapping-decision)).

Tenants are created through the API with the operator key. There is no
self-service signup:

```sh
BASE=https://flows.example.com
OP="Authorization: Bearer $KILASFLOW_AUTH_OPERATOR_KEY"

# The tenant. The id is stable, appears in URLs and on every row, and must match
# ^[a-z0-9][a-z0-9_-]*$. A taken id answers 409.
curl -X POST "$BASE/api/v1/tenants" -H "$OP" -H 'content-type: application/json' \
  -d '{"id":"acme","name":"Acme"}'

# The tenant's first API key: the credential your backend will hold.
curl -X POST "$BASE/api/v1/tenants/acme/api-keys" -H "$OP" -H 'content-type: application/json' \
  -d '{"label":"acme backend"}'
# 201 {"id":"key_…","prefix":"c5d70e1c3ba1","label":"acme backend","token":"kfa1_…","createdAt":"…"}
```

With the SDK, the same calls need the operator key as the client's `apiKey`:

```ts
const tenant = await operator.createTenant({ id: 'acme', name: 'Acme' });
const key = await operator.createTenantApiKey(tenant.id, { label: 'acme backend' });
// key.token is shown exactly once
```

A tenant needs no user account to be embedded. `POST /tenants/{id}/users`
creates a dashboard login, which you want only if a person will sign in to the
KilasFlow dashboard for that tenant.

Keep the mapping from your customer to the tenant id in your own database, and
make the provisioning job safe to re-run. A `409` on the tenant means it exists.
Mint another key only if you did not store the first. The operator key itself
is registered at boot and has its own rotation; see
[Tenants, provisioning and identity](/operate/tenants/#the-operator-credential).

## 2. Store and rotate the tenant key

The token in the response is shown once. KilasFlow keeps only a hash and cannot
show it again.

- Store it in your secret manager, encrypted at rest, under the customer's id.
  Store the key's `id` and `prefix` beside it. The `id` is what you revoke by,
  and the `prefix` is how you recognise the key in a log.
- Never send it to a browser, put it in a URL or log it.
- Build one client per tenant with the key fixed, and never swap keys on a
  shared client. `tenantClientFactory` does this
  ([reference host](https://github.com/kilaslab/kilas-flow/blob/main/sdk/examples/reference-host/server.mjs)):

```js
import { tenantClientFactory } from '@kilasflow/sdk/server';

const forTenant = tenantClientFactory({ baseUrl: process.env.KILASFLOW_URL });
const client = forTenant(await secrets.tenantKey(organisationId));
```

**Rotate** by minting a successor with the current key, moving your backend to
it, then revoking the old one with the new one. This sequence was run against a
real server, and the old key answered `401` straight after the revoke:

```sh
# 1. The current tenant key mints its successor. Omit `scopes` for a tenant-wide key.
curl -X POST "$BASE/api/v1/api-keys" -H "Authorization: Bearer $OLD_KEY" \
  -H 'content-type: application/json' -d '{"label":"acme backend 2"}'

# 2. Store the new token, and switch your backend to it.

# 3. Revoke the old key by its id, with the new key. It stops working at once.
curl -X DELETE "$BASE/api/v1/api-keys/$OLD_KEY_ID" -H "Authorization: Bearer $NEW_KEY"
```

If you lost the key, the operator mints a new first key with
`POST /tenants/{id}/api-keys`. Use that key to list the tenant's keys
(`GET /api-keys`) and revoke the lost one. An operator cannot list or revoke
another tenant's keys directly, because key listing is scoped to the caller's own
tenant. Set `expiresAt` when you mint a key that should not live forever.

## 3. Map your roles to scopes

KilasFlow has no roles for the people who use your product. It has scopes, and
you put them into a token when you mint it. Your roles map onto scopes in your
code. This is a mapping you decide. One that works:

| Your role | Embed session scopes | The user can |
| --- | --- | --- |
| Viewer | `workflow:read` | open the workflow |
| Editor | `workflow:read`, `workflow:write`, `workflow:run` | edit drafts and test-run them |
| Publisher | the editor's, plus `workflow:publish` | pin a revision as the version production runs |
| Admin | the publisher's, plus `workflow:activate` | switch the workflow on and off |
| Data user | `datastore:read`, `datastore:write` on a datastore session | read or write one table's rows |

Three rules follow from how the scopes work:

- **Nothing implies write, run, publish or activate.** `workflow:write`,
  `workflow:run`, `workflow:publish` and `workflow:activate` each imply
  `workflow:read` and nothing else, so an editor cannot change what is live
  ([publish and activate authority](/guides/embedding/#publish-and-activate-authority)).
- **The check is yours, and you run it at every mint.** The server confirms the
  workflow exists in the tenant. It cannot know whether this user may touch it.
- **Hiding a button is not a permission.** `branding.hideRun` and `hideSave` are
  presentation only. The server enforces the scopes.

For your own jobs, not for people, mint a **scoped API key** instead: up to five
scopes (`workflow:read`, `workflow:write`, `workflow:run`, `datastore:read`,
`datastore:write`), optionally bound to one workflow, optionally expiring.
A key bound to one workflow with only `workflow:run` can start, retry and read
that workflow and its runs, and every other workflow answers `404`. See
[Scoped API keys](/operate/tenants/#scoped-api-keys).

## 4. Build the list and create UI with the tenant key

An embed session is confined to one workflow. It cannot list the tenant's
workflows, because that would enumerate them. So your workflow list and your
"New automation" button are your own pages, backed by your own routes, which call
KilasFlow with the tenant key after your authorization check:

```js
const client = forTenant(await secrets.tenantKey(user.organisationId));

// The list. Returns one page as a bare array; the next-page cursor is in the
// X-Next-Cursor response header (see Pagination).
const workflows = await client.listWorkflows();
// [{ id, name, active, latestRevision, updatedAt, tags }]

// Create. A workflow document needs schemaVersion, name, nodes, connections and settings.
const created = await client.createWorkflow({
  schemaVersion: 1,
  name: 'Welcome flow',
  nodes: [],
  connections: [],
  settings: {}
});
```

The same calls over HTTP are `GET /api/v1/workflows` and `POST /api/v1/workflows`
with `Authorization: Bearer <tenant key>`. To start a customer from a template,
create the workflow from your own document, or import an n8n document with
`importWorkflow`, which also reports the webhook address it minted. Listings
use two cursor conventions, so read [Pagination](/reference/api/pagination/)
before you page through a long list.

Your "Edit" button is the next step. It sends the user to a page of yours that
mints a session for that one workflow.

## 5. Mint embed sessions and seed confinement

When a user opens a workflow, your backend checks that this user may edit it,
then asks KilasFlow for a session. This is the call, with the optional `actor`
so the audit trail names the user (this exact request was run against a real
server):

```js
const session = await client.createEmbedSession({
  workflowId,
  scopes: ['workflow:read', 'workflow:write', 'workflow:run'],
  origin: 'https://app.example.com', // a constant in your code, never a request header
  branding: { name: 'Acme Flows', accent: '#0ea5e9' },
  actor: { id: user.id, displayName: user.name }
});
// 201 { token: 'kfe1.…', embedUrl, expiresAt, origin, scopes, branding }
```

```sh
curl -X POST "$BASE/api/v1/embed-sessions" -H "Authorization: Bearer $TENANT_KEY" \
  -H 'content-type: application/json' \
  -d '{"workflowId":"wf_…","scopes":["workflow:read","workflow:write","workflow:run"],"origin":"https://app.example.com","actor":{"id":"user_17","displayName":"Ada"}}'
```

The token is valid for 15 minutes by default and 30 at most. Mint on every page
load. Sixty seconds before expiry the editor posts `session-expiring`, and your
page fetches a fresh token and hands it over with `editor.setSession(session)`.
The editor swaps it in without reloading, so no work is lost.

Return the whole response to your page, including `branding`, and add the
KilasFlow address as `baseUrl` the way the reference host does. Then mount the
editor with the SDK's `mountWorkflowEditor`. The handshake, the
refresh and the page-side events are in
[Embedding and multi-tenancy](/guides/embedding/#4-mount-the-editor-watch-the-run).
Three settings must be right or the editor fails silently: the origin is
exact (`scheme://host:port`, no wildcards), it must be in
`KILASFLOW_EMBED_ALLOWED_ORIGINS`, and `KILASFLOW_SERVER_PUBLIC_URL` must be set
behind a proxy.

### Seed the confinement before you mint

A session is confined. The workflow a user edits may use only the credentials,
data tables and sub-workflows that the workflow already uses. Anything else is
refused on save, publish, activate, restore, run and retry. KilasFlow reads that
list from the workflow's own document **at mint time** and writes it into the
token. It reads the active revision, or the latest saved revision when none is
active.

So seeding is a step of your provisioning, and it comes before the mint:

1. Create the workflow from your template.
2. Reference every credential, data table and sub-workflow the user is allowed
   to use, in the document. A node that holds a credential id is how the
   credential gets into the list.
3. Mint the session.

A credential you add to the workflow after the mint is not in that session's
list. Mint again. A credential that a node sends over HTTP must have
`allowedDomains`, or the session may not save a document that attaches it
([credentials in a confined caller's document](/concepts/tenancy-and-embedding/#credentials-in-a-confined-callers-document)).

## 6. Proxy activation

Activating a workflow publishes an endpoint for the whole deployment, so no
scope an editor carries grants it. Your "Go live" button has two valid designs:

- **Proxy it (the default).** The button calls your backend. Your backend
  checks entitlement and calls KilasFlow with the tenant key:

  ```js
  await client.publishWorkflowVersion(workflowId, versionId, 'customer requested');
  await client.activateWorkflow(workflowId); // deactivateWorkflow turns it off
  ```

  This keeps go-live a step you control, for example behind a plan check or a
  review.
- **Mint the scopes.** Put `workflow:publish` and `workflow:activate` into the
  session, and the editor shows the switch itself. See
  [go-live](/guides/white-label/#go-live-putting-an-embedded-users-workflow-into-production).

Activation answers with the same refusals as the dashboard. A workflow that uses
a node the tenant may not use is refused with `node.not_available`. A trigger
address is minted once and survives deactivation, so a sender configured
against it keeps working.

## 7. Provision credentials

A credential holds a secret the workflow needs, such as a CRM token. It is sealed
at rest, stored per tenant, and never returned: reads show `••••••••`. Create it
from your backend, so the secret never reaches the browser:

```sh
curl -X POST "$BASE/api/v1/credentials" -H "Authorization: Bearer $TENANT_KEY" \
  -H 'content-type: application/json' \
  -d '{"name":"CRM token","type":"httpBearerAuth","fields":{"token":"…"},"allowedDomains":["api.crm.example.com"]}'
# 201 {"id":"cred_…","name":"CRM token","type":"httpBearerAuth","fields":{"token":"••••••••"},"allowedDomains":["api.crm.example.com"],…}
```

```ts
const credential = await client.createCredential({
  name: 'CRM token',
  type: 'httpBearerAuth',
  fields: { token },
  allowedDomains: ['api.crm.example.com']
});
```

`allowedDomains` limits which hosts the credential may be sent to. Set it for
every credential a node sends. Put the credential's id into the workflow
document where the node carries it, then seed confinement as in step 5. The
types you can create are in [Credential types](/reference/credential-types/),
and `listCredentialTypes` returns them with their fields. Keys that seal
credentials rotate separately ([Key rotation](/operate/key-rotation/)).

## 8. Dispatch host events into workflows

When something happens in your product, publish one event, and every active
workflow of that customer that listens for it starts:

```ts
const outcome = await client.publishEvent(
  { eventId: message.id, type: 'message.received', payload: message },
  { idempotencyKey: `message:${message.id}` }
);
// outcome.matched: [{ workflowId, executionId }]
// outcome.filtered: [{ workflowId, reason }]
```

The workflows listen with the Host Event trigger. Which path to use when, the
trigger's item shape and the webhook alternative are in
[Trigger workflows from your app](/guides/trigger-workflows/).

## 9. Make every write idempotent

A retried request is a second side effect unless you stop it. Decide this once
per call:

| Call | How to make a retry safe |
| --- | --- |
| Create a tenant | A taken id answers `409`. Treat that as "already exists". |
| Mint a key | Mint only if you have not stored one. Keys are not idempotent, so an extra key is a key to revoke. |
| Publish a host event | `Idempotency-Key` and your own `eventId`, both stable per event. |
| Run a workflow | `Idempotency-Key`, persisted before the first attempt. |
| Insert or upsert a datastore row | `Idempotency-Key`. |
| Deliver to a webhook | the trigger's `deliveryIdHeader`. |
| Delete a tenant | Send it again. It is idempotent and says so. |

Generate one key per logical operation and store it before the first attempt.
A key generated per attempt protects nothing. The rules, the `409` codes and the
limits are in [Idempotent requests](/guides/idempotency/).

## 10. Delete a tenant

When a customer leaves, delete the tenant with the operator key:

```sh
curl -X DELETE "$BASE/api/v1/tenants/acme" -H "$OP"
# 200 {"tenantId":"acme","tenantRemoved":true,"removed":{"api_keys":3,"credentials":1,…},"datastoreTables":0,"binaries":{…}}
```

It removes everything KilasFlow holds for that tenant: workflows, runs,
credentials, keys, triggers, data tables (including the physical tables) and
stored payloads. It cannot be undone, so take a backup first if you may need the
data. The tenant's keys stop working at the start (they answer `401` afterwards).

The deletion is idempotent and runs in steps. To confirm it finished, send it
again. `tenantRemoved: false` with every count at zero means nothing is left. A
non-zero count means rows were still arriving, so repeat. An unknown id answers
`200` with zero counts. Then delete the key from your secret manager. The steps
and what is not removed are on [Tenant deletion](/operate/tenant-deletion/).

## 11. Watch it in production

Four views, from your customer's run up to the whole deployment:

- **One run.** `GET /api/v1/executions/{id}` has the node-by-node trace, and
  `…/events` streams it live. Your backend sends the API key. A browser needs a
  stream ticket ([Get notified of results](/guides/get-notified/)).
- **A customer's failures.** Poll `GET /api/v1/executions?status=failed` with
  that tenant's key, or put an error workflow in the tenant that calls your
  backend. There are no outbound execution webhooks yet, and
  [Get notified of results](/guides/get-notified/) says exactly what to do
  instead.
- **The deployment's logs.** Every run that ends writes one `execution finished`
  line, at `WARN` for a failure, with the tenant, workflow, failing node and
  error. Alert on it. `log.execution_level` tunes it.
- **The deployment's metrics.** Enable the Prometheus endpoint (`metrics.enabled`)
  for queue depth, running executions against worker capacity, and run and webhook
  outcomes. Set `metrics.tenant_label` to `top` to label the busiest tenants
  without unbounded cardinality ([Metrics](/operate/metrics/)).

Health and readiness are `GET /api/v1/health` and `GET /api/v1/ready`. Readiness
answers `503` while a datastore migration is outstanding.

Each tenant's ingress is rate limited. An over-limit caller gets `429` with
`Retry-After`. The defaults are `ratelimit.run_per_minute` 120,
`ratelimit.webhook_per_minute` 600 and `ratelimit.events_per_minute` 600, and
`PATCH /api/v1/tenants/{id}` changes one tenant's
([Tenant settings](/operate/tenants/#tenant-settings)).

## 12. Go-live checklist

- [ ] Authentication is on, with the signing key, the operator key and a bootstrap
  account set on the first start.
- [ ] `KILASFLOW_EMBED_SIGNING_KEY`, `KILASFLOW_EMBED_ALLOWED_ORIGINS` and
  `KILASFLOW_SERVER_PUBLIC_URL` are set. Staging and production origins are both
  listed.
- [ ] KilasFlow has an origin of its own, such as `flows.example.com`. It does not
  serve under a path.
- [ ] The database is PostgreSQL, in a schema and role of its own (a deployment
  that splits API and workers requires it), and you have counted
  `processes × pool + your app` against `max_connections`
  ([Deploy for SaaS](/operate/deploy-for-saas/)).
- [ ] `KILASFLOW_ENCRYPTION_KEY` is set and backed up. Losing it makes stored
  credentials unreadable ([Key rotation](/operate/key-rotation/)).
- [ ] `webhook.require_auth` is on if every trigger should authenticate, and
  every trigger has a credential.
- [ ] Workflows that call your own services can reach them. Private addresses are
  refused unless the operator lists them in `outbound.allowed_private_endpoints`
  ([Safety boundaries](/concepts/safety-boundaries/)).
- [ ] The SDK version and the server build are pinned together. No container
  image is published yet, so record the commit you build from
  ([Deploy for SaaS](/operate/deploy-for-saas/#backups-pinning-and-upgrades-on-one-page)).
- [ ] There is a backup, and you have rehearsed a restore
  ([Backups and upgrades](/operate/upgrades/)).
- [ ] You have chosen how you will learn about failures, since the server will not
  tell you ([Get notified of results](/guides/get-notified/)).
- [ ] You have read [Security posture](/operate/security/), including what a
  tenant boundary is and is not.

## Recipe: a workflow as a synchronous tool for your AI agent

Your product has its own AI agent. You want it to call a KilasFlow workflow as
a tool, wait for the answer and carry on. KilasFlow is the tool's body. The agent
stays in your product.

The shape is a Webhook trigger that answers from the graph:

```text
Webhook (responseMode: responseNode)  →  the work  →  Respond to Webhook
```

### Build the workflow

These two nodes were run against a real server for this page. A call with
`{"orderId":"A-1001"}` returned `200 {"orderId":"A-1001","status":"shipped"}`:

```json
{
  "id": "hook",
  "name": "Tool call",
  "type": "kilasflow.webhook",
  "typeVersion": 1,
  "position": { "x": 0, "y": 0 },
  "parameters": {
    "path": "order-lookup",
    "httpMethod": "POST",
    "authentication": "headerAuth",
    "responseMode": "responseNode",
    "deliveryIdHeader": "X-Tool-Call-Id"
  },
  "credentials": { "httpHeaderAuth": "cred_…" }
}
```

```json
{
  "id": "answer",
  "name": "Answer",
  "type": "kilasflow.respondToWebhook",
  "typeVersion": 1,
  "position": { "x": 240, "y": 0 },
  "parameters": {
    "respondWith": "json",
    "responseCode": 200,
    "responseBodyJSON": {
      "mode": "expression",
      "value": "{{ { orderId: $json.body.orderId, status: 'shipped' } }}"
    }
  }
}
```

(The tested run used `"authentication": "none"`. Use `headerAuth` with a stored
`httpHeaderAuth` credential, as in
[store a credential, attach it to the trigger](/guides/embedding/#2-store-a-credential-attach-it-to-the-trigger),
for anything that is not a local test.) Replace the middle with the real work:
HTTP Request nodes, a Data table lookup, a sub-workflow. The input is
`$json.body`. Activate the workflow, then read its address with
`GET /api/v1/workflows/{id}/webhooks`.

### Call it from your agent

The call is server to server. Your agent runtime calls it, never a browser:

```js
const response = await fetch(`${KILASFLOW_URL}${toolRoute}`, {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'X-Reference-Key': toolSecret,   // the header credential
    'X-Tool-Call-Id': toolCallId     // the agent framework's own call id
  },
  body: JSON.stringify(toolArguments)
});
```

What comes back, and what your agent should do:

| Answer | Meaning | Do |
| --- | --- | --- |
| `200` and your JSON | the Respond node ran | give it to the model |
| `500 {"message":"Error in workflow"}` | the run failed, and no Respond node answered | give the model a failure result. The text is deliberately generic. |
| `504` | the run did not answer within `webhook.response_timeout` | give the model a "timed out" result. **The run keeps going.** |
| `401` | the header credential did not match | fix the secret. Do not retry. |
| `404` | the workflow is inactive, deleted or the route is wrong | all three answer the same on purpose |
| `429` | the tenant's webhook allowance is spent | wait `Retry-After`, then retry |
| `200 {"duplicate":true,…}` | this call id was seen in the last five minutes | see idempotency below |

### The timeout

`webhook.response_timeout` is the longest a caller waits, 30 seconds by default
(`KILASFLOW_WEBHOOK_RESPONSE_TIMEOUT`). It is a deployment-wide setting. It does
not stop the run. It only abandons the synchronous answer, so a tool that is too
slow still finishes its work after the agent gave up. Agent frameworks have
their own, usually shorter, tool timeouts, so set the lower of the two. Keep
tools short. For work that takes longer, answer at once with the run id and let
the agent poll ([Trigger workflows from your app](/guides/trigger-workflows/#what-the-caller-gets-back)).

### Idempotency

Decide per tool:

- **A read-only tool.** Leave `deliveryIdHeader` empty. A retry then just runs
  the workflow again, which costs nothing.
- **A tool with side effects.** Set `deliveryIdHeader` and send your framework's
  call id. A repeat inside five minutes starts nothing. The repeat does **not**
  get the first answer. It gets `{"duplicate":true,"executionId":"…","status":"…"}`.
  The first run's response body is not stored, so keep the result in your own
  cache, keyed by the call id, and return that. The five-minute window is fixed.

### Schema ownership

KilasFlow does not publish or enforce an input or output schema for a Webhook
trigger. The tool definition your agent shows the model, with its name, its
description and its JSON schema, belongs to **your** product. The workflow's
Respond node is the other half of the contract. Keep the two together:

- Store the tool's schema next to the workflow id in your own code or database,
  and change both in one release.
- Validate inputs inside the workflow with an IF node and answer a `422` from a
  second Respond node, so a bad call fails loudly and not in the middle of the
  work.
- Treat the workflow's published revision as the unit of change. Publish a new
  revision for a new schema. The webhook address does not change.
- Do not reuse a tool route for two schemas. Add another Webhook node, which gets
  its own address.

This is a different mechanism from the **Workflow Tool** node
(`kilasflow.workflowTool`), which lets an AI agent running *inside* KilasFlow call
another workflow. Here the agent is yours and the workflow is the tool.
