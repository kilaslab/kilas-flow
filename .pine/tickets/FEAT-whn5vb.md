---
id: FEAT-whn5vb
title: Serve dynamic property options from the server
status: todo
priority: high
labels:
    - registry
    - metadata
    - editor
deps:
    - FEAT-5s1w0t
parent: EPIC-m42s3g
phase: p2
created: "2026-09-05T05:02:02Z"
updated: "2026-09-05T05:02:02Z"
---

## Scope

Every selectable value in KilasFlow is a compile-time constant. `PropertyOption{Label, Value}` lists are written into the definition in `nodes/*.go` and returned unchanged by `/api/v1/node-types`. That works for an HTTP method or a merge mode. It does not work for anything whose valid values live on the customer's own service: a WAHA node cannot offer "pick one of your sessions", and a chat model node cannot offer "pick one of the models your OpenRouter key can reach" — `nodes/ai.go` currently makes `model` a free-text string defaulting to `gpt-4o-mini`, which means a typo is indistinguishable from a valid model until the run fails.

Add `POST /api/v1/node-types/{type}/load-options`, evaluated against a partially configured node the editor sends up. The node catalogue handler in `internal/api/handlers/nodes.go` registers exactly one operation today (`list-node-types`, `GET /node-types`); this is the second.

The loader stays **declarative**. n8n's dominant form is `typeOptions.loadOptionsMethod`, which names a JavaScript function on the node class — that is exactly the thing KilasFlow does not have and does not want. n8n's other form, `typeOptions.loadOptions.routing`, is a request descriptor, and that is the one to copy: `{endpoint, method, itemsPath, labelTemplate, valueField}` plus a `dependsOn` list of parameter keys. A loader is data, so it cannot execute anything, and it flows through `internal/safehttp` and the credential store like any other outbound call — SSRF policy and the credential's own `AllowedDomains` scope still apply. A JavaScript sidecar could not have preserved either.

## Acceptance criteria

- [ ] A property may declare a declarative options loader; `/api/v1/node-types` returns it, and the panel fetches options instead of rendering an empty select.
- [ ] `POST /api/v1/node-types/{type}/load-options` accepts a node type, version, the property key and the node's partially configured parameters, and returns a list of `{label, value}`.
- [ ] The request body is validated against the registered definition before any URL is built: an unknown type, unknown property, or property with no loader is refused, and parameter values are never concatenated into a URL unescaped.
- [ ] Every outbound request goes through `internal/safehttp` with the instance's configured policy and, where a credential is used, that credential's `AllowedDomains`; a loader aimed at a disallowed host fails with the same named error an HTTP node would produce.
- [ ] Credentials are referenced by ID and resolved under the caller's tenant scope; the endpoint never accepts inline credential fields and never returns a secret in a label, value or error.
- [ ] A parameter the loader depends on that holds an expression yields an empty list with a stated reason rather than a guessed or partial URL.
- [ ] Results are cached per tenant, node type, credential and dependency values for a bounded TTL, so reopening a dropdown does not re-hit the customer's service.
- [ ] An embed session may load options for its own workflow and cannot reach a credential outside its session's tenant.

## Implementation Plan

Model the loader in `internal/node` next to the property kinds, then implement the endpoint in `internal/api/handlers/nodes.go` and register it on the v1 group in `internal/api/routes.go`. The execution half — build request, apply credential, call, walk `itemsPath`, render `labelTemplate`, read `valueField` — is the same shape p3-1's declarative routing interpreter will need, so put it somewhere both can use rather than inside the handler. `internal/safehttp.NewClient(policy)` and `credentials.Apply` are the existing seams.

The trap is trust. The body carries an *unsaved, partially configured* node straight from a browser. It is untrusted input in the strongest sense: it names a node type, a property and a parameter map, all attacker-controlled, and the server then makes an outbound HTTP request shaped by them. Three rules follow. Validate the node type and version against the registry and take the loader from the *registered definition*, never from the request. Treat every parameter value as data — URL-escape into path and query, never string-concatenate. And resolve the credential by ID under the caller's tenant, so a request naming another tenant's credential resolves to nothing rather than to a secret.

Expressions are the second trap. A dependency parameter may hold `{"mode":"expression","value":"…"}`, and there is no item to evaluate it against at edit time — there is no execution. Recommend refusing: return an empty list plus a stated reason ("depends on an expression, which cannot be resolved while editing"), which the panel shows in place of the dropdown and which leaves the field free-text. The alternatives are worse: guessing a value produces a wrong list, and evaluating against an empty item produces a confidently wrong one.

Caching needs a decision. Recommend a small in-process TTL cache keyed by `(tenant, node type, version, property, credential ID, dependency values)` with a short expiry, plus `Cache-Control` on the response so the browser does not refetch on every focus. A dropdown that fires a request to the customer's WAHA box on every open is the kind of thing that gets noticed in production and not in review. Rate-limit the endpoint per tenant for the same reason.

`dependsOn` is what tells the panel to refetch: when a listed parameter changes, the cached list for that property is discarded. Without it a user changes `resource` and keeps the previous resource's operations.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, section "p2 — Node metadata foundation", entry V2-p2-4.
- `internal/api/handlers/nodes.go`, `internal/api/routes.go` — where the operation is added.
- `internal/safehttp/safehttp.go` — `Policy`, `NewClient`.
- `internal/credentials/credentials.go` — `Record.AllowsHost`, `Apply`.
- `nodes/ai.go` — the free-text `model` parameter this endpoint replaces.
- `internal/api/handlers/embed.go` — the embed session boundary the endpoint must respect.
- n8n 2.34.0 reference (read-only, outside this repo): `packages/workflow/src/interfaces.ts` — `INodePropertyTypeOptions.loadOptions` / `loadOptionsMethod` / `loadOptionsDependsOn`, and `ILoadOptions`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 05 — the Model dropdown is populated by loadOptions against the configured credential. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
