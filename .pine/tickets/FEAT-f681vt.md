---
id: FEAT-f681vt
title: Ship the generic canvas editor and first runnable workflow slice
status: todo
priority: critical
labels:
    - ui
    - canvas
    - workflow-editor
deps:
    - FEAT-3mady6
    - FEAT-7j7c84
    - FEAT-19f1ny
parent: EPIC-c7gbdp
phase: p1
created: "2026-08-29T15:40:40Z"
updated: "2026-08-29T15:40:40Z"
---

## Scope

Build `/app/workflows/:id` as a generic server-backed workflow editor using the node registry. The first demonstrable workflow is Manual Trigger → Set, saved through the workflow API and executed by the Go runtime. HTTP Request extends this exact slice later.

## Acceptance criteria

- The editor loads/saves canonical workflow JSON through REST, reconciles save failures visibly, and never treats browser canvas state as authoritative after refresh.
- Empty canvas, trigger picker, node-category/search picker, node creation/removal/positioning, edge creation/removal, and selection all work for registered core nodes.
- Node handles and connection validation derive from registry metadata; incompatible ports are blocked in the canvas and rejected again by the server.
- The property panel is generated from node metadata, keeps shared Settings separate from node-specific Parameters, and supports Manual Trigger, Set, IF, and Merge without separate hard-coded full-panel implementations.
- A user can create Manual Trigger → Set, save, run, and see success/failure feedback. Browser coverage proves this flow against a running API.
- Editor chrome is accessible and fits desktop and narrow mobile viewports without an accidental nested page scroller.

## References

- PRD: §§3.1, 23, 38, 43–44, 65 first vertical slice.
- Design reference: `06-canvas-empty-add-first-step.png`, `07-node-picker-triggers.png`, `08-node-picker-categories.png`, `09-node-picker-search-results.png`, `10-ndv-set-edit-fields.png`, `12-canvas-wired-manual-set-http.png`, `14-canvas-if-branching.png`.

## Relevant documentation

- Official Svelte Flow connection validation: https://svelteflow.dev/examples/interaction/validation
- Official Svelte Flow custom handles: https://svelteflow.dev/learn/customization/handles
- Use `find-docs` to refresh SvelteKit/Svelte Flow APIs for the installed versions before implementation; record exact links/versions.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `verification-before-completion`.
- `impeccable`, `web-design-guidelines`, `mobile-responsive-audit`, `playwright-cli` — use when their triggers apply.
