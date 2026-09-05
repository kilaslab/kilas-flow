---
title: Triggers and webhooks
description: The opaque per-node route, why every kind of miss returns the same 404, and the four ways a webhook can answer its caller.
sidebar:
  order: 8
---

A workflow starts from a trigger. Four kinds exist — a manual run, an inbound
HTTP request, a cron schedule, and a call from another workflow — and all four
write the same kind of queued execution row through the same durable queue. A
trigger cannot bypass lifecycle, validation or execution-record rules by having
its own path into the engine.

This page is about the inbound HTTP surface, because it is the one exposed to the
public internet and therefore the one with a security posture.

## The route is opaque, per node, and permanent

`/webhook/{route}` is served outside `/api/v1`, and is deliberately outside the
authentication middleware — the senders that will call it are third-party
services that cannot present a KilasFlow credential.

A route is 16 bytes from `crypto/rand`, hex-encoded to 32 lowercase characters.
Sixteen bytes is far beyond guessable, and an unguessable route is a meaningful
defence for an endpoint that is very often unauthenticated.

It is minted **per trigger node**, keyed by tenant, workflow and node, on the
node's first activation — and then reused forever. Reuse is the whole point: a
route minted per activation would change the public URL every time a workflow was
deactivated and reactivated, breaking every sender already configured against it.
The route row therefore outlives the *binding*, which exists only while the
workflow is active.

Because the route already carries the tenant implicitly, no tenant identifier
appears in the URL and the sender learns nothing about the installation from it.

## Every miss looks identical

An unknown route, an inactive workflow, a deleted workflow, a workflow that was
never activated, and a request with the wrong method all return exactly the same
answer:

```
HTTP/1.1 404 Not Found
Content-Type: application/problem+json

{"title":"Not Found","status":404,"detail":"No active workflow is bound to this webhook."}
```

The reason is stated in the handler: answering both the same way keeps the
endpoint from confirming which workflows exist. An endpoint that distinguished
"no such route" from "that route exists but is inactive" would be an oracle for
enumerating a customer's workflows.

What makes this structurally honest rather than a cosmetic flattening is that the
different cases genuinely *are* the same case at the storage layer. Bindings
exist only for active workflows, so routing never has to ask whether a workflow
is active — an unroutable workflow simply has no row. And the HTTP method is part
of the lookup's `WHERE` clause, so a wrong method literally produces "no row
found" rather than being detected and then deliberately disguised.

## Bounds

| Bound | Default | Config key |
| --- | --- | --- |
| Request body | 1 MiB | `webhook.max_body_bytes` |
| Response wait | 30 s | `webhook.response_timeout` |
| Duplicate-delivery window | 5 minutes | not configurable |

An oversized body is `413`, not a truncated read — though so is any other failure
to read the delivery, since the handler maps every error from that step to the
same status. A workflow that does not finish inside the response timeout gets
`504`; the execution keeps running, only the synchronous answer is abandoned.

The five-minute deduplication window is a constant rather than a setting because
it has to outlast the *sender's* whole retry sequence, and those are known
quantities: WAHA retries fifteen times at two-second intervals, so thirty seconds
of retrying needs comfortably more than thirty seconds of memory. A trigger names
the header carrying the delivery identifier, and the claim on it is taken
*before* the execution is queued, so a duplicate never becomes a second run.

## Four ways to answer

The trigger's `responseMode` parameter decides what the caller gets back.

**Immediate** (the default) acknowledges without waiting:
`{"executionId":…, "status":"queued"}`. The status code can be overridden by the
trigger's `responseCode` parameter, within 100–599. This is the right mode for
anything that will take longer than the sender's own timeout.

**Last node** waits for the run and returns the last node's data as the body —
where "last" means the highest-sequence node run that succeeded and produced
items. A sub-setting chooses between `204 No Content`, the first item's JSON as
an object, or all items as a JSON array. This mode used to write KilasFlow's own
`{executionId, status, data}` envelope instead, which meant an imported workflow
configured for `lastNode` returned `200` and handed its caller the wrong body
with nothing reporting it.

**Respond node** waits, then answers from a Respond to Webhook node's item — its
`statusCode`, `headers` and `body`. If no `Content-Type` was set, the handler
picks `application/json` when the body is valid JSON and `text/plain` otherwise.
If the graph finished without reaching a Respond node, the answer is `500` saying
exactly that, which beats returning a misleading `200` with the last node's data.

