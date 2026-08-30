---
id: FEAT-209rxk
title: Complete reproducible single-origin platform foundation
status: done
priority: high
labels:
    - foundation
    - backend
    - frontend
parent: EPIC-c7gbdp
phase: p0
created: "2026-08-29T15:39:28Z"
updated: "2026-08-29T16:54:14Z"
---

## Scope

Complete and prove the production-shaped foundation: Go HTTP server, SvelteKit SPA, relative API proxying, embedded static assets, configuration, migrations, SQLite default persistence, optional PostgreSQL internal persistence, and health/readiness checks.

## Acceptance criteria

- `make dev` starts API and web development servers; browser API requests use relative `/api/v1/*` paths through the Vite proxy.
- Production build embeds the SPA in the Go binary and serves SPA, `/api/*`, and `/webhook/*` from one origin without a hard-coded frontend API host.
- SQLite creates/configures the internal database safely; PostgreSQL is selectable through configuration; migrations run deterministically on a fresh database.
- `/api/v1/health` and `/api/v1/ready` are covered by automated tests and readiness verifies database connectivity.
- CI-relevant Go and web checks pass, and the ticket records the commands/results.

## References

- PRD: §§7–16, 46–49, 54; Milestone 0 (§59).
- Existing integration scaffold: `cmd/kflow/main.go`, `internal/api/`, `internal/database/`, `web/vite.config.ts`.

## Relevant documentation

- Consult current GORM migration/connection-pool documentation and current SvelteKit/Vite deployment guidance using `find-docs`; record the exact links and versions used in the work evidence.

## Relevant skills

- `pine` — maintain progress and close with evidence.
- `find-docs` — mandatory for GORM, SvelteKit, or Vite API/configuration decisions.
- `test-driven-development`, `systematic-debugging`, `verification-before-completion` — use when their task conditions apply.

## Implementation notes

- Added an explicit `database.Migrate(db, models...)` AutoMigrate boundary and invoked it at startup. P0 passes no domain models; P1 supplies the first workflow/execution models without coupling connection setup to them.
- `make dev` resolves Air from `GOBIN` (or `GOPATH/bin`) instead of assuming that directory is on the shell PATH, while retaining `AIR=/path/to/air` as an override.
- Added independent SQLite, Vite-proxy, Docker, and Compose PostgreSQL smoke targets. PostgreSQL smoke runs the opt-in migration probe against its disposable database before starting the app image.

## Documentation consulted

- GORM AutoMigrate: https://gorm.io/docs/migration.html (current Context7 result, 2026-08-29). It confirms explicit multiple-model migration and that AutoMigrate does not remove unused columns.

## Verification

- `make smoke-sqlite` — passed: fresh SQLite, embedded SPA/binary, health, readiness, OpenAPI, deep-link fallback.
- `make smoke-dev` — passed: Vite root plus relative `/api/v1/health` proxy to a temporary Go server.
- `KILASFLOW_SMOKE_SKIP_BUILD=1 make smoke-docker` — passed: non-root image and persisted temporary SQLite bind mount.
- `KILASFLOW_SMOKE_SKIP_BUILD=1 make smoke-postgres` — passed: disposable Compose PostgreSQL, AutoMigrate probe, and app readiness.
- `make test` — passed with `-race`.
- `make lint` — passed: `go vet`, gofmt check, and `svelte-check` with 0 errors and 0 warnings.

## Work Evidence

Closed by `pine close --evidence` on 2026-08-29.

- Base: _(none — ticket predates git history or creation time unknown; showing uncommitted changes only)_
- _(no file changes detected since ticket creation)_
