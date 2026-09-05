---
id: FEAT-7tgasa
title: Build the CI pipeline the repository already assumes
status: done
priority: high
labels:
    - release
    - platform
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:46:13Z"
updated: "2026-09-05T15:36:58Z"
---

## Scope

There is no continuous integration in this repository. `ls .github` fails, and a recursive search finds no `.gitlab-ci.yml`, `Jenkinsfile`, `.circleci`, `.drone.yml` or any other CI configuration — the only `.github` on disk belongs to a gitignored third-party clone under `.corpus-cache/`. Every quality instrument this project has built is run by hand or not at all.

The instruments themselves are good, which is what makes their absence from an automated run the defect. `make lint` runs `go vet`, a `gofmt -l` check and `svelte-check`. `make test` runs `go test ./... -race`. Four smoke suites exist and each proves something a unit test cannot: `smoke-sqlite` boots a real binary and asserts `/api/v1/health`, `/api/openapi.json` and the SPA history fallback; `smoke-dev` proves the Vite proxy; `smoke-docker` proves the distroless image serves and writes to a bind mount; `smoke-postgres` brings up `postgres:17-alpine`, runs `TestMigratePostgres` against it in a throwaway container, then boots the app against it.

Two drift detectors were written specifically to be automated and are automated nowhere. `web/scripts/check-api-client.mjs` snapshots `web/src/lib/api/generated/`, regenerates it from a freshly built binary's OpenAPI document, byte-compares, restores on failure and throws "Generated API client is stale". `sdk/scripts/check-types.mjs` does the same for `sdk/src/generated/models.ts`. Both import `scripts/openapi-spec.mjs`, whose own header states the reason the whole chain exists: "The document comes from a real KilasFlow binary rather than a checked-in copy, so a generated client can never drift from the API the server actually serves." Nothing runs either one, so the guarantee is aspirational.

The Makefile also has holes that a CI file would immediately expose. `web/` carries a `vitest` suite (`pnpm test`) that no Make target invokes. `sdk/` has `build`, `check`, `test`, `generate:types` and `generate:types:check` and the root Makefile has no SDK target at all. There is no `generate:api` target either, so the one command a contributor must run after changing a handler type is discoverable only by reading `web/package.json`.

This ticket is the foundation of p10 and it is also a correction. `FEAT-xx6p22` (V2-p9-0) owns making eight ticket bodies honest today by rewording their "runs in CI" claims to "run by hand"; this ticket is what earns the claim back afterwards, for exactly the checks CI actually runs and no others.

## Acceptance criteria

- [x] A push and a pull request both run `make lint`, `make test`, `pnpm check` and `pnpm test` in `web/`, and `pnpm check` and `pnpm test` in `sdk/`, and a failure in any one of them fails the run.
- [x] Both drift detectors run on every pull request, and a change to a handler type that is not followed by `pnpm generate:api` and `pnpm generate:types` fails the run with the message the scripts already emit.
- [x] `smoke-sqlite` and `smoke-docker` run on every pull request; `smoke-postgres` runs on the default branch or on demand, since it starts a database container.
- [x] The n8n corpus is handled honestly: the run either verifies `internal/interop/n8n/corpus/BASELINE.md` against a regenerated baseline, or states in the workflow file why it cannot, and never silently skips.
- [x] The Makefile gains `test-web`, `test-sdk`, `check-sdk`, `build-sdk`, `generate:api` and `generate:api:check` targets, and the CI workflow invokes those targets rather than inlining commands, so a laptop and CI run the same thing. — met in substance, with three naming deviations recorded in the Outcome.
- [x] Toolchain versions in CI match the ones already pinned everywhere else — Go 1.27, Node 24, pnpm 10 — and the workflow reads them from the existing manifests rather than repeating them as literals.
- [x] After this lands, the ticket bodies that `FEAT-xx6p22` reworded may assert "runs in CI" again only for checks this workflow actually runs; the sweep is recorded on this ticket with the list of claims restored.
- [x] A first-time contributor can read one file and learn every check their change must pass.

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

## Outcome

`.github/workflows/ci.yml` and one local composite action, `.github/actions/js-toolchain`.
Six jobs, every one of them calling Make targets rather than its own copy of the
commands, so the workflow cannot drift away from what a laptop runs.

### What the pipeline runs

On every pull request and every push to `main`:

- **lint** — `make lint`: `go vet`, the `gofmt -l` check, `svelte-check` and the
  web vitest suite. This target already grew past Go when `web-test` landed, so
  the job installs the frontend despite its name.
- **test** — `make test` (`go test ./... -race`) against real PostgreSQL, MySQL
  and MariaDB service containers, then `make corpus-check`.
