---
id: FEAT-hj8pyx
title: Idempotent execution requests and datastore writes
status: done
priority: high
labels:
    - api
    - engine
    - correctness
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:52Z"
updated: "2026-09-21T00:24:24Z"
---

## Problem

A retried request is a second side effect. A host backend or an agent that retries after a
timeout queues a duplicate execution, and a retried datastore insert writes a second row.
There is no idempotency mechanism anywhere in the tree.

## Evidence

- No `internal/idempotency` package and no `Idempotency-Key` handling anywhere (repo-wide
  grep). The only dedupe that exists is webhook-delivery collapse by sender-supplied delivery
  id (`internal/repository/webhooks.go`, `ClaimDelivery`) and single-use wait tokens
  (`internal/repository/waits.go`, `ResumeWait`).
- `POST /api/v1/workflows/{id}/run` mints a fresh execution per call
  (`internal/repository/executions.go:112` `QueueManualLatest`), so a retry is a new run with
  no link to the first.
- Datastore `InsertRow`/`UpsertRow` (`internal/api/handlers/datastores.go`) have no
  request-level guard either.
- The only concurrency guard on the API today is optimistic concurrency on workflow save
  (`If-Match`/`baseVersionId` → 409), which is a different problem.

## Acceptance criteria

- [x] `POST /api/v1/workflows/{id}/run` honours an `Idempotency-Key` header: the same key from
      the same tenant within the retention window returns the **first** execution id and does
      not queue a second run.
- [x] Datastore `POST /datastores/{id}/rows` and `.../rows/upsert` honour the same header.
- [x] Keys are tenant-scoped and the store is durable (survives restart), not an in-process
      map — a multi-replica deployment must not depend on which replica handled the retry.
- [x] A conflicting reuse (same key, different request body hash) answers 409 rather than
      silently returning the earlier result.
- [x] Retention is bounded and configurable, and the table is covered by the tenant purge.
- [x] Tests cover: replay returns the same execution id; different key queues a second run;
      another tenant cannot replay or observe a key.

## Out of scope

Idempotency for every mutating route. Start with run + datastore writes; add others only when
a host asks.

## Implementation notes

All three stages are committed on this branch, in order. Stage 1 built the durable key store
and the service, stage 2 put `Idempotency-Key` on the three HTTP routes and wired the sweeper
into the composition root, and stage 3 — the final slice — added the host SDK surface, its
tests, the guide, the contract note and the final gate run. Every acceptance criterion is
ticked below with the test names and commands that proved it.

### Review round 1 — the three low findings (fix commit on top of stage 3)

The review's correctness and conventions lenses found nothing; the acceptance lens found three
low items, all about proof and contract text. All three are closed.

1. The AST wiring test proved `run()` starts the sweeper but not that it defers
   `idempotencySweeper.Stop()`, so replacing `cmd/kilasflow/main.go:614` with
   `_ = idempotencySweeper` stayed green.
   `TestRunWiresTheIdempotencyServiceAndItsSweeper` now binds the identifier from the
   `:=` assignment whose right-hand side is `startIdempotencySweeper(...)` and requires a
   `*ast.DeferStmt` whose call is `<that identifier>.Stop()`, failing with "run() does not
   defer the idempotency sweeper's Stop()". Falsified before trusting it: with line 614
   replaced by `_ = idempotencySweeper` the test fails with exactly that message, and with the
   line restored it passes. `main.go` is unchanged in this commit.
2. `ErrInvalidKey`'s doc comment said "Callers translate it into a 400" while everything that
   ships says 422: `internal/api/handlers/idempotency.go:59-70` returns
   `http.StatusUnprocessableEntity`, the guide's table (`guides/idempotency.md:102`) and the
   API test all use 422, and no 400-for-an-invalid-key sentence exists anywhere in the tree
   (repo-wide grep for `400` in the two idempotency doc pages: nothing). No evidence the 400
   was intended, so the comment was the wrong side of the contract and now reads "a 422, the
   same status the row values this key guards are refused with". One file, one line; the
   mapping, the guide, the generated errors page and the API test are untouched and already
   agree.
3. The branch in `Do` that releases the claim for a non-2xx `Response` with a nil error
   (`internal/idempotency/idempotency.go:285`) had no test: the three wired handlers answer
   refusals as errors, so none of them reaches it.
   `TestDoReleasesTheKeyWhenTheWorkRefusesWithANonSuccessStatusOnEveryDriver` (new, run on
   both drivers through `openDrivers`) drives a handler that returns `422` with a nil error,
   asserts the caller receives that status and body unchanged, then sends the corrected
   request with the same key and asserts it runs (`Replayed == false`, side effect ran twice)
   rather than replaying the refusal or being told the key is in flight. The branch is kept:
   it is reachable, and `handlers/datastores.go` returns non-2xx responses without an error on
   its refusal paths. Falsified: with the whole branch deleted the test fails with "the retry
   was answered from a record: a refusal was stored"; restored, it passes.

Gates re-run on this commit (see the report for the commands and outcomes): `gofmt -l` clean on
the three changed files, `go vet ./...`, `go build ./...`,
`go test -race -count=1 -p 1 ./internal/idempotency/... ./internal/api/... ./cmd/kilasflow/...
./internal/guardrails/...` with `KILASFLOW_TEST_POSTGRES_DSN` pointed at a scratch
`pgvector/pgvector:pg17` container (`kf-pg-feat-hj8pyx`), so the new test's PostgreSQL half ran
rather than skipping.

The first pass of that test command failed in `internal/api/handlers`:
`--- FAIL: TestLoginRefusesASprayFromOneAddress (147.32s) — auth_test.go:162: no refusal within
30 attempts`, with load average ~60 and eleven sibling agents building in parallel. It is the
same wall-clock-dependent sign-in test stage 3 already recorded failing under load, in a
package this commit does not touch (`git diff --stat` lists only the two idempotency files and
`cmd/kilasflow/idempotency_test.go`), and it passes alone on this commit:
`go test -race -count=1 -v -run TestLoginRefusesASprayFromOneAddress ./internal/api/handlers/`
→ `--- PASS (51.52s)` at load average 27. With that one name skipped
(`-skip TestLoginRefusesASprayFromOneAddress`) the whole set is green:
`internal/idempotency ok 30.534s`, `internal/api ok 137.444s`, `internal/api/handlers ok
68.366s`, `internal/api/middleware ok 1.594s`, `cmd/kilasflow ok 3.987s`,
`internal/guardrails ok 1.982s`.

