---
id: FEAT-ddzk2k
title: Authenticate the main API and resolve real tenants
status: todo
priority: high
labels:
    - platform
    - longtail
deps:
    - FEAT-gvn62x
parent: EPIC-m42s3g
phase: p8
created: "2026-09-05T05:10:22Z"
updated: "2026-09-05T05:10:22Z"
---

## Scope

The main API has no authentication and no tenancy. `internal/api/handlers/workflows.go` declares a `TenantResolver` interface and a `defaultTenantResolver` whose `Resolve` returns `repository.TenantScope{ID: repository.DefaultTenantID}` — the constant `"default"` defined in `internal/repository/workflows.go`. `cmd/kilasflow/main.go` never sets `api.Deps.Tenants` in its `api.NewServer(api.Deps{…})` call, and every handler constructor (`NewWorkflows`, `NewExecutions`, `NewCredentials`, `NewSchedules`, `NewEmbedSessions`, `NewInterop`) substitutes `defaultTenantResolver{}` when the field is nil. Every request on `/api/v1` therefore resolves to tenant `default`, and nothing checks who sent it. The only authentication in the tree is `middleware.EmbedAuth`, mounted globally in `internal/api/server.go`, and its documented behaviour is that "a request without a token passes through untouched".

The consequence is that anyone who can reach the port owns the installation. `POST /api/v1/embed-sessions` mints a workflow-scoped token for any workflow without presenting a credential of its own. `GET /api/v1/credentials` lists every credential in the deployment. `POST /api/v1/workflows/{id}/run` executes any workflow with that tenant's decrypted secrets. The embed boundary V1 built confines a token that leaked out of an iframe, but it guards only the inner surface — it never asked the outer caller for identity.

The tenancy the schema already carries is also fiction. `internal/repository/models.go` puts `tenant_id` on all seven models and every repository method takes a `TenantScope`, exactly as PRD §56 asks. But `internal/api/handlers/embed.go` mints sessions with `TenantID: tenant.ID`, which is always `"default"`, so a white-label host embedding KilasFlow for many customers is running all of them in one tenant. Fixing the plumbing without fixing the resolver would not have helped: the seam was built correctly and simply never had a principal to read.

This ticket introduces that principal. Two authentication paths share one identity model: a tenant-scoped API key for machine callers (a host backend, the SDK, CI) and a browser session for the dashboard. Both resolve to a tenant, and the resolved tenant flows through the existing `TenantResolver` seam, so no repository call site changes. RBAC, SSO and a permission matrix stay out — PRD §64 defers all three. The goal here is that a request has an owner and a tenant.

**Amendment for p10.** p9 already promotes this ticket because a host-driven Datastore API over one shared `default` tenant is a cross-customer data pool. p10 adds two more consumers, and both are blocked on it rather than merely improved by it. V2-p10-6 (`FEAT-jq84xk`) makes `@kilasflow/sdk` usable as a multi-tenant host credential — per-tenant client construction, rotation, and the client half of the stream-ticket handshake this ticket's plan chooses, which `sdk/src/browser.ts` and `web/src/lib/workflow-editor/event-stream.svelte.ts` both need and which this ticket's plan describes only server-side. V2-p10-13 (`FEAT-frvez8`) is the multi-tenant embedding guide and its two-tenant reference application, which cannot demonstrate anything while every request resolves to `"default"`. Publishing an SDK before this lands would ship a cross-customer data pool as its documented happy path, so V2-p10-8 sequences behind it in substance even where it does not in form.

## Acceptance criteria

- [ ] An unauthenticated request to any `/api/v1` operation other than the documented public ones (health, the OpenAPI document, the docs page) is refused with 401, and the refusal names no tenant, workflow, or user.
- [ ] Every `/api/v1` handler resolves its `repository.TenantScope` from the authenticated principal; with authentication enabled, `defaultTenantResolver` decides tenancy for no request.
- [ ] A tenant-scoped API key authenticates a machine caller. Keys are stored hashed, returned in full exactly once at creation, revocable, and never returned by any listing.
- [ ] `POST /api/v1/embed-sessions` requires an authenticated principal, and the session it mints carries that principal's tenant instead of `default`.
- [ ] A request carrying both an embed token and an API key is served with the embed session's narrower authority, never the key's.
- [ ] Two tenants sharing one deployment cannot reach each other's workflows, credentials, executions, schedules, or event streams, proven by a test that drives both through the HTTP surface.
- [ ] The execution event stream authenticates without request headers, so the existing `EventSource` clients in `web/src/lib/workflow-editor/event-stream.svelte.ts` and `sdk/src/browser.ts` keep working.
- [ ] Upgrading an existing installation leaves its data reachable: the rows already written under tenant `default` belong to a real tenant record with that ID.

