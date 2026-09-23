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
first read of the workflow's addresses — `GET /workflows/{id}/webhooks` — or on
its first activation, whichever comes first, and then reused forever. Minting on
a read is what lets a sender be configured before the workflow is ever activated:
the address is idempotent, and a read writes only the route row, never a binding,
so reading an address cannot make an inactive workflow answer. Reuse is the whole
point: a route minted per activation would change the public URL every time a
workflow was deactivated and reactivated, breaking every sender already
configured against it. The route row therefore outlives the *binding*, which
exists only while the workflow is active.

Because the route already carries the tenant implicitly, no tenant identifier
appears in the URL and the sender learns nothing about the installation from it.

## The path label is never an address

A request is matched by method and route, and by nothing else. The `path` an
author gives a webhook node is display metadata: what the editor shows, and the
pattern a request's trailing `:param` segments are matched against. It is not
unique — two tenants that import one template hold the same path — so it cannot
say which workflow a request is for, and a request that names only a path
matches nothing and gets the same 404 as any other miss.

A binding that has no minted route, which only a database that predates routes
holds, is given one when the server boots, by the `webhook_route_backfill`
migration. Its address therefore changes from `/webhook/<label>` to
`/webhook/<route>`; read the new one with `GET /workflows/{id}/webhooks`. A
trigger that registers its own address with the sender when the workflow is
activated — Telegram's `setWebhook`, whose secret is derived from the route, and
WAHA — has to be activated again so the sender is given the new address.

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

**Immediate** (the default) acknowledges without waiting, with n8n's own body:
`{"message":"Workflow was started"}`. Four options change that answer.
`options.responseCode` sets the status (100–599, otherwise `200`), and the
legacy top-level `responseCode` is read too, so a workflow saved either way keeps
answering with the code it was configured for. `options.responseHeaders` adds
headers, in n8n's `{entries: [{name, value}]}` shape or as a plain map.
`options.responseData` sends that text instead of the JSON body, as
`text/html; charset=utf-8` — which is what n8n's own HTTP layer does with a
string. `options.noResponseBody` sends the status with no body at all. This is
the right mode for anything that will take longer than the sender's own timeout.

**Last node** waits for the run and returns the last node's data as the body —
where "last" means the highest-sequence node run that succeeded and produced
items, with a skipped node and an empty node both passed over. The trigger's
`responseData` parameter picks the shape: the default is the first item as a JSON
object, `allEntries` is always a JSON array (including for one item), and
`noData` sends the configured status with an empty body — n8n's own "No Data",
and deliberately not a `204`, because the option is about the body rather than
about the status. Two answers come from the platform rather than the workflow: a
successful run whose last node produced nothing gets n8n's own
`500 {"message":"No item to return was found"}` instead of an echo of the
trigger's request data, and a run that did not succeed gets
`500 {"message":"Error in workflow"}` — deliberately generic, because the caller
is whoever found the URL, and the specific failure used to name nodes and quote
the upstream's own error text. This mode used to write KilasFlow's own
`{executionId, status, data}` envelope instead, which meant an imported workflow
configured for `lastNode` returned `200` and handed its caller the wrong body
with nothing reporting it.

**Respond node** answers when the Respond to Webhook node runs. Its `statusCode`,
`headers` and `body` are the answer, and if it set no `Content-Type` the handler
picks `application/json` when the body parses as JSON and
`text/html; charset=utf-8` otherwise. The answer travels as an event the node
publishes, so the caller is answered at that moment and **the rest of the
workflow keeps going** — a Slack or WhatsApp webhook has about three seconds to
acknowledge, and a workflow that acknowledged before doing slow work used to time
out, be retried, and duplicate its side effects. Events already retained for the
execution are replayed to the waiter, so a response published before anything
subscribed is not missed. A run that finished successfully without reaching a
Respond node is answered `200` with an empty body, which is n8n's own answer; a
run that failed is answered `500 {"message":"Error in workflow"}`.

**Filtered** is not a mode but is worth knowing about. A trigger restricted to
certain event types answers `200 {"filtered": true, …}` for an update it was
restricted away from. Filtering is not verification and the answer is different
on purpose: the delivery was received correctly and deliberately not acted on, so
the sender is told `200` and stops retrying.

## Authentication on the way in

A trigger may require basic auth, a fixed header, or a JSON Web Token, checked
against a stored [credential](/concepts/credentials/) with a constant-time
comparison. There is also HMAC signature verification for triggers that declare
it, with a missing or empty signature counting as a refusal.

The JWT mode (`jwtAuth`) reads a `Bearer` token from `Authorization` and verifies
it with the key material its credential holds: a shared passphrase for the HS
algorithms, a PEM public key for RS, PS and ES. The algorithm comes from the
credential, never from the token's own header — the header is compared against it
and an algorithm the credential did not name is refused before the token is even
read, which is what closes algorithm confusion. `exp` and `nbf` are honoured when
the token carries them, and a token with no `exp` never expires, matching n8n's
own `jwt.verify`. A verified payload reaches the workflow as `jwtPayload` on the
item, so an imported workflow reading `$json.jwtPayload.sub` sees who the caller
is.

