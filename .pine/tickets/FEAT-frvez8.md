---
id: FEAT-frvez8
title: Write the multi-tenant embedding guide and a reference host app
status: todo
priority: high
labels:
    - docs
    - sdk
deps:
    - FEAT-3taswf
    - FEAT-5mvech
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:55:31Z"
updated: "2026-09-05T11:55:31Z"
---

## Scope

The epic's stated ICP is white-label, multi-tenant and embedded — KilasFlow as the workflow and datastore engine inside somebody else's SaaS. Every mechanism that serves that story exists and there is no document that assembles them into one.

What exists, scattered: `POST /api/v1/embed-sessions` mints a `kfe1.` token; `sdk/src/browser.ts` mounts an iframe at `/embed/{workflowId}` with `sandbox="allow-scripts allow-same-origin allow-forms"` and performs a `kilasflow:embed-ready` → `kilasflow:embed-session` handshake against an exact origin with a fifteen-second timeout; `subscribeExecutionEvents` opens an SSE stream and closes itself on terminal events; `Branding` carries a validated name, https-only logo, accent colour and `hideRun`/`hideSave`; `permits()` bounds what the session may reach; `sdk/examples/host-page` demonstrates the backend-mints, page-mounts sequence and runs.

What does not exist is the reasoning a host integrator needs before any of that is useful:

- **How a host's tenant maps onto a KilasFlow tenant.** `repository.TenantScope` is one field and today every request resolves to `"default"`. Once `FEAT-ddzk2k` lands, the mapping is the single most consequential decision an integrator makes and there is nothing telling them how to make it.
- **What the embed token is not.** It is a fifteen-minute, one-workflow, one-origin capability, deliberately unable to activate, delete or import. An integrator who assumes it is a session cookie will build the wrong architecture and discover it late.
- **Which calls belong on the backend.** Workflow creation, credential provisioning, activation and datastore management are all API-key operations that `permits()` refuses to an embed session — by design, and nowhere stated as a design.
- **What branding can and cannot do.** It is a validated value set, never markup, because the editor renders inside a customer's page.
- **The full request path**, end to end, for one real feature.

`sdk/examples/host-page` is the right foundation and is scoped as a handshake demonstration: it mints a session and mounts an editor. It does not provision a credential, activate a workflow, receive a webhook, write a datastore row, or show two tenants coexisting — which is the whole product.

## Acceptance criteria

- [ ] A guide walks one complete feature end to end: a host tenant is created, a workflow is provisioned server-side, a credential is stored, the editor is embedded for an end user, the workflow is activated, a webhook fires it, and the host observes the execution.
- [ ] The host-tenant to KilasFlow-tenant mapping is documented as a decision with its options and consequences, not as a single recommended snippet.
- [ ] The embed token's limits are stated plainly — one workflow, one exact origin, minutes not hours, no activate, no delete, no import — with the reason for each.
- [ ] The split between backend API-key calls and browser embed-token calls is documented as a table an integrator can check a design against.
- [ ] Branding is documented as validated values with the reason markup is refused, and the list of what is themeable is accurate against `embed.Branding`.
- [ ] The reference host app runs against a published image and a published package, with no checkout of this repository, and demonstrates **two tenants coexisting** rather than one.
- [ ] The datastore path is included: a host provisions a datastore, a workflow writes to it, and the host reads it back through the SDK.
- [ ] Security guidance is explicit about the two failure modes that matter — never mint a session for a workflow the requesting user does not own, and never send a token to an origin the host did not verify — and the reference app demonstrates the correct pattern for both.

## Implementation Plan

Extend `sdk/examples/host-page` rather than starting a second example. It already has the parts that are tedious to rebuild — a session-minting backend, a mounting page, an event subscriber — and its README is accurate. Growing it into a two-tenant reference is less work and leaves one example rather than two that disagree.

Write the guide against the reference app so every snippet is copied from code that runs. Prose examples in an integration guide rot faster than anything else in a documentation set, because they are the part nothing executes.

Sequence this after V2-p10-8 for a real reason, not for tidiness: a guide whose first instruction is "clone this repository and build the SDK" is not an integration guide, and the two-tenant demonstration needs `FEAT-ddzk2k`'s API keys to be anything other than a fiction, since every request resolves to `"default"` until it lands.

The mapping decision deserves the most care, because it is the one that is expensive to change later. Present the options honestly: one KilasFlow tenant per host customer, which is the model `FEAT-ddzk2k` builds and the one the isolation tests prove; or one shared tenant with host-side partitioning, which is simpler on day one and gives up every isolation guarantee the product offers. Recommend the first, and say what the second actually costs — cross-customer visibility of workflows, credentials, executions and datastores — rather than leaving it as a trade-off the reader is invited to weigh.

Two traps to document because both fail silently. `embed.allowed_origins` is exact `scheme://host[:port]` matching with no wildcards and no suffix matching, and an empty list disables embedding entirely; an integrator whose iframe never loads will look everywhere except at a config key they never knew existed. And the origin is re-checked on **every** request, not only at mint, so a host serving the same app from two domains needs both listed.

One thing to state that the SDK cannot enforce: minting a session is an authorization decision the host owns. `EmbedSessions.Create` verifies the workflow exists in the tenant; it cannot know whether the end user in front of the browser is entitled to it. That check belongs in the host's own handler, and the guide should show it in the reference app rather than mention it in a warning box.

## References

- Roadmap plan, p10 section, entry V2-p10-13: `.pine/roadmap.md`.
- `sdk/examples/host-page/server.mjs`, `index.html`, `README.md` — the foundation to extend.
- `sdk/src/browser.ts` — `mountWorkflowEditor`, the sandbox attributes, the `kilasflow:embed-ready` handshake and its timeout, and `subscribeExecutionEvents`.
- `sdk/src/server.ts` — `createEmbedSession` and the backend surface.
- `internal/embed/embed.go` — `Session`, `Scope`, `Allows`, `DefaultLifetime`, `MaxLifetime`, `OriginAllowed`, `Branding` and its validation.
- `internal/api/handlers/embed.go` — `EmbedSessions.Create` and the workflow-exists check that is not an authorization check.
- `internal/api/middleware/embed.go` — `permits()` and the per-request origin re-check.
- `internal/config/config.go` — `Embed.AllowedOrigins` and its fail-closed behaviour.
- `internal/repository/workflows.go` — `TenantScope` and `DefaultTenantID`.
- `.pine/tickets/FEAT-ddzk2k.md` — V2-p8-1, which makes the multi-tenant story real.
