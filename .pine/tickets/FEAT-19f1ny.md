---
id: FEAT-19f1ny
title: Create dashboard shell and workflow-list experience
status: todo
priority: high
labels:
    - ui
    - dashboard
    - svelte
deps:
    - FEAT-209rxk
    - FEAT-7j7c84
    - FEAT-rcm205
parent: EPIC-c7gbdp
phase: p1
created: "2026-08-29T15:40:24Z"
updated: "2026-08-29T15:40:24Z"
---

## Scope

Introduce the internal-app shell before detailed canvas work: navigation, contextual header, route boundaries, workflow list/create state, and empty/loading/error states. Keep it deliberately data-led; execution metrics and credential content arrive with their owning features.

## Acceptance criteria

- `/app/workflows` is the first functional internal page and loads the workflow list through the REST API, with create, empty, loading, error, and retry states.
- The shared dashboard layout owns sidebar/header/navigation for `/app/*`, `/executions*`, and `/settings*`; it does not wrap `/embed/:id`.
- The dashboard shell contains no hard-coded workflow/execution data and no duplicated client-side workflow source of truth.
- Canvas pages can take over the content area with editor-specific toolbar/canvas chrome without nesting scroll containers or clipping the viewport.
- Keyboard focus, navigation labels, and narrow-screen behaviour are tested; a browser smoke test verifies route separation between app shell and embed shell.

## References

- PRD: §§38, 43–44, 46; Milestone 1.
- Design reference: `01-workflows-list.png`, `06-canvas-empty-add-first-step.png`, and INDEX patterns for list/navigation. Use only as IA/interaction reference, not as visual copy.

## Relevant documentation

- Use current SvelteKit routing/layout documentation via `find-docs` before choosing route-group or layout APIs.

## Relevant skills

- `pine`, `find-docs`.
- `impeccable`, `web-design-guidelines`, `mobile-responsive-audit`, `playwright-cli` — use when their specific UI/audit/browser-test trigger applies.
- `verification-before-completion`.
