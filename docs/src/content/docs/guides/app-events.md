---
title: App events as triggers
description: Fire events from your own application into a pack trigger — the exact HMAC signature to produce, the delivery-id header that makes retries safe, and the vocabulary a registration lifecycle can template.
---

A trigger pack turns your application's events into workflow starts: it
declares the events, binds a webhook route, and verifies each delivery's
signature before an execution exists. When *your* app is the sender — you
embedded KilasFlow and your product fires its own events — you are also the
signer, and the signature format is a contract you implement twice: once in
the pack that declares it, once in the sender that produces it.

This page is the sender's half. The pack's half — declaring `hmac`,
`deliveryIdHeader`, `lifecycle` and `media` on a trigger — is
[Step 5 of the authoring guide](/guides/node-authoring/#step-5-triggers);
generating the trigger from an OpenAPI document is in
[Generate a node pack from OpenAPI](/guides/openapi-pack/).

Everything below is verified against a real trigger: a scratch server, a
workflow on the shipped WAHA trigger pack (`pack.wahaTrigger`) with an HMAC
secret set, and the deliveries this page describes.

## The signature, exactly

The default scheme — and the one the WAHA trigger declares — is the simplest
possible: **HMAC-SHA512 over the exact raw request body, written as lowercase
hex, with no prefix and no timestamp**. The header name is whatever the
pack's `hmac.header` says (`X-Webhook-Hmac` for WAHA).

- Sign the **raw bytes** of the body, before any transport encoding and after
  any JSON serialisation — sign what you send, byte for byte.
- Hex, lowercase by convention; the comparison is case-insensitive, so an
  uppercase-hex sender passes too. Base64 is a different declared `encoding`,
  not a different way to write hex.
- No `sha512=` prefix, no version marker, nothing between the header's start
  and the digest.
- No timestamp, and therefore no staleness check — replay protection comes
  from the delivery-id header, not from the signature.

In Node:

```ts
import { createHmac } from 'node:crypto';

const body = JSON.stringify(event);
const signature = createHmac('sha512', secret).update(body).digest('hex');

await fetch(`${kilasflow}/webhook/${route}`, {
  method: 'POST',
  headers: {
    'content-type': 'application/json',
    'X-Webhook-Hmac': signature,
    'X-Webhook-Request-Id': deliveryId,
  },
  body,
});
```

In Go:

```go
mac := hmac.New(sha512.New, []byte(secret))
mac.Write(body) // the exact bytes you send, not a re-marshalled copy
signature := hex.EncodeToString(mac.Sum(nil))

req, _ := http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(body))
req.Header.Set("Content-Type", "application/json")
req.Header.Set("X-Webhook-Hmac", signature)
req.Header.Set("X-Webhook-Request-Id", deliveryID)
```

Two failure modes are refusal, not delivery — the run never exists:

| What the sender did | Answer |
| --- | --- |
| Omitted the header | `401` — `this endpoint requires a X-Webhook-Hmac signature`. |
| Signed different bytes, or with a different secret | `401` — `the X-Webhook-Hmac signature does not match the request body`. |

A pack may declare a scheme that is not the default one — `sha256`, base64
encoding, a prefix such as `sha256=` or `v1,`, a `signingString` template
over body, timestamp and headers, a `timestampHeader` with a
`toleranceSeconds` replay guard, or the `standardWebhooks` preset. The
[reference table](/guides/node-authoring/#the-scheme-algorithm-encoding-prefix-signing-string-timestamp)
lists every field. If your product already signs its webhooks a particular
way, declare that way; the recipe above is only the zero-configuration case.

## Retries and the delivery-id header

Your app will retry a delivery when KilasFlow is slow or the network blinks,
and a retried delivery must not run the workflow twice. The trigger declares
which header carries your identifier for the delivery (`deliveryIdHeader`;
WAHA names `X-Webhook-Request-Id`), and you fill it with a value unique to
this event — a message id, an event sequence number, a UUID minted per send.

- A repeated identifier inside a five-minute window is answered **without
  running the workflow again**, and the answer mirrors the original outcome:
  when the first execution already finished, the retry sees its status
  (`{"duplicate": true, "executionId": …, "status": "succeeded"}`); while it
  is still running, the retry is answered `202` — the delivery was accepted,
  because it was.
- The window is a dedup window, not a replay guard. It makes *your* retries
  safe; it cannot stop a malicious replay of a captured delivery — the HMAC
  does that, and a scheme with no timestamp has no opinion about freshness.
- Send the identifier on **every** delivery, including the first: the window
  is claimed before the execution is queued, so two concurrent retries of the
  same delivery cannot both run.
- One caveat verified while writing this page: the header name must be
  **set on the trigger node's parameters in the workflow document**. The
  pack's parameter default fills the editor's form, but a document written
  without the key binds no header name, and such a trigger dedupes nothing —
  by design, because the only content-based alternative (hashing the body)
  would treat two genuinely identical messages as one delivery.

## The route

A trigger's inbound address is `/webhook/{route}`, where the route segment is
minted opaquely when the workflow activates — it is a capability, not a name:
anyone who holds it can deliver, which is why the signature matters. Read the
address from `GET /api/v1/workflows/{id}/webhooks`, which returns the full
URL per trigger node. Deactivating the workflow unbinds the route. The
routing answers — unknown route, wrong method, filtered deliveries — are in
[Webhooks](/reference/api/webhooks/).

If your pack declares a registration `lifecycle`, activation can also *tell
your app* the new URL: the `set` request templates the minted URL as
`{{ .PublicURL }}`. A lifecycle request is an ordinary governed outbound call
under the egress policy and the credential's scope, and it templates over a
fixed vocabulary:

| Family | Reads | Escaped? |
| --- | --- | --- |
| `{{ .Parameter.<key> }}` | a node parameter, as text — string, number, boolean | yes — one path segment, or a query value, or JSON-escaped in a body string |
| `{{ .ParameterJSON.<key> }}` | any parameter as JSON — a `multiOptions` list, a collection | yes, and outside a JSON string it is the JSON value itself |
| `{{ .Captured.<key> }}` | a value the registration's answer kept (`lifecycle.set.capture`) | yes, like a parameter |
| `{{ .PublicURL }}`, `{{ .Route }}` | the minted delivery URL, the route segment | no — they are the addresses the request is built on |
| `{{ .<field> }}` | a non-secret field of the credential the request authenticates with | no — a credential's base URL is an address by design |

Anything else in a lifecycle template is refused when the pack loads, and the
[authoring guide](/guides/node-authoring/#step-5-triggers) covers capture,
removal and the secret-from-answer scheme in full.

## A verification recipe

The page's claims, as a script you can run against any install with the WAHA
pack (which ships in the binary). It creates a workflow on the trigger,
activates it, and fires the four deliveries above:

```sh
# 1. Create a credential the trigger binds (activation refuses without one).
curl -X POST "$BASE/api/v1/credentials" -H 'content-type: application/json' \
  -d '{"name":"verify","type":"wahaApi","fields":{"baseUrl":"https://waha.invalid","apiKey":"k"}}'

# 2. Create the workflow. hmacSecret signs; deliveryIdHeader dedupes.
curl -X POST "$BASE/api/v1/workflows" -H 'content-type: application/json' \
  -d '{"schemaVersion":1,"name":"verify-app-events","settings":{},"connections":[],
       "nodes":[{"id":"trigger","name":"WAHA Trigger","type":"pack.wahaTrigger","typeVersion":202502,
         "position":{"x":0,"y":0},"credentials":{"wahaApi":"<credential id>"},
         "parameters":{"path":"recipe","session":"default","hmacSecret":"topsecret",
           "deliveryIdHeader":"X-Webhook-Request-Id"}}]}'

# 3. Activate and read the minted route.
curl -X POST "$BASE/api/v1/workflows/<id>/activate"
curl "$BASE/api/v1/workflows/<id>/webhooks"   # .url ends in /webhook/<route>

# 4. Sign and deliver.
BODY='{"event":"message.any","session":"default","payload":{"from":"me"}}'
SIG=$(printf '%s' "$BODY" | openssl dgst -sha512 -hmac topsecret | awk '{print $2}')
curl -X POST "$BASE/webhook/<route>" -H 'content-type: application/json' \
  -H "X-Webhook-Hmac: $SIG" -H 'X-Webhook-Request-Id: d-1' -d "$BODY"
# → 200 {"message":"Workflow was started"}

# 5. Replay the same delivery id: answered, not run.
#    → 200 {"duplicate":true,"executionId":"…","status":"succeeded"}

# 6. Drop the signature, or sign different bytes: 401, no execution.
```

`GET /api/v1/executions?workflowId=<id>` shows exactly one execution after
steps 4–6: the replay answered from the record, and the refusals never became
runs.
