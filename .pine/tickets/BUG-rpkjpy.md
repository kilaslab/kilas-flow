---
id: BUG-rpkjpy
title: 'CI has been red on main since at least 2026-09-11: every failing job, its cause and the fix'
status: doing
priority: high
labels:
    - ci
    - testing
    - dx
created: "2026-09-20T04:29:14Z"
updated: "2026-09-20T05:20:00Z"
---

# Description

CI has run on `main` three times and has never been green: `failure` (2b7e35a2,
2026-09-11), `cancelled` (bf802ab8, 2026-09-15), `failure` (458b038c, 2026-09-20),
`failure` (11e7d2c9, 2026-09-20 — the first run that could be read in full). Seven
of the ten jobs failed on that last one.

Two findings were reached from the failing jobs but are their own defects and
have their own tickets: the module path (BUG-341sxn, done) and the engine's wait
timer race (BUG-5gws7n, fixed here).

# Fixed

1. **Lint: `gofmt` on five files.** `internal/api/handlers/interop.go`,
   `internal/api/handlers/workflows.go`, `internal/database/migrate_test.go`,
   `internal/engine/wait_service_test.go` and `internal/safehttp/safehttp.go`
   were already unformatted at HEAD (`git show HEAD:<file> | gofmt -l` says so).
   Fixed in `e5e5f9f`; the Lint job passed on 11e7d2c9.

2. **Go tests: `internal/nodepack` data race.** `go test -race` reported a write
   to the process-wide credentials registry from `registerExampleCredential()`
   inside a parallel test, racing the sibling tests that read it through
   `credentials.Lookup`. The registry's contract is composition-time
   registration, so the test was out of order rather than the registry unsafe;
   the registration moved to `TestMain`. Fixed in `e5e5f9f`.

3. **Go tests: `internal/config` fed by the developer's environment.** The
   Makefile exports `KILASFLOW_WEB_PORT` for `make dev` and `make test` inherits
   it: the loader composes a `web.port` key that matches no field, so
   `TestAKnownKeyIsNeverReportedAsUnknown` and
   `TestTheUnknownKeyWarningFollowsTheConfiguredLogFormat` fail in CI and on any
   machine with that variable exported, while the same suite passes when run
   directly. A `TestMain` now drops ambient `KILASFLOW_*` first. Fixed in
   `1357f6d`.

4. **Generated client drift — all four checks, a different cause each.**
   `generate-types-check` and `generate-config-reference-check` were stale
   generated output; `generate-api-reference-check` could not run at all because
   `workflow-diagnostics` had no contract group, and it joins the Workflows
   group; the web client's check passed. Fixed in `6bd5a96`.

5. **Host SDK: ten operations with no client method and no exclusion.** Nine
   tenant methods and `getWorkflowDiagnostics` now exist, under a section that
   says what the tenant group is — the operator surface, refused for a tenant's
   own key and for every embed session. Nothing was added to `EXCLUSIONS`: the
   gate's own comment says an entry must justify itself. Fixed in `35a32bf`, with
   the two counts that had drifted with the surface (63 in `sdk/README.md`, 72 in
   the contract page) corrected to 73.

6. **Go tests: the sign-in throttle tests raced their own refill.**
   `TestLoginRefusesASprayFromOneAddress` expects 429 on the attempt after the
   allowance and gets 401 **under `-race` only**, in CI and locally — the token
   bucket refills once per six seconds while the burst costs about a second an
   attempt under the detector, so the token is back before the eleventh attempt.
   The policy is already proven against a clock the test owns in
   `internal/api/middleware/loginlimit_test.go`; the handler tests now spend
   until refused inside a bound and assert the 429 carries a Retry-After, which
   proves the wiring without racing a wall clock. Fixed in `8bbff74`.

7. **Smoke (PostgreSQL): concurrent starts crashed on recording a skipped
   migration.** `TestConcurrentPostgresStartsApplyTheBaselineExactlyOnce` failed
   with `duplicate key value violates unique constraint "schema_migrations_pkey"`
   from `record skipped vector migration 000006_vector_store`: `apply` recovers
   from losing that race by re-reading the table, but the skip path had borrowed
   the plain insert without the recovery, and a skip has no DDL to wait on.
   `recordSkippedVersion` declares the conflict in the statement instead. Fixed in
   `bff7380`, reproduced on an unpatched tree first.

8. **Go tests: `internal/engine` data race in the wait timers** — found while
   verifying the rest, tracked and fixed as BUG-5gws7n (`9e3134f`).

# Still failing

9. **End-to-end (Playwright): 12 specs, unchanged by all of the above.** The set
   is identical on 458b038 (before this work) and on 11e7d2c9 (after it):
   `datastore-pg.spec.ts:36`, `datastore.spec.ts:128`,
   `n8n-compare.spec.ts:{192,363,599,653}`, `node-coverage.spec.ts:{211,400,696}`,
   `pack-editor.spec.ts:{43,76,146}`. The summarised errors are
   `expect(locator).toBeVisible() failed`, `expect(received).toBe(expected)` and
   `expect(locator).toBeAttached() failed` — locator drift in editor surfaces,
   not a crash. The skip budget is healthy on that run: 22/24 skipped, every one
   explained by an allowlisted prefix.

   FEAT-5fhj6p owns the e2e surface; this entry is the count and the attribution,
   not a second home for the work.

# Acceptance Criteria
- [x] `make lint` passes (Lint green on 11e7d2c9).
- [x] `make test` is deterministic: `internal/config`, `internal/nodepack`,
      `internal/api/handlers` and `internal/engine` each pass under `-race`,
      repeatedly, and the full suite reports no failures.
- [x] All four drift checks pass with regenerated output committed.
- [x] `make sdk-check`, `make sdk-build`, `make sdk-test` pass with the ten
      operations covered and zero missing or stale.
- [x] Smoke (PostgreSQL) passes: reproduced first, then fixed, then the two
      blocks `scripts/smoke-postgres.sh` runs pass locally against a scratch
      pgvector/pgvector:pg17.
- [ ] The 12 e2e specs pass (item 9).
- [ ] CI is green on `main`, which is what the README badge points at.

# Notes

The badge in README.md links the CI workflow, so this ticket is visible from the
repository's front page in the most direct way available. It is left in place
rather than hidden until green: a red badge that names a real, tracked set of
failures is better information than no badge.

Two of these — the `internal/nodepack` race and the engine wait-timer race —
were invisible for a while because a cached test result stood in for a run. A
lesson worth keeping: a package that has not been re-executed since a dependency
changed is not evidence.

# Related Files

- `Makefile` (`test`, `lint`, the `generate-*-check` targets, `smoke-postgres`)
- `.github/workflows/ci.yml`
- `internal/database/migrate.go`, `internal/engine/wait_service.go`
- `internal/api/handlers/auth_test.go`, `internal/api/middleware/loginlimit.go`
- `internal/config/config_test.go`, `internal/nodepack/author_test.go`
- `sdk/src/server.ts`, `sdk/test/operation-coverage.test.mjs`
- `README.md` (badge)

# Attachments