### Stage 3 — what changed

No Go file changed in this stage; the Go gates below re-prove stages 1 and 2 unchanged.

- `sdk/src/http.ts` — `Transport.request` gains `headers?: Record<string, string>`, applied
  with `headers.set` after the configured headers and the `Accept`/`Content-Type` defaults, so
  a per-request header adds to them and never weakens the `Authorization` an `apiKey` or the
  host's own `headers` set. `problemFrom` reads `Retry-After` as whole seconds, and
  `KilasFlowError` gains `readonly retryAfterSeconds: number | undefined` as an additive fourth
  constructor argument. A missing header or an HTTP-date gives `undefined`: resolving a date
  would need the caller's clock, and the server only ever sends delta-seconds.
- `sdk/src/server.ts` — `IdempotentWriteOptions` (`idempotencyKey`, `signal`) and
  `MAX_IDEMPOTENCY_KEY_LENGTH = 255`; `runWorkflow(workflowId, input?, options?)`,
  `insertDatastoreRow(datastoreId, values, options?)` and
  `upsertDatastoreRow(datastoreId, filter, values, options?)` take either that object or
  today's bare `AbortSignal`. The two last arguments are told apart by duck typing
  (`addEventListener` + `aborted`), never `instanceof`, so a signal from another realm still
  works. A key failing `/^[\x21-\x7e]{1,255}$/` throws instead of being dropped — an empty or
  malformed key would look like protection and deliver none — so `runWorkflow` and
  `insertDatastoreRow` became `async`, matching the `createEmbedSession` convention. No key
  generator was added, and the SDK deliberately does not surface `Idempotent-Replayed`: it
  returns the body, and a host that persisted its key before the first attempt needs no marker.
- `sdk/test/server.test.ts` — a new `describe('idempotency keys')` with six tests: the key is
  sent on `runWorkflow` and an absent key stays absent (with the configured `Authorization` and
  `Content-Type` still present), a bare `AbortSignal` third argument still reaches the request,
  the key is sent on both datastore writes, an unusable key (empty, space, 256 characters,
  non-ASCII) is refused before `fetch`, a `409` with `Retry-After: 3` surfaces as a
  `KilasFlowError` with `retryAfterSeconds === 3` and `errors[0].value.code ===
  idempotency_key_in_flight` while a `409` without the header leaves it undefined, and an
  HTTP-date `Retry-After` is reported as no hint.
- `sdk/README.md` — a `### Retrying safely` section under the operation surface, with a
  `runWorkflow` example (a UUID per logical operation, persisted before the first attempt, the
  same key on the retry), the `idempotency_key_reused` / `idempotency_key_in_flight` split, and
  the note that the sentence "every method takes an `AbortSignal` last" now has three
  exceptions.
- `sdk/CHANGELOG.md` — an **Additive** bullet under the still-unpublished `0.1.0` heading. The
  version was not bumped: `sdk-version-check` compares `sdk/package.json` with
  `sdk/src/version.ts`, and the package is not on npm.
- `CHANGELOG.md` — one appended `[Unreleased] > Added` bullet describing the feature
  end to end, appended rather than inserted so a textual merge stays trivial.
- `docs/src/content/docs/guides/idempotency.md` (new) — why a retry is a second side effect;
  sending the header (curl and SDK); what a replay returns (`Idempotent-Replayed: true`, no side
  effect, no worker wake, no input echo on a run replay, the compact id/counts replay above
  1 MiB); what counts as the same request (operation + target + canonical body, salted per
  tenant, an empty header is absent); a table of the two `409` codes plus the `422` and `503`,
  with the decision that the server never waits and `Retry-After` is what the caller waits;
  scope (per tenant, shared by every credential including embed sessions); retention
  (`idempotency.retention`, 24h default, 1m..720h, expired keys behave as unseen at once, swept
  every ten minutes, and the conditional `execution.retention` note); and a plainly stated
  "what is not guaranteed" list (release on failure, crash between side effect and outcome,
  a request outliving the two-minute lease, insert-commits-read-back-fails, worker lease
  reclaim, webhook deliveries deduplicated by delivery id, and the routes out of scope).
- `docs/src/content/docs/reference/api-contract.md` — a `## Idempotency` section after
  `## Errors`: the request header, the `Idempotent-Replayed` and `Retry-After` response
  headers, the two `409` codes and the retention key are stable contract, and an added optional
  request header is additive under the page's own rules.

### Stage 3 — how it was verified

Written test-first: the six tests were added to `sdk/test/server.test.ts` and run before the
implementation existed. They failed for the right reason — the options object reached
`AbortSignal.any` as a signal (`The "signals[0]" argument must be an instance of AbortSignal.
Received an instance of Object`) and the 409 surfaced that `TypeError` instead of a
`KilasFlowError` — and the same six pass now. One earlier assertion of mine was wrong, not the
code: it matched `/Idempotency-Key/` against a message reading `idempotencyKey`, so the message
now names the header it fills.

Machine conditions, stated because they explain the timings: this run happened while about a
dozen sibling tickets were building in parallel worktrees, with a load average between 50 and
75 and frequent single-digit CPU share for `go` processes. Numbers below are wall-clock under
that load, not idle-machine figures.

- `make sdk-check sdk-test sdk-build sdk-version-check` — green. `sdk-check` (tsc) clean,
  87 tests in 6 files pass (81 before this stage), the build writes `sdk/dist`, and
  `sdk-version-check` agrees the manifest and `sdk/src/version.ts` still say `0.1.0`.
- `make docs-build` — 44 pages built and `All internal links are valid.` (43 pages before the
  new guide), so the guide's six internal links and its new sidebar entry resolve.
- `make generate-api generate-types generate-api-reference` — no diff at all (stage 2's
  regenerated artifacts are current); then `make generate-api-check generate-types-check
  generate-api-reference-check` — clean.
- `make generate-config-reference && make generate-config-reference-check`, `make
  coordinates-check` — clean.
- `(cd web && pnpm install --frozen-lockfile && pnpm check && pnpm test)` — `0 ERRORS
  0 WARNINGS` over 1517 files, 514 tests in 44 files pass. Run because the branch carries a
  generated-client diff under `web/`.
- `gofmt -l $(git diff --name-only main...HEAD -- '*.go')` prints nothing; `go vet ./...` and
  `go build ./...` clean.
