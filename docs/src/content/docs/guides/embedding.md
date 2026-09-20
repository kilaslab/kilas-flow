---
title: Embedding and multi-tenancy
description: Run KilasFlow inside your SaaS for many customers — tenant mapping, backend minting, the embedded editor, webhooks, and datastores, walked end to end.
---

KilasFlow is built to run inside somebody else's product: your backend holds
one API key per customer, your pages embed the workflow editor in an iframe,
and KilasFlow stores each customer's workflows, credentials, executions, and
datastores in that customer's tenant. Every mechanism for that story already
exists; this page assembles them into one feature. The code it quotes runs:
`sdk/examples/reference-host` is a minimal host that serves two tenants
from one process, and every snippet below is copied from it.

Background on any single mechanism lives in [Tenancy and the embed
boundary](/concepts/tenancy-and-embedding/), [Triggers and
webhooks](/concepts/webhooks/), and [Credentials](/concepts/credentials/).
The operation surface is the [HTTP API reference](/reference/api/) under
its [contract](/reference/api-contract/).

## The mapping decision

Before any code: decide how your customers map onto KilasFlow tenants. It
is the one decision that is expensive to change later, because every stored
row carries the tenant it was written under.

**One KilasFlow tenant per customer.** Your backend holds one `kfa1_…` key
per customer and builds one client per key with `tenantClientFactory`.
Isolation is a `WHERE` clause on every repository call, so one customer's
workflows, credentials, executions, and datastores do not exist for
another: naming them answers 404, not 403. This is the model the
isolation tests prove, and the one this guide recommends.

**One shared tenant with host-side partitioning.** Simpler on day one —
one key, names or tags separating customers — and it gives up every
isolation guarantee the product offers. Any key in the tenant lists every
workflow, resolves every credential, pages every execution, and reads
every datastore row. Partitioning becomes a convention each of your
handlers must remember rather than a clause the storage layer enforces.
Do this only for a single-customer deployment, where the tenant is yours
alone.

## One complete feature

A customer signs up in your product. Your operator has already provisioned
their KilasFlow tenant (tenant rows are operator-managed — there is no
self-service signup endpoint — and the first key per tenant is minted at
the store layer, because key creation is scoped to the caller's own
tenant). Your backend holds that key; the end user never sees it.

### 1. Provision the workflow server-side

Import, rather than create, when the workflow starts from a trigger. The
import response is the only call that reports the minted webhook address
back — activation reports nothing — and activating never changes the
reported address. Without it the host would own a workflow and have no
URL to send anything to:

```js
const imported = await tenant.client.importWorkflow({
  format: 'n8n',
  name: `Reference ${tenant.label}`,
  workflow: {
    name: `Reference ${tenant.label}`,
    nodes: [
      {
        id: 'hook', name: 'Signup webhook', type: 'n8n-nodes-base.webhook', typeVersion: 2,
        position: [80, 80],
        parameters: { path: `reference-${slug}`, httpMethod: 'POST', responseMode: 'onReceived' }
      }
    ],
    connections: {}
  }
const workflowId = imported.workflow.id;
const webhookUrl = imported.webhooks?.[0]?.url; // e.g. "/webhook/<opaque route>"
```

The `path` is a label for the author to recognise, not the address: the
public URL is an opaque route minted per tenant, workflow, and trigger
node, stable across deactivation and reactivation, so two tenants
importing the same template never collide.

One tenant cannot claim the same endpoint label twice: activating a second
workflow with the same `path` is refused, because the label routes within
the tenant. Re-provisioning under a fixed label therefore retires the
previous workflow (deactivate, then delete) before importing — the
reference host does exactly that on boot.

### 2. Store a credential, attach it to the trigger

Deliveries must authenticate, and the secret belongs in the credential
store — sealed at rest — not in the workflow document. The trigger names
the credential by type, and the webhook boundary verifies the header
server-side. A delivery without it answers 401 and never becomes an
execution:

