---
id: FEAT-91as16
title: Make webhook bindings registry-driven with trigger lifecycle hooks
status: todo
priority: high
labels:
    - registry
    - metadata
    - editor
deps:
    - FEAT-5kv1jq
    - FEAT-hv4q8e
parent: EPIC-m42s3g
phase: p2
created: "2026-09-05T05:05:08Z"
updated: "2026-09-05T05:05:08Z"
---

## Scope

Exactly one node type in KilasFlow can bind an inbound HTTP path, and it is named at composition. `webhook.Extract(nodeType string, normalizePath func(string) string)` in `internal/webhook/webhook.go` returns a `repository.WebhookExtractor` closure whose first act is `if node.Type != nodeType { continue }`; it then reads `node.Parameters["path"]` and `node.Parameters["httpMethod"]` by those literal keys. It is wired once, in `cmd/kilasflow/main.go`, as `webhook.Extract(nodes.WebhookNodeType, nodes.WebhookPath)`, where `nodes.WebhookNodeType` is the constant `"kilasflow.webhook"`.

So a Telegram Trigger, a WAHA Trigger, or anything a generated pack ships can be registered in the node catalogue, placed on a canvas, saved, and activated — and will never receive a request, because activation extracts no binding for it. There is no error; the workflow is simply active and unreachable. Every trigger p3 delivers depends on fixing this.

The second half is registration with the remote service. n8n gives a trigger three lifecycle methods per webhook — `webhookMethods[name].checkExists | create | delete`, typed as `WebhookSetupMethodNames` — called when a workflow is activated and deactivated. KilasFlow has no equivalent, so a trigger cannot tell a remote service where to deliver. Telegram requires it outright: a bot receives nothing until `setWebhook` is called with a public HTTPS URL, and stops cleanly only on `deleteWebhook`. WAHA's optional auto-registration in p3-4 reuses the same hooks.

Per-tenant path uniqueness and delivery deduplication are a separate ticket in p1 and are deliberately not in scope here; this ticket changes *which nodes produce a binding* and *what happens at activation*, not how the binding is keyed.

## Acceptance criteria

- [ ] Any registered node type may declare a webhook binding in its definition; extraction reads the catalogue and no node type name appears in `internal/webhook` or in the composition call.
- [ ] The path and method are resolved from parameter keys the definition names, so a trigger whose path parameter is not called `path` still binds.
- [ ] A node may declare activate and deactivate lifecycle hooks; activating a workflow runs them for every such trigger and deactivating runs the teardown.
- [ ] Lifecycle hooks run outside the activation database transaction, and a hook that fails leaves the workflow inactive with its bindings removed and a named error returned to the caller.
- [ ] Hooks are idempotent: activating an already-active workflow re-checks rather than re-registers, and a teardown failure is reported without blocking deactivation.
- [ ] A lifecycle hook reaches the network only through `internal/safehttp` and resolves credentials through the same tenant-scoped resolver an executor uses; it can never read a credential outside the workflow's tenant.
- [ ] Existing `kilasflow.webhook` behaviour is unchanged end to end, proven by the existing webhook tests passing without modification to their assertions.

## Implementation Plan

Start with extraction, which is the contained half. `repository.WebhookExtractor` in `internal/repository/webhooks.go` stays exactly as it is — the injection seam is correct and is what keeps node-type knowledge out of persistence. Only the implementation changes: instead of closing over one type name, close over the node registry and, for each node in the document, look up its definition and read the webhook declaration off it. Model the declaration on n8n's `IWebhookDescription`: a name, an HTTP method, the parameter key holding the path, and the response-mode fields. Then update the single call in `cmd/kilasflow/main.go` and move the `nodes.WebhookNodeType` knowledge into `nodes/webhook.go`'s own definition, where it belongs.

Lifecycle hooks are where the trap is. `GORMWorkflowStore.Activate` in `internal/repository/workflows.go` syncs bindings *inside* the activation transaction, deliberately, so there is never a window in which a workflow is active but unroutable; `Deactivate` drops them inside its own transaction for the mirror reason. A remote HTTP call must not go inside either. It can block for seconds against someone else's API, it holds a row lock while it does, and it cannot be rolled back — un-calling `setWebhook` is another network call, not a rollback.

Recommend running hooks in the handler layer, in `internal/api/handlers/workflows.go`'s `Activate` and `Deactivate`, after the store call returns. Activation then reads: commit the transaction, run `create` for every declaring trigger, and on failure call `Deactivate` and return an error naming the trigger and the remote failure. That leaves a brief window in which the workflow is active but the remote service has not been told — which is harmless, because an unregistered webhook simply delivers nothing — whereas the alternative leaves a lock held across an untrusted network call. Deactivation runs `delete` after the commit and logs a failure without failing the request: a user deactivating a workflow must not be blocked by a remote service being down, and a stale remote registration will deliver to a path that no longer resolves.

Idempotence matters because `Activate` may be called on an already-active workflow. That is what `checkExists` is for; keep all three of n8n's methods rather than collapsing to two.

A hook needs a credential and an outbound client, exactly as an executor does. Recommend a `TriggerLifecycle` interface parallel to `engine.Executor`, bound by the same opaque server-owned ID pattern as `ExecutorID`, so the binding test from the source-tagging ticket covers lifecycle bindings too and a trigger declaring a hook that was never registered fails at startup rather than at activation.

Last, a forward-looking decision: p3-1 brings a declarative routing interpreter, and a `setWebhook` call is a single HTTP request with a templated body — expressible as data. Recommend designing the hook interface so a declarative request descriptor is one implementation of it, not a special case beside it, so a generated pack can register a webhook with no hand-written Go. The Go form still has to exist first; Telegram's `secret_token` verification on every delivery is not something a request descriptor can express.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, section "p2 — Node metadata foundation", entry V2-p2-9; and p3-6 for the Telegram trigger that consumes it.
- `internal/webhook/webhook.go` — `Extract` and its single-node-type filter.
- `cmd/kilasflow/main.go` — `WithWebhooks(webhook.Extract(nodes.WebhookNodeType, nodes.WebhookPath))`.
- `nodes/webhook.go` — `WebhookNodeType`, `RespondNodeType`, the response-mode constants.
- `internal/repository/webhooks.go` — `WebhookTrigger`, `WebhookExtractor`, `WebhookBinding`, `syncWebhookBindings`.
- `internal/repository/workflows.go` — `Activate` and `Deactivate` and the transactions the hooks must stay out of.
- `internal/api/handlers/workflows.go` — the `Activate` and `Deactivate` handlers where hooks should run.
- `internal/engine/runner.go` — `Executor` and `Registry`, the binding pattern to mirror.
- n8n 2.34.0 reference (read-only, outside this repo): `packages/workflow/src/interfaces.ts` — `webhookMethods`, `WebhookSetupMethodNames`, `IWebhookDescription`, `INodeTypeDescription.webhooks`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 11 — the collapsible Webhook URLs block a self-registering trigger exposes. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