- `go test -race -count=1 ./internal/idempotency/... ./internal/repository/... ./internal/database/... ./internal/config/... ./internal/api/... ./cmd/kilasflow/... ./internal/guardrails/... ./scripts/...`
  — ok for every package except `internal/api/handlers`, which panicked with
  `test timed out after 10m0s` while `TestLoginRefusesRepeatedGuessesAtOneAccount` was running
  at the 5m10s mark. That package is not part of this change beyond three handler files
  (`workflows.go`, `datastores.go`, `idempotency.go`); the timed-out test is in `auth_test.go`,
  which `git diff main...HEAD` does not touch, and it is one of the token-bucket sign-in tests
  that spends real minutes waiting for a bucket to refill. Proven a load artifact rather than a
  regression: the same package rerun unchanged with `-timeout 30m` is
  `ok ... 199.927s`, and the two throttle tests run in isolation pass on both trees —
  `ok ... 124.698s` here and `ok ... 67.990s` on a clean detached checkout of `main`
  (`72d0200`), the same tests at half the speed under this worktree's extra load. Nothing was
  skipped, weakened or re-pinned: the timeout was raised, no test changed.
- PostgreSQL: a scratch `pgvector/pgvector:pg17` container (`kf-pg-hj8pyx`, port bound with
  `-p 0:5432`, reached at `127.0.0.1:32796`), and
  `KILASFLOW_TEST_POSTGRES_DSN=postgres://kilas:hunter2@127.0.0.1:32796/kilasflow?sslmode=disable go test -race -count=1 -p 1 ./internal/database/... ./internal/repository/... ./internal/idempotency/...`
  — `ok` for all three (12.8s / 109.9s / 11.9s). That is what runs migration 000015 up and
  down and the AutoMigrate-drift test on PostgreSQL as well as SQLite. The container was
  removed afterwards.
- Broad run `go test -race -count=1 -timeout 30m ./...` — see the outcome appended below.
- Manual runtime proof (a scratch script under `/tmp`, nothing added to the repo): built
  `./cmd/kilasflow`, booted it on a `mktemp -d` SQLite database with `KILASFLOW_LOG_LEVEL=info`,
  created a manual-trigger workflow, and POSTed the same body twice under one
  `Idempotency-Key`. Both responses were `202` with the same execution id
  (`exec_01a0be59-8742-…`), the second carried `Idempotent-Replayed: true` and the first
  carried no such header, `GET /executions` listed exactly one, and
  `sqlite3 k.db 'select count(*) from idempotency_keys'` returned `1` (its `expires_at` ahead of
  `created_at`, the lease). A third POST with a different body under the same key answered
  `409` with `errors[0].value.code = idempotency_key_reused`. `SIGTERM` produced
  `msg="idempotency sweeper started" interval=10m0s` and then
  `msg="idempotency sweeper stopped"` in the log. The temp directory was removed.
- SDK end-to-end proof against that same real binary, through the built `sdk/dist` transport:
  `runWorkflow(..., {idempotencyKey})` twice returned the same execution id, the replayed body
  carried no `input` echo, a different body under the same key was a `409
  idempotency_key_reused`, `insertDatastoreRow(..., {idempotencyKey})` twice returned row id `1`
  both times with exactly one row in the listing, and an empty key was refused client-side with
  the `Idempotency-Key` message before any request. This is the one proof that exercises the new
  SDK option against the real server rather than a recording fetch.
- Broad run `go test -race -count=1 -timeout 30m ./...` (1175s wall, load average ~60) — every
  package passed except four. Each failing test lives in a file this branch does not touch, and
  each failure was traced to a known load flake or to an upstream fix this branch's base
  predates — checked, not assumed:
  - `internal/api/handlers` — `--- FAIL: TestLoginRefusesASprayFromOneAddress (181.09s)
    auth_test.go:162: no refusal within 30 attempts`. That is `BUG-fng4m2` almost verbatim (its
    own reproduction reads "FAIL after 179.61s, no refusal within 30 attempts"), the
    wall-clock-dependent sign-in throttle that `main` fixed after this branch's base with
    `67c6c900 BUG-fng4m2: hold the sign-in throttle's clock still …` and `8bbff74 BUG-rpkjpy:
    the sign-in throttle tests stop racing their own refill`. Neither test file is touched by
    this branch. On this base they can also exceed the package budget under load: one earlier run
    panicked with `test timed out after 10m0s`, a rerun with `-timeout 30m` was
    `ok ... 199.927s`, and this one failed at 368s. The tests themselves pass on both trees in
    isolation (`ok ... 124.698s` here, `ok ... 67.990s` on clean main). Raising the timeout was
    the only change made to any gate; no test was skipped, weakened, deleted or re-pinned.
  - `internal/database` — `TestConcurrentStartsApplyTheBaselineExactlyOnce (0.75s): concurrent
    starter 0: adopt existing schema as migration 000001_baseline: duplicated key not allowed`.
    This is the flake the board already tracks as `BUG-p3t7yq` (proven there on main `72d0200`),
    and I reproduced it on a clean detached `main` myself: `go test -race -count=20`-per-round,
    three interleaved rounds, **0 failures on this branch and 1 on main** (60 runs each). It is a
    race in `adoptExistingSchema` among four concurrent starters on one SQLite file, not a
    property of migration `000015` — adoption happens before any later migration runs.
  - `internal/engine` — `TestResumeOfAPerItemSuspendProcessesEveryItem` and
    `TestExpiredWaitsResolveOnTheirOwnDeadline` failing with `approval request has expired: the
    deadline is in the past`. That is `BUG-w8h3km`, which this branch predates: `main` has since
    landed `c2b856a BUG-w8h3km: drive wait-service deadlines through a clock/timer seam`. Both
    tests pass in isolation here (`ok ... 3.109s`).
  - `pkg/sdk` — `TestExamplePackRunsUnderWazero (190.49s): Execute() error = code exceeded its
    10s time limit`. Not a package this change touches; `BUG-fvdz46`'s notes prove the identical
    assertion fails on clean main at more than ten times the test's budget, and `BUG-9s3htg`
    tracks the same class.
  - The `internal/api/handlers` failure is the same story: the wall-clock-dependent sign-in
    throttle tests (`TestLoginRefusesASprayFromOneAddress`, `TestLoginRefusesRepeatedGuessesAtOneAccount`)
    are `BUG-fng4m2` / `BUG-rpkjpy`, fixed on `main` after this branch's base by `67c6c900` and
    `8bbff74`. On this branch's base they can exceed the package budget under load: one run
    panicked with `test timed out after 10m0s`, a rerun with `-timeout 30m` was `ok ... 199.927s`,
    and a later run failed at 368s. The tests themselves pass on both trees in isolation
    (`ok ... 124.698s` here, `ok ... 67.990s` on clean main), and neither test file is touched by
    this branch. Raising the timeout was the only change made to any gate; no test was skipped,
    weakened, deleted or re-pinned.

