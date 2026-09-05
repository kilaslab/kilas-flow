---
id: FEAT-5kv1jq
title: Shape webhook trigger payloads per node type and keep the raw body
status: todo
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

- [ ] The exact request bytes are captured at ingest and remain available to the trigger node that consumes them, byte-for-byte identical to what the client sent.
- [ ] The item shape a webhook delivery produces is determined by the bound trigger node's type, not hardcoded in the HTTP handler.
- [ ] The existing `kilasflow.webhook` trigger keeps producing `{method, path, headers, query, body}` so no already-activated workflow changes behaviour.
- [ ] A trigger type can declare an n8n-compatible shape and receive `{body, headers, params, query}`, and a trigger type can declare that the parsed body *is* the item so `$json.event` resolves at top level.
- [ ] A body that is not valid JSON — form-encoded, plain text, binary — reaches the trigger without being lost or corrupted, and its content type is available.
- [ ] The raw bytes never enter a stored execution record or an API response unless the trigger's shape puts them there deliberately.
- [ ] A trigger can verify an HMAC over the raw body before the execution is queued, and a failed verification is refused at the HTTP boundary rather than inside the workflow.

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
