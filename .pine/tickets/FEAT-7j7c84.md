---
id: FEAT-7j7c84
title: Establish frontend UI theme and OpenAPI query foundation
status: done
priority: high
labels:
    - ui
    - api
    - platform
deps:
    - FEAT-209rxk
parent: EPIC-c7gbdp
phase: p1
created: "2026-08-30T02:20:52Z"
updated: "2026-08-30T07:04:39Z"
---

# Scope

Establish the reusable Svelte UI primitives, KilasFlow jade-and-graphite theme,
and generated OpenAPI/TanStack Query client before dashboard or canvas work.
This ticket deliberately adds no product screen or workflow-editor behavior.

# Acceptance Criteria

- [x] shadcn-svelte primitives are installed as local source with accessible
  shared controls; Svelte Flow remains the only canvas package.
- [x] Semantic light and dark tokens use KilasFlow jade and graphite rather
  than n8n purple, while allowing a future embed session to select light,
  dark, or system mode without arbitrary host CSS.
- [x] Orval generates typed native-fetch Svelte Query modules from the live
  Huma OpenAPI document; generated files are never hand edited.
- [x] All generated calls use one same-origin transport that preserves RFC 9457
  problem details, abort signals, JSON bodies, and 204 responses.
- [x] A reproducible command starts an isolated temporary KilasFlow instance,
  regenerates the client, and detects stale generated output.
- [x] Unit, typecheck, production build, dev-proxy, embedded-binary, and Pine
  verification gates pass.

# Implementation Plan

- Add shadcn-svelte primitives, icons, class utilities, and motion CSS without
  introducing a second form or canvas library.
- Replace the scaffold palette with semantic jade-and-graphite tokens and
  provide the root TanStack Query client.
- Generate `web/src/lib/api/generated` through Orval `svelte-query` using the
  server-owned `/api/openapi.json` document and a handwritten transport only.
- Add test-first coverage for the transport and code-generation checks.

# Notes

- References: PRD §§11–13, 38–41, 45–46; `design-refs/n8n/INDEX.md` is an
  interaction reference only, never a branding template.
- Documentation consulted: shadcn-svelte Tailwind v4 theming, Orval
  `svelte-query`, and TanStack Query Svelte v5 through Context7.
- Relevant skills: pine, test-driven-development, verification-before-completion.
- Verification 2026-08-30: `pnpm test` (3 files, 7 tests), `pnpm check` (0
  errors/warnings), `pnpm build`, `pnpm generate:api:check`, `make lint`,
  `make smoke-dev`, and `make smoke-sqlite` all passed.
- Independent read-only review found and confirmed resolution of a backslash
  same-origin bypass, missing default generated `ApiError` typing, and absent
  abort-propagation coverage.

# Related Files

- `web/package.json`, `web/src/app.css`, `web/src/routes/+layout.svelte`
- `web/src/lib/api/`, `web/orval.config.ts`, `web/scripts/`

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-08-30.

- Base: _(none — ticket predates git history or creation time unknown; showing uncommitted changes only)_
- _(no file changes detected since ticket creation)_
