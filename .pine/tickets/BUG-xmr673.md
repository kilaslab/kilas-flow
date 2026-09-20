---
id: BUG-xmr673
title: Datastore fleet migration runner is never started, and rows refuse any version mismatch
status: done
priority: high
labels:
    - datastore
    - storage
    - correctness
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:52Z"
updated: "2026-09-20T11:48:48Z"
---

## Problem

The per-datastore schema migration runner exists and nothing constructs it, while the row
store refuses to serve a datastore whose `schema_version` differs from the current one. The
first bump of `CurrentSchemaVersion` therefore takes every existing datastore offline with no
mechanism to migrate it — in an embedded deployment, that is a host's customer data going
dark after a routine upgrade.

## Evidence

- `internal/datastore/fleet.go:36-40` states it in the code: nothing starts a runner, the
  composition root does not construct one, and readiness does not report the fleet's version
  spread.
- `internal/datastore/rows.go:78-87` (`checkSchemaVersion`) refuses row operations on a
  mismatch; `refuseAhead` exists in the runner.
- `VersionSpread` (`fleet.go:148`) has no HTTP consumer.
- Repo-wide, the only callers of `NewFleetRunner` are tests.

## Acceptance criteria

- [x] The runner starts at boot after `database.Migrate`, in the composition root, and its
      failure refuses boot the way a failed schema migration does. (Stage 2:
      `cmd/kilasflow/fleet.go` + one call in `main.go`; proven by re-executing the real
      `main()` against seeded databases: exit status 1 with the runner's own sentence on
      stderr, SQLite and PostgreSQL.)
- [x] `GET /api/v1/ready` (or a documented operations endpoint) reports the version spread,
      and reports not-ready while a migration is outstanding. (Stage 3: `handlers.System`
      gains the optional `datastores` block and answers 503 while any datastore is behind;
      proven by `TestReadyReportsTheDatastoreVersionSpread`,
      `TestReadyIsNotReadyWhileADatastoreMigrationIsOutstanding` (503, then 200 once the
      datastore is restored), `TestReadyReportsButToleratesADatastoreAheadOfTheBuild`,
      `TestReadyWithoutADatastoreEngineOmitsTheBlock` and
      `TestReadyIsNotReadyWhenTheCatalogueCannotBeRead`, plus a curl of the built binary on
      SQLite and PostgreSQL.)
- [x] A test creates a datastore at the previous schema version, starts the runner, and proves
      the datastore is migrated and serves rows afterwards — instead of refusing traffic.
      (Stage 1: `TestMigrateFleetMovesAPreviousVersionDatastoreAndItServesRows`, SQLite and
      PostgreSQL.)
- [x] A datastore ahead of the binary is still refused, with a diagnostic naming it.
      (Stage 1, at the row gate and at `MigrateFleet`, exact sentence asserted; the boot-level
      demonstration through the real `main()` belongs to stage 2.)

## Out of scope

Adding a second schema version. This ticket makes the existing machinery real; the version
stays at 1.

## Implementation notes

### Stage 1 - datastore package: engine-owned target version, hardened runner, MigrateFleet and FleetStatus

What changed (all in `internal/datastore`, no migration, no config key, no dependency,
`CurrentSchemaVersion` still 1):

- `engine.go`: the engine now owns the version it serves. Two unexported fields,
  `schemaVersion` (default `CurrentSchemaVersion`) and `fleetSteps` (default
  `shippedFleetSteps()`), set by `NewEngine`; `Create` stamps `e.schemaVersion`. Nothing outside
  the package can set them, so the product still ships exactly one version, but a test can stand
  in for a future build.
- `rows.go`: `checkSchemaVersion(dsID, version, target)` takes the engine's target, so Create,
  the row gate, the runner and the status read one value. The ahead sentence is unchanged; the
  behind sentence lost "run the fleet migration first" (there is no manual command) for
  "the datastore fleet migration has not reached it yet (boot runs it; GET /api/v1/ready
  reports what is outstanding)". No test or handler matched the old text.
- `fleet.go`: `Engine.MigrateFleet(ctx)` and `Engine.FleetStatus(ctx)`; `FleetStatus{Version,
  Spread, Behind, Ahead}` with `Ready()` (== no datastore behind; ahead does not fail readiness)
  and `Problem()` (counts and versions only, never an id or a tenant); `shippedFleetSteps()` and
  `missingFleetSteps()` plus a tripwire test so bumping `CurrentSchemaVersion` without its step
  fails the build; `FleetRunner` gained `target`, `prefix` and a test-only `afterSelect` hook.
- The runner is hardened: `migrateOne` loops one step per transaction, and `advance` re-reads the
  catalogue row inside the step transaction (`SELECT ... FOR UPDATE` on PostgreSQL), skips when a
  peer moved or dropped the datastore first, hands the step the whole `Datastore` (prefixed
  `Table`, ordered `Columns`, the FROM version; the old runner left `Table` and `Columns` empty)
  and stamps the version with a compare-and-swap. Two processes booting together therefore
  apply each step once. Columns are read through `tx` (`columnsIn`), never the engine's handle,
  because a second connection deadlocks SQLite. `catalogue.go` is untouched.
- `concurrency.go`: one comment sentence noting the runner's re-read is the one deliberate
  `FOR UPDATE` (PostgreSQL only). `doc.go`: one note that `MigrateFleet` is the boot entry point.

