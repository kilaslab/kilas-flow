---
title: Using the host SDK
description: Drive KilasFlow from your own product with @kilasflow/sdk — the server client, the browser mount, the embed handshake protocol, and install instructions that match the release state.
---

`@kilasflow/sdk` is the TypeScript SDK for a host product embedding
KilasFlow: one thin, typed client method per API operation on the backend
side, and the iframe mount, session handshake and event subscription on the
browser side. It has no runtime dependencies, ships as ESM only, and every
shape it exposes is generated from the server's own OpenAPI document — the
SDK cannot describe an endpoint the server does not serve.

The full API surface, symbol by symbol, is the generated
[Host SDK reference](/reference/sdk/). This page is the guide: what the two
entry points are for, how the embed handshake works, and how to install the
package in its current, not-yet-published state.

## Install (from a checkout, until the first release)

**The package is not on npm yet.** `npm install @kilasflow/sdk` fails with
`E404` today; no release tag exists, and publishing happens by pushing an
`sdk-vX.Y.Z` tag. Until then, install a tarball packed from a checkout:

```sh
git clone https://github.com/kilaslab/kilas-flow.git
cd kilas-flow/sdk
pnpm install
pnpm pack --pack-destination /tmp
npm install /tmp/kilasflow-sdk-0.1.0.tgz
```

The tarball carries the built output (`dist/`), the README and the licence —
`pnpm pack` runs the build first, so what you install is what `prepack`
produced. Pin the exact tarball in production, and re-pack after pulling:
the SDK tracks `main`, and so do its types.

After the first release this section flips: `npm install @kilasflow/sdk`
with the version pinned, releases cut from `sdk-vX.Y.Z` tags with npm
provenance attested. Until a tag exists, treat any `npm install` instruction
you find elsewhere as describing that future, not the present.

## Two entry points, on purpose

The SDK's one design rule: **credentials live on the server, and the browser
half cannot even import them.** Hence two subpaths, and no import path from
the browser module back to the server one:

| Import | For | Carries |
| --- | --- | --- |
| `@kilasflow/sdk/server` | your backend | the host's API key, every operation |
| `@kilasflow/sdk/browser` | your page | no credential at all — only short-lived session tokens |
| `@kilasflow/sdk` | both, when the bundle is not browser-restricted | re-exports the two plus the generated model types |

## The server client

```ts
import { KilasFlowClient, KilasFlowError } from "@kilasflow/sdk/server";

const client = new KilasFlowClient({
  baseUrl: "https://flows.example", // no trailing path: the SDK adds /api/v1
  apiKey: process.env.KILASFLOW_API_KEY // sent as Authorization: Bearer …
});

const { id: executionId } = await client.runWorkflow(workflowId, input);
```

Constructor options are the full surface of configuration: `baseUrl`
(absolute), `apiKey` (a convenience over `headers.Authorization`, which wins
if both are set), `headers` for anything else, `fetch` for tests or tracing,
and `timeoutMs` (30 seconds by default). There is no retry loop and no
state to refresh — a client is one credential for its whole life, which is
what makes `tenantClientFactory` safe: build one fixed-credential client per
tenant key, never a client that swaps credentials.

