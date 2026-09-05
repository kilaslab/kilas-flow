---
id: FEAT-5kv1jq
title: Shape webhook trigger payloads per node type and keep the raw body
status: done
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:03:04Z"
updated: "2026-09-05T05:03:04Z"
---

## Scope

`requestPayload` in `internal/webhook/webhook.go:210-250` builds one item shape for every inbound delivery, regardless of which trigger node is bound to the path: `{method, path, headers, query, body}`. The body is decoded into an `any`, then the whole map is re-marshalled (webhook.go:232-245), so the bytes that actually arrived on the wire are gone by the time anything else can look at them. `executeWebhook` (nodes/webhook.go:210-218) then hands `request.Input` through unchanged, so `$json` at the top of a workflow is always that five-key envelope.

Two problems follow. First, no imported trigger sees the shape it expects. n8n's webhook context exposes `getBodyData`, `getHeaderData`, `getParamsData` and `getQueryData` (`packages/workflow/src/interfaces.ts:1463-1547` in the reference checkout), and n8n's core Webhook node emits `{body, headers, params, query}` — a different set of keys, with the path parameters n8n extracts and KilasFlow does not. A real third-party trigger differs further still: the owner's own `MitraChatWebhookTrigger` returns `{event, eventId, occurredAt, …}` at `$json` top level, and WAHA templates read their envelope at `$json` top level too. Every one of these workflows breaks on import because `$json.event` is `undefined` when the event is actually at `$json.body.event`.

Second, no signature can ever be verified. Both WAHA's `X-Webhook-Hmac` (sha512 over the raw body) and the owner's own trigger require the exact bytes: `MitraChatWebhookTrigger.node.ts:150-167` reads `this.getRequestObject().rawBody` and warns loudly when it has to fall back to `JSON.stringify(bodyData)`, precisely because a re-marshalled body does not hash to the same value. KilasFlow has no equivalent of `rawBody` at all, so HMAC verification is not merely unimplemented — it is impossible.

## Acceptance criteria

- [x] The exact request bytes are captured at ingest and remain available to the trigger node that consumes them, byte-for-byte identical to what the client sent.
- [x] The item shape a webhook delivery produces is determined by the bound trigger node's type, not hardcoded in the HTTP handler.
- [x] The existing `kilasflow.webhook` trigger keeps producing `{method, path, headers, query, body}` so no already-activated workflow changes behaviour.
- [x] A trigger type can declare an n8n-compatible shape and receive `{body, headers, params, query}`, and a trigger type can declare that the parsed body *is* the item so `$json.event` resolves at top level.
- [x] A body that is not valid JSON — form-encoded, plain text, binary — reaches the trigger without being lost or corrupted, and its content type is available.
- [x] The raw bytes never enter a stored execution record or an API response unless the trigger's shape puts them there deliberately.
- [x] A trigger can verify an HMAC over the raw body before the execution is queued, and a failed verification is refused at the HTTP boundary rather than inside the workflow.

## Implementation Plan

Start with capture, because everything else depends on it. `requestPayload` already reads the body through a `LimitReader` into `body []byte` (webhook.go:211-217); that slice is the raw body and it is simply discarded after decoding. Carry it forward rather than only its decoded form.

Then decide where the raw bytes live. Two options: attach them to the trigger item as a base64 field, or keep them out of the item entirely and pass them to a shaping/verification hook that runs at the HTTP boundary. Recommendation: the hook. Putting raw bytes on the item doubles every payload in the executions table, makes redaction's job harder, and exposes the body twice in the API. Model it as a function on the trigger's registration — given the `*http.Request`, the raw bytes and the resolved binding, return the item and an optional refusal — so verification and shaping share the one place that has the bytes.

Dispatch by node type. The handler resolves a `repository.WebhookBinding` that already carries the `NodeID`, and the binding's `Parameters` map already carries the trigger node's configuration; what it does not carry is the node *type*. Add it to the binding row and to `repository.WebhookTrigger`, populated by the extractor in `webhook.Extract` (webhook.go:393-427). That column is needed by the registry-driven binding work in the next phase anyway, so adding it here is not throwaway.