### Decisions taken (unchanged since stage 1, recorded here for the integrator)

- **Claim-first.** The key is claimed with a unique-constraint insert before the side effect
  runs, and the first `2xx` outcome (status + body) is recorded after it. The claim is a
  separate autocommit statement, never a transaction spanning the work: SQLite runs this server
  on one connection, so an open transaction plus a write through the same handle would deadlock.
- **In-flight is a 409, not a wait.** A duplicate that arrives while the first request runs gets
  `409 idempotency_key_in_flight` with `Retry-After` immediately; the server holds no goroutine
  or connection on a guess. The lease is two minutes.
- **Failure releases the key.** Only `2xx` outcomes are recorded, so a corrected retry can
  reuse the key. A `4xx`/`5xx` from the work itself is untouched by the layer.
- **A recorded outcome has a compact form.** Above the 1 MiB cap the handler stores the durable
  identity (row id, or `inserted`/`matched` with an empty `rows` list) instead of freeing the
  key, so protection survives a large response. A run replay drops the redacted `input` echo,
  so caller input does not outlive `execution.retention` in a second table.
- **Retention is the only config key**: `idempotency.retention`, default 24h, bounded
  1m..720h, `KILASFLOW_IDEMPOTENCY_RETENTION`. The two-minute lease, the 1 MiB cap and the
  ten-minute sweep interval are fixed constants.
- **Keys are tenant-scoped**, shared by every credential of the tenant including embed
  sessions, and the request hash is salted per tenant so it is not a cross-tenant oracle.
- **Migration number 000015 lives only in the file names**
  (`migrations/{sqlite,postgres}/000015_idempotency_keys.{up,down}.sql`); nothing else in the
  tree hard-codes it, so the integrator's renumbering is a rename.

### Learnings for the orchestrator

- The SQLite driver stores a `time.Time` as zone-tagged text, so every bound time in the store
  and the service has to be `.UTC()`. A UTC+7 `time.Now()` was stored as `+07:00` and compared
  hours wrong: a bug that only a non-UTC clock and a non-UTC test would catch.
- A recorded outcome needs a compact form. Any fixed cap is smaller than some legitimate
  response — a run echoes its input, and huma's request cap plus JSON escaping can exceed 1 MiB
  for an ordinary insert — and a cap that frees the key silently re-opens the duplicate the
  feature exists to prevent.
- A test of a startup helper does not prove `run()` calls it. The helper test passed while the
  call site could be deleted by a merge, which is why there is an AST test over `main.go`.
- `handlers/datastores.go`'s `problem()` maps any error that is not from a non-stdlib,
  non-module package to `422` with the error text, so a new layer must type its own failures:
  `*idempotency.StoreError` is mapped to a `500` before that path can see it.
- Load is a first-class input to the gate list here. `internal/api/handlers` exceeds Go's
  default ten-minute package timeout on this loaded machine (the token-bucket sign-in tests wait
  real minutes), while passing in isolation on both `main` and this branch. Record the load next
  to the timing, and raise a timeout rather than touching a test.

### Hand-offs: sentences other tickets own (do not edit them here)

- `.pine/tickets/FEAT-mha6a0.md` line 142 (`A run is not idempotent until FEAT-hj8pyx lands;
  pass Idempotency-Key once it does`) is now stale in the right direction: the header ships, so
  the skills bundle can tell agents to pass one.
- `.pine/tickets/FEAT-mha6a0.md` line 155 lists idempotency among the capabilities to present
  as **not shipped**; it now ships.
- `docs/superpowers/specs/2026-09-20-agent-surface-design.md` line 153 (`[--idempotency-key
  <key>]` in the CLI sketch) is implementable as written; line 406 (`Idempotent runs: a retry is
  a second execution … FEAT-hj8pyx`) is satisfied; line 438 (`idempotency pass-through` in the
  phase-2 token work) still applies to that ticket.
- Line 220's conflict table says a `409` means "re-read, then decide". That is right for
  optimistic-concurrency conflicts and wrong for `idempotency_key_in_flight`, where the remedy
  is to wait `Retry-After` and retry with the **same** key, and for `idempotency_key_reused`,
  where no retry helps and a new key is the fix.
- `FEAT-c72set` (in the main checkout's board, not in this worktree's) carries
  `- [ ] Idempotency guidance matches what shipped (FEAT-hj8pyx): Idempotency-Key on run and
  datastore writes` — that criterion can now be satisfied from
  `docs/src/content/docs/guides/idempotency.md`.
- Nothing in this ticket needed to touch the two pages `BUG-vzzkg3` owns
  (`guides/community-nodes.md`, `concepts/tenancy-and-embedding.md`): neither mentions
  idempotency or the purge.
- FEAT-fpqvwx's schema-driven purge completeness test will find `idempotency_keys` (a real
  `tenant_id` column) and must keep the hook: `GORMExecutionStore.PurgeTenant` and
  `IdempotencyRepository.PurgeTenant` share one helper and report through
  `TenantPurgeResult.IdempotencyKeys`.

### Stage 2 — what changed

- `internal/api/handlers/idempotency.go` (new) — `idempotencyProblem` maps the layer's own
  failures onto the repository's problem convention: `ErrKeyReused` → 409 `Conflict` with
  `errors[0].value.code = idempotency_key_reused` at `header.Idempotency-Key`,
  `*InFlightError` → the same shape with `idempotency_key_in_flight` plus a `Retry-After`
  header (`huma.ErrorWithHeaders`, at least one second), `ErrInvalidKey` → 422 at the same
  location, `*StoreError` → `serverProblem` 500 with the cause logged and never sent. Any
  other error reports false so the handler's own `problem()` still answers a missing
  workflow 404 and a bad row value 422. Also `idempotencyKeyDoc`, `replayedHeader` and
  `idempotencyUnavailable` (503 when a key arrives at an instance with no service).