Every non-success response is a `KilasFlowError` carrying `status`, the RFC
9457 `problem` document, and `retryAfterSeconds` when the server asked for a
wait (a `409` idempotency-in-flight). Branch on 401 versus 403 the way
[the tenants guide](/operate/tenants/#401-and-403) describes: 401 means get a
new credential, 403 means this credential may not, and no retry changes
either. The writes that accept an idempotency key take it as an option —
`client.runWorkflow(id, input, { idempotencyKey })` — and
[idempotency](/guides/idempotency/) explains when you want one.

## The browser half: mount, handshake, refresh

The browser entry point mounts the embedded editor and nothing else:

```ts
import { mountWorkflowEditor } from "@kilasflow/sdk/browser";

const editor = mountWorkflowEditor({
  container: document.getElementById("editor")!,
  baseUrl: "https://flows.example",
  session,                       // what createEmbedSession returned
  onEvent: (event) => { /* ready, saved, published, finished, … */ },
  onError: (error) => { /* handshake timeouts land here */ },
});

await editor.save();             // a command; resolves with the outcome
editor.unmount();                // removes the iframe and every listener
```

The handshake is the part worth understanding, because it is where the
security model lives. `mountWorkflowEditor` creates an iframe at
`${baseUrl}/embed/${workflowId}` with `sandbox="allow-scripts
allow-same-origin allow-forms"`, and then:

1. The frame posts `kilasflow:embed-ready` to the *exact origin it was
   framed from* (derived from its referrer, never `'*'`).
2. Only then does the host post `kilasflow:embed-session` — the token — to
   the *editor's exact origin*. The token never travels in a URL.
3. The frame attaches the token as an `X-KilasFlow-Embed` header on every
   API request, plus `X-KilasFlow-Embed-Parent` naming the host origin it
   verified, which the server re-checks per request.
4. Every later message in both directions is checked against both `origin`
   and `source` before its payload is read.

A session token is short-lived — 15 minutes by default, 30 at the cap — and
protocol v2 refreshes it without reloading the editor: sixty seconds before
expiry the frame emits `session-expiring`, your page mints a fresh session
server-side and hands it over with `editor.setSession(session)`, and the
canvas, selection and viewport survive. A token that dies anyway latches a
single `unauthorized` event, and the only recoveries are `setSession` or
`unmount`.

The events a host receives (`ready`, `workflow-saved`,
`workflow-published`, `execution-started`, `execution-finished`,
`dirty-changed`, `session-expiring`, `unauthorized`, `error`) and the
commands it may send (`save`, `run`, `focusNode`) are tabulated with their
payloads in the [generated reference](/reference/sdk/) and in the
[embedding guide](/guides/embedding/)'s protocol section.

## Watching a run: stream tickets

The event stream is an SSE endpoint, and `EventSource` cannot send an
`Authorization` header. On an authenticated server the ticket is the answer:

```ts
import { subscribeExecutionEvents } from "@kilasflow/sdk/browser";

const stop = subscribeExecutionEvents({
  baseUrl: "https://flows.example",
  executionId,
  onEvent: (event) => { /* node.started, node.completed, execution.* */ },
  onClose: () => { /* a terminal event closed the stream */ },
  ticket: async () => (await backendClient.createStreamTicket(executionId)).ticket,
});
```

The ticket mint is called before every (re)connect — tickets are single-use
and live for seconds — and the SDK resumes via `?from=<lastEventId>` after
its two-second reconnect delay. Terminal events (`execution.completed`,
`.failed`, `.cancelled`) close the stream and fire `onClose`; a client must
stop then.

## Datastores: the embed half

A datastore-scoped embed session (`createEmbedSession({ datastoreId,
scopes: ["datastore:read"] })`) may read its own datastore's definition,
rows and single rows, and — with `datastore:write` — insert, upsert,
increment, update and delete rows. That is exactly what the server's scope
gate allows and no more: the listing of every datastore is denied to a
session (it names sibling tables), schema work (columns, clear, rename,
drop) is denied, and another datastore's id reads as `404` so ids cannot be
probed. The [generated permit table](/reference/api/datastores/) states the
same verdicts per operation — the SDK's embed half and that table cannot
disagree, because both are generated from the surfaces that implement them.

## What the SDK does not do

- **No pagination of the header-cursor listings** — `listWorkflows()` and
  friends return one page; the body-cursor listings have
  `iterateExecutions` and `iterateDatastoreRows`. See
  [Pagination](/reference/api/pagination/).
- **No credential storage or refresh callbacks** — rotation is mint, build a
  new client, move work to it.
- **No OAuth popup handling** — Google Connect is a browser flow the SDK
  deliberately leaves to the editor.
- **One operation has no method**: `start-credential-oauth`, which is bound
  to a browser nonce cookie and not callable from a backend.

The [examples in the SDK directory](https://github.com/kilaslab/kilas-flow/tree/main/sdk/examples)
are complete runnable hosts — `host-page` is the minimal loop, and
`reference-host` is a two-tenant integration with provisioning, webhooks and
isolated datastores.
