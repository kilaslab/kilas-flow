---
id: FEAT-7tgasa
title: Build the CI pipeline the repository already assumes
status: todo
priority: high
labels:
    - release
    - platform
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:46:13Z"
updated: "2026-09-05T11:46:13Z"
---

## Scope

There is no continuous integration in this repository. `ls .github` fails, and a recursive search finds no `.gitlab-ci.yml`, `Jenkinsfile`, `.circleci`, `.drone.yml` or any other CI configuration — the only `.github` on disk belongs to a gitignored third-party clone under `.corpus-cache/`. Every quality instrument this project has built is run by hand or not at all.

The instruments themselves are good, which is what makes their absence from an automated run the defect. `make lint` runs `go vet`, a `gofmt -l` check and `svelte-check`. `make test` runs `go test ./... -race`. Four smoke suites exist and each proves something a unit test cannot: `smoke-sqlite` boots a real binary and asserts `/api/v1/health`, `/api/openapi.json` and the SPA history fallback; `smoke-dev` proves the Vite proxy; `smoke-docker` proves the distroless image serves and writes to a bind mount; `smoke-postgres` brings up `postgres:17-alpine`, runs `TestMigratePostgres` against it in a throwaway container, then boots the app against it.

Two drift detectors were written specifically to be automated and are automated nowhere. `web/scripts/check-api-client.mjs` snapshots `web/src/lib/api/generated/`, regenerates it from a freshly built binary's OpenAPI document, byte-compares, restores on failure and throws "Generated API client is stale". `sdk/scripts/check-types.mjs` does the same for `sdk/src/generated/models.ts`. Both import `scripts/openapi-spec.mjs`, whose own header states the reason the whole chain exists: "The document comes from a real KilasFlow binary rather than a checked-in copy, so a generated client can never drift from the API the server actually serves." Nothing runs either one, so the guarantee is aspirational.

The Makefile also has holes that a CI file would immediately expose. `web/` carries a `vitest` suite (`pnpm test`) that no Make target invokes. `sdk/` has `build`, `check`, `test`, `generate:types` and `generate:types:check` and the root Makefile has no SDK target at all. There is no `generate:api` target either, so the one command a contributor must run after changing a handler type is discoverable only by reading `web/package.json`.

This ticket is the foundation of p10 and it is also a correction. `FEAT-xx6p22` (V2-p9-0) owns making eight ticket bodies honest today by rewording their "runs in CI" claims to "run by hand"; this ticket is what earns the claim back afterwards, for exactly the checks CI actually runs and no others.

## Acceptance criteria

- [ ] A push and a pull request both run `make lint`, `make test`, `pnpm check` and `pnpm test` in `web/`, and `pnpm check` and `pnpm test` in `sdk/`, and a failure in any one of them fails the run.
- [ ] Both drift detectors run on every pull request, and a change to a handler type that is not followed by `pnpm generate:api` and `pnpm generate:types` fails the run with the message the scripts already emit.
- [ ] `smoke-sqlite` and `smoke-docker` run on every pull request; `smoke-postgres` runs on the default branch or on demand, since it starts a database container.
- [ ] The n8n corpus is handled honestly: the run either verifies `internal/interop/n8n/corpus/BASELINE.md` against a regenerated baseline, or states in the workflow file why it cannot, and never silently skips.
- [ ] The Makefile gains `test-web`, `test-sdk`, `check-sdk`, `build-sdk`, `generate:api` and `generate:api:check` targets, and the CI workflow invokes those targets rather than inlining commands, so a laptop and CI run the same thing.
- [ ] Toolchain versions in CI match the ones already pinned everywhere else — Go 1.27, Node 24, pnpm 10 — and the workflow reads them from the existing manifests rather than repeating them as literals.
- [ ] After this lands, the ticket bodies that `FEAT-xx6p22` reworded may assert "runs in CI" again only for checks this workflow actually runs; the sweep is recorded on this ticket with the list of claims restored.
- [ ] A first-time contributor can read one file and learn every check their change must pass.

## Implementation Plan

GitHub Actions, because the repository is `github.com/kilaslabs/kilas-flow` and every downstream p10 ticket — image publishing, npm publishing, docs deployment — needs a runner in the same place.

Split the work across jobs rather than one long script, so a failure names itself: a `go` job for `lint` plus `test`, a `web` job for `check`/`test`/`generate:api:check`, an `sdk` job for `check`/`test`/`generate:types:check`, and a `smoke` job. The drift jobs and the smoke jobs both need a **built binary**, and `scripts/openapi-spec.mjs` builds one itself into a temp dir; do not try to share a binary artifact between jobs on the first pass, because the script's whole value is that it boots the real thing.

The one trap worth naming ahead of time is the `dist-placeholder` invariant. `internal/web/embed.go` embeds `dist` unconditionally, so `go build`, `go vet` and `go test` all fail when `internal/web/dist/index.html` is absent — the Makefile already encodes this and every Go target depends on it. A CI job that runs `go test` after a `pnpm build` step that wiped the directory will fail in a way that looks like a Go problem and is not. Use the Make targets, which handle it, rather than raw `go` commands.

Cache `~/go/pkg/mod` and the pnpm store. Do not cache the build output; the smoke suites are only meaningful against a fresh build.

One decision to settle rather than leave implicit: whether `smoke-postgres` runs on pull requests. It brings up a container, runs a migration probe in a second container and tears down with `down -v`, so it is the slowest check by a wide margin. Recommend default branch plus `workflow_dispatch`, and say so in the workflow file, because a check that is silently slow becomes a check somebody disables.

Do not add a coverage gate in this ticket. `make test-cover` exists; a threshold argued over in the same change that introduces CI is how CI adoption stalls.

## References

- Roadmap plan, p10 section, entry V2-p10-1: `.pine/roadmap.md`.
- `Makefile` — `lint`, `test`, `test-cover`, `dist-placeholder`, `build-all`, `smoke-sqlite`, `smoke-dev`, `smoke-docker`, `smoke-postgres`, `corpus-baseline`, `node-packs`, and the missing web/SDK targets.
- `scripts/openapi-spec.mjs` — `dumpOpenAPISpec`, which builds and boots a binary and fetches `/api/openapi.json`; imported by both drift detectors.
- `web/scripts/check-api-client.mjs` and `sdk/scripts/check-types.mjs` — the two detectors that run nowhere.
- `scripts/smoke-sqlite.sh`, `scripts/smoke-dev.sh`, `scripts/smoke-docker.sh`, `scripts/smoke-postgres.sh`.
- `internal/web/embed.go` — the `//go:embed all:dist` directive that makes `dist-placeholder` load-bearing for every Go command.
- `.pine/tickets/FEAT-xx6p22.md` — lines 24 and 35, which own rewording the eight "runs in CI" claims; this ticket earns the claim back.
- `devbox.json`, `go.mod`, `web/package.json`, `sdk/package.json` — the pinned toolchain versions CI must match.