## Implementation Plan

Start with storage. Add `tenants`, `users` and `api_keys` models to `internal/repository/models.go` and to `Models()`. An API key row holds the tenant, a lookup prefix, the hash of the secret, a label, `last_used_at` and `revoked_at`. Hash with SHA-256 rather than bcrypt: the secret is high-entropy random bytes we generate, not a human password, and a password-hashing KDF on the hot path of every request buys nothing and costs milliseconds per call.

Then `internal/auth`: key minting and verification, browser-session issue and verification, and the principal type. Add a koanf `auth` config section — one word, because `envKeyToPath` treats the first underscore as the section separator, which is the same rule that forced `outbound` to be one word rather than `outbound_http`. Carry `enabled`, `signing_key_env`, `session_ttl`, and a bootstrap path for the first tenant and user.

Then the middleware and the resolver. Mount authentication ahead of `middleware.EmbedAuth` and store the resolved principal in the request context; implement a real `handlers.TenantResolver` that reads it, and wire it into `api.Deps.Tenants` in `cmd/kilasflow/main.go`. Add the key-management, login, logout and `GET /me` handlers to `internal/api/routes.go`. Finish with `pnpm generate:api` in `web/` and `pnpm generate:types` in `sdk/` — `generate:api:check` and the SDK's `check-types` run in CI and fail on drift — then the SPA login route with a 401 redirect, and an `apiKey` convenience on the SDK's `TransportOptions` beside the existing `headers` escape hatch, which already documents itself as "where a host puts its API key" for a server that never read one.

Three traps.

The router is flat. `internal/api/routes.go` mounts the webhook prefix with `router.Handle(WebhookPrefix, …)` and the SPA with `router.Handle("/*", web.Handler())` on the same `chi.Mux` that carries `router.Use(middleware.EmbedAuth(…))`. A globally mounted `router.Use` for authentication would gate the public trigger surface and the SPA's own static assets along with the API. Scope the new middleware to the `/api/v1` prefix explicitly, and add a test that a webhook delivery still succeeds with no credential.

The two layers must compose rather than stack. `EmbedAuth` deliberately lets a token-less request through, and `permits()` in `internal/api/middleware/embed.go` is the authority for a request that does carry a token. The new middleware must recognise a `kfe1.`-prefixed bearer or an `X-KilasFlow-Embed` header as an embed request and defer to that path instead of demanding an API key as well; a request presenting both must end up with the embed session's authority.

`EventSource` cannot set headers, and both clients open `/api/v1/executions/{id}/events` with a bare constructor. Three options: a cookie, which works for the dashboard and breaks for a cross-origin embed; a token in the query string, which lands in every proxy access log; or a short-lived stream ticket minted by an authenticated POST and spent on connect. Take the ticket: scope it to one execution, give it seconds of life and single use, and a URL recovered from a log is worthless.

## References

- Roadmap plan, p8 section, entry V2-p8-1: `.pine/roadmap.md`.
- PRD: `gflow-prd-v1.md` §56 Multi-Tenancy, §57 Security Requirements, §39 Embed Authentication, §64 (RBAC and enterprise SSO deferred).
- Code: `internal/api/handlers/workflows.go` (`TenantResolver`, `defaultTenantResolver`), `internal/repository/workflows.go` (`DefaultTenantID`), `internal/api/server.go` (`Deps.Tenants`, the global `EmbedAuth` mount), `internal/api/routes.go`, `internal/api/middleware/embed.go`, `internal/api/handlers/embed.go`, `cmd/kilasflow/main.go`, `internal/config/config.go` (the one-word section rule), `sdk/src/http.ts`, `sdk/src/browser.ts`, `web/src/lib/api/http.ts`, `web/src/lib/workflow-editor/event-stream.svelte.ts`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 17 — the settings sidebar showing Users, Roles, SSO and LDAP as the surface this ticket is the precondition for. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
- `.pine/tickets/FEAT-jq84xk.md` — V2-p10-6, which implements the client half of the stream-ticket handshake this ticket's plan chooses.
- `.pine/tickets/FEAT-frvez8.md` — V2-p10-13, the multi-tenant embedding guide that cannot be written until this lands.