Honest limits: there is no row lock on SQLite. The glebarez driver silently discards
`clause.Locking` (pinned by `TestDatastoreWritesTakeNoRowLocks`); SQLite installs are
single-process by construction (`validateRoleForDriver` refuses a split role) and the pool is one
connection, so the same re-read is safe there and no lock is claimed. Comments in `fleet.go`
describe the composition-root call and the readiness endpoint that stages 2 and 3 add.

Tests (new file `internal/datastore/fleet_engine_test.go`, every existing test untouched; per
driver, so PostgreSQL joins when `KILASFLOW_TEST_POSTGRES_DSN` is set): the previous-version
datastore migrates and serves rows (test-only step at version 2 creating a physical index and
backfilling titles, refusal sentences asserted whole before and after, the old build then
refuses the datastore as ahead, a datastore created by the next build is born at 2);
`FleetStatus` behind/ahead classification and the id-free problem text; two runners parked at a
barrier apply the step once, plus a 4-runner stress; the tripwire (and the function behind it
proven able to trip); failing step rolls back its DDL; multi-step gap advances one transaction per
step and resumes; a datastore dropped after it was read is skipped; the prefixed table name
reaches the step; nil/unconfigured engine.

Proof that the tests can fail: with the `afterSelect` hook wired and the old `migrateOne`, the
barrier test failed with `the step ran 2 times across two concurrent runs, want exactly 1` and
runs reported `[1 1]`; the stress test ran the step 13 times for 5 datastores; the dropped-after-read
test ran the step for a vanished datastore. Mutation check on PostgreSQL: with the
`clause.Locking` line removed the barrier test fails (2 invocations) and the stress test fails
(8 invocations for 5 datastores), while SQLite still passes because its single connection
serializes the transactions, so the lock is what enforces once-only on PostgreSQL.

Commands run and outcomes:

- baseline `go test ./internal/datastore/ -run 'Fleet|VersionSpread|RowOpsRefuse|WritesTakeNoRowLocks' -count=1 -race`: ok before any change.
- `gofmt -l internal/datastore`: empty. `go vet ./...`: clean. `go build ./...`: clean.
- `go test -race -count=1 ./internal/datastore/... ./internal/guardrails/... ./internal/api/... ./internal/database/... ./cmd/kilasflow/... ./internal/engine/... ./nodes/...`: all ok on SQLite.
- `go test -race -count=25 -run TestFleetConcurrentRuns ./internal/datastore/` (SQLite): ok; the same with `-count=10` against PostgreSQL: ok.
- scratch `pgvector/pgvector:pg17` container `kf-pg-xmr673`: `KILASFLOW_TEST_POSTGRES_DSN=... go test -race -p 1 -count=1 ./internal/datastore/... ./internal/database/...`: ok; container removed afterwards.
- `grep -n 'CurrentSchemaVersion = 1' internal/datastore/fleet.go`: still matches (line 27).

### Stage 2 - composition root: boot pass that refuses boot on failure, plus a resume tick

What changed (one new file plus one insertion; no migration, no config key, no dependency):

- `cmd/kilasflow/fleet.go` (new): `fleetMigrator` (the one method of `*datastore.Engine` this
  uses, so the wrapping and the retry loop are testable without a database),
  `migrateDatastoreFleet` (one pass; wraps a failure as `migrate the datastore fleet: %w` and
  logs `migrated datastores to the current schema version` with the count when it moved any),
  `runFleetResumer` (ticker loop, logs `resuming the datastore fleet migration` and continues,
  returns when ctx ends) and `bootDatastoreFleet` (synchronous first pass, then the ticker
  goroutine). `fleetResumeInterval = 30s`, a package constant: no config key, because the pass
  exists to clear a straggler an older peer creates during a rolling upgrade, and a knob nobody
  sets is a support surface with no owner.
- `cmd/kilasflow/main.go`: one call to `bootDatastoreFleet(ctx, datastoreEngine, log)` with its
  comment, immediately after the `SetLimits` block — i.e. after `database.Migrate` and after
  `NewEngine`, before anything else is constructed. Nothing else in main.go changed.

Why a resume tick at all: a boot-only pass would pin EVERY upgraded pod at 503 for a datastore an
older peer created behind after the pass, and readiness failures do not restart pods, so the
feature would cause the outage it exists to prevent. It mirrors `startHistorySweeper`. It is the
one addition beyond the literal brief and is flagged for the owner in the plan's
`decisionsForOwner`.

Tests (new file `cmd/kilasflow/fleet_test.go`, every existing test untouched):

- `TestMigrateDatastoreFleetIsANoOpOnACurrentFleet`: two passes over a fleet already current
  return nil and log nothing.
- `TestMigrateDatastoreFleetNamesTheDatastoreAheadOfTheBuild`: the whole sentence
  `migrate the datastore fleet: datastore: <id> is at schema version 99 but this build knows
  version 1` is asserted (bare digits would match any UUID).
- `TestMigrateDatastoreFleetLogsWhatItMigrated` and `TestMigrateDatastoreFleetWrapsTheFailure`:
  the two branches no second schema version can reach from package main, through a fake.
- `TestFleetResumerRetriesAndStops`: at least three calls, the failure logged, calls still
  increasing afterwards, return within 2s of cancel and no further calls after it.
- `TestBootRefusesADatastoreAheadOfTheBinary` (99) and `TestBootRefusesADatastoreTheBinaryHasNoStepFor`
  (0): the parent seeds a SQLite file, then re-executes this test binary's real `main()`
  (`-test.run=^TestBootHelperProcess$`) with every `KILASFLOW_*` variable scrubbed from the
  child's environment, a real free port (port 0 is refused by `config.Validate`) and a 20s
  deadline; asserted: exit status 1 and the full refusal sentence, so a child that died earlier
  (config, database open) cannot satisfy it.
