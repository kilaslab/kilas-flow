---
id: FEAT-hv4q8e
title: Scope webhook paths per tenant and dedupe repeated deliveries
status: todo
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:03:37Z"
updated: "2026-09-05T05:03:37Z"
---

## Scope

`webhookBindingModel` in `internal/repository/models.go:27-42` carries a unique index named `uidx_webhook_bindings_route` spanning `(method, path)` and nothing else. The comment above it states the intent plainly: two active workflows must not claim the same endpoint, whichever tenant owns them. `syncWebhookBindings` (internal/repository/webhooks.go:82-106) turns the resulting constraint violation into `webhook path %q is already claimed by another active workflow`, raised inside the activation transaction.

That rule directly blocks the business goal. An n8n template ships a hardcoded webhook path, so importing the official WAHA chatting template for a second client produces a second workflow with the same path, and activation fails. Replicating one client automation across many customers is the stated reason V2 exists, and one unique index makes it impossible.

The index is not gratuitous. `Resolve` (internal/repository/webhooks.go:57-74) matches on `method` and `path` alone, and the comment there explains why: an inbound webhook has no session, so the path is the only thing identifying it. Scoping the index by tenant without changing what the URL carries would produce two rows that both match one inbound request, and `First(&model)` would pick whichever the database returned — a cross-tenant routing bug far worse than a refused activation.

Separately, nothing dedupes. WAHA retries a failed delivery fifteen times at two-second intervals and identifies each logical delivery with `X-Webhook-Request-Id`. KilasFlow queues an execution per HTTP request (`ServeHTTP` → `QueueWebhook`, webhook.go:95), so a slow workflow that eventually succeeds can be run fifteen times and send fifteen WhatsApp replies.

## Acceptance criteria

- [ ] The same n8n template imported and activated for two different tenants both activate, and each receives only its own deliveries.
- [ ] Two active workflows in the same tenant still cannot claim the same endpoint; the existing activation error is unchanged for that case.
- [ ] An inbound request resolves to exactly one binding with no ambiguity, and a request that names an unknown tenant is answered with the same 404 as an unknown path, revealing nothing about which tenants exist.
- [ ] The import mints a path that cannot collide and reports the resulting URL in the import response, so the user knows what to paste into the sending system.
- [ ] Repeated deliveries carrying the same delivery identifier within a configured window queue exactly one execution; the duplicates are answered without running the workflow again.
- [ ] A delivery with no identifier header is never deduped, and two genuinely distinct deliveries are never collapsed.
- [ ] Already-activated workflows keep working on the URLs they were activated with.

## Implementation Plan

Settle the URL shape first — everything else follows from it. Three options. Keep `/webhook/<path>` and scope the unique index to `(tenant_id, method, path)`, accepting that `Resolve` must learn the tenant from somewhere the request does not currently carry: unworkable without a host- or header-based tenant hint, and a header hint is trivially forgeable. Prefix the tenant into the URL — `/webhook/<tenant>/<path>` — which makes routing unambiguous and leaks the tenant identifier to anyone who sees the URL. Mint an opaque, unguessable path segment per binding at import or activation time, keep the global unique index, and let the imported `path` parameter become a display label rather than the route.

Recommendation: mint an opaque path. It requires no change to `Resolve`, keeps the existing unique index honest, removes the collision class entirely rather than narrowing it, and an unguessable path is a meaningful defence for an endpoint that is often unauthenticated. Store the imported path alongside so the editor can still show the author what n8n called it. Add `tenant_id` to the unique index anyway, as defence in depth against a future minting bug.

Then thread the minted path outward. The import handler (`internal/api/handlers/interop.go:92-125`) currently returns `ImportedWorkflowResource{Workflow, Unsupported}`; it must also return the resolved webhook URLs, which is an OpenAPI change requiring `pnpm generate:api` in `web/` and `pnpm generate:types` in `sdk/`. Without that the user has an activated workflow and no way to learn its address, which is the same failure as not importing it.

Dedupe is a separate, smaller change at the HTTP boundary. Read the delivery identifier from a header the trigger declares — `X-Webhook-Request-Id` for WAHA, and Telegram and others use their own — hash it with the binding, and record it in a small table with a TTL. On a hit, return the same response the first delivery got without queueing. Two traps: the window must be long enough to cover WAHA's full 30-second retry sequence, and the record must be written *before* the execution is queued or two concurrent retries both miss. Use an insert that fails on conflict rather than a read-then-write. Do not dedupe on a body hash — two genuinely identical messages sent twice by a user are not a duplicate delivery.

Migration matters. Existing bindings were activated on their imported paths; changing the route scheme must not silently break them. Either keep serving old-style paths for existing rows, or require reactivation and say so.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, entry V2-p1-8.
- `internal/repository/models.go` — `webhookBindingModel`, `uidx_webhook_bindings_route`.
- `internal/repository/webhooks.go` — `Resolve`, `syncWebhookBindings`, `removeWebhookBindings`.
- `internal/webhook/webhook.go` — `ServeHTTP`, `Extract`.
- `internal/api/handlers/interop.go` — `Import`, `ImportedWorkflowResource`.