- **drift** — `make generate-api-check` and `make generate-types-check`. Each
  builds a KilasFlow binary, boots it, and byte-compares the regenerated client
  against the committed one.
- **sdk** — `make sdk-check`, `make sdk-test`, `make sdk-build`.
- **smoke** — `make smoke-sqlite`, `make smoke-dev`, `make smoke-docker`.

Default branch and `workflow_dispatch` only:

- **smoke-postgres** — `make smoke-postgres`, kept off pull requests because it
  builds the image, brings up Compose and tears down with `down -v`. The
  migration coverage it carries is not left to that job alone: the `test` job
  runs the same PostgreSQL coverage against a service container on every pull
  request.

### Decisions

**CI provides the databases.** `internal/database`, `internal/sqlbuild`,
`internal/loadoptions` and `nodes/database_test.go` all gate integration coverage
on a DSN and `t.Skip` without one, and a skip reads as a pass in a summarised
run — precisely the untruth this ticket exists to remove. MariaDB is a third
service rather than a MySQL alias for the reason `.pine/memory/live-databases.md`
records: the two differ on `INSERT … RETURNING` and on MySQL 8.0.19's row-alias
upsert, the exact two decisions the SQL builder branches on. Verified locally
against throwaway containers on the documented procedure: `internal/database`,
`internal/sqlbuild` and `internal/loadoptions` all pass with the three DSNs set.

**The corpus baseline is not verified in CI, and the workflow says so.**
`BASELINE.md` scores 39 fixtures; two are committed and the rest are third-party
n8n and WAHA workflows that `.gitignore` deliberately excludes, being
`LicenseRef-n8n-sustainable-use` or unlicensed outright. Materialising them needs
a local n8n reference checkout, which `.pine/memory/licensing.md` forbids as a
build input of any kind. So CI scores the committed control fixtures and prints
`TestCorpusScoreboard`'s skip line rather than letting the skip pass silently —
that is the whole point of `corpus-check` running with `-v`. Regenerating the
baseline stays `make corpus-baseline`, by hand, recorded on the ticket that moves
the numbers.

**Actions are pinned to commit SHAs, not tags.** Every SHA was resolved from the
GitHub API rather than typed from memory, and the version it corresponds to is in
a comment beside it. A mutable tag in a repository whose licence posture is about
controlling which third-party code enters it would be the wrong default. The
composite action exists so those four pins live in one file instead of four.

**Toolchain versions come from the repository.** Go from `go.mod` via
`setup-go`'s `go-version-file`; pnpm from `web/package.json`'s `packageManager`
via `pnpm/action-setup`'s `package_json_file`. Node has no `.nvmrc`, no
`.node-version` and no `engines` field, so `devbox.json` is the only pin and the
composite action parses it — failing loudly if the entry is ever renamed, rather
than silently falling through to whatever Node the runner ships.

**No coverage gate**, as the plan asked.

### A latent bug this work surfaced and fixed

`make generate-api-check` did not work on a clean checkout. orval loads
`web/tsconfig.json`, which extends the generated, gitignored
`.svelte-kit/tsconfig.json`; without it the generator dies with
`File './.svelte-kit/tsconfig.json' not found` before ever reaching the spec.
Nothing noticed because `pnpm check` syncs as a side effect, so the failure only
appears when the generators run on their own — which is exactly what CI does. The
new `web-sync` target is now a prerequisite of both API-generation targets, so a
fresh clone works too. Confirmed by deleting `web/.svelte-kit` and re-running.

### Naming deviations from the acceptance criteria

Three, all deliberate:

- `test-web` was not added. `web-test` already exists — it landed one commit
  before this work — and renaming a just-added target is churn for nothing.
- The SDK targets are `sdk-check`, `sdk-test` and `sdk-build`, matching
  `web-test`'s `<area>-<verb>` shape rather than the criteria's `check-sdk` form.
- `generate:api` and `generate:api:check` cannot be Make target names at all: a
  colon is Make's rule separator. They are `generate-api` and
  `generate-api-check`, with `generate-types` and `generate-types-check` beside
  them.

### The eight "runs in CI" claims

`FEAT-xx6p22` is still `doing` and had not yet reworded anything, so rather than
reword and immediately restore, the final-state sweep was done directly. Its own
AC — "No ticket body asserts that a check runs in CI" — is now stale and should
not be executed as written; that ticket needs a note.

Six verified and left standing, because the pipeline genuinely runs them:

- `FEAT-qcm5ec:26`, `FEAT-je4f4t:47`, `FEAT-jwhdsy:77`, `FEAT-ddzk2k:46` and
  `EPIC-c7gbdp:71` — all five concern `generate:api:check` / `generate:types:check`
  failing on drift. The `drift` job runs both on every pull request; both were
  run locally to confirm they pass and that the stale-client path throws.