```js
const webhookSecret = randomBytes(24).toString('hex');
const credential = await tenant.client.createCredential({
  name: `Reference ${tenant.label} webhook`,
  type: 'httpHeaderAuth',
  fields: { name: 'X-Reference-Key', value: webhookSecret }
});

const document = (await tenant.client.getWorkflow(workflowId)).latestVersion.document;
const webhook = (document.nodes ?? []).find((node) => node.type === 'kilasflow.webhook');
webhook.parameters = { ...webhook.parameters, authentication: 'headerAuth' };
webhook.credentials = { httpHeaderAuth: credential.id };
await tenant.client.updateWorkflow(workflowId, {
  schemaVersion: document.schemaVersion,
  name: document.name,
  nodes: document.nodes,
  connections: document.connections,
  settings: document.settings
});
await tenant.client.activateWorkflow(workflowId);
```

Activation is an owner action on purpose: it publishes a deployment-wide
endpoint, so no embed scope grants it. Only the backend key may call it.

### 3. Mint an embed session for the end user

The page holds no key. It asks its own backend, and the backend decides —
`EmbedSessions.Create` verifies the workflow exists in the tenant, it
cannot know whether the user in front of this browser is entitled to it.
That check is yours, and in the reference host it is a registry lookup:
an unknown tenant slug answers 404 before any token exists.

```js
// The slug comes from your session, not from the request path alone —
// whoever maps "this user" to "this tenant" is the authorization check.
const session = await tenant.client.createEmbedSession({
  workflowId: tenant.workflowId,
  scopes: ['workflow:read', 'workflow:write', 'workflow:run'],
  origin, // a server-side constant, never taken from request headers
  branding: tenant.branding
});
```

Minting is cheap, so mint per page load and re-check entitlement each
time. A default-length (fifteen-minute) token is also the point at which a
long editing session re-verifies the user is still allowed.

### 4. Mount the editor, watch the run