- `TestBootRunsTheFleetPassAfterTheSchemaMigration`: the same child against a fresh SQLite path
  and a real port; polls `/api/v1/ready` to 200. A pass placed before `database.Migrate` would
  fail on a missing table and the child would exit 1 first, so this pins the ordering.

Proof the tests can fail (observed before the implementation existed, with a stub file whose
`migrateDatastoreFleet` returned nil and no `main.go` call): `migrateDatastoreFleet() error = nil,
want "migrate the datastore fleet: datastore: datastore_01a0be22-... is at schema version 99 but
this build knows version 1"`; `log = "", want the migration message`; `resumer made 0 calls in
5s, want at least 3`; and both boot tests `boot helper timed out` after 20s, because the child
booted a full server instead of refusing — exactly the red the plan predicted for a missing
`main.go` call. The no-op and fresh-boot tests passed under the stub by construction.

Commands run and outcomes (all after the implementation):

- `gofmt -l cmd/kilasflow internal/datastore internal/api`: empty.
- `go vet ./...`: clean. `go build ./...`: clean.
- `go test -race -count=1 -timeout 900s ./cmd/kilasflow/... ./internal/datastore/... ./internal/guardrails/...`:
  all ok (`cmd/kilasflow` 5.0s, `internal/datastore` 29.1s, `internal/guardrails` 3.7s).
- `make smoke-sqlite` (after `pnpm install --frozen-lockfile` in `web/`, which a fresh worktree
  does not have): `smoke-sqlite: passed` — the real binary booted against a fresh SQLite
  database and answered `/api/v1/ready`.
- Hand boot, SQLite: `KILASFLOW_SERVER_HOST=127.0.0.1 KILASFLOW_SERVER_PORT=18099
  KILASFLOW_DATABASE_DSN=$TMP/kf.db ./kilasflow -config ''` → ready
  `{"status":"ok","database":"ok"}`; created a datastore over the API; stopped; `sqlite3 $TMP/kf.db
  "UPDATE datastores SET schema_version = 99"`; restarted → exit status 1 and
  `kilasflow: migrate the datastore fleet: datastore: datastore_01a0be25-501c-... is at schema
  version 99 but this build knows version 1`. The same with `schema_version = 0` → exit status 1
  and `... is at schema version 0 but this build knows no step from version 0 to 1`.
- Hand boot, PostgreSQL (scratch `pgvector/pgvector:pg17` container `kf-pg-xmr673`, port from
  `docker port`): fresh boot ready `{"status":"ok","database":"ok"}`; datastore created over the
  API; forced to `schema_version = 99` → exit status 1 with the same sentence naming that
  datastore; forced to `0` → exit status 1 with `... knows no step from version 0 to 1`.
  Container removed afterwards (`docker rm -f kf-pg-xmr673`).
- No config key, migration, dependency, API or generated artifact changed in this stage, so
  `generate-config-reference`, the `generate-*-check` targets and `./scripts/...` do not apply
  here; stage 3 owns the `/ready` schema change and its regeneration.

Note for the next reader: the child process logs one harmless warning,
`configuration key matches nothing and was ignored key=test.boot_helper` — the helper flag is a
`KILASFLOW_*` variable by the plan's naming and the config loader reports unknown keys. It does
not affect the assertions, which match the whole refusal sentence.

### Stage 3 - readiness reports the fleet spread and goes not-ready while a migration is outstanding

What changed (no migration, no config key, no dependency, `CurrentSchemaVersion` still 1):

- `internal/api/handlers/system.go`: `FleetReporter` (the one method of `*datastore.Engine`
  readiness needs, so a handler test can stand in for a fleet), `System.fleet` and
  `System.WithFleet`, the `ReadyDatastores` output type
  (`schemaVersion`, `spread`, `behind`, `ahead`) and an optional `datastores` field on the
  readiness body. `Ready()` pings the database first, then asks the reporter: a reporter error
  is logged with `request_id` and the cause (`readiness: datastore fleet status failed`, the
  shape `serverProblem` uses) and answers a generic
  `503 datastore fleet status unavailable`, because the endpoint is public and a driver error
  names tables; a fleet with a datastore behind answers `503` with `FleetStatus.Problem()`
  (counts and versions, never an id or a tenant); a fleet with datastores ahead answers `200`
  and reports the `ahead` count. The operation description now names both checks.
- `internal/api/routes.go`: `handlers.NewSystem(...)`, then `WithFleet(deps.Datastores)` only
  when `deps.Datastores != nil`. The guard is load-bearing: a nil `*datastore.Engine` stored in
  the reporter interface is a non-nil interface, so an unconditional `WithFleet` would call
  through a nil pointer and 503 every instance without a row store.
- Generated artifacts, regenerated with the Makefile targets (never hand-edited):
  `web/src/lib/api/generated/models/readyDatastores.ts` and `readyDatastoresSpread.ts` (new),
  `models/index.ts`, `models/readyOutputBody.ts`, `system/system.ts`, `sdk/src/generated/models.ts`
  and `docs/src/content/docs/reference/api/system.md`. No SDK version bump: the change is an
  additive optional response field, which `docs/reference/api-contract.md` calls additive.