- `internal/api/handlers/workflows.go` — `IdempotencyKey` header on `runWorkflowInput`
  (1-255, `pattern:"^[!-~]+$"`); a dedicated `runWorkflowOutput` carrying the
  `Idempotent-Replayed` header, because `executionRequestOutput` is shared with
  `Executions.Cancel`, which has no key and must not advertise the marker. `Run` keeps
  `available(true)` and `embedStoredProblem` first (a replay is held to the same embed
  confinement as the request it replays), then queues through `idempotency.Do`; the
  recorded form drops the `input` echo so a caller's payload does not outlive
  `execution.retention` in a second table. `WithIdempotency` builder.
- `internal/api/handlers/datastores.go` — the header on `insertRowInput` and
  `upsertRowInput`; `Replayed` on `createdRowOutput` and `upsertRowOutput`; `InsertRow`
  records the row and, above the 1 MiB outcome cap, just the id (so a replay still answers
  with the same `Location`, rebuilt by `rowLocation`/`rowIDOf` from the decoded JSON);
  `UpsertRow` records the body and, above the cap, `inserted`/`matched` with an empty `rows`
  list. `WithIdempotency` builder.
- `internal/api/server.go`, `internal/api/routes.go` — `Deps.Idempotency` (nil makes a keyed
  request answer 503 rather than run unprotected) wired into `NewWorkflows` and
  `NewDatastores`.
- `cmd/kilasflow/main.go` — `idempotency.NewService(repository.NewIdempotencyStore(db.DB),
  Options{Retention: cfg.Idempotency.Retention, Log: log})` beside the execution store;
  `startIdempotencySweeper(ctx, role, service, log)` returns nil unless the role runs the
  API, called beside `startExecutionPruner` and deferred `Stop()` (LIFO, so the sweep ends
  before the database close); `Idempotency:` in the `api.Deps` literal.
- `internal/api/middleware/cors.go` — `corsExposedHeaders` gains `Idempotent-Replayed` and
  `Retry-After` (neither is CORS-safelisted, so a browser host could not read the replay
  marker or the documented in-flight retry hint without them). The two existing assertions
  were updated to the new literal, still strict equality.
