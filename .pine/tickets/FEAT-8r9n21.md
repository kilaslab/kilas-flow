---
id: FEAT-8r9n21
title: Interpret declarative node routing in Go
status: todo
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-5s1w0t
    - FEAT-pd3p6x
    - FEAT-2f68r8
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T04:59:38Z"
updated: "2026-09-05T04:59:38Z"
---

## Scope

n8n has two ways to write a node. A programmatic node ships an `execute()` function; a declarative node ships only metadata — `requestDefaults` on the description, and a `routing` object on each property that says how that property becomes part of an HTTP request. `@devlikeapro/n8n-nodes-waha`'s action node is entirely the second kind: it has no `execute()` at all, just 124 OpenAPI-derived operations of `routing: {request: {…}}`. That is the single fact that makes WAHA reachable without a JavaScript runtime, and this ticket is the interpreter that cashes it in.

KilasFlow has no such interpreter today. Every outbound call is hand-written Go against fixed parameter keys: `nodes/http.go`'s `HTTPExecutor` reads `method`, `url`, `sendQuery`, `queryParameters`, `sendHeaders`, `headers`, `sendBody`, `bodyType`, `body`, `responseFormat`, `neverError` and `timeoutSeconds`, and nothing else in the process can describe a request from data. Adding a node therefore means adding Go, which does not scale to 124 operations and does not scale at all to a generated pack.

Implementing the routing model in Go is also what preserves the security posture a JavaScript sidecar would have thrown away. Every request this interpreter makes goes through `internal/safehttp` — `safehttp.NewClient(policy)` re-checks the resolved IP at dial time and on every redirect hop, and `Policy.ReadBody` bounds the response — and through the same credential path `HTTPExecutor.authenticate` uses: resolve via `engine.Request.Credentials.ResolveCredential`, check the declared credential type against `resolved.Type`, check the target host against `credentials.Record{AllowedDomains}.AllowsHost`, then `credentials.Apply`. Third-party JavaScript executing its own `fetch` could not have been held to any of that.

The subset to implement is the subset the generated packs and the Telegram node actually emit: `requestDefaults` (baseURL, headers, `url` suffix), `routing.request` (method, url, qs, body, headers), `routing.send` (property placement into body or query, dot notation, `type`, `value`), `routing.output.postReceive` (`rootProperty`, `setKeyValue`, `limit`) and offset pagination. n8n's `preSend`/`postReceive` function forms are JavaScript closures and have no representation here; a pack that needs one must fail at registration rather than at run time.

## Acceptance criteria

- [ ] A node definition can carry request defaults and per-property routing metadata, and one shared executor runs any node so described with no node-specific Go code.
- [ ] `routing.request` supports method, URL (including a baseURL from `requestDefaults` and placeholders resolved from the node's own parameters), query string, headers and body; `routing.send` places a parameter into body or query, honouring `property` in dot notation, `type` and a literal `value`.
- [ ] `postReceive` supports `rootProperty`, `setKeyValue` and `limit`, and offset pagination walks pages until the page is short or a bound is reached; a `postReceive` or pagination type the interpreter does not implement is rejected when the pack is registered, never ignored at run time.
- [ ] Every request the interpreter makes passes the `safehttp` policy: a routed node aimed at a loopback or private address fails with the policy's own error, proven by test.
- [ ] A routed node authenticates only with a credential of the type its definition declares, and only when the target host is inside that credential's `AllowedDomains`; both refusals are proven by test.
- [ ] A failing routed call reports the node name, the resource and operation values in effect, and the HTTP status, so the error names what a user can actually change.
- [ ] Response items are produced deterministically: one item per element of the extracted root property, or one item for a non-array response, with binary responses deferred to the binary-store ticket rather than silently discarded.

## Implementation Plan

Put the model in a new package — `internal/routing` — holding the metadata types and the interpreter, and add a `RoutingExecutorID` registered in `nodes/executors.go`'s `RegisterExecutors` map alongside `HTTPExecutorID`. Keep the metadata types plain data with JSON tags: a generated pack is data on disk, and the same types must survive a round trip through it. Do not put routing metadata on `node.Definition` itself yet if that forces an API change you do not want in this ticket — the executor can read it from a pack-side table keyed by `{type, version}` — but decide once and write the decision into the package doc.

Build the interpreter in the order a request is built: resolve the effective request (defaults merged with `routing.request`), then apply every property's `routing.send`, then execute through `safehttp`, then run `postReceive` over the decoded response, then paginate. Lift the credential block out of `nodes/http.go:299` (`HTTPExecutor.authenticate`) into a shared helper both executors call, rather than copying it — a second copy of that check is a second place for the host-scope test to be forgotten.

The trap is the expression dialect. n8n's routing metadata is full of strings like `={{ $parameter["chatId"] }}` and `={{ $credentials.baseUrl }}`; KilasFlow marks an expression explicitly as `{"mode":"expression","value":"…"}` and its grammar (`internal/expression`) is data access only, over the roots `$json`, `$input`, `$node`, `$env`, `$execution` and `$itemIndex` — there is no `$parameter` and no `$credentials`. Two options: teach the interpreter a second, private evaluator for routing templates, or translate routing strings into KilasFlow's marker at pack-generation time and add `$parameter` and `$credentials` roots to `expression.Context`. Take the second. A second evaluator is a second attack surface over tenant-authored data and would drift from the first within a release; adding two roots to the one evaluator keeps the "parameters can never become code" property that `TestEvaluateRejectsAnythingThatIsNotDataAccess` guards. `$credentials` must expose non-secret fields only — a base URL, never a token — and the test that proves it belongs in this ticket.

The second trap is quieter: a declarative node runs once per item, so an operation with pagination produces items per input item, and the interpreter must keep the two loops distinct or a two-item input silently doubles a paginated list.

## References

- Roadmap plan, p3 section, entry V2-p3-1: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- n8n 2.34.0 reference checkout, `packages/workflow/src/Interfaces.ts`: `INodePropertyRouting`, `INodeRequestSend`, `INodeRequestOutput`, `PostReceiveAction` and its `IPostReceiveRootProperty` / `IPostReceiveSetKeyValue` / `IPostReceiveLimit` variants, `IN8nRequestOperations` with `IN8nRequestOperationPaginationOffset`, and `requestDefaults` on the node description.
- `nodes/http.go` — the hand-written executor this generalises, and `authenticate` at line 299 for the credential and host-scope path.
- `internal/safehttp/safehttp.go` — `Policy`, `NewClient`, `CheckURL`, `CheckAddress`, `ReadBody`.
- `internal/expression/expression.go` — `Context` and the roots it currently exposes.
