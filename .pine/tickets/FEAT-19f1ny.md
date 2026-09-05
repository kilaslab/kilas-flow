---
id: FEAT-19f1ny
title: Create dashboard shell and workflow-list experience
status: done
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
updated: "2026-08-30T14:38:53Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-08-30.

- Base: _(none — ticket predates git history or creation time unknown; showing uncommitted changes only)_
- Commits (1):
  - `6a9f56bb` — chore: initialize governance and Pine tracking
- Files changed (base → working tree):

```
 .pine/tickets/FEAT-19f1ny.md              |   4 +-
 .pine/tickets/FEAT-3mady6.md              |  34 ++++++-
 cmd/kilasflow/main.go                     |  34 +++++--
 internal/api/handlers/workflows.go        |  71 ++++++++++++-
 internal/api/routes.go                    |   3 +-
 internal/api/server.go                    |   7 +-
 internal/api/workflows_test.go            |  74 +++++++++++++-
 internal/execution/records.go             |  36 +++----
 internal/node/registry.go                 |  23 +++--
 internal/node/registry_test.go            |  23 ++---
 internal/repository/executions.go         | 159 +++++++++++++++++++++++++++---
 internal/repository/models.go             |  29 +++---
 internal/repository/models_test.go        |  48 +++++++++
 internal/workflow/compiler.go             | 101 +++++++++++++++++--
 internal/workflow/document_test.go        |  72 ++++++++++++++
 nodes/core.go                             |   4 +
 web/src/lib/api/generated/models/index.ts |   2 +
17 files changed, 634 insertions(+), 90 deletions(-)
```

- New untracked implementation files at close: `web/src/lib/components/dashboard/dashboard-nav.svelte`, the `(dashboard)` route group, and the bare `web/src/routes/embed/[id]/+page.svelte` boundary.
- Verified with `cd web && pnpm generate:api:check && pnpm check && pnpm test && pnpm build`, `make smoke-dev`, and Playwright CLI smoke coverage for create → redirect, isolated embed routing, dialog focus, navigation labels, and 375×667 plus 393×852 layouts.