- Docs: a new `## Datastore schema versions` section in
  `docs/src/content/docs/operate/upgrades.md` (one transaction per step per datastore; every
  role runs the pass and concurrent boots apply each step once; the boot refusal and its exact
  causes; the listener opens only after the first pass; the 30-second resume; behind fails
  readiness, ahead does not; the served version is 1 and no step has shipped, so today the pass
  migrates nothing) and its Upgrading steps 3-4; the readiness bullet in
  `docs/src/content/docs/operate/deployment.md` (also 503 while a migration is outstanding; the
  block is counts and versions only but `/ready` is public, so it discloses the total datastore
  count); the `/api/v1/ready` row in `README.md`.

Tests (new file `internal/api/ready_fleet_test.go`, every existing test untouched): the spread
on an empty install (`{}` as a JSON object, not null) and after two datastores (`{"1":2}`);
503 with `application/problem+json` while one datastore is behind, the detail naming the
outstanding migration and the counts but neither the datastore id nor the tenant id, and 200
again once it is restored (readiness follows the live catalogue, nothing is cached); 200 with
`ahead: 1` and a `"99"` spread entry for a datastore ahead of the build; the block omitted when
`Deps.Datastores` is nil; 503 with the generic detail, and no `no such table` leaked, when the
catalogue table is dropped.

Proof the tests can fail: before the implementation, with the four behavioural tests present
and nothing else changed, `go test -run TestReady ./internal/api/` failed with
`datastores block missing from the readiness body:
{"status":"ok","database":"ok"}` (spread and ahead),
`status = 200, want 503 while a datastore is behind` and
`status = 200, want 503 when the fleet cannot be read`. The nil-engine test passes under the
old code by construction.

Commands run and outcomes (all after the implementation):

- `gofmt -l internal/api`: empty. `go vet ./...`: clean. `go build ./...`: clean.
- `go test -race -count=1 ./internal/api/... ./internal/guardrails/...`: all ok
  (`internal/api` 87.2s, `internal/api/handlers` 102.1s, `middleware` 2.6s, `guardrails` 3.5s).
- Baseline before any edit: `make generate-api-check generate-types-check
  generate-api-reference-check` → all fresh (no pre-existing drift), then
  `make generate-api generate-types generate-api-reference` → 14 pages, 74 operations.
- `make generate-api-check generate-types-check generate-api-reference-check sdk-check
  sdk-test sdk-version-check`: all pass (`api-reference: 14 pages fresh`, sdk 6 files / 81 tests
  passed, version check clean).
- `cd web && pnpm install --frozen-lockfile && pnpm check && pnpm test`: 0 errors 0 warnings
  over 1517 files; 44 test files / 514 tests passed.
- `make docs-build`: passed (links validated).
- `make smoke-sqlite`: passed.
- `KILASFLOW_TEST_POSTGRES_DSN=... go test -race -p 1 -count=1 ./internal/datastore/...
  ./internal/database/... ./internal/api/...` against the scratch container:
  `internal/datastore` ok, `internal/database` ok, `internal/api` 81.8s ok,
  `internal/api/handlers` 162.2s ok, `internal/api/middleware` ok; container `kf-pg-xmr673`
  removed afterwards. The first attempt of this command failed to build `internal/api` with
  `embed dist/_app/...: no such file or directory`: it was running while `make smoke-sqlite`
  rebuilt and re-copied the embedded SPA into `internal/web/dist` (a gitignored build artifact),
  so the two compiles raced over that directory. Re-run alone, it is green.
- Manual boot, SQLite: fresh boot `/api/v1/ready` →
  `"datastores":{"schemaVersion":1,"spread":{},"behind":0,"ahead":0}`; after creating two
  datastores → `"spread":{"1":2}`; forcing one to `schema_version = 0` while the process ran →
  `HTTP/1.1 503 Service Unavailable` with
  `"detail":"datastore migration outstanding: 1 datastore(s) behind schema version 1 (spread v0=1, v1=1)"`;
  forcing it to 99 and restarting → exit status 1 with
  `kilasflow: migrate the datastore fleet: datastore: datastore_... is at schema version 99 but this build knows version 1`.
- Manual boot, PostgreSQL: the same two observations on the scratch container — the fresh
  `/ready` block, a 503 with
  `datastore migration outstanding: 1 datastore(s) behind schema version 1 (spread v0=1)`, and
  exit status 1 with the naming refusal for `schema_version = 99`.
- `grep -n 'CurrentSchemaVersion = 1' internal/datastore/fleet.go`: still matches (the version
  stays 1).
- No config key, migration or dependency was added, so `generate-config-reference` and
  `./scripts/...` do not apply to this ticket.

Honest limits: with no step shipped, a datastore behind cannot be migrated today — the resume
tick logs the failure and readiness stays 503, which is the correct answer (the operator must
run a build that has the step, or restore); on a running process the `behind` state is
observable only for a datastore that goes behind after boot, because the boot pass refuses to
open the listener otherwise.

### Review round 1 fixes - the boot hang when a step writes the catalogue row, and three doc gaps

Review findings at `/tmp/kf-wave1/reviews/BUG-xmr673.confirmed.json`; one medium (correctness)
and three low (acceptance, conventions x2). All four are closed in this commit. Criteria 2 and 3
keep their tick: their evidence is unchanged except where noted below.

- **Medium: the runner looped forever when a step wrote the datastore's own catalogue row.**
  `advance` answered a version stamp that matched no row with `errFleetSkip`, and `migrateOne`
  turns a skip into `(false, nil)`; the transaction then rolled back, restoring the row at the
  version the work list re-selected, so `Run` re-ran the same step forever. Fixed at the root:
  after `stamped.RowsAffected != 1` the runner re-reads the row inside the same transaction
  (`unstampedCatalogueRow` in `internal/datastore/fleet.go`) and refuses the run — naming the
  datastore and both versions — because nothing but the step can have moved a row this
  transaction holds, so the skip answer cannot be true here. The step contract is now stated in
  the `FleetStep` doc comment (a step MUST NOT write or delete the datastore's own catalogue
  row), and `errFleetSkip`'s comment records that it is only ever answered from the pre-step
  re-read, where what it reports belongs to a committed write.