- `FEAT-gvn62x:36` — "drift is caught in CI rather than at run time", about a
  models-versus-migrations test not yet written. The `test` job runs
  `go test ./... -race`, so a Go test genuinely is caught in CI once it exists.

Two corrected, because the pipeline does not deliver them:

- `FEAT-csqgg5:41` claimed the corpus "has to run in CI". Only the committed
  control fixtures are scored there; reworded to say so and to point the full
  baseline at `make corpus-baseline` by hand.
- `FEAT-48hreg:41` claimed an example pack "built in CI under
  `GOOS=wasip1 GOARCH=wasm`". No such job exists and none was added — the ticket
  is `todo` and no example pack exists yet. Reworded to "built by hand … and the
  build output recorded on this ticket".

`FEAT-cwz4ac:46` and `FEAT-27km39:47` were left alone. Both say a future artefact
should be checked "in CI the way the API client is checked" — instructions for
work not yet done, not assertions about the present.

### What could not be verified

- **The workflow is valid YAML and its structure parses as intended; that is not
  the same as being a valid GitHub Actions workflow.** Both files were parsed
  with the `yaml` package already in `web/node_modules`, and every job, step,
  `uses:` and service came back as written. No `actionlint` is available here and
  nothing was pushed, so the Actions *schema* — input names, expression syntax,
  service-container semantics — is unverified. Action inputs were checked against
  each action's own `action.yml` fetched from GitHub, which is as close as this
  gets without a run.
- **The smoke jobs were not all executed.** `make smoke-dev` was run and passes.
  `smoke-sqlite` and `smoke-docker` were not, because `make build-all` overwrites
  the committed `internal/web/dist/index.html` and this work was scoped to leave
  tracked files outside `.github/` and the `Makefile` alone.
- **Runner behaviour generally.** Nothing here has run on a GitHub runner.

### A pre-existing test failure, unrelated to this change

`make test` currently fails in this worktree on
`TestTheGoCodeNodeRunsOncePerItemWhenAsked`: the Go-to-wasm compile the Code node
performs takes about 23s under `-race` against the node's hard 10s limit. It is
not caused by anything here, proven three ways — it reproduces through
`go test ./nodes -run … -race` with the Makefile bypassed entirely; it reproduces
under the pre-change Makefile (`make -f` on `git show HEAD:Makefile`); and this
change is 56 added lines with the `test` target untouched. Without `-race` the
same test passes in 4s, and it passed on a first, unloaded run, so it is
wall-clock sensitivity rather than a logic fault.

This matters for CI beyond this ticket: a 2-vCPU GitHub runner is a loaded
machine by this test's standards, so the `test` job is likely to be flaky on it
until that limit is made proportional to the machine. Fixing it means touching
`nodes/`, which was out of scope here. It needs its own ticket.

`make lint` passes: 0 errors, 0 warnings across 1340 files, 212 web tests.

## Work Evidence

`pine close --evidence` computed its diffstat from the last commit at or before
this ticket's creation, which is most of p9 and p10 — 356 files, almost none of
them this change. Replaced by hand rather than left standing, because a ticket
about the repository telling the truth about itself is the last place to leave a
diffstat claiming credit for other people's commits.

The actual change:

```
 .github/actions/js-toolchain/action.yml |  46 +++++
 .github/workflows/ci.yml                | 253 +++++++++++++++++++++++
 .pine/tickets/FEAT-48hreg.md            |   2 +-
 .pine/tickets/FEAT-csqgg5.md            |   2 +-
 Makefile                                |  56 +++++
```

Verification run in this worktree:

- `make lint` — passes. 1340 files, 0 errors, 0 warnings; 21 web test files, 212 tests.
- `make sdk-check`, `make sdk-test` (23 tests), `make sdk-build` — all pass.
- `make generate-api-check`, `make generate-types-check` — both pass, the first
  re-run after deleting `web/.svelte-kit` to prove the fresh-clone path.
- `make corpus-check` — control fixtures pass, `TestCorpusScoreboard` skips with
  its reason printed rather than silently.
- `make smoke-dev` — passes.
- DSN-gated coverage against throwaway PostgreSQL 17, MySQL 8 and MariaDB 11
  containers — `internal/database`, `internal/sqlbuild` and `internal/loadoptions`
  all pass with the three DSNs set.
- `make test` — fails on `TestTheGoCodeNodeRunsOncePerItemWhenAsked`, pre-existing
  and unrelated; see the Outcome for the three-way proof that this change did not
  cause it.
