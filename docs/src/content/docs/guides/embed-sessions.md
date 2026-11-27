---
title: Embed sessions in depth
description: What an embed session may call, what its document may reference, how long it lives and how to refresh it, and which origins and CORS rules apply, with the exact refusal texts.
---

An embed session is the credential your page holds instead of an API key. Your
backend mints it with the tenant's key; the browser spends it. This page is the
reference for what a session can and cannot do, so you can predict it before an
end user hits a `403`. The walk-through that mints and mounts one is
[Embedding and multi-tenancy](/guides/embedding/). The reasoning behind the
boundary is [Tenancy and the embed boundary](/concepts/tenancy-and-embedding/).

A session names exactly one **tenant**, one **subject** (a workflow or a
datastore, never both), a list of **scopes**, one exact **origin** and an expiry.
A workflow session also carries a **confinement**. All of it is signed into the
token, so a browser cannot change any of it.

## Scopes and subjects

There are seven scopes, in two families. A workflow session takes workflow
scopes only, and a datastore session takes datastore scopes only. Mixing them is
refused at mint with a `422`, for example `scope "datastore:read" does not belong
on a workflow session: mint with a workflow scope`.

| Subject | Scope | What it opens |
| --- | --- | --- |
| Workflow | `workflow:read` | Load the workflow, its versions and its executions, the node catalogue and the credential picker. |
| Workflow | `workflow:write` | Save drafts and restore a revision. Carries `workflow:read`. |
| Workflow | `workflow:run` | Run the workflow and retry an execution. Carries `workflow:read`. |
| Workflow | `workflow:publish` | Pin a named revision as the version production traffic runs. Carries `workflow:read`. |
| Workflow | `workflow:activate` | Activate and deactivate. Carries `workflow:read`. |
| Datastore | `datastore:read` | Read the datastore's definition and rows, including the CSV export. |
| Datastore | `datastore:write` | Insert, upsert, increment, update and delete rows, and import CSV. Carries `datastore:read`. |