- Deviation from the review's suggested wording, and why: the finding suggested returning
  `errFleetSkip` when the row is gone or its version differs from `from`. That was reproduced
  and rejected — the skip propagates out of the transaction callback, so gorm rolls the step
  back, the rollback restores the row at `from` (or resurrects the deleted row), and the outer
  re-query selects it again: the same loop. Both cases are contract violations and both refuse
  now. `advance`'s locked re-read still answers `errFleetSkip` for the peer-moved and dropped
  cases, which is sound because those states are committed and the re-query cannot select them.
- **Low (acceptance): the readiness `503` now carries the `datastores` block.** Chosen over
  narrowing the two doc sentences because the block is the only stable channel for the spread in
  exactly the state that reports a migration outstanding, and `detail` is contract-unstable. The
  503 body stays an RFC 9457 `application/problem+json` problem document (`NotReadyProblem`
  embeds `huma.ErrorModel` and adds `datastores`), so `internal/api/ready_fleet_test.go`'s
  content-type assertion and every existing consumer are unaffected. The operation declares the
  `503` response (`problemResponse`, schema generated from the returned type) *and* the
  `default` response, which Huma only adds while it is an operation's sole declared response —
  without that, my change would have dropped the documented generic error response from
  `/ready`.
- **Low (conventions): `docs/src/content/docs/start/install.md`** quickstart sample updated to
  the body a fresh install actually returns, with a sentence on what the block means and that it
  is on the `503` too. `operate/deployment.md` and `operate/upgrades.md` were sharpened to say
  the block is served on both answers, and `reference/api-contract.md`'s Errors section now
  records the one declared failure that carries an extension member (extension members are
  additive). The generated `reference/api/errors.md` sentence about `default` responses was
  edited in its generator (`scripts/generate-api-reference.mjs`) and regenerated.
- **Low (conventions): `CHANGELOG.md`** gained a `### Changed` entry for the observable pair (boot
  now migrates and can refuse to start; `/ready` gained the block and can answer 503 carrying it).

Tests (TDD, finding 1 first):

- Throwaway repro, deleted before the commit: a step that stamps `schema_version = 99` through
  its own transaction against the unfixed runner, a 3s context deadline and no counter bound —
  `MigrateFleet = 0, context deadline exceeded after 3.001s; the step ran 9775 times` (SQLite).
  The same step after the fix: `MigrateFleet = 0, datastore: datastore_...-... is at schema
  version 99 after the step from version 0 wrote the catalogue row: the step MUST NOT write or
  delete the datastore's own catalogue row, the runner owns the version stamp after 1ms; the step
  ran 1 times`.
- `TestFleetRunRefusesAStepThatWritesTheCatalogueRow` (`internal/datastore/fleet_engine_test.go`)
  is the permanent, bounded form: the step stops the run after four invocations and the run has a
  30s deadline, so a regression fails the suite instead of hanging it. It asserts the step ran
  once, the whole refusal sentence, `migrated = 0` and the dial-back to version 0. Failing on the
  unfixed code, both dialects: `the step ran 5 times, want 1: the runner re-selected the datastore
  it had just rolled back` (SQLite and PostgreSQL, with a temporary `errFleetSkip` restored to
  prove it). Passing on the fixed code, both dialects.
- `TestReadyNotReadyCarriesTheDatastoreSpreadInTheProblem` (`internal/api/ready_fleet_test.go`):
  the 503 problem document still carries `application/problem+json`, `title`, `status` and the
  human-readable `detail`, and now also `schemaVersion`, `behind: 1` and the full
  `{"0":1,"1":1}` spread, with no datastore id or tenant id leaked. Proven able to fail by
  restoring the old `huma.Error503ServiceUnavailable(status.Problem())` return:
  `datastores block missing from the 503 problem document:
  {"$schema":".../ErrorModel.json","title":"Service Unavailable","status":503,"detail":"..."}`.
- Criterion 2's evidence now also includes the 503 block test; criterion 3 is untouched.

Commands run and outcomes:

- `gofmt -l internal/datastore internal/api internal/api/handlers cmd scripts`: empty.
- `go vet ./...`: clean. `go build ./...`: clean.
- Scratch `pgvector/pgvector:pg17` container `kf-pg-bug-xmr673` (port 32795 from `docker port`):
  `KILASFLOW_TEST_POSTGRES_DSN=... go test -race -p 1 -count=1 ./internal/datastore/...` ok
  (29.2s), and the fleet tests themselves show both `sqlite` and `postgres` subtests passing.
  Container removed afterwards.
- `make generate-api generate-types generate-api-reference` regenerated the web client
  (`models/notReadyProblem.ts` new, `models/index.ts` and `system/system.ts` updated), the SDK
  types (`NotReadyProblem` added, `ReadyDatastores` reordered by the generator) and the reference
  pages. `make generate-api-check generate-types-check generate-api-reference-check` all pass;
  `api-reference: 14 pages fresh (KilasFlow 0.1.0-dev, 74 operations)`.
- No SDK version bump: an additive response field on an existing operation is what
  `reference/api-contract.md` calls additive, and `make sdk-version-check` checks only that
  `SDK_VERSION` and `sdk/package.json` agree.