The SDK is not on npm yet, so the import below resolves only after building it
from the checkout: `cd sdk && pnpm install && pnpm build`, then
`pnpm add file:/path/to/kilas-flow/sdk` in the host. See the
[SDK README](https://github.com/kilaslab/kilas-flow/blob/main/sdk/README.md).

```js
import { mountWorkflowEditor, subscribeExecutionEvents } from '@kilasflow/sdk/browser';

const editor = mountWorkflowEditor({
  container: document.getElementById('editor'),
  baseUrl: session.baseUrl,
  session, // fetched from your own backend
  onEvent(event) {
    if (event.type === 'execution-started') {
      subscribeExecutionEvents({
        baseUrl: session.baseUrl,
        executionId: event.executionId,
        onEvent: (execution) => console.log(execution.type, execution.nodeId),
        // A minter, not a string: tickets are single-use and live for
        // seconds, so the SDK calls it before every connect and resumes
        // after the last event received.
        ticket: async () => (await (await fetch(`/api/${slug}/stream-ticket?executionId=${event.executionId}`)).json()).ticket
      });
    }
  }
});
```

The handshake is origin-checked both ways: the editor announces itself,
and only then is the token posted — to the editor's exact origin, never
`'*'`. A handshake that does not complete in fifteen seconds calls
`onError`; tearing the iframe down is the host's decision.

### 5. Fire the webhook, observe the execution

The browser never touches the webhook secret. The page calls the
backend, the backend delivers server-to-server, waits for the run to
finish, and answers with the outcome:

```js
const delivery = await fetch(kilasflowUrl + tenant.webhookUrl, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', 'X-Reference-Key': tenant.webhookSecret },
  body: JSON.stringify({ email, plan: 'trial' })
});
const receipt = await delivery.json();
// Immediate mode (the default) acknowledges with n8n's own body and no run id:
// {"message":"Workflow was started"}. The one answer that does carry one is a
// repeat of the same delivery: { executionId, status, duplicate: true }.
```

The host then finds the run through the tenant's own client — list the
workflow's executions and take the newest, or have the trigger answer from its
graph with a Respond to Webhook node — and polls `getExecution` until the status
is terminal. Ticket minting and execution polling both re-check
that the execution belongs to the tenant's workflow first — a ticket for
another customer's run would be a cross-tenant read.

## What runs where

Backend calls carry the tenant key; browser calls carry the embed token.
The middleware enforces the split, and anything not listed for the token
is refused by a genuine default arm — a route added tomorrow is denied
until somebody deliberately permits it.

| Call | Credential | Notes |
| --- | --- | --- |
| Create / update workflow | API key | Provisioning is a backend action |
| Store / manage credentials | API key | Values are never returned to a picker beyond names |
| Activate / deactivate | API key | No embed scope grants this, ever |
| Import workflow | API key | Refused to embed sessions outright |
| Delete workflow | API key | Refused to embed sessions outright |
| Mint embed session | API key | The backend's authorization decision |
| Mint stream ticket | API key | After checking the execution is yours |
| Manage datastores | API key | Provisioning and schema stay with the backend key — no embed scope grants them, ever |
| Load / save the one workflow | Embed token | Needs `workflow:read` / `workflow:write` |
| Run the one workflow | Embed token | Needs `workflow:run` |
| List executions of the one workflow | Embed token | Needs `workflow:read`, with `workflowId` equal to the session's |
| Read one datastore's rows | Embed token | Needs `datastore:read` on a session minted for that datastore |
| Write one datastore's rows | Embed token | Needs `datastore:write`, which implies `datastore:read` |
| Watch an execution stream | Stream ticket | Single-use, seconds-lived, spent as `?ticket=` |
| Deliver to a webhook | Webhook credential | The trigger's header or basic check, no KilasFlow identity |

## What the embed token is not

- **One subject: a workflow or a datastore.** A workflow session reaches
  only its workflow — every request outside it is refused as scoped to a
  different workflow, including listing all workflows, which would
  otherwise enumerate the tenant. A datastore session reaches only its
  datastore's definition and rows, and reads any other datastore as
  unknown (404) rather than as forbidden, so it never learns siblings
  exist. Mint a session per subject; a token never names both.
- **One exact origin.** `scheme://host[:port]`, no wildcards, no suffix
  match: trusting "anything under this domain" is the loophole a
  subdomain takeover walks through. The origin is re-checked on every
  request, not only at mint, so a host serving from two domains lists
  both — and a request with no `Origin` header at all skips the
  per-request check, which is fine for browsers (they always send it)
  and worth knowing for anything else.
- **Minutes, not hours.** Fifteen by default (an operator changes the
  default with `embed.session_ttl`), thirty maximum; a longer request is
  clamped, not refused. The setting is a default, not a ceiling: a host
  that passes `ttlSeconds` still gets the lifetime it asked for, up to the
  thirty-minute cap, which no configuration raises. A configured default
  outside one second to thirty minutes is refused at startup rather than
  clamped. A leaked token stays useful only briefly, and minting another
  is the re-authorization point.
- **No activate, no delete, no import.** Activation publishes a public
  endpoint, deletion destroys the tenant's data, import creates
  workflows outside the session's single-workflow authority. Each
  refusal names the reason rather than answering 404, because the
  caller is the host's own page and deserves a diagnostic.
- **Never a session cookie.** A request with no token at all passes the
  embed middleware untouched. Embedding confines a browser to one
  workflow; anything reachable by your users sits behind your own
  authentication first.

Two traps fail silently and deserve stating twice. `embed.allowed_origins`
is exact matching with no wildcards, and **an empty list disables
embedding entirely** — an iframe that never loads is usually a config
key nobody told the integrator about. And sessions are bound at mint to
the tenant of the key that minted them: minting with the wrong
customer's client is a cross-tenant grant no browser check can undo.

## Branding: values, never markup

The editor renders inside your customer's page, so branding is a
validated value set the editor renders into elements it controls. There
is deliberately no way to pass CSS, HTML, or a script. What is
themeable, exactly:

| Field | Accepted | Rejected, and why |
| --- | --- | --- |
| `name` | Letters, digits, spaces, and `.,'&()-_` up to 60 characters | Anything else could escape its element |
| `logoUrl` | Absolute `https` URL | `data:` and `javascript:` URLs are the classic injection through a "safe" string field |
| `accent` | Hex, `oklch()`, `rgb()`, or a plain colour name | Anything that is not a colour literal |
| `hideRun`, `hideSave` | Booleans | They are presentation only: the server still enforces scopes, so hiding a control can never be the thing that stops an action |

### Deployment defaults

An operator may set `branding.name` and `branding.logo` to the values an
embedded editor carries when the session that opens it names none. Values the
host passes win field by field; an empty value cannot blank a deployment
default, so a deployment hosting more than one brand leaves both settings empty
and passes everything per session.

Both sets of values pass the same validation — the deployment's at boot, the
host's at mint — so a logo the editor could never render stops the server
instead of refusing every session a host asks for. The merged values come back
in the mint response's `branding` field, which means the host has to forward
that response to its page the way the [reference host](https://github.com/kilaslab/kilas-flow/blob/main/sdk/examples/reference-host/server.mjs)
does; a session handle built by hand carries no branding. The operator
dashboard does not read these settings: it keeps its own name, logo and icon,
and only the embedded editor a host's end users see is white-labelled.

## The datastore path

Each tenant provisions its own datastore with its own key. The catalogue
lookup clauses on tenant and id together, so naming another tenant's
datastore answers 404 — isolation the host relies on rather than
reimplements:

The SDK's datastore subset covers the embed path — `getDatastore`,
`listDatastoreRows` with its filter triples, `getDatastoreRow`, and
`insertDatastoreRow` — while provisioning and schema stay on the
documented REST endpoints with the tenant key. The backend key below
provisions and writes with full authority. A browser never holds it: to
let an embedded surface touch rows, the backend mints a datastore session
instead of a workflow one —

```js
const session = await tenant.client.createEmbedSession({
  datastoreId: id,
  scopes: ['datastore:read', 'datastore:write'],
  origin // a server-side constant, never taken from request headers
});
// -> 201 { token, datastoreId, scopes, origin } with an empty embedUrl:
// a datastore session names no workflow, so there is no editor to open and
// mountWorkflowEditor refuses it outright rather than loading a dead frame.
```

That token reads the one datastore's definition and rows, writes rows with
`datastore:write`, and reaches nothing else: other datastores read as
unknown, workflow routes stay forbidden, and creating, dropping, clearing,
or changing a table's columns stays with the backend key.

```js
const created = await api('POST', '/datastores', {
  name: `reference-${slug}`,
  columns: [
    { name: 'email', type: 'string' },
    { name: 'plan', type: 'string' }
  ]
}); // -> 201 { id, name, columns } + Location

await api('POST', `/datastores/${id}/rows`, { values: { email, plan: 'trial' } });

await api('GET', `/datastores/${id}/rows?limit=25&columnName=email&condition=eq&value=${email}`);
```

Rows carry `id`, `createdAt`, and `updatedAt` beside the user columns.
Filters come in two spellings of one model: the query string zips
repeated `columnName` / `condition` / `value` triples (`match=any|all`),
while bulk update, delete, and upsert take the JSON envelope
`{type, filters: [{columnName, condition, value}]}`. Conditions are `eq`,
`neq`, `like`, `ilike`, `gt`, `gte`, `lt`, `lte`, `isEmpty`,
`isNotEmpty`. Column types are `string`, `number`, `boolean`, `date`.

Writing from inside a workflow uses the datastore node rather than the
service API — an insert names the table by id and maps the columns:

```json
{ "resource": "row", "operation": "insert",
  "dataTableId": { "__rl": true, "mode": "id", "value": "<datastore-id>" },
  "columns": { "mappingMode": "autoMapInputData" } }
```

Manual mapping sets `mappingMode` to `defineBelow` with the column values
as the object. The reference host writes host-side today and swaps in this
node once it lands; the table id it passes is already the per-tenant
datastore the backend provisioned.

## The two failure modes that matter

1. **Minting for a workflow the user does not own.** The server checks
   the workflow exists in the tenant; only your backend knows the user
   is entitled to it. Do the lookup against your own session data on
   every mint — the reference host's registry check is the whole
   pattern, three lines, and there is no SDK flag that replaces it.
2. **Sending a token to an origin you did not verify.** The mint origin
   and the iframe target are both exact constants in backend code.
   Never reflect a request `Origin` header into either: whoever
   controls that header receives your customer's token.

## Operator checklist

- `KILASFLOW_AUTH_ENABLED=true` with a 32-byte auth signing key, or
  every request resolves to the shared `default` tenant and the two
  keys below are theatre.
- `KILASFLOW_EMBED_SIGNING_KEY` exactly 32 bytes (base64, hex, or raw),
  or embedding answers 503.
- `KILASFLOW_EMBED_ALLOWED_ORIGINS` listing every origin that frames
  the editor, exactly — empty fails closed, and same-app-different-domain
  needs every domain listed.
- One `kfa1_…` key per customer, stored as hashes server-side, shown in
  full exactly once at creation. Rotate by minting the new key, building
  a new client with it, and directing new work at the new client;
  in-flight requests on the old client finish or fail with a 401 the
  caller already handles.
- Pin the server image tag and the SDK version together; the reference
  host's README shows the exact four-file setup with no repository
  checkout.
