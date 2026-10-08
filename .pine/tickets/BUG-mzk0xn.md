---
id: BUG-mzk0xn
title: Consistent publish and activate authority for embeds
status: done
priority: medium
labels:
    - saas
    - embedding
    - authz
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-10-08T16:09:31Z"
---

# Description

- Activation is refused to every embed session (`internal/api/middleware/scope.go:242-245`). That is a sound default, but a white-label host has no way to opt in.
- Publishing a version is allowed to embeds holding `workflow:write`: the refusal at `scope.go:246-250` applies only to API keys, and `embedscope.go:80-85` confirms it.
- Meanwhile the frontend hides publish, waiting for a `workflow:publish` scope the server never mints (`web/src/lib/embed/session.svelte.ts:70-86`).

# Acceptance Criteria
- [x] The server mints `workflow:publish` and, optionally, `workflow:activate`.
- [x] Publishing from an embed requires `workflow:publish`.
- [x] Activating with the scope reuses the compile and `node.not_available` checks.
- [x] Server, UI and docs agree on the three levels.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid, refs accurate (scope.go:242-250, internal/embed/embed.go:33-35, `NormalizeScopes` :466, web/src/lib/embed/session.svelte.ts:70-86, embed-editor.svelte:36).
- Also fix the false comment at embedscope.go:59-61 ("activation and publishing are refused to embed sessions by the routing middleware") — publish is not refused; :76-84 contradicts it.
- guides/embedding.md:271 says nothing about publish; update with the three levels.

## Fix

Three levels, each its own scope, none implying another; publish and activate both imply `workflow:read`.

- `internal/embed/embed.go`: `ScopePublish` (`workflow:publish`) and `ScopeActivate` (`workflow:activate`) are in `NormalizeScopes`; `Session.Allows` reads them as implying read (`impliesRead`). New `NormalizeKeyScopes` refuses both by name ("is for embed sessions only"), used by the key mint handler, `CreateScopedAPIKey` and `parseScopes`, so a scoped API key keeps its old vocabulary.
- `internal/api/middleware/scope.go`: publish (`.../versions/{id}/publish`) needs `workflow:publish` for an embed ("This embed session cannot publish a revision without the workflow:publish scope."); activate/deactivate need `workflow:activate` (sentence unchanged: "cannot change activation"). Scoped keys are refused both whatever they hold, as before.
- `internal/api/handlers/workflows.go` `Activate`: runs `confinedStoredProblem` first (no-op for unconfined callers), so an embed activation is held to the session confinement the same way publish and run are; then the unchanged compile/catalogue path, so `node.not_available` arrives identically to the dashboard's.
- `internal/api/handlers/embedscope.go`: the false "activation and publishing are refused to embed sessions" comment now states the real invariant (an embed can only make live a revision that passed its own confinement, so it can narrow, never widen, the next session's confinement).
- UI: `session.svelte.ts` mints `SCOPE_ACTIVATE`, read implication mirrors the server's list, new `embedControls()`; the embed editor shows Publish on `workflow:publish`, and the activate switch plus version-panel Unpublish (a deactivate) on `workflow:activate` (new `canUnpublish` on `WorkflowHistoryHost`; the editor's switch is gated on `!previewing` instead of `!locked`, so a read-only activate-only session still gets it). Dashboard passes `canUnpublish: true`, no change there.
- Docs: guides/embedding.md has a "Publish and activate authority" table of the three levels; concepts/tenancy-and-embedding.md, reference/api-contract.md, the generated reference (scripts/generate-api-reference.mjs embed verdicts), the embedding skill references and the SDK `EmbedScope` type all list the new scopes. OpenAPI `EmbedSessionBody.scopes` doc regenerated into web and SDK clients.
- Tests: internal/embed/embed_publish_test.go, internal/api/middleware/embed_publish_test.go (+ sentence pin), internal/api/embed_publish_test.go (router: embed activation with an unavailable node gets the dashboard's exact 422; publish needs the scope; activation held to confinement; key mint refuses both scopes), web/src/lib/embed/session.test.ts (`scopeAllows`, `embedControls`).

Noted, not changed: `PublishVersion` sets the workflow active but does not call the trigger lifecycle hooks (`triggers.Activated`) the way `Activate` does — pre-existing for the dashboard too.

# Related Files

- internal/embed/embed.go
- internal/api/middleware/scope.go
- internal/api/handlers/workflows.go
- internal/api/handlers/embedscope.go
- web/src/lib/embed/session.svelte.ts
- web/src/lib/embed/embed-editor.svelte
- docs/src/content/docs/guides/embedding.md

# Attachments