Honest limits: the refusal names the datastore and both versions but cannot say which statement
in the step wrote the row — a step author reads the step's own SQL. The `datastores` block on the
`503` reports the same counts as the `200` body, so it discloses the installation's total
datastore count to anyone who can reach a public `/ready`, which `operate/deployment.md` already
says.

### Review round 2 fixes - the 503 prose overclaimed, and the database-unreachable 503 leaked the driver error

Two medium findings from the delta review (`/tmp/kf-wave1/reviews/BUG-xmr673.confirmed.json`).
Both are closed in this commit. No acceptance criterion changes: criterion 2 keeps its tick and
its evidence widens to the two behaviours below.

- **Medium: the new prose claimed every `/ready` `503` carries the `datastores` block.** Only
  the migration-outstanding `503` does; the database-unreachable `503` and the
  fleet-status-error `503` are plain `ErrorModel`s. A monitor that read `datastores.behind` on
  every `503` would misfire exactly during a database outage. Qualified the claim at its source:
  the operation description and the declared `503` description and the `NotReadyProblem` doc
  comment in `internal/api/handlers/system.go`, plus `README.md`,
  `docs/src/content/docs/start/install.md` and `docs/src/content/docs/operate/deployment.md`,
  plus the generator of the generated `reference/api/errors.md` sentence
  (`scripts/generate-api-reference.mjs`). The reference pages and the web/SDK clients that embed
  the description were regenerated from it. `reference/api-contract.md` and
  `operate/upgrades.md` already qualified it and were left alone.
- **Medium: the database-unreachable `503` served the raw driver error.** `Ready` answered
  `huma.Error503ServiceUnavailable("database unreachable", err)`, putting the driver text in the
  public problem body's `errors[]` and contradicting the policy the fleet-status branch stated in
  the same handler. Both `503`s now answer through a new `unavailableProblem`
  (`internal/api/handlers/problem.go`), the 503 counterpart of `serverProblem`: it logs the cause
  beside the request id and returns the detail with no error. Status and human sentence are
  unchanged.

Tests (TDD, finding 2 first):

- `TestReadyDatabaseUnreachableServesNoDriverTextAndNoSpread` (`internal/api/ready_fleet_test.go`,
  new): a `stubPinger` failing with a driver-shaped message; asserts `503`, the exact
  `database unreachable` detail, `application/problem+json`, no `datastores` member, and none of
  the driver text (`modernc.org/sqlite`, `db.internal`, `no such host`) anywhere in the body.
  Proven able to fail on the unfixed code — it reported the leak three times:
  `{"$schema":"...","title":"Service Unavailable","status":503,"detail":"database unreachable","errors":[{"message":"modernc.org/sqlite: dial tcp: lookup db.internal:5432: no such host"}]}`.
- `TestReadyNotReadyCarriesTheDatastoreSpreadInTheProblem` (existing, unchanged) pins the
  migration-outstanding `503` as carrying the block; it still passes.

Commands run and outcomes:

- `gofmt -l internal/api/handlers/problem.go internal/api/handlers/system.go
  internal/api/ready_fleet_test.go`: empty. `go vet ./...`: clean. `go build ./...`: clean.
- `go test -race -count=1 -p 1 ./internal/api/... ./cmd/kilasflow/... ./internal/guardrails/`:
  all ok — `internal/api` 111.4s, `internal/api/handlers` 177.5s, `internal/api/middleware` 1.7s,
  `cmd/kilasflow` 5.4s, `internal/guardrails` 1.9s. One earlier run of the same command without
  `-p 1` failed `internal/api/handlers` on `TestLoginRefusesASprayFromOneAddress`
  (`auth_test.go:162: no refusal within 30 attempts`) while the whole tree ran in parallel on a
  host several sibling worktrees were also loading. That test's own comment says the bound is
  timing-sensitive on "a shared runner"; it passes when the package runs alone and in the `-p 1`
  run, and nothing in this change touches the auth path.
- `make generate-api generate-types generate-api-reference`: 14 pages, 74 operations.
  `make generate-api-check generate-types-check generate-api-reference-check sdk-check sdk-test
  sdk-version-check`: all pass (`api-reference: 14 pages fresh`, sdk 6 files / 81 tests passed,
  version check clean). `make docs-build`: passed, all internal links valid.
- No storage code, migration, config key or dependency was touched, so the SQL dialect check and
  `generate-config-reference` do not apply.

### Review round 3 fixes - the two hand-written pages still carried the unconditional 503 claim

One finding class from the round-3 delta review, reported as two medium defects on the two
hand-written pages the round-2 note wrongly recorded as "already qualified and left alone".
Both are closed in this commit, docs only. No acceptance criterion changes: criterion 2 keeps
its tick, since the behaviour it proves is unchanged and this commit changes prose about it.

- **Medium: `docs/src/content/docs/reference/api-contract.md` claimed every `/ready` `503`
  carries the `datastores` block.** The paragraph read "`GET /api/v1/ready` answers `503` with
  the same `datastores` block its `200` body carries". `Ready` has three `503` producers and
  only one attaches a block (`internal/api/handlers/system.go:165-199`): the
  migration-outstanding branch returns `NotReadyProblem`, while `database unreachable` and
  `datastore fleet status unavailable` go through `unavailableProblem`
  (`internal/api/handlers/problem.go:43-50`) as plain `ErrorModel`s. The paragraph now names
  the state that carries the block and the two that do not, and keeps the existing
  `[System](/reference/api/system/)` link and the rest of the paragraph (why the block is
  repeated, and that extension members are additive) untouched.
