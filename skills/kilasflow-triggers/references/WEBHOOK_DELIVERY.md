# Webhook delivery

One inbound request walks a fixed path from the route to a queued execution and
then to an answer. This file is that path, in order, with the answer each step
can produce — and what a caller's 200 does and does not prove. Source:
internal/webhook/webhook.go unless a step names its own file.

## 1. Route, method, admission

`/webhook/{route}` is served outside `/api/v1` and outside the authentication
middleware, because the senders that call it cannot present a KilasFlow
credential (internal/webhook/webhook.go, `ServeHTTP`).

- A request is matched by upper-cased **method** and **route**, and by nothing
  else. A method the node does not declare finds no binding, and so does a
  workflow that was never activated or has since been deactivated.
- Every one of those misses answers the same body:
  `{"title":"Not Found","status":404,"detail":"No active workflow is bound to this webhook."}`
  An endpoint that told "no such route" from "that route is inactive" would be
  an oracle for enumerating a tenant's workflows.
- `OPTIONS` is answered from the methods actually bound on the route plus
  `OPTIONS`, so a browser learns a GET-only endpoint will refuse its POST rather
  than reading it as an opaque CORS failure (`servePreflight`, `applyCORS`).
- `admit` runs the address allow-list (`options.ipWhitelist`, from the
  connection's own peer address, deliberately not trusting `X-Forwarded-For`)
  and then the credential, in that order, so a refused caller cannot use the
  endpoint to probe for valid secrets. A body that cannot be read is **413**,
  including an oversized one (`webhook.max_body_bytes`, 1 MiB by default);
  `readDelivery` maps every failure at that step to the same status.

## 2. The trigger's own checks

The node type decides what a delivery means, and both checks run before an
execution exists (internal/webhook/webhook.go):

- **Verify.** A trigger that declares one (a pack trigger's HMAC over the raw
  body, Telegram's secret header) refuses a delivery that fails it with **401**,
  and a missing or empty signature counts as a refusal. Verification belongs at
  the boundary: a request that fails it must never become a run.
- **Accept.** A trigger restricted to certain events answers
  `200 {"filtered": true, "reason": …}` for one it was restricted away from, and
  **Ignore Bots** answers `200 {"filtered": true, …}` for a delivery whose
  User-Agent names a crawler, so a chat client's link preview does not trigger
  the workflow's side effects (`ignoreBots`). Filtering is not verification: the
  delivery arrived correctly and was deliberately not acted on, so the sender is
  told 200 and stops retrying.

The rendered item comes from the trigger's shape, not from the request
(internal/webhook/shape.go):

| Shape | What the item is |
| --- | --- |
| `envelope` (default) | `method`, `path`, `headers`, `query`, `body`, `contentType` |
| `n8nCore` | `body`, `headers`, `params` (always an empty object here), `query`, `webhookUrl`, `executionMode`; `jwtPayload` only when a JWT was verified |
| `bodyAsItem` | the parsed body at the top level, which is what readers of `$json.event` expect |
| `formSubmission` | the form's own field labels, plus `submittedAt` and `formMode` stamped by the boundary (internal/webhook/form.go, `SubmissionFields`) |

## 3. Duplicate deliveries

A sender that retries must not run the workflow twice. The node names the header
carrying the sender's own identifier in its `deliveryIdHeader` parameter, and a
claim on that identifier is taken **before** the execution is queued
(internal/webhook/webhook.go; internal/repository/webhooks.go, `ClaimDelivery`).

- With no header configured, or the sender omitting it, the request is never
  deduplicated: hashing the body instead would collapse two genuinely identical
  messages a user sent twice. The window is five minutes
  (`repository.DefaultDeliveryWindow`), a constant rather than a setting because
  it has to outlast the sender's whole retry sequence — WAHA retries fifteen
  times at two-second intervals.
- A duplicate is answered from the original: **200** with
  `{executionId, status, duplicate: true}` once the original execution reads
  back, otherwise **202** with the id and the same marker (`answerDuplicate`). A
  claim that cannot be stored fails open: the delivery runs rather than being
  mistaken for somebody else's duplicate.

## 4. The four answers

`responseMode` decides what the caller receives. These modes are declared in
nodes/webhook.go and answered in internal/webhook/webhook.go.

**Immediate** (the default) acknowledges without waiting, with n8n's own body
`{"message":"Workflow was started"}`. `options.responseCode` sets the status
(100–599; anything else is 200) and the legacy top-level `responseCode` is read
too; `options.responseHeaders` adds headers in either shape;
`options.responseData` sends that text instead of the JSON body as
`text/html; charset=utf-8`; `options.noResponseBody` sends the status with no
body. A form trigger answers its submitter with a rendered page instead.

**Last node** waits for the run and returns the items of the node that ran last
— the highest sequence that produced items, with a skipped node and an empty one
both passed over. `responseData` picks the shape: `firstEntryJson` (the
default), `allEntries` (always an array, even for one item) or `noData` (the
configured status and an empty body, deliberately not a 204). Two answers come
from the platform: a run that succeeded whose last node produced nothing is
`500 {"message":"No item to return was found"}`, and a run that did not succeed
is `500 {"message":"Error in workflow"}` — generic on purpose, because the
caller is whoever found the URL.

**Respond node** answers when a `kilasflow.respondToWebhook` node runs. Its
status, headers and body are published as an event and the caller is answered at
that moment while **the rest of the graph keeps going** — which is how a chat
webhook acknowledges inside its three-second budget and still does slow work.
The event is also written with the node's trace row, so a boundary in another
process answers from the record (`findResponse`). A `Content-Type` the node did
not set is chosen from the body: `application/json` when it parses,
`text/html; charset=utf-8` otherwise. A run that finished successfully without
reaching the node is **200 with an empty body**; a failed one is the generic 500
above. A run that has not finished within `webhook.response_timeout` (30 s) gets
**504** while the execution keeps running, and a delivery that could not be
queued at all is **500**, with the cause logged rather than returned.

## 5. Authentication on the way in

The `authentication` parameter takes `none`, `basicAuth`, `headerAuth` or
`jwtAuth`, and each mode except `none` needs a credential of the matching type
attached to the node (`httpBasicAuth`, `httpHeaderAuth`, `jwtAuth`) — the
activation validator refuses the workflow otherwise (nodes/webhook.go,
`credentialTypesForAuth`, `validateWebhookConfiguration`).

- Comparison is constant time, and a refusal is **401**. `WWW-Authenticate` is
  sent only for basic auth, because a browser prompt is not how a header or JWT
  caller answers a refusal.
- `jwtAuth` reads a `Bearer` token and verifies it with the algorithm and key
  material its credential holds — a shared passphrase for HS, a PEM public key
  for RS, PS and ES. The algorithm comes from the credential, never from the
  token's own header, which is what closes algorithm confusion; `exp` and `nbf`
  are honoured and a token with no `exp` never expires. A verified payload
  reaches the workflow as `jwtPayload` on the item.
- A credential that cannot verify anything at all — missing key material, an
  algorithm this server does not verify, a key type that disagrees — is **500**,
  because the fault is the endpoint's configuration rather than the request. The
  failure mode is closed, never open: a trigger configured to authenticate but
  unable refuses the request rather than publishing an unprotected endpoint.
- `webhook.require_auth` (`KILASFLOW_WEBHOOK_REQUIRE_AUTH`) is the deployment's
  own posture. With it on, a delivery to a trigger whose mode is `none` is
  **403** whose body names the workflow and the fix (internal/webhook/
  require_auth.go, `refuseUnauthenticated`). A trigger that verifies its own
  senders counts as authenticated; an IP allow-list does **not**. A form's page
  faces the same gate as its submission, and a CORS preflight is not gated
  because it cannot carry a credential.

## 6. What a 200 proves

`200` means the delivery was received and either queued or deliberately not
acted on; it does not mean the workflow succeeded. Tell the two apart by reading
the run: `kilasflow exec list --workflow <workflowId> --trigger webhook`
(`list-executions`) and `kilasflow exec get <executionId>` (`get-execution`).
