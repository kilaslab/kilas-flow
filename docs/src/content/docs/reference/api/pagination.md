---
title: Pagination
description: The two cursor conventions the listing operations use, which endpoints use which, and the helpers the host SDK ships so you never hand-roll the loop.
sidebar:
  order: 13
---

<!-- Hand-written companion to the generated reference. The OpenAPI document
     names the shapes; which convention an endpoint follows, and why, is
     behaviour this page curates — the same arrangement as webhooks.md. -->

Every listing operation in this API is cursor-paginated: you ask with `limit`
and `cursor`, and the answer tells you how to ask for more. There is no
page-number or offset convention anywhere — a cursor is an opaque string you
pass back untouched, and it stays valid only for the ordering it was minted
from.

The API uses the same idea in two places, and an integrator writing a client
by hand has to know which one an endpoint speaks:

| Convention | How you see it | Endpoints |
| --- | --- | --- |
| **Cursor in the `X-Next-Cursor` response header** | Body is the list itself — a bare array for `list-workflows`, `list-credentials` and `list-schedules`; a `{items}` object for `list-api-keys` and `list-datastores` | those five |
| **Cursor in the response body** | Body is `{items, nextCursor}` | `list-executions`, `list-datastore-rows`, `list-workflow-versions` |

The difference is historical, not semantic, and it is deliberate that both
spell the end the same way: **no cursor means no more pages**. An empty
`X-Next-Cursor` header and an absent/empty `nextCursor` field both end the
iteration.

## Limits and clamps

`limit` is capped per endpoint — `list-workflows` and `list-credentials` at
500, `list-executions` at 100, and so on; the operation pages name their own.
A `limit` above the cap is clamped to it rather than refused, and a cursor
past the last page answers an empty list. Cursors expire: they are derived
from row positions, and a page minted before rows were deleted or added may
skip or repeat — treat pagination as a best-effort sweep, not a snapshot
(what you need a snapshot for, the
[idempotency](/guides/idempotency/) and concurrency pages cover).

## Not everything pages

A few listings are deliberately unpaginated because their answer is small
and bounded: `list-credential-types`, `list-node-types`, a workflow's
`list-workflow-webhooks` and `list-workflow-publish-events`, and the operator
surface's `list-tenants` and `list-tenant-users`, which return everything.
Don't build a cursor loop against those — there is no header and no field to
read.

## The SDK helpers

The host SDK's `KilasFlowClient` covers the body-cursor convention with one
shape:

```ts
import { paginateCursor } from "@kilasflow/sdk/server";

// One shared helper: give it a page-fetcher, get every item.
for await (const execution of client.iterateExecutions({ workflowId })) {
  // …
}
```

- `paginateCursor(fetchPage)` — pages any `{items, nextCursor}`-shaped
  listing to exhaustion, yielding one item at a time.
- `client.iterateExecutions(query)` — the executions sweep as one loop.
- `client.iterateDatastoreRows(datastoreId, query)` — the row sweep; `TRow`
  is your declared row type when you have one.

The header convention is not hidden, on purpose: `listWorkflows()`,
`listCredentials()`, `listSchedules()`, `listApiKeys()` and `listDatastores()`
return one page (the server's default limit) and stop, and the SDK's request
layer answers with the body only — so a caller who needs everything past the
first page makes those calls with its own `fetch`, which is where reading a
response header belongs anyway. See the [HTTP API reference](/reference/api/)
for each operation's exact response shape, and
[Using the host SDK](/guides/sdk/) for the client itself.