- **Medium: `docs/src/content/docs/operate/upgrades.md` was false in both halves.** It said
  `/ready` "answers `503` only while a datastore is **behind**" — an unreachable database and
  an unreadable fleet status also answer `503` — and "The spread is in the `503`'s problem
  document", the same unqualified claim. The bullet now lists all three not-ready states and
  says the spread is in the migration-outstanding `503`'s problem document, with the other two
  carrying no block because neither can read the catalogue. The following sentence about a
  datastore **ahead** is unchanged.

Verification of every sentence written, against `internal/api/handlers/system.go`:

- Three `503` producers: `Ready` pings the database (line 166-168, detail `database
  unreachable`), then calls `FleetStatus` (line 178-181, detail `datastore fleet status
  unavailable`), then checks `!status.Ready()` (line 182-194, `NotReadyProblem` with
  `Datastores: readyDatastores(status)`). `FleetStatus.Ready()` is `Behind == 0`
  (`internal/datastore/fleet.go:400`), so "a migration is outstanding" and "a datastore is
  behind" name the same branch.
- Only the third branch carries the block, so "carries no block, because neither state can
  read the catalogue" is right for the first two: the first cannot reach the database at all
  and the second is the catalogue read failing.
- The qualified wording matches the surfaces round 2 already fixed: `README.md:88`,
  `docs/src/content/docs/start/install.md:31-37`,
  `docs/src/content/docs/operate/deployment.md:179-187` and the generated
  `reference/api/system.md` and `reference/api/errors.md`.

Commands run and outcomes:

