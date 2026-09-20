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
updated: "2026-09-20T04:29:14Z"
---

# Description

CI has run on `main` three times and has never been green: `failure` (2b7e35a2,
2026-09-11), `cancelled` (bf802ab8, 2026-09-15), `failure` (458b038c, 2026-09-20).
The run at 458b038c is the one dissected here — eight of ten jobs failed.

Nobody sees this today because the badge did not exist until FEAT-a5fhjw added
one, and because the repository's own workflow is `make <target>` per job: a
contributor following CONTRIBUTING.md runs `make test` and gets a red result
without knowing which of these is theirs.

## Fixed in this pass

1. **Lint: `gofmt` on five files.** `internal/api/handlers/interop.go`,
   `internal/api/handlers/workflows.go`, `internal/database/migrate_test.go`,
   `internal/engine/wait_service_test.go` and `internal/safehttp/safehttp.go`
   were already unformatted at HEAD (`git show HEAD:<file> | gofmt -l` says so),
   so `make lint` failed on main before anything here. Fixed in `e5e5f9f`.

2. **Go tests: `internal/nodepack` data race.** `go test -race` reported a write
   to the process-wide credentials registry from `registerExampleCredential()`
   inside a parallel test, racing the sibling tests that read it through
   `credentials.Lookup`. The registry's contract is composition-time
   registration, so the test was out of order rather than the registry unsafe;
   the registration moved to `TestMain`. Fixed in `e5e5f9f`.

3. **Go tests: `internal/config` fed by the developer's environment.** The
   Makefile exports `KILASFLOW_WEB_PORT` for `make dev`, and `make test` inherits
   it: the loader then composes a `web.port` key that matches no field, and
   `TestAKnownKeyIsNeverReportedAsUnknown` and
   `TestTheUnknownKeyWarningFollowsTheConfiguredLogFormat` fail — in CI and on
   any machine where the variable is exported, while the same suite passes when
   run directly. Fixed by a `TestMain` that drops ambient `KILASFLOW_*` before
   any case runs.

   Evidence for the whole mechanism:
   `KILASFLOW_WEB_PORT=5173 go test ./internal/config/` → FAIL with
   `key=web.port ... did_you_mean=webhook`; the same command without the
   variable → ok.

## Still failing

4. **Go tests: `internal/api/handlers`, "a spray from one address" times its own
   refill.** `TestLoginRefusesASprayFromOneAddress` expects the eleventh attempt
   to be 429 and gets 401 **under `-race` only**:

   - `go test ./internal/api/handlers/ -run TestLoginRefusesASprayFromOneAddress -count=1` → ok, 1.185s
   - the same with `-race` → FAIL, 10.47s
   - the same with `-race` at 61afc0b (before any of this work) → FAIL, so it is
     not new here.

   Cause: `middleware.LoginLimiter` is a token bucket with continuous refill —
   ten tokens, one per six seconds. Each attempt in that test runs the
   deliberate 600,000-iteration PBKDF2 of a failed sign-in, which under `-race`
   costs about a second, so the ten-attempt burst takes longer than a refill
   interval and a token is refunded before the eleventh attempt arrives. The
   product is behaving as designed; the test assumes a burst faster than the
   refill. CI also failed `TestLoginRefusesRepeatedGuessesAtOneAccount` in the
   same window (15.54s and 7.77s), which is the same shape.

   Suggested fix, in order of preference:
   - Assert the policy where it can be deterministic — a `LoginLimiter` test in
     `internal/api/middleware` with an injected clock (an unexported
     `now func() time.Time` field defaulting to `time.Now` needs no exported
     API change) — and leave the handler-level test asserting the property that
     matters: a spray from one address is refused within some bounded number of
     attempts.
   - Or run the handler-level case with a small allowance (2 or 3 attempts,
     which still proves the address bucket trips) so the burst finishes well
     inside one refill interval. Do not touch the refill rate to make a test
     pass.

5. **Generated client drift — all three checks fail.**
   `make generate-types-check` → "SDK types are out of date. Run
   `pnpm generate:types` and commit the result.";
   `make generate-api-reference-check` and `make generate-config-reference-check`
   fail too. Fix: run the three generators and commit the output; the checks are
   the reason the drift cannot reach a release, so the failing state is the gate
   working.

6. **Host SDK: `test/operation-coverage.test.mjs`.** "operations with no client
   method and no exclusion: list-tenants, create-tenant, get-tenant,
   create-tenant-api-key, list-tenant-users, create-tenant-user,
   disable-tenant-user, enable-tenant-user, set-tenant-user-password,
   workflow-diagnostics". Either the ten client methods are added or each is
   recorded as an exclusion with a reason — the test exists to make that choice
   explicit rather than to be silenced.

7. **End-to-end (Playwright): 12 failed, 22 skipped, 63 passed (9.8m).** Failing
   specs: `datastore-pg.spec.ts:36`, `datastore.spec.ts:128`,
   `n8n-compare.spec.ts:{192,363,599,653}`, `node-coverage.spec.ts:{211,400,696}`,
   `pack-editor.spec.ts:{43,76,146}`. FEAT-5fhj6p is the ticket that owns the e2e
   surface; this entry is the current count rather than a second home for it.

8. **Smoke (PostgreSQL).** Failed; the failing step is not quoted in this ticket
   because the log was not captured before the run aged out. Rerun
   `make smoke-postgres` locally (needs a Docker daemon) to reproduce.

# Acceptance Criteria
- [x] The five `gofmt` files are formatted (`make lint` reaches the frontend
      checks).
- [x] `go test ./internal/nodepack/ -race -count=2` is clean.
- [x] `KILASFLOW_WEB_PORT=5173 make test` no longer fails in `internal/config`.
- [ ] `go test ./internal/api/handlers/ -race` is deterministic, by one of the
      two routes above rather than by lowering the refill rate.
- [ ] `make generate-types-check generate-api-reference-check
      generate-config-reference-check` pass with freshly generated output
      committed.
- [ ] `make sdk-test` passes with the ten operations either implemented or
      excluded with a stated reason.
- [ ] `make test-e2e` is back to its baseline, and the skip budget still holds.
- [ ] CI is green on `main`, which is what the README badge now points at.

# Notes

The badge in README.md links the CI workflow, so this ticket is now visible from
the repository's front page in the most direct way available. It is left in place
rather than hidden until green: a red badge that names a real, tracked set of
failures is better information than no badge.

# Related Files

- `Makefile` (the `test`, `lint` and `generate-*-check` targets)
- `internal/api/handlers/auth_test.go`, `internal/api/middleware/loginlimit.go`
- `internal/config/config_test.go`
- `internal/nodepack/author_test.go`
- `.github/workflows/ci.yml`
- `README.md` (badge)

# Attachments
