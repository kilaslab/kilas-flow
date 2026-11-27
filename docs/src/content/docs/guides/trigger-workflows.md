---
title: Trigger workflows from your app
description: Start workflows from your backend with host events, a webhook URL, or the run API. Which one to use, what each one authenticates with, and how to make a retry safe.
---

Your backend has three ways to start a workflow. They differ in who names the
workflow, which revision runs, and what credential the call carries.

| You want | Use | Runs | Credential |
| --- | --- | --- | --- |
| Every workflow that listens for a domain event to start | [`POST /api/v1/events`](#publish-a-host-event) | each workflow's **active** revision | the tenant API key |
| One workflow, called at an address you were given | [`POST /webhook/{route}`](#call-a-webhook) | the active revision | whatever the trigger node checks, or nothing |
| One workflow, by id, with input you choose | [`POST /api/v1/workflows/{id}/run`](#run-a-workflow-by-id) | the **latest saved** revision, active or not | the tenant API key, or a scoped key with `workflow:run` |

Start with host events. One publish reaches every workflow that listens, the
call is idempotent, and the workflow that runs is the one you put live. Use a
webhook when the sender is not your backend or you want a synchronous answer.
Use the run API for tests and one-off jobs, because it ignores whether a
workflow is active.

Every path writes a queued execution and answers before the work is done. To
learn how a run ended, see [Get notified of results](/guides/get-notified/).

## Publish a host event

A host event is a fact your product raises: a message arrived, a contact was
tagged, a booking was made. You publish it once, and KilasFlow starts every
active workflow whose event trigger lists that type.

```sh
curl -X POST "$KILASFLOW_URL/api/v1/events" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: message:m-1" \
  -d '{"eventId":"m-1","type":"message.received","payload":{"text":"hi"}}'
```

The answer is `202` and names what happened:

```json
{
  "matched": [{ "workflowId": "wf_…", "executionId": "exec_…" }],
  "filtered": []
}
```

`matched` has one entry for each workflow that queued an execution. `filtered`
lists workflows that were considered and did not run, each with a `reason`
(`duplicate` for an event already delivered to that workflow). An event that no
workflow listens for is accepted and matches nothing, so a publish never fails
because nobody is subscribed. The request fields are `type` (required),
`eventId`, `occurredAt` and `payload`. The full contract is
[Host events](/reference/api/host-events/).

With the SDK:

```ts
const outcome = await client.publishEvent(
  { eventId: message.id, type: 'message.received', payload: message },
  { idempotencyKey: `message:${message.id}` }
);
```

### Subscribe a workflow to the event

A workflow listens with the **Host Event** trigger (`kilasflow.hostEvent`). Its
`events` parameter lists the types it starts for:

```json
{
  "id": "trigger",
  "name": "Host Event",
  "type": "kilasflow.hostEvent",
  "typeVersion": 1,
  "position": { "x": 0, "y": 0 },
  "parameters": { "events": ["message.received"] }
}
```

The editor's picker offers the types that node packs on the server declare. The
host's own product pack is the usual place to declare them, with a schema and a
sample for each ([the pack guide](/guides/node-authoring/)). The server routes
on the exact string in `events`, so a workflow saved through the API can name a
type no pack declares.

The trigger's item is the event document, `{type, payload, occurredAt,
eventId}`. A workflow branches on `{{ $json.type }}` and reads
`{{ $json.payload }}`. The workflow must be active. Deactivating it removes it
from the fan-out.

### Retries

Two things make a retried publish safe, and they stack:

- **`Idempotency-Key`.** A retry with the same key and the same request gets the
  first answer back with `Idempotent-Replayed: true` and starts nothing. The key
  is remembered for `idempotency.retention` (24 hours by default). The same key
  with a different request is a `409`. See [Idempotent requests](/guides/idempotency/).
- **`eventId`.** Without a header, the `eventId` is the idempotency key. Either
  way, the same `eventId` reaches each workflow once within a five-minute
  window, even when the two publishes carry different keys. The second publish
  answers with that workflow under `filtered` with reason `duplicate`.

Use your own stable id for both, such as the message id. Do not mint a new key
for each attempt, because that defeats both.

Only the tenant's full API key may publish. Embed sessions and scoped keys are
refused, because one publish fans out across the whole tenant.

## Call a webhook

A workflow with a Webhook trigger (`kilasflow.webhook`) has its own address.
Read it from the API. It is minted once, before the workflow is ever active, and
never changes:

```sh
curl "$KILASFLOW_URL/api/v1/workflows/$WORKFLOW_ID/webhooks" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY"
# [{"nodeId":"hook","method":"POST","path":"order-lookup","url":"/webhook/2fca2844…"}]
```

`url` is relative. Put your KilasFlow base URL in front of it. The route is
opaque and per trigger node, and anyone who holds it can deliver, so protect it
with the trigger's own check. The Webhook node supports basic auth, a fixed
header and a JWT, each against a stored credential. A delivery that fails the
check answers `401` and never becomes an execution. The embedding guide shows
how to attach a header credential to the trigger from your backend
([store a credential, attach it to the trigger](/guides/embedding/#2-store-a-credential-attach-it-to-the-trigger)).
A deployment can refuse unauthenticated triggers outright with
`webhook.require_auth`.

```sh
curl -X POST "$KILASFLOW_URL/webhook/$ROUTE" \
  -H "Content-Type: application/json" \
  -H "X-Reference-Key: $WEBHOOK_SECRET" \
  -d '{"email":"ada@example.com","plan":"trial"}'
```

The route is served outside `/api/v1`, so it is not in the OpenAPI document and
the [API contract](/reference/api-contract/) does not cover it. Its behaviour is
on [Webhooks](/reference/api/webhooks/) and in
[Triggers and webhooks](/concepts/webhooks/).

### What the caller gets back

The trigger's `responseMode` decides it:

- **`immediate`** (the default) answers `{"message":"Workflow was started"}`
  and does not name the run.
- **`lastNode`** waits for the run and returns the last node's data.
- **`responseNode`** answers when a Respond to Webhook node runs, and the rest
  of the workflow keeps going.

For a backend that wants to hand the user an answer now and track the run later,
use `responseNode` and answer with the run id. `$execution.id` is available in
expressions. These two nodes were run against a real server for this page:

```json
{
  "id": "hook",
  "name": "Signup",
  "type": "kilasflow.webhook",
  "typeVersion": 1,
  "position": { "x": 0, "y": 0 },
  "parameters": {
    "path": "signup",
    "httpMethod": "POST",
    "authentication": "none",
    "responseMode": "responseNode"
  }
}
```

```json
{
  "id": "accepted",
  "name": "Accepted",
  "type": "kilasflow.respondToWebhook",
  "typeVersion": 1,
  "position": { "x": 240, "y": 0 },
  "parameters": {
    "respondWith": "json",
    "responseCode": 202,
    "responseBodyJSON": { "mode": "expression", "value": "{{ { executionId: $execution.id } }}" }
  }
}
```

Connect the Webhook node to the Respond node and the Respond node to the real
work. The caller gets `202 {"executionId":"exec_…"}` at once, and the workflow
continues behind it. Store that id and watch the run through
[Get notified of results](/guides/get-notified/). This replaces the older
advice to list executions and take the newest, which can pick the wrong run
when two deliveries arrive together.

Use a real `authentication` mode in production. `none` only keeps the example short.

### Retries and limits

- **Duplicates.** Set the trigger's `deliveryIdHeader` to the name of a header
  your sender fills with a value unique to each event. A repeated value inside a
  five-minute window starts nothing. The window is fixed. The retry gets
  `{"executionId":"…","status":"…","duplicate":true}`, or a `202` if the first
  delivery has not recorded its run yet. The retry does **not** get the first
  run's response body. See [App events as triggers](/guides/app-events/#retries-and-the-delivery-id-header).
- **Size.** A body over `webhook.max_body_bytes` (1 MiB by default) answers `413`.
- **Waiting.** A caller that waits for an answer is cut off after
  `webhook.response_timeout` (30 seconds by default) with a `504`. The run keeps
  going. Only the synchronous answer is abandoned.
- **Rate.** Each tenant has a webhook allowance of `ratelimit.webhook_per_minute`
  (600 by default). Over it, the caller gets `429` with `Retry-After`, before any
  execution exists.

If your own product is the sender and you want KilasFlow to verify a signature
over each delivery, declare it on a pack trigger. The signature format and the
sender's half are in [App events as triggers](/guides/app-events/).

## Run a workflow by id

```sh
curl -X POST "$KILASFLOW_URL/api/v1/workflows/$WORKFLOW_ID/run" \
  -H "Authorization: Bearer $KILASFLOW_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: 7a1c9f0e-2f5b-4c3d-9e10-4b2f8d6a1c33" \
  -d '{"input":{"customerId":"c_42"}}'
```

The answer is `202` with the new execution, including its `id`. `input` must be
a JSON object. It becomes the trigger's item. The body also takes
`workflowVersionId` to pin a revision and `triggerNodeId` to start from one
trigger when the workflow has several. Without `triggerNodeId` it starts from
every trigger.

```ts
const { id: executionId } = await client.runWorkflow(workflowId, { customerId: 'c_42' }, { idempotencyKey });
```

Two behaviours matter:

- **It runs the latest saved revision and ignores activation.** That is right for
  a test run. It is wrong for production traffic, where the version that is live
  should run. Use a host event or a webhook for that.
- **A key can be limited to this one call.** A key with `workflow:run`, bound to
  one workflow, can start that workflow and nothing else
  ([scoped keys](/operate/tenants/#scoped-api-keys)). That suits a job runner.

Each tenant has a run allowance of `ratelimit.run_per_minute` (120 by default).
Over it, the answer is `429` with `Retry-After`.

## Make retries safe

| Path | Protection | Remembered for |
| --- | --- | --- |
| Host event | `Idempotency-Key`, or `eventId` when there is no header. `eventId` also dedupes per workflow. | the key for `idempotency.retention`; the `eventId` for five minutes |
| Webhook | the trigger's `deliveryIdHeader` | five minutes, fixed |
| Run API | `Idempotency-Key` | `idempotency.retention` |

The limits of idempotency, such as a crash between the side effect and the
recorded answer, are listed in [Idempotent requests](/guides/idempotency/#what-is-not-guaranteed).
Protection stops at the request. A worker that dies mid-run is reclaimed, and the
reclaimed run starts again from the beginning, so a node with a side effect can
perform it twice. Make the side effect safe on the receiving end too.

## Per-tenant limits

Every ingress above is counted per tenant. The defaults come from the
`ratelimit` section ([configuration reference](/operate/configuration-reference/)).
An operator can set different numbers for one customer through its tenant
settings ([Tenant settings](/operate/tenants/#tenant-settings)). With more than
one server process, set `ratelimit.store: database` so the processes share one
allowance.