- `make docs-build`: 43 pages built, `All internal links are valid.` (this tree has 43 pages at
  `934f44a`; the review note's "44" counts a page another branch adds).
- `go test -count=1 ./internal/guardrails/...`: ok (0.865s).
- `sh scripts/check-coordinates.sh`: `check-coordinates: every published coordinate names
  ghcr.io/kilaslab/kilasflow's owner`.
- Left alone deliberately: `docs/src/content/docs/operate/upgrades.md:113-117` (runbook step 4)
  says the endpoint's `datastores` block shows the spread; that is literally true of the
  endpoint (the block is on the `200` and on the migration-outstanding `503`) and does not
  claim a block on every `503`, so qualifying it would rewrite a statement that is not false.

### Landing (integrator) - squash-merged onto main as one commit

The six stage commits were squashed onto `main` (`5328524` at the time) with `git merge --squash
worktree-wf_d1e04be5-af7-27`. Git reported no textual conflict; the eight files main had also
touched (`CHANGELOG.md`, `README.md`, `cmd/kilasflow/main.go`,
`docs/src/content/docs/operate/upgrades.md`, `docs/src/content/docs/reference/api-contract.md`,
`scripts/generate-api-reference.mjs`, `sdk/src/generated/models.ts`,
`.pine/tickets/BUG-xmr673.md`) auto-merged, and both intents survive: `bootDatastoreFleet` sits
after `SetLimits` (i.e. after `database.Migrate`) in the composition root, the changelog entry
lands under `[Unreleased]`, and the api-contract paragraph lands after main's errors section. No
migration was added by this ticket, so nothing needed renumbering.

Gates run on the integrated tree (all green): `gofmt -l` on the changed Go files printed nothing;
`go vet ./...`; `go build ./...`; `go test ./...` (whole module); `go test -race -count=1 -p 1`
over `./cmd/kilasflow/... ./internal/datastore/... ./internal/api/... ./internal/guardrails/...`;
`make generate-api-check generate-types-check generate-api-reference-check
generate-config-reference-check sdk-check sdk-test sdk-version-check` (all fresh/pass, nothing to
regenerate); `cd web && pnpm install --frozen-lockfile && pnpm check (1520 files, 0 errors) &&
pnpm test (44 files, 556 tests)`; `make docs-build` (44 pages, internal links valid); `make
smoke-sqlite`.

Behaviour proven on the integrated binary (throwaway probes, removed afterwards): a fresh SQLite
boot answers `/api/v1/ready` `200` with
`"datastores":{"schemaVersion":1,"spread":{},"behind":0,"ahead":0}`; a datastore forced to
`schema_version = 0` answers `503 application/problem+json` with
`"detail":"datastore migration outstanding: 1 datastore(s) behind schema version 1 (spread v0=1)"`
and the same block; a datastore at `99` and one at `0` each refuse boot with exit status 1 and
`kilasflow: migrate the datastore fleet: datastore: datastore_probe is at schema version 99 but
this build knows version 1` / `... knows no step from version 0 to 1`, with the listener never
opening.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `794adbb3` (last commit at or before ticket created 2026-09-20)
- Commits (3):
  - `d3685486` — BUG-xmr673: start the datastore fleet migration at boot and report its spread from readiness
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
 .pine/tickets/BUG-xmr673.md                        |  535 +++++++++
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
 .pine/tickets/FEAT-bp59m4.md                       |   32 +
 .pine/tickets/FEAT-c72set.md                       |   26 +
 .pine/tickets/FEAT-cwmw90.md                       |  513 ++++++++-
 .pine/tickets/FEAT-emf6k5.md                       |  573 ++++++++++
 .pine/tickets/FEAT-ew46cb.md                       |   26 +
 .pine/tickets/FEAT-fpqvwx.md                       |   57 +
 .pine/tickets/FEAT-g07pj8.md                       |   67 ++
 .pine/tickets/FEAT-hj8pyx.md                       |   52 +
 .pine/tickets/FEAT-m4d2y1.md                       |   30 +
 .pine/tickets/FEAT-mha6a0.md                       |  259 +++++
 .pine/tickets/FEAT-p77zr3.md                       |   67 ++
 .pine/tickets/FEAT-qdedm0.md                       |  993 +++++++++++++++-
 .pine/tickets/FEAT-x5qqpm.md                       |   26 +
 .pine/tickets/FEAT-yxwyav.md                       |   26 +
 CHANGELOG.md                                       |   81 ++
 CONTRIBUTING.md                                    |    3 +
 Makefile                                           |   44 +
 README.md                                          |   11 +-
 cmd/kilasflow/embed_issuer.go                      |   18 +
 cmd/kilasflow/embed_issuer_test.go                 |  134 +++
 cmd/kilasflow/fleet.go                             |   92 ++
 cmd/kilasflow/fleet_test.go                        |  395 +++++++
 cmd/kilasflow/main.go                              |   84 +-
 cmd/kilasflow/webhook_wiring_test.go               |  138 +++
 config.example.yaml                                |   53 +-
 docs/src/content/docs/concepts/credentials.md      |   21 +-
 docs/src/content/docs/concepts/execution-model.md  |    1 +
 docs/src/content/docs/concepts/node-registry.md    |   40 +
 docs/src/content/docs/concepts/webhooks.md         |  107 +-
 docs/src/content/docs/guides/embedding.md          |   32 +-
 docs/src/content/docs/guides/node-authoring.md     |    6 +
 .../src/content/docs/guides/tenant-scoped-nodes.md |  183 +++
 .../docs/operate/configuration-reference.md        |   85 +-
 docs/src/content/docs/operate/deployment.md        |   10 +-
 docs/src/content/docs/operate/security.md          |   23 +-
 docs/src/content/docs/operate/upgrades.md          |   61 +-
 docs/src/content/docs/reference/api-contract.md    |   17 +-
 docs/src/content/docs/reference/api.md             |    4 +-
 docs/src/content/docs/reference/api/errors.md      |    2 +-
 docs/src/content/docs/reference/api/nodes.md       |   16 +-
 docs/src/content/docs/reference/api/system.md      |    3 +-
 docs/src/content/docs/reference/api/workflows.md   |   21 +
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
 go.mod                                             |    1 +
 go.sum                                             |    2 +
 internal/api/embed_defaults_test.go                |  150 +++
 internal/api/handlers/auth_test.go                 |   63 +-
 internal/api/handlers/interop.go                   |    6 +-
 internal/api/handlers/nodes.go                     |   76 +-
 internal/api/handlers/problem.go                   |   17 +
 internal/api/handlers/system.go                    |  142 ++-
 internal/api/handlers/workflows.go                 |   81 +-
 internal/api/middleware/embed.go                   |    3 +-
 internal/api/middleware/loginlimit.go              |   13 +
 internal/api/middleware/loginlimit_test.go         |    3 +-
 internal/api/node_visibility_test.go               |  544 +++++++++
 internal/api/ready_fleet_test.go                   |  358 ++++++
 internal/api/routes.go                             |   10 +-
 internal/api/workflows_test.go                     |   71 +-
 internal/config/config.go                          |   79 +-
 internal/config/embed_branding_test.go             |  192 ++++
 internal/config/embed_validate.go                  |   34 +
 internal/config/packs_visibility.go                |   83 ++
 internal/config/packs_visibility_test.go           |  168 +++
 internal/config/webhook_require_auth_test.go       |   50 +
 internal/credentials/builtin.go                    |   68 ++
 internal/credentials/credentials_test.go           |   58 +
 internal/credentials/registry.go                   |   63 ++
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
 internal/repository/models.go                      |   10 +-
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
 .../sqlite/000014_webhook_route_backfill.down.sql  |   14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |   58 +
 nodes/core.go                                      |    5 +
 nodes/error_workflow.go                            |    4 +
 nodes/http.go                                      |    2 +
 nodes/presentation_test.go                         |   35 +
 nodes/webhook.go                                   |   18 +-
 scripts/check-coordinates.sh                       |   21 +
 scripts/generate-api-reference.mjs                 |    6 +-
 sdk/CHANGELOG.md                                   |    9 +-
 sdk/LICENSE                                        |  202 ++++
 sdk/README.md                                      |   58 +-
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
 sdk/src/generated/models.ts                        |  111 +-
 sdk/src/server.ts                                  |   10 +
 sdk/test/operation-coverage.test.mjs               |   20 +
 sdk/test/release-workflow.test.mjs                 |  223 ++++
 sdk/test/release.test.mjs                          |  390 +++++++
 .../generated/models/executionNodeRunResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |    3 +
 .../lib/api/generated/models/notReadyProblem.ts    |   31 +
 .../lib/api/generated/models/readyDatastores.ts    |   19 +
 .../api/generated/models/readyDatastoresSpread.ts  |   12 +
 .../lib/api/generated/models/readyOutputBody.ts    |    3 +
 web/src/lib/api/generated/nodes/nodes.ts           |    8 +-
 web/src/lib/api/generated/system/system.ts         |   18 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   97 ++
 web/src/lib/dashboard/cursor-page.test.ts          |  285 ++++-
 web/src/lib/dashboard/cursor-page.ts               |   92 +-
 web/src/lib/dashboard/execution-list.test.ts       |  148 ++-
 web/src/lib/dashboard/execution-list.ts            |   98 +-
 web/src/lib/dashboard/workflow-list.test.ts        |   20 +-
 web/src/lib/dashboard/workflow-list.ts             |   21 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |    9 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  143 ++-
 187 files changed, 20764 insertions(+), 891 deletions(-)
```