The status split is the caller's fault against the endpoint's: a missing, forged
or expired token is `401`, while a credential that cannot verify anything —
missing key material, an algorithm this server does not verify, a key type that
disagrees with the algorithm — is `500`, because the fault is the endpoint's
configuration rather than the request. The `WWW-Authenticate` challenge is sent
only for basic auth, since a browser prompt is not how a header or JWT caller
answers a refusal.

Verification happens in the handler, before an execution exists. A signature
check belongs there rather than inside the workflow: a request that fails it
should never become an execution.

The failure mode is closed. A webhook configured to authenticate but unable to —
because its credential is missing or unreadable — refuses the request. Failing
open would silently publish an unprotected endpoint.

## Requiring authentication on every trigger

A deployment can refuse unauthenticated deliveries outright with
`webhook.require_auth` (environment: `KILASFLOW_WEBHOOK_REQUIRE_AUTH`). It is
off by default, so nothing changes until an operator sets it, and it is a
posture for the whole process rather than a per-workflow switch.

With it on, a delivery to a trigger whose authentication mode is `none` is a
`403` whose body names the workflow and the fix:

> This deployment requires webhook authentication (webhook.require_auth) and
> workflow `<workflow id>` does not authenticate this trigger. Set the trigger
> node's Authentication to Basic auth, Header auth or JWT auth and attach a
> credential, then activate the workflow again.

A trigger that checks its own senders counts as authenticated without an
`authentication` mode: Telegram's secret-token header, and a pack trigger's HMAC
over the raw body — but only while that pack trigger actually holds a secret to
check against, since one with no secret verifies nothing. An IP allow-list does
**not** count: it restricts by network address rather than by credential, and
the peer address is a proxy's wherever one sits in front. A caller outside the
allow-list is refused by the address check first, so the workflow id is never
handed to a caller that check already rejected.

The hosted page of a form trigger faces the same gate as its submission — with
the flag on, an unauthenticated form is neither served nor accepted. A CORS
preflight is not gated: it cannot carry a credential, and refusing it would
break the browser flow the route exists for. The check happens at delivery time,
so a workflow with an unauthenticated trigger still activates and is refused
when it is called; refusing it at activation instead is a follow-up.

The boot log states the posture either way:

```
msg="inbound webhooks require authentication" require_auth=true
msg="inbound webhooks accept unauthenticated deliveries unless the trigger sets its own authentication" require_auth=false enable_with=KILASFLOW_WEBHOOK_REQUIRE_AUTH=true
```

## Shapes: what the trigger emits

A trigger decides the shape of the item it produces, and three exist. The default
`envelope` carries `method`, `path`, `headers`, `query`, `body` and
`contentType`. `n8nCore` matches what n8n's own webhook node emits — `body`,
`headers`, `params` and `query`, plus `webhookUrl`, `executionMode` and, when the
trigger verified a JWT, `jwtPayload` — so an imported workflow's expressions
resolve against the field names they were written for. `params` there is always
an empty object: n8n's path parameters come from a route pattern, and a KilasFlow
route is one opaque segment with no pattern behind it. The key is present rather
than absent so an expression reading it gets an empty object instead of failing.
`bodyAsItem` makes the parsed body the item itself.

The delivery is recorded exactly as it arrived — headers included — because the
stored record *is* the input the run executes on: a workflow reading its own
`headers['x-api-key']`, or a cookie, sees what the caller sent rather than a
placeholder. Redaction belongs to the surfaces that hand a record back instead.
API responses, the live feed and the inspector pass the payload through one rule
that normalises header names and withholds credential keys, so a reader never
sees them even though storage holds them. That is a storage-posture fact, not a
detail: see [the security page](/operate/security/) for what it means for
backups and dumps.

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

### Receiving Telegram updates on a laptop

The Telegram trigger has a **Delivery** parameter with two values, and which one
you want depends on whether the machine has a public HTTPS address.

**Webhook** is the default and the only one for production. Activating the
workflow calls `setWebhook` with the workflow's own URL — built from
`server.public_url`, so that has to be the address Telegram can reach — plus the
updates you selected and a secret derived from the bot token and the route.
Telegram returns that secret in `X-Telegram-Bot-Api-Secret-Token` on every
delivery and the endpoint refuses anything that does not match, so a leaked URL
is not enough to inject updates. Telegram accepts only HTTPS; activation says so
by name rather than passing along the Bot API's own error.

To use it from a laptop, put a tunnel in front:

```sh
cloudflared tunnel --url http://localhost:8080   # or: ngrok http 8080
KILASFLOW_SERVER_PUBLIC_URL=https://<the-tunnel-host> make run
```

**Polling** needs no public address at all. The server calls `getUpdates` in a
long poll for as long as the workflow is active, and everything downstream of
the update is identical — same item shape, same restriction filters, same
downloads. Two things to know: it runs in **one process**, so it is wrong for a
deployment running several workers, and Telegram refuses `getUpdates` while a
webhook is registered — so activating a polling trigger deletes the webhook
first, and re-activating a webhook trigger puts it back.

Either way the bot token is a `telegramApi` credential. Its Base URL field is
normally empty; set it only if you run [Telegram's own local Bot API
server](https://core.telegram.org/bots/api#using-a-local-bot-api-server).

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
