---
id: FEAT-dn6s8s
title: White-label, localisation and go-live from the host UI
status: todo
priority: low
labels:
    - docs
    - white-label
    - i18n
parent: EPIC-62zt4j
created: "2026-09-23T02:04:35Z"
updated: "2026-09-23T02:04:35Z"
---

# Description

Branding fields are documented, but not which locales ship (en, id), how to add one, what cannot be themed or hidden, or how embedded users publish a workflow.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-20). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

**n8n:** n/a

# Steps to Reproduce

1. `guides/embedding.md:179-186` says the locale is "checked against the catalogs the editor ships" but never lists them. `web/project.inlang/settings.json` has `"locales": ["en","id"]`.
2. `MountOptions` (`sdk/src/browser.ts:26-53`) has no theme option. Branding is `name`, `logoUrl`, `accent`, `hideRun` and `hideSave` only. The docs do not say that light or dark and other controls cannot be set per host.
3. `web/src/lib/embed/session.svelte.ts` (SCOPE_PUBLISH comment) and the embed permits refuse activation, so an embedded user cannot put a workflow live. The docs say "Only the backend key may call it" (`embedding.md:118-119`) but give no host-UX recipe (a "Go live" button that calls the host backend, which calls `activateWorkflow`).

# Expected

A "White-label and localisation" page covering the branding fields and validation, deployment defaults, shipped locales, how to contribute a locale (it requires a rebuild), what is not themeable, and the activation or publish recipe.

# Actual

Integrators cannot plan white-label or localisation work from the docs.

# Acceptance Criteria
- [ ] The shipped locale list is checked against the inlang settings, and adding a locale is documented
- [ ] What can and cannot be customised is stated (branding, theme tokens, hidden controls)
- [ ] A go-live recipe (activation from the host UI) works end to end
- [ ] State today's white-label limits explicitly: no light mode for the embed (FEAT-yrnkz0), no fonts, and the exact `hideRun`/`hideSave` semantics

# Implementation Plan

Write the page.

# Notes

Also from the host-SaaS integration review (D7).

Related tickets: FEAT-15k49d, FEAT-a3dwj2

Related (from the audit): FEAT-15k49d (done), FEAT-a3dwj2 (todo)

# Related Files

the file references above.

# Attachments
