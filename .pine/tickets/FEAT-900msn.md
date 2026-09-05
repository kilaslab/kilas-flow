---
id: FEAT-900msn
title: Deliver secure white-label embedded editor sessions
status: done
priority: high
labels:
    - embed
    - auth
    - whitelabel
    - security
deps:
    - FEAT-w3s12y
    - FEAT-0j7r5s
parent: EPIC-c7gbdp
phase: p6
created: "2026-08-29T15:42:14Z"
updated: "2026-09-05T02:50:00Z"
---

## Scope

Expose the proven standalone editor to host SaaS products through `/embed/:workflowID` and short-lived embed sessions. Keep the embed UI bare, branded by explicit configuration, and isolated from the internal dashboard shell.

## Acceptance criteria

- `POST /api/v1/embed-sessions` creates a short-lived, scoped session for an authorized workflow and returns only the embed data required by the host integration.
- `/embed/:id` renders the editor without internal sidebar/header/navigation and rejects expired, malformed, wrong-workflow, or insufficient-scope sessions.
- Host origin allowlisting is enforced at session creation and runtime. `postMessage` specifies target origin and validates `event.origin`; wildcard trust is not used.
- Supported white-label options (name/logo/theme/feature visibility) are explicit, validated, and cannot override security boundaries or inject arbitrary markup/styles/scripts.
- Browser integration tests mount the route in an iframe and prove origin denial, valid edit/run permission, read-only behaviour for restricted scopes, session expiry, and shell separation.

## References

- PRD: §§3.2, 38–41, 43–46, 57; Milestone 6; Definition of Done items 5, 18.
- Design reference: `01-workflows-list.png` and `12-canvas-wired-manual-set-http.png` establish the standalone-vs-editor chrome distinction; do not copy branding.

## Relevant documentation

- Use `find-docs` for the installed SvelteKit routing/build APIs and any session/JWT library used. Consult MDN/official browser security guidance for iframe, `postMessage`, and origin semantics; record exact sources.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.
- `web-design-guidelines`, `playwright-cli`, `mobile-responsive-audit` when their browser/UI triggers apply.

## Implementation Plan

- `internal/embed` mints and verifies a signed, versioned token carrying tenant, one workflow, scopes, and a single allowed origin. HMAC-SHA256 over a versioned payload; no external JWT dependency for a token this project fully controls.
- `middleware.EmbedAuth` is mounted for every request but inert unless one carries a token, so the internal dashboard is unaffected. A request with a token is confined to its workflow and scopes by what it *targets*, with everything else refused by default rather than enumerated as forbidden.
- `/embed/:id` renders the shared canvas with none of the dashboard chrome, and the editor gained a `readOnly` mode that the dashboard does not use.

## Work Evidence

- Origin handling has no wildcard anywhere. The allowlist is enforced at session creation and the origin is re-checked on every request, so a token copied into another page stops working. `TestMatchesOriginIsExactWithNoWildcard` proves a subdomain, a suffix trick, a scheme change, `*`, and `null` are all refused; an empty allowlist allows nothing rather than everything.
- `postMessage` names an exact target origin in both directions and validates `event.origin` before the payload is inspected at all. Proven live: with the host posting to `https://evil.example` instead of its own origin, the iframe never received the session and refused to open.
- Scope enforcement is server-side. A read-only token is refused for PUT and run (403); a write token can save but not run; and no token can list workflows, mint another session, delete, or change activation. Hiding a control is never what stops an action — `TestBrandingCannotOverrideSecurity` proves branding grants no scope.
- Branding is validated values, never markup: `<script>` names, `javascript:` and `data:` logos, non-https logos, and CSS-escape accents are all rejected server-side, and the frame re-checks them because the token arrives through the host's browser.
- Session creation returns only a token, an embed URL, an expiry, scopes, and the origin — no workflow content, no tenant detail, asserted directly on the response body.
- Lifetime is capped at 30 minutes regardless of what a caller asks for, and expired, forged, and malformed tokens all answer 401 identically.
- Browser-verified in a real iframe against a running API: shell separation (`[data-embed-shell]` present, no `[data-dashboard-shell]`, no workspace navigation), read-only behaviour (no controls, nodes not draggable, handles not connectable), and write+run behaviour (Add step / Save / Run present, nodes draggable).
- A real defect was found and fixed during that verification: the embedded editor sat on "Loading…" forever. TanStack Query only notifies on properties a template happened to read first, and the `{#if}` chain's reads were not enough to trigger a re-render. `notifyOnChangeProps: 'all'` is now set on the shared query client — the property-tracking optimisation saved nothing at this scale and had already cost correctness.
- Two runcode tests were also corrected: their limits were tight enough that the race detector, not the product, decided the outcome. The timeout test now times execution alone and bounds it loosely, because the guarantee under test is that an endless loop is stopped, not how many microseconds the unwind takes.
- `go test ./...`, `go test ./... -race`, `go vet ./...`, `pnpm test`, `pnpm check`, `pnpm generate:api:check`, `pnpm build`, `make smoke-sqlite`.