The search for that node walks the node runs in **execution order** rather than
iterating the output map, because Go randomises map iteration: with a Respond
node on each arm of an `IF`, which one answered the caller was decided by a coin
flip.

**Filtered** is not a mode but is worth knowing about. A trigger restricted to
certain event types answers `200 {"filtered": true, …}` for an update it was
restricted away from. Filtering is not verification and the answer is different
on purpose: the delivery was received correctly and deliberately not acted on, so
the sender is told `200` and stops retrying.

## Authentication on the way in

A trigger may require basic auth or a fixed header, checked against a stored
[credential](/concepts/credentials/) with a constant-time comparison. There is
also HMAC signature verification for triggers that declare it, with a missing or
empty signature counting as a refusal.

Verification happens in the handler, before an execution exists. A signature
check belongs there rather than inside the workflow: a request that fails it
should never become an execution.

The failure mode is closed. A webhook configured to authenticate but unable to —
because its credential is missing or unreadable — refuses the request. Failing
open would silently publish an unprotected endpoint.

## Shapes: what the trigger emits

A trigger decides the shape of the item it produces, and three exist. The default
`envelope` carries `method`, `path`, `headers`, `query`, `body` and
`contentType`. `n8nCore` matches what n8n's own webhook node emits — `body`,
`headers`, `params` and `query` — so an imported workflow's expressions resolve
against the field names they were written for. `params` there is always an empty
object: n8n's path parameters come from a route pattern, and a KilasFlow route is
one opaque segment with no pattern behind it. The key is present rather than
absent so an expression reading it gets an empty object instead of failing.
`bodyAsItem` makes the parsed body the item itself.

Headers are redacted before the delivery is recorded. The **body is deliberately
not**: a workflow's whole purpose is usually the body, and redacting it would
make the execution trace useless for the thing people actually open it for.

## Which node types can bind a route

A node type binds an inbound path only if its definition carries a `Webhook`
declaration. Before that declaration existed, exactly one node type could receive
requests and it was named at composition — so a Telegram or WAHA trigger could be
registered, placed on a canvas, saved, activated, and would simply never receive
a request. No error; just an active workflow that was unreachable.

The declaration names the *parameter keys* holding the path and the method rather
than literal keys the extractor knows, so a trigger whose path lives under
`chatPath` still binds. A trigger with no path parameter at all declares a static
path, which is what a node whose route is entirely minted needs.

A trigger may also declare a `LifecycleID`, which is how it registers itself with
the service that will deliver to it — telling Telegram where to send updates, for
instance. Every declared lifecycle is verified to have an implementation bound at
startup, so a trigger declaring a hook nobody registered fails at boot rather
than silently never registering at its first activation.

Self-registration needs `server.public_url`. With it unset, self-registration is
disabled rather than guessing an address, because a bot registered against a
wrong address receives nothing and reports success.

## There is no separate test URL

n8n distinguishes a test webhook URL from a production one. KilasFlow does not:
there is exactly one inbound surface, and a route is the same URL whether you are
testing or not.

The nearest equivalent is Telegram's delivery mode. A webhook needs a public
HTTPS address; polling calls `getUpdates` from this process instead, which is how
you test on a laptop. It runs in one process only and is not for production.

## Scheduling

The cron scheduler claims due rows transactionally, so a second instance will not
double-fire the same schedule. It is nonetheless single-process: distributed
scheduling is not designed. Schedules are managed through
`/api/v1/schedules`.

## API operations

The inbound surface is `/webhook/{route}` and is **not** in the OpenAPI document
— it is registered as a plain handler for all methods, because its behaviour is
defined by the trigger node rather than by a route definition.
`POST /api/v1/workflows/{id}/activate` is what mints and binds routes;
`POST /api/v1/workflows/import` returns the routes it reserved for an imported
workflow's triggers. See the [HTTP API reference](/reference/api/).

## Source

`internal/webhook/webhook.go` (the handler, the uniform 404, the response
modes), `internal/webhook/shape.go` (item shapes and HMAC verification),
`internal/webhook/lifecycle.go` (self-registration),
`internal/repository/webhooks.go` (`mintWebhookRoute`, binding resolution,
deduplication), `internal/api/routes.go` (where the prefix is reserved),
`internal/node/registry.go` (`WebhookDeclaration`).
