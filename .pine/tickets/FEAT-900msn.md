---
id: FEAT-900msn
title: Deliver secure white-label embedded editor sessions
status: todo
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
updated: "2026-08-29T15:42:14Z"
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