Keep the shapes as named, declarative variants rather than per-node Go functions where possible: `envelope` (today's five keys), `n8nCore` (`{body, headers, params, query}`), and `bodyAsItem`. A generated node pack cannot ship Go code, so a pack-supplied trigger must be able to name a shape rather than implement one.

The traps. `strings.Trim(strings.TrimPrefix(r.URL.Path, "/webhook"), "/")` (webhook.go:62, repeated at 241) is the only path handling there is, so `params` has nothing behind it — n8n's path parameters come from a route pattern the binding does not have; either add pattern support or emit `params` as an empty object and say so in a diagnostic. And `execution.Redact` runs over the payload on the way out of `requestPayload`; the redaction ticket changes what that means, so land these two in a known order rather than both editing the same function blindly.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, entry V2-p1-7, and V2-p3-4 for the WAHA HMAC this unblocks.
- `internal/webhook/webhook.go` — `requestPayload`, `ServeHTTP`, `Extract`.
- `nodes/webhook.go` — `executeWebhook`, the webhook trigger definition.
- `internal/repository/webhooks.go`, `internal/repository/models.go` — `WebhookBinding`, `WebhookTrigger`, `webhookBindingModel`.
- Reference checkout: `/Users/izzadev/projects/mitrachat/n8n/packages/workflow/src/interfaces.ts` — `IWebhookFunctions` (`getBodyData`, `getHeaderData`, `getParamsData`, `getQueryData`).
- Reference package: `/Users/izzadev/projects/mitrachat/mitrachat-orpc-input-fix/packages/n8n-nodes-mitrachat/nodes/MitraChatWebhookTrigger/MitraChatWebhookTrigger.node.ts` — raw-body HMAC verification and the top-level item shape it returns.

## Outcome

### Raw bytes

Captured once at ingest and carried on a `Delivery` rather than on the item, as
recommended. Putting them on the item would double every payload in the
executions table, make redaction's job harder and expose the body twice in the
API — so verification, which is the only thing that needs them, reaches them
where they are, and nothing stores them.

`TestRawBytesNeverReachAStoredRecord` proves both halves at once by sending a
body with incidental whitespace: the value the workflow runs on is in the
record, the exact bytes are not.

### Shaping by trigger type

`webhook.Registry` maps a trigger node type to a `TriggerKind`, populated at
composition exactly like the executor registry, so the HTTP boundary holds no
node-type knowledge of its own. Three named shapes rather than Go functions per
node, because a generated node pack cannot ship Go code and must be able to
*name* the shape it wants:

- `envelope` — today's keys, unchanged, and the default for any unregistered
  type including every binding written before node types were recorded.
- `n8nCore` — `{body, headers, params, query}`, what n8n's own Webhook node
  emits.
- `bodyAsItem` — the parsed body at the top level, so a WAHA workflow's
  `$json.event` resolves instead of being `undefined` at `$json.body.event`.

`NodeType` was added to `WebhookTrigger` and the binding row, populated by the
extractor. The next phase's registry-driven binding work needs that column
anyway, so it is not throwaway.

### params

Emitted as a present, empty object. n8n's path parameters come from a route
pattern, and a binding has no pattern behind it — a route is one opaque segment.
Present-and-empty means an expression reading it gets an empty object rather
than failing, and the reason is recorded where the shape is defined.

### Non-JSON bodies

A body that is not JSON is carried as a string rather than lost, form-encoded
bodies are decoded into an object so a form POST reads like JSON, and the
content type travels on the envelope so a workflow can tell what it received.

### HMAC

`webhook.HMACVerifier` signs over the raw bytes and runs at the HTTP boundary
before the execution is queued, so a request that fails is answered with a 401
and never becomes an execution at all.

The test does not merely check a good signature passes — it also asserts that a
re-marshalled body produces a *different* signature, which is the property that
made this impossible before the bytes were carried and the reason n8n's own
community trigger warns loudly when it has to fall back to re-serialising. A
missing signature is refused rather than passed, because a verifier that accepts
an absent signature verifies nothing.

### One deliberate addition

The envelope gained a `contentType` key. It is additive, so no existing
expression changes meaning, and it is what makes "its content type is available"
true for the five-key shape rather than only for the new ones.