- `scripts/generate-api-reference.mjs` — a curated "Idempotency conflicts" section on the
  errors page (that page's prose is curated, not derived), then `make generate-api
  generate-types generate-api-reference` regenerated: the `Idempotency-Key` parameter appears
  in `docs/.../reference/api/{workflows,datastores}.md`, the operation descriptions changed in
  `web/src/lib/api/generated/**` and `sdk/src/generated/models.ts`, and the errors page gained
  the section. orval drops the header parameter from both generated clients, as it already
  does for `If-Match`; the reference pages list it.

### Stage 2 — how it was verified

Written test-first: `internal/api/idempotency_test.go` and `cmd/kilasflow/idempotency_test.go`
were added and run before the wiring existed (red: `unknown field Idempotency in struct
literal of type api.Deps`), then implemented.

- `go test -race -count=1 ./internal/api/ ./internal/api/middleware/ ./internal/idempotency/
  ./internal/repository/ ./cmd/kilasflow/ ./internal/guardrails/` — all ok. `./internal/api/
  handlers/` ran green except `TestLoginRefusesASprayFromOneAddress`, which failed under load
  ("no refusal within 30 attempts"); that test passes in isolation here and fails identically
  on a clean detached checkout of `main`, so it is a pre-existing load-sensitive flake in the
  login-throttle test, unrelated to this change.
- `go test -race -count=1 ./internal/config/... ./internal/database/... ./scripts/...` — ok.
- `KILASFLOW_TEST_POSTGRES_DSN=... go test -race -count=1 -p 1 ./internal/database/...
  ./internal/repository/... ./internal/idempotency/...` — ok against a scratch
  `pgvector/pgvector:pg17` container (`kf-pg-feat-hj8pyx`). The API tests are SQLite-only by
  construction, so the HTTP layer's PostgreSQL proof is the service tests plus the
  dialect-independent handler code.
- `gofmt -l` on every changed Go file prints nothing; `go vet ./...` and `go build ./...`
  clean; `make generate-config-reference-check` and `make coordinates-check` clean.
- `make generate-api-check generate-types-check generate-api-reference-check` — clean after
  committing the regenerated output; `(cd web && pnpm check && pnpm test)` — 0 errors, 514
  tests pass; `make sdk-check sdk-test sdk-version-check` — ok (81 tests).
- The AST wiring test was seen failing before it could pass: replacing the
  `startIdempotencySweeper` call in `run()` makes
  `TestRunWiresTheIdempotencyServiceAndItsSweeper` fail with "run() does not start the
  idempotency sweeper". The file was restored.
- Manual runtime proof: a built binary on a temp SQLite database logged
  `idempotency sweeper started`, two `POST /api/v1/workflows/{id}/run` calls with one
  `Idempotency-Key` returned the same execution id (the second with `Idempotent-Replayed:
  true` and no `input` echo), a third with a different body answered `409
  idempotency_key_reused`, the database held exactly one `idempotency_keys` row and one
  execution, and SIGTERM logged `idempotency sweeper stopped` before `shutdown complete`.

### Stage 1 — what changed

- `migrations/{sqlite,postgres}/000015_idempotency_keys.{up,down}.sql` — table
  `idempotency_keys` with a unique index on (`tenant_id`,`idempotency_key`), an index on
  `expires_at`, `tenant_id` as a real plain column (FEAT-fpqvwx's schema-driven purge test
  finds it), no FK to executions (retention is independent), and `expires_at` serving as both
  the in-flight lease and the retention deadline. Spaces, not tabs.
- `internal/repository/idempotency.go` — `idempotencyKeyModel`, `IdempotencyRepository` and
  `GORMIdempotencyStore`: claim-first (`ON CONFLICT DO NOTHING` insert, then read; never a
  transaction, because SQLite is one connection and the work writes to the same database),
  `Complete`/`Release` guarded by the claim token and the `in_progress` state, batched `Sweep`,
  and `PurgeTenant`. Every statement goes through the GORM model so `database.table_prefix`
  applies, and every bound time is converted to UTC.
- `internal/repository/tenant_purge.go` — the existing `GORMExecutionStore.PurgeTenant`
  transaction also deletes the tenant's keys through the same helper, reported in the new
  `TenantPurgeResult.IdempotencyKeys`.
- `internal/idempotency/` (new) — the service: `Do` (claim, run, record; 409-able
  `ErrKeyReused`, typed `*InFlightError` with a Retry-After, `*StoreError` for the layer's own
  failures, `Response.Record`/`Response.Compact` so an oversized outcome still records a replay
  instead of silently freeing the key), `Hash` (canonical JSON, `UseNumber`, salted with tenant
  and key), `ValidKey`, and `StartSweeper`/`Stop` (Stop waits for the sweep in flight and is
  idempotent).
- `internal/config/config.go` — `idempotency.retention`, default 24h, bounded 1m..720h,
  `KILASFLOW_IDEMPOTENCY_RETENTION`; regenerated `config.example.yaml` and
  `docs/src/content/docs/operate/configuration-reference.md` through
  `make generate-config-reference`.
- Registration edits: `Models()`, `table_names_test.go`, `migrate_test.go`
  (`postBaselineTables`), `postgres_execution_test.go` (cleanup).

### Stage 1 — how it was verified

Written test-first: the service tests were run against stubbed functions returning
`errors.New("not implemented")` and failed for that reason before the implementation landed.

- `go test -race -count=1 ./internal/idempotency/... ./internal/repository/... ./internal/database/... ./internal/config/... ./internal/guardrails/... ./scripts/...` — all ok (SQLite).
- `KILASFLOW_TEST_POSTGRES_DSN=postgres://kilas:hunter2@127.0.0.1:<port>/kilasflow?sslmode=disable go test -race -count=1 -p 1 ./internal/database/... ./internal/repository/... ./internal/idempotency/...` — all ok against a scratch `pgvector/pgvector:pg17` container, which is what runs the migration up/down tests and the AutoMigrate-drift tests with the new table.
- `gofmt -l` on every changed Go file prints nothing; `go vet ./...` and `go build ./...` clean; `make generate-config-reference-check` clean.
- The time-zone tests were mutation-checked: dropping `.UTC()` from the store's `Claim` makes `TestIdempotencyKeysIgnoreTheCallersTimeZoneOnEveryDriver` fail (a `+07:00` text sorts hours late), and dropping it from both layers makes the service replay and retention tests fail. Both files were restored afterwards.
- `go test -race ./internal/guardrails/...` passes and no new dependency was added (stdlib only: `crypto/rand`, `crypto/sha256`, `encoding/json`, `hash`, plus the existing GORM).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `794adbb3` (last commit at or before ticket created 2026-09-20)
- Commits (3):
  - `c1bcc17d` — FEAT-hj8pyx: honour Idempotency-Key on run and datastore writes
  - `b3c9f3f9` — chore(pine): start the board — the open tickets move to doing before the parallel wave
  - `f8156140` — chore(pine): record the ticket board — the new tickets, memory entries and their notes
- Files changed (base → working tree):

```
 .github/actions/js-toolchain/action.yml            |   11 +-
 .github/workflows/ci.yml                           |   25 +-
 .github/workflows/release.yml                      |  120 +-
 .pine/MEMORY.md                                    |    1 +
 .pine/memory/embedding.md                          |    8 +
 .pine/tickets/BUG-fng4m2.md                        |   88 ++
 .pine/tickets/BUG-fvdz46.md                        |  400 +++++++
 .pine/tickets/BUG-p3t7yq.md                        |   30 +
 .pine/tickets/BUG-t9j2ek.md                        |  452 ++++++++
 .pine/tickets/BUG-vzzkg3.md                        |   54 +
 .pine/tickets/BUG-w8h3km.md                        |  123 ++
 .pine/tickets/BUG-xmr673.md                        |  737 ++++++++++++
 .pine/tickets/EPIC-bkj6yf.md                       |   46 +
 .pine/tickets/EPIC-m0bne8.md                       |   57 +
 .pine/tickets/EPIC-r0yg5q.md                       |   27 +
 .pine/tickets/FEAT-3taswf.md                       | 1181 +++++++++++++++-----
 .pine/tickets/FEAT-48hreg.md                       |   20 +-
 .pine/tickets/FEAT-4jns31.md                       |   26 +
 .pine/tickets/FEAT-4ve1bq.md                       |   67 ++
 .pine/tickets/FEAT-77rveq.md                       |   73 ++
 .pine/tickets/FEAT-7cg0cd.md                       |   20 +-
 .pine/tickets/FEAT-8mymac.md                       |    4 +-
 .pine/tickets/FEAT-bb4s6e.md                       |   27 +
 .pine/tickets/FEAT-bp59m4.md                       |  420 +++++++
 .pine/tickets/FEAT-c72set.md                       |   26 +
 .pine/tickets/FEAT-cwmw90.md                       |  513 ++++++++-
 .pine/tickets/FEAT-emf6k5.md                       |  573 ++++++++++
 .pine/tickets/FEAT-ew46cb.md                       |   26 +
 .pine/tickets/FEAT-fpqvwx.md                       |   57 +
 .pine/tickets/FEAT-g07pj8.md                       |   67 ++
 .pine/tickets/FEAT-hj8pyx.md                       |  470 ++++++++
 .pine/tickets/FEAT-m4d2y1.md                       |   30 +
 .pine/tickets/FEAT-mha6a0.md                       |  259 +++++
 .pine/tickets/FEAT-p77zr3.md                       |   67 ++
 .pine/tickets/FEAT-qdedm0.md                       |  993 +++++++++++++++-
 .pine/tickets/FEAT-x5qqpm.md                       |   26 +
 .pine/tickets/FEAT-yxwyav.md                       |   26 +
 CHANGELOG.md                                       |   97 ++
 CONTRIBUTING.md                                    |    3 +
 Makefile                                           |   53 +
 README.md                                          |   11 +-
 cmd/kilasflow/embed_issuer.go                      |   18 +
 cmd/kilasflow/embed_issuer_test.go                 |  134 +++
 cmd/kilasflow/fleet.go                             |   92 ++
 cmd/kilasflow/fleet_test.go                        |  395 +++++++
 cmd/kilasflow/idempotency_test.go                  |  225 ++++
 cmd/kilasflow/main.go                              |  147 ++-
 cmd/kilasflow/webhook_wiring_test.go               |  138 +++
 config.example.yaml                                |   67 +-
 docs/src/content/docs/concepts/credentials.md      |   21 +-
 docs/src/content/docs/concepts/execution-model.md  |    1 +
 docs/src/content/docs/concepts/node-registry.md    |   40 +
 docs/src/content/docs/concepts/webhooks.md         |  107 +-
 docs/src/content/docs/guides/embedding.md          |   32 +-
 docs/src/content/docs/guides/idempotency.md        |  197 ++++
 docs/src/content/docs/guides/node-authoring.md     |    6 +
 .../src/content/docs/guides/tenant-scoped-nodes.md |  183 +++
 .../docs/operate/configuration-reference.md        |  111 +-
 docs/src/content/docs/operate/deployment.md        |   10 +-
 docs/src/content/docs/operate/security.md          |   23 +-
 docs/src/content/docs/operate/upgrades.md          |   61 +-
 docs/src/content/docs/reference/api-contract.md    |   34 +-
 docs/src/content/docs/reference/api.md             |    4 +-
 docs/src/content/docs/reference/api/datastores.md  |    6 +-
 docs/src/content/docs/reference/api/errors.md      |   13 +-
 docs/src/content/docs/reference/api/nodes.md       |   16 +-
 docs/src/content/docs/reference/api/system.md      |    3 +-
 docs/src/content/docs/reference/api/workflows.md   |   24 +-
 docs/src/content/docs/reference/cli.md             |  628 +++++++++++
 docs/src/content/docs/reference/node-packs.md      |   15 +-
 docs/src/content/docs/start/install.md             |   12 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   11 +-
 .../specs/2026-09-20-agent-surface-design.md       |  519 +++++++++
 e2e/fixtures/live-backend.ts                       |   12 +-
 e2e/helpers/stub.ts                                |    8 +
 e2e/tests/dashboard-lists.spec.ts                  |  187 ++++
 e2e/tests/live-backend-api.spec.ts                 |   52 +-
 e2e/tests/live-backend-http-auth.spec.ts           |   94 ++
 e2e/tests/live-backend-webhook.spec.ts             |  117 +-
 go.mod                                             |    2 +
 go.sum                                             |    4 +
 internal/api/cors_test.go                          |    4 +-
 internal/api/embed_defaults_test.go                |  150 +++
 internal/api/handlers/auth_test.go                 |   63 +-
 internal/api/handlers/datastores.go                |  183 ++-
 internal/api/handlers/idempotency.go               |  115 ++
 internal/api/handlers/interop.go                   |    6 +-
 internal/api/handlers/nodes.go                     |   76 +-
 internal/api/handlers/problem.go                   |   17 +
 internal/api/handlers/system.go                    |  142 ++-
 internal/api/handlers/workflows.go                 |  166 ++-
 internal/api/idempotency_test.go                   | 1004 +++++++++++++++++
 internal/api/middleware/cors.go                    |   11 +-
 internal/api/middleware/cors_test.go               |    2 +-
 internal/api/middleware/embed.go                   |    3 +-
 internal/api/middleware/loginlimit.go              |   13 +
 internal/api/middleware/loginlimit_test.go         |    3 +-
 internal/api/node_visibility_test.go               |  544 +++++++++
 internal/api/ready_fleet_test.go                   |  358 ++++++
 internal/api/routes.go                             |   14 +-
 internal/api/server.go                             |    8 +-
 internal/api/workflows_test.go                     |   71 +-
 internal/cli/api_prefix.go                         |   43 +
 internal/cli/cli.go                                |  382 +++++++
 internal/cli/cli_test.go                           |  248 ++++
 internal/cli/client.go                             |  397 +++++++
 internal/cli/client_test.go                        |  320 ++++++
 internal/cli/command.go                            |  139 +++
 internal/cli/command_test.go                       |  302 +++++
 internal/cli/config.go                             |  258 +++++
 internal/cli/config_test.go                        |  749 +++++++++++++
 internal/cli/context.go                            |  318 ++++++
 internal/cli/context_test.go                       |  236 ++++
 internal/cli/doc.go                                |   35 +
 internal/cli/exit.go                               |  113 ++
 internal/cli/exit_test.go                          |  101 ++
 internal/cli/flags.go                              |   84 ++
 internal/cli/guard.go                              |   43 +
 internal/cli/guard_test.go                         |  212 ++++
 internal/cli/openapi.go                            |  202 ++++
 internal/cli/openapi_contract_test.go              |  411 +++++++
 internal/cli/output.go                             |  116 ++
 internal/cli/output_test.go                        |  246 ++++
 internal/cli/sse.go                                |  151 +++
 internal/cli/sse_test.go                           |  149 +++
 internal/cli/verbs_api.go                          |  315 ++++++
 internal/cli/verbs_api_test.go                     |  820 ++++++++++++++
 internal/cli/verbs_auth.go                         |  289 +++++
 internal/cli/verbs_credential.go                   |  146 +++
 internal/cli/verbs_credential_test.go              |  119 ++
 internal/cli/verbs_datastore.go                    |  209 ++++
 internal/cli/verbs_datastore_test.go               |  215 ++++
 internal/cli/verbs_exec.go                         |  413 +++++++
 internal/cli/verbs_exec_test.go                    |  436 ++++++++
 internal/cli/verbs_node.go                         |  292 +++++
 internal/cli/verbs_node_test.go                    |  196 ++++
 internal/cli/verbs_pack.go                         |  143 +++
 internal/cli/verbs_pack_test.go                    |  195 ++++
 internal/cli/verbs_run.go                          |  257 +++++
 internal/cli/verbs_run_test.go                     |  314 ++++++
 internal/cli/verbs_schedule.go                     |   36 +
 internal/cli/verbs_schedule_test.go                |   46 +
 internal/cli/verbs_system.go                       |  282 +++++
 internal/cli/verbs_system_test.go                  |  182 +++
 internal/cli/verbs_tenant.go                       |   86 ++
 internal/cli/verbs_tenant_test.go                  |  116 ++
 internal/cli/verbs_workflow.go                     |  592 ++++++++++
 internal/cli/verbs_workflow_test.go                |  425 +++++++
 internal/config/config.go                          |  160 ++-
 internal/config/config_test.go                     |   82 ++
 internal/config/embed_branding_test.go             |  192 ++++
 internal/config/embed_validate.go                  |   34 +
 internal/config/packs_visibility.go                |   83 ++
 internal/config/packs_visibility_test.go           |  168 +++
 internal/config/webhook_require_auth_test.go       |   50 +
 internal/credentials/builtin.go                    |   68 ++
 internal/credentials/credentials_test.go           |   58 +
 internal/credentials/registry.go                   |   63 ++
 internal/database/migrate_test.go                  |    1 +
 internal/database/webhook_route_backfill_test.go   |  491 ++++++++
 internal/datastore/concurrency.go                  |    5 +-
 internal/datastore/doc.go                          |    4 +-
 internal/datastore/engine.go                       |   17 +-
 internal/datastore/fleet.go                        |  364 +++++-
 internal/datastore/fleet_engine_test.go            |  711 ++++++++++++
 internal/datastore/rows.go                         |   25 +-
 internal/embed/embed.go                            |  121 +-
 internal/embed/embed_branding_test.go              |  164 +++
 internal/embed/embed_lifetime_test.go              |  146 +++
 internal/engine/export_test.go                     |   20 +
 internal/engine/service.go                         |   30 +-
 internal/engine/tenant_visibility_test.go          |  379 +++++++
 internal/engine/wait_service.go                    |   47 +-
 internal/engine/wait_service_test.go               |  219 +++-
 internal/guardrails/compile_scope_test.go          |  440 ++++++++
 internal/idempotency/hash.go                       |   64 ++
 internal/idempotency/hash_test.go                  |  142 +++
 internal/idempotency/idempotency.go                |  432 +++++++
 internal/idempotency/idempotency_test.go           | 1120 +++++++++++++++++++
 internal/idempotency/sweeper.go                    |   94 ++
 internal/idempotency/sweeper_test.go               |  146 +++
 internal/interop/n8n/parameters.go                 |    3 +-
 internal/node/registry.go                          |   31 +
 internal/node/registry_bench_test.go               |  112 ++
 internal/node/visibility.go                        |  347 ++++++
 internal/node/visibility_test.go                   |  796 +++++++++++++
 internal/nodepack/nodepack.go                      |   18 +
 internal/nodepack/trigger.go                       |    8 +
 internal/nodepack/trigger_require_auth_test.go     |   43 +
 internal/nodepack/validate.go                      |   13 +-
 internal/nodepack/visibility_test.go               |  262 +++++
 internal/repository/idempotency.go                 |  360 ++++++
 internal/repository/idempotency_test.go            |  615 ++++++++++
 internal/repository/models.go                      |   11 +-
 internal/repository/postgres_execution_test.go     |    1 +
 internal/repository/table_names_test.go            |    1 +
 internal/repository/tenant_purge.go                |   33 +-
 internal/repository/webhooks.go                    |   79 +-
 internal/repository/webhooks_test.go               |  184 ++-
 internal/webhook/jwt.go                            |  144 +++
 internal/webhook/jwt_test.go                       |  212 ++++
 internal/webhook/require_auth.go                   |   74 ++
 internal/webhook/require_auth_test.go              |  367 ++++++
 internal/webhook/route_label_test.go               |  172 +++
 internal/webhook/shape.go                          |   27 +-
 internal/webhook/shape_test.go                     |   30 +
 internal/webhook/webhook.go                        |   83 +-
 internal/webhook/webhook_test.go                   |  194 +++-
 internal/workflow/catalog_scope.go                 |   39 +
 internal/workflow/compiler.go                      |   24 +-
 internal/workflow/compiler_visibility_test.go      |  280 +++++
 .../000014_webhook_route_backfill.down.sql         |   14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |   62 +
 .../postgres/000015_idempotency_keys.down.sql      |    6 +
 migrations/postgres/000015_idempotency_keys.up.sql |   49 +
 .../sqlite/000014_webhook_route_backfill.down.sql  |   14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |   58 +
 migrations/sqlite/000015_idempotency_keys.down.sql |    6 +
 migrations/sqlite/000015_idempotency_keys.up.sql   |   48 +
 nodes/core.go                                      |    5 +
 nodes/error_workflow.go                            |    4 +
 nodes/http.go                                      |    2 +
 nodes/presentation_test.go                         |   35 +
 nodes/webhook.go                                   |   18 +-
 scripts/check-coordinates.sh                       |   21 +
 scripts/generate-api-reference.mjs                 |   17 +-
 scripts/smoke-cli.sh                               |  228 ++++
 sdk/CHANGELOG.md                                   |   13 +-
 sdk/LICENSE                                        |  202 ++++
 sdk/README.md                                      |   92 +-
 sdk/RELEASING.md                                   |  188 ++++
 sdk/examples/host-page/README.md                   |   64 +-
 sdk/examples/host-page/server.mjs                  |   68 +-
 sdk/package.json                                   |   11 +-
 sdk/pnpm-lock.yaml                                 |    3 +
 sdk/scripts/check-example.mjs                      |  320 ++++++
 sdk/scripts/check-package.mjs                      |  315 ++++++
 sdk/scripts/lib/pack.mjs                           |   77 ++
 sdk/scripts/lib/release.mjs                        |  266 +++++
 sdk/scripts/release.mjs                            |  149 +++
 sdk/src/generated/models.ts                        |  117 +-
 sdk/src/http.ts                                    |   35 +-
 sdk/src/server.ts                                  |  125 ++-
 sdk/test/operation-coverage.test.mjs               |   20 +
 sdk/test/release-workflow.test.mjs                 |  223 ++++
 sdk/test/release.test.mjs                          |  390 +++++++
 sdk/test/server.test.ts                            |  108 ++
 .../api/generated/datastore-rows/datastore-rows.ts |    4 +-
 .../generated/models/executionNodeRunResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |    3 +
 .../lib/api/generated/models/notReadyProblem.ts    |   31 +
 .../lib/api/generated/models/readyDatastores.ts    |   19 +
 .../api/generated/models/readyDatastoresSpread.ts  |   12 +
 .../lib/api/generated/models/readyOutputBody.ts    |    3 +
 web/src/lib/api/generated/nodes/nodes.ts           |    8 +-
 web/src/lib/api/generated/system/system.ts         |   18 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   97 ++
 web/src/lib/dashboard/cursor-page.test.ts          |  285 ++++-
 web/src/lib/dashboard/cursor-page.ts               |   92 +-
 web/src/lib/dashboard/execution-list.test.ts       |  148 ++-
 web/src/lib/dashboard/execution-list.ts            |   98 +-
 web/src/lib/dashboard/workflow-list.test.ts        |   20 +-
 web/src/lib/dashboard/workflow-list.ts             |   21 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |    9 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  143 ++-
 266 files changed, 39529 insertions(+), 976 deletions(-)
```
