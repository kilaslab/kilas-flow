---
id: FEAT-jq84xk
title: Make the SDK usable as a multi-tenant host credential
status: todo
priority: high
labels:
    - sdk
    - api
    - platform
deps:
    - FEAT-ddzk2k
    - FEAT-yx0qt6
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:50:02Z"
updated: "2026-09-05T11:50:02Z"
---

## Scope

This ticket is deliberately narrow, because `FEAT-ddzk2k` (V2-p8-1) already owns the authentication mechanism and names one SDK change in its implementation plan: "an `apiKey` convenience on the SDK's `TransportOptions` beside the existing `headers` escape hatch". That single field is not what a multi-tenant host needs, and the rest is unowned.

Three things are missing once API keys exist.

**Per-tenant client construction has no shape.** `Transport` is constructed once with a fixed `baseUrl`, a fixed `headers` map and a fixed `fetch`, and `KilasFlowClient` holds one. A SaaS serving many customers against one KilasFlow installation holds one key per tenant and must build one client per tenant per request, or hold a cache of them. Neither pattern is expressed, tested or documented, so every host invents its own and some of them will invent a shared mutable client whose key is swapped between requests — the exact bug that leaks one customer's data to another.

**The event stream is going to break, and the ticket that breaks it does not fix the client.** `sdk/src/browser.ts` opens `new EventSource(...)` against `/api/v1/executions/{id}/events`, and `EventSource` cannot set request headers. `FEAT-ddzk2k` sees this — its seventh acceptance criterion requires the existing clients to keep working, and its plan chooses a short-lived single-use stream ticket minted by an authenticated POST — but everything it describes is server-side. The client half of that handshake, in both `sdk/src/browser.ts` and the dashboard's `web/src/lib/workflow-editor/event-stream.svelte.ts`, is written nowhere.

**Rotation is undefined.** A key that can be revoked is a key that must be rotated, and a host doing so against a long-lived client has no defined behaviour: no way to swap a credential without discarding in-flight requests, and no defined result for a request that was authorized when it started and revoked before it finished.

The one thing not to change is the transport's refusal to have an opinion. `sdk/src/http.ts` documents `headers` as the escape hatch because a host's credential may come from a vault, a signed proxy or an ambient identity, and "inventing one shape would force the others to work around it". The `apiKey` option is a convenience layered on that, never a replacement for it.

## Acceptance criteria

- [ ] Constructing one client per tenant is the documented pattern, with a stated, tested rule that a client's credential is fixed for its lifetime and never mutated in place.
- [ ] A test proves that two clients built with two tenants' keys cannot observe each other's workflows, executions, credentials or schedules through the SDK, exercised over HTTP against a real server.
- [ ] `subscribeExecutionEvents` works against an authenticated server: it performs the stream-ticket handshake `FEAT-ddzk2k` defines, spends the ticket on connect, and its existing auto-close on terminal events is unchanged.
- [ ] Reconnection after a dropped stream mints a fresh ticket rather than replaying a spent one, and resumes from `Last-Event-ID` so no execution event is lost across the reconnect.
- [ ] Credential rotation is defined and documented: how a host swaps a key, what happens to in-flight requests, and what a caller observes when a key is revoked mid-flight.
- [ ] A 401 and a 403 are distinguishable by a caller through `KilasFlowError` without parsing message text, so a host can tell "your key is wrong" from "your key is fine and this is not yours".
- [ ] The `headers` escape hatch remains supported and documented as supported, not as a transitional path superseded by `apiKey`.
- [ ] No credential is read from the process environment or any ambient source by any code path in this package.

## Implementation Plan

Wait for `FEAT-ddzk2k` to settle the wire format before writing anything here; the shape of a key, the header it travels in and the exact ticket-minting endpoint are all its decisions, and guessing them produces a client that must be rewritten.

For per-tenant construction, recommend a small factory over a mutable client — something that takes the base URL and shared options once and returns a fixed-credential client per tenant. The reason to prefer a factory is that it makes the dangerous pattern unrepresentable: a client whose key can be reassigned is a client that will be reassigned by a concurrent request handler, and no amount of documentation prevents that.

The stream-ticket handshake is the substantial piece of work and it lands in two places that must behave identically. `sdk/src/browser.ts` runs in a page with no API key at all — it holds an embed token — while `web/src/lib/workflow-editor/event-stream.svelte.ts` runs in the dashboard. Both open a bare `EventSource` today. Write the handshake once and use it in both, or the two will drift and only one will be tested.

Two traps in that handshake worth stating now. A ticket minted before a long pause may expire before it is spent, so the mint must be adjacent to the connect rather than at component setup. And on reconnect, the natural implementation reuses the last ticket, which is single-use by design and will fail on the second attempt in a way that looks like a server bug — mint per connect, always.

Rotation should be defined as the boring answer rather than an elegant one: build a new client with the new key, direct new work at it, let in-flight requests on the old client finish or fail on their own. Do not add a credential-refresh callback. A callback invites a host to hold one long-lived client across a rotation, which is the shared-mutable-client bug wearing a different hat.

One question this ticket must answer explicitly rather than leave to a reader: whether a host is expected to hold one API key per tenant, or one operator key it uses to act on behalf of tenants. `FEAT-ddzk2k` describes a tenant-scoped key and no impersonation mechanism, so the answer is one key per tenant — state it, because the other model is what most SaaS integrators will assume and there is nothing on the server to support it.

## References

- Roadmap plan, p10 section, entry V2-p10-6: `.pine/roadmap.md`.
- `.pine/tickets/FEAT-ddzk2k.md` — V2-p8-1, which owns the mechanism, the `apiKey` field, and the stream-ticket decision this ticket implements client-side.
- `sdk/src/http.ts` — `TransportOptions`, the `headers` escape hatch and its rationale, `KilasFlowError` and `ProblemDetail`.
- `sdk/src/server.ts` — `KilasFlowClient` and its single fixed `Transport`.
- `sdk/src/browser.ts` — `subscribeExecutionEvents`, the bare `EventSource` construction, the nine event names and the auto-close on terminal events.
- `web/src/lib/workflow-editor/event-stream.svelte.ts` — the dashboard's `EventSource` client, which needs the same handshake.
- `internal/api/handlers/executions.go` — the SSE registration, `Last-Event-ID` resume and the `ownsExecution` check that answers 404 rather than 403.
- `internal/api/middleware/embed.go` — the embed path that must keep working unchanged alongside API-key authentication.