Scopes combine by union. Nothing implies write, run, publish or activate, and no
scope reaches across families. A session must carry at least one scope. What
publishing and activating do, and why each is its own scope, is in [publish and
activate authority](/guides/embedding/#publish-and-activate-authority).

The complete answer to "may this session call that operation" is the generated
[embed permit table](/reference/api/embed-permits/). It is exported from the Go
code that enforces it and checked in CI, so it cannot drift from the server.
Anything the table does not list as allowed is refused to every scope. A route
added later is refused the same way until someone permits it on purpose.

Two things the table cannot show, because the handler decides them after the
scope check passes:

- An execution that belongs to another workflow answers `404`. A sub-workflow run
  started from the session's own workflow is readable.
- Another datastore's id answers `404`, not `403`, so a datastore session cannot
  probe for ids.

### Datastore sessions

This is the one description of what a datastore session reaches. The other pages
link here.

A datastore session is minted with `datastoreId` instead of `workflowId`. It has
no editor, so its `embedUrl` is empty and `mountWorkflowEditor` refuses it. Your
page uses the token directly against the API.

- With `datastore:read` it can read the datastore's definition (`GET
  /datastores/{id}`), list and read rows, and export CSV.
- With `datastore:write` it can also insert, upsert, increment, update and
  delete rows, and import CSV. All of these are `POST`, `PUT` or `DELETE` under
  `/datastores/{id}/rows`.
- It cannot list or create datastores, rename or drop one, clear it, or add,
  rename or drop a column. Those stay with the backend key.
- It cannot reach any workflow, execution or credential route.
- `allow` does not apply to it. A mint that sends `allow` with `datastoreId`
  answers `422`.

## Confinement

Scopes bound which endpoints a session may call. Its **confinement** bounds what
the document it saves, publishes, activates, restores or runs may reference:
credentials, data tables and sub-workflows. Without it, a session holding
`workflow:write` could save a graph that attaches any credential of the tenant or
reads any table, and the graph would run with the tenant's authority.

The check runs on every save, publish, activation, restore, run and retry. The
credential picker in the editor is filtered to the same set, so a user is not
offered what the server would refuse.

### How it is derived

The confinement is computed once, at mint, and signed into the token. It is not
re-derived per request, because a document cannot be its own authority.

1. The source revision is the workflow's **active** (published) revision. If it
   was never activated, it is the **latest draft**.
2. The confinement is every credential, data table and sub-workflow that revision
   references, plus the workflow itself.
3. Whatever the host lists in the mint body's `allow` is added to it. See
   [host-declared `allow`](#the-alternative-allow).

Three consequences:

- A blank workflow references nothing, so a user who builds a new automation
  inside the embed cannot pick the credential or table you provisioned for their
  organisation unless the revision or `allow` already names it.
- Once a revision is active, a draft saved after it is not read. A credential you
  attach in a newer draft joins the next session's confinement only when that
  draft becomes the active revision, by a publish or an activation.
- A token minted before confinement existed carries none, and that reads as the
  strictest case: it may reference nothing it did not already have.

### Pre-attach, then mint

You know what your customer is entitled to, and the end user in the editor does
not choose it. So attach it on the server first, then mint:

```js
// 1. With the tenant's own key: create what the customer may use.
const credential = await client.createCredential({
  name: 'Acme CRM',
  type: 'httpHeaderAuth',
  fields: { name: 'X-Api-Key', value: crmKey },
  allowedDomains: ['crm.example.com'] // an HTTP node needs a scoped credential
});
const table = await client.createDatastore({
  name: 'Acme contacts',
  columns: [{ name: 'email', type: 'string' }]
});

// 2. Save a document that already references both, as a starting point.
const document = {
  ...template,
  nodes: [
    ...template.nodes,
    {
      id: 'crm', name: 'CRM lookup', type: 'kilasflow.httpRequest', typeVersion: 1,
      position: [320, 120],
      parameters: { method: 'GET', url: 'https://crm.example.com/v1/contacts' },
      credentials: { httpHeaderAuth: credential.id }
    },
    {
      id: 'store', name: 'Save contact', type: 'kilasflow.datastore', typeVersion: 1,
      position: [560, 120],
      parameters: {
        resource: 'row', operation: 'insert',
        dataTableId: { __rl: true, mode: 'id', value: table.id },
        columns: { mappingMode: 'autoMapInputData' }
      }
    }
  ]
};
const workflow = await client.createWorkflow(document);

// 3. Mint. The session's confinement now holds the credential and the table.
const session = await client.createEmbedSession({
  workflowId: workflow.id,
  scopes: ['workflow:read', 'workflow:write', 'workflow:run'],
  origin // a server-side constant
});
```

The end user can now open the CRM node, see `Acme CRM` in the picker, and save.
Nothing else of the tenant's credentials or tables is listed or accepted.

If the workflow already has an active revision, publish the revision that uses
the new credential before you mint, or use `allow` for the new ids.

### The alternative: `allow`

When you would rather not edit the workflow first, declare the ids in the mint
body. The token carries the union of the derived set and what you declare:

```js
await client.createEmbedSession({
  workflowId: workflow.id,
  scopes: ['workflow:read', 'workflow:write', 'workflow:run'],
  origin,
  allow: { credentials: [credential.id], datastores: [table.id] }
});
```

Each id is checked against the minting tenant before a token exists, and an
unknown or foreign id answers `404` (`allow.credentials: credential cred_x not
found`). `"tenant"` opens a whole kind and needs a tenant-wide key. The limits,
the 50-id cap and what `allow` does not change are in
[What the editor may reference](/guides/embedding/#what-the-editor-may-reference-confinement-and-allow).

A declared grant outlives its session if the user publishes a revision that uses
it, because later sessions derive their confinement from that revision. If one
workflow is shared by users with different entitlements, give each user their own
workflow.

### The refusal texts

A document that reaches outside the confinement is refused with a `403` problem.
The `detail` starts with the caller and names every offending node, separated by
`; `:

```text
this embed session cannot use that workflow document: node "Collect" attaches credential cred_8f2c, which this embed session was not granted
```

The messages a node can add, as the server writes them (`%s` is the node's name,
`%q` is quoted):

| Cause | Text |
| --- | --- |
| A credential outside the grant | `node %s attaches credential %s, which this embed session was not granted` |
| A table by id | `node %s addresses data table %s, which this embed session was not granted` |
| A table by name | `node %s addresses the data table named %q, which this embed session was not granted` |
| A table chosen by an expression | `node %s chooses its data table with an expression, which this embed session cannot be confined to` |
| A table operation that manages tables | `node %s performs the data table operation %q, and an embed session cannot manage data tables` |
| A sub-workflow outside the grant | `node %s calls workflow %s, which this embed session was not granted` |
| A sub-workflow chosen by an expression | `node %s chooses its sub-workflow with an expression, which this embed session cannot be confined to` |

An unscoped credential on a node that sends requests is refused too, with its own
sentence; see [credentials in a confined caller's
document](/concepts/tenancy-and-embedding/#credentials-in-a-confined-callers-document).

The refusals you will meet from the scope check, as the server writes them:

| Status | Text | When |
| --- | --- | --- |
| `403` | `This embed session cannot read.` | The session lacks the read scope for what it asked. |
| `403` | `This embed session is read-only.` | A write without `workflow:write` (or `datastore:write`). |
| `403` | `This embed session cannot run workflows.` | A run or retry without `workflow:run`. |
| `403` | `This embed session cannot publish a revision without the workflow:publish scope.` | A publish without its own scope. |
| `403` | `This embed session cannot change activation.` | Activate or deactivate without `workflow:activate`. |
| `403` | `This embed session is scoped to a different workflow.` | A path naming another workflow. |
| `403` | `This embed session must list executions of its own workflow.` | `GET /executions` without the session's `workflowId`. |
| `403` | `This embed session cannot use this endpoint.` | Anything the table does not allow. |

The generated table lists the exact sentence for every refused operation.

## Lifetime and refresh

- A session lives 15 minutes unless you ask otherwise. An operator changes that
  default with `embed.session_ttl`, which must be between one second and 30
  minutes; a value outside it stops the server at startup.
- Pass `ttlSeconds` when minting to ask for a different lifetime. The cap is
  **30 minutes** and no configuration raises it. A longer request is clamped,
  not refused, so read `expiresAt` from the response rather than assuming what
  you asked for.
- The token is checked on every request. After `expiresAt` it answers `401` with
  `The embed session is not valid.`. A forged, malformed and expired token get
  the same answer.

**There is no refresh endpoint, and a session is never extended.** Refreshing is
minting a new session, which re-checks your user's entitlement and derives the
confinement again from the current revision. For an editor, the SDK hands the new
token over without a reload, so the canvas and unsaved edits survive:

```js
const editor = mountWorkflowEditor({
  container, baseUrl, session,
  async onEvent(event) {
    // Sixty seconds before expiry, or already past it.
    if (event.type === 'session-expiring' || event.type === 'unauthorized') {
      const next = await fetch('/my-backend/embed-session', { method: 'POST' }).then((r) => r.json());
      editor.setSession(next); // the editor is not reloaded
    }
  }
});
```

`unauthorized` means the server refused the token outright, so only a new session
recovers it. The details of the message exchange are in [the postMessage
protocol](/guides/embedding/#the-postmessage-protocol-v2).

A datastore session has no editor and no events. Your page owns the loop: read
`expiresAt` from the mint response, ask your backend for a new session shortly
before it, and send the new token on the next request.

## Origins and CORS

Origins are checked in four separate places. Three use the deployment's
allowlist and one uses the session's own origin.

**The allowlist.** `embed.allowed_origins` (environment variable
`KILASFLOW_EMBED_ALLOWED_ORIGINS`) is the deployment's list. Each entry is
`scheme://host[:port]`, lowercased, with no wildcard and no suffix match. An
empty list disables embedding. It is used in three places:

1. **At mint.** The session's `origin` must be on the list, or the mint answers
   `422` with `origin "…" is not in this deployment's embed allowlist`.
2. **Framing.** The editor route `/embed/*` is served with
   `Content-Security-Policy: frame-ancestors 'self'` followed by the listed
   origins, so only those pages can frame the editor. The dashboard allows
   `'self'` only.
3. **CORS.** The same list is the CORS allowlist for the API.

**The session's own origin.** A session carries one origin. On every request that
carries an `Origin` header, it must equal the session's origin, or the request
answers `403` with `This embed session is not allowed from that origin.`. The
editor iframe is the one exception and is covered by the
`X-KilasFlow-Embed-Parent` rule in [What the embed token is
not](/guides/embedding/#what-the-embed-token-is-not). A request with no `Origin`
and no `X-KilasFlow-Embed-Parent` header skips this check, because browsers leave
`Origin` off same-origin reads.

### CORS behaviour

CORS applies to `/api/v1/*` only, and only to a request whose `Origin` is on the
allowlist. The host page needs it when it calls the API itself, for example a
datastore session, or an `EventSource` for an execution's stream.

| Part | Behaviour |
| --- | --- |
| Allowed origin | The origin is reflected exactly as the browser sent it, with `Vary: Origin`. Never `*`. |
| Origins off the list | No CORS headers at all. The request itself is still served or refused by the usual rules. |
| Credentials | `Access-Control-Allow-Credentials` is never sent. Cookies do not cross origins here; the design is bearer token and ticket. |
| Methods | `GET, POST, PUT, PATCH, DELETE, OPTIONS` |
| Request headers | The preflight's `Access-Control-Request-Headers` are echoed. With none named, the answer is `Authorization, Content-Type, X-KilasFlow-Embed, Last-Event-ID`. |
| Readable response headers | `X-Request-ID, X-Next-Cursor, Idempotent-Replayed, Retry-After` |
| Preflight cache | `Access-Control-Max-Age: 600` (ten minutes), so a changed allowlist takes up to that long to reach a browser that cached it. |
| Preflight | An `OPTIONS` request with `Access-Control-Request-Method` is answered `204` before authentication, because a preflight carries no credential. It never spends a stream ticket. |

Send the token as `X-KilasFlow-Embed: <token>` or as `Authorization: Bearer
kfe1.…`. A browser `EventSource` cannot set headers, so your backend mints a
single-use stream ticket with its key and the page passes that; an embed session
cannot mint one itself (`POST /stream-tickets` is refused to every scope).

If a call from your page fails in the browser with a CORS error and nothing
reaches the server log, check that the page's origin is in
`embed.allowed_origins` exactly as the browser writes it, including the port.
