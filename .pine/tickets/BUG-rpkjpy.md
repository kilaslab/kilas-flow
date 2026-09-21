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
   `expect(locator).toBeAttached() failed`. Most of them are locator drift in
   editor surfaces, but two are **real product regressions, not drift** — this
   entry's first reading of them was wrong: `pack-editor.spec.ts:76/146` fail
   because `40cc8db` deleted the `options` branch of `property-field.svelte`, so
   every options property (the Set node's Mode, an HTTP method, every
   loader-fed cascade) rendered as a free-text input; `datastore.spec.ts:128`
   and `datastore-pg.spec.ts:36` fail because `df6c87c` made each editable grid
   cell's accessible name `Edit <col> in row <id>`, which drops the value a
   screen reader used to hear (WCAG 2.5.3). Both are fixed in the product, with
   unit tests, in `3880205`. The skip budget is healthy on that run: 22/24
   skipped, every one explained by an allowlisted prefix.

   FEAT-5fhj6p owns the e2e surface; the run brief for this entry assigned these
   twelve specs to its stages 1-3, and they are fixed here — see the stage notes
   below. The attribution stands so the two efforts do not both claim the work.

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
- [x] The 12 e2e specs pass (item 9).
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

## Implementation notes (stage 1: the two product regressions, web/, TDD)

Stage 1 of 4. The other stages (spec drift, catalogue ratchets, the Go tests job) are
not started, so the criterion "the 12 e2e specs pass" stays unticked: two of the
twelve are green after this stage, ten are not.

**Fixed — an `options` property renders as a select again.**
`web/src/lib/components/workflow-editor/property-field.svelte` had no
`{:else if property.kind === 'options'}` branch since `40cc8db` (BUG-rjd6fm,
2026-09-19); it is present at `3836b7a` (line 465). Every options property — the
Set node's Mode, an HTTP method, every loader-fed cascade — fell through to the
last `RENDERED.has(property.kind)` branch and rendered a free-text input, so the
customer could type a value the node refuses and a loader's options were fetched
and never shown. The branch is restored, plus an option for a value that is not
in the list: a saved node, a loader still pending or a loader that failed must
not render as the first option and read back as a silent change.

**Fixed — a datastore grid cell's accessible name carries the value it shows.**
`df6c87c` (BUG-esb9sh) named every editable cell `Edit <column> in row <id>`,
which replaced the visible value in the accessibility tree (WCAG 2.5.3 Label in
Name) and made `getByRole('cell', { name: <value> })` match nothing. The new
`cellButtonLabel` in `web/src/lib/datastore/columns.ts` is used at both button
sites in `web/src/routes/(dashboard)/datastores/[id]/+page.svelte` and keeps the
old prefix, so `{ name: 'Edit email in row 1' }` still matches. The inline-edit
`<input>` at ~line 696 keeps its old label deliberately: it exists only while a
cell is being edited and no spec reads it in that state.

**Tests, written failing first.** `web/src/lib/components/workflow-editor/property-field.test.ts`
(new; SSR render through `svelte/server`, no new dependency) and four
`cellButtonLabel` tests in `web/src/lib/datastore/columns.test.ts`. Before the
change: `pnpm vitest run` on both files reported 8 failed / 13 passed — the field
rendered `<input id="property-s1" value="mailbox" type="text">`, and the label
tests failed because the function did not exist. After: 21 passed.

| command | outcome |
| --- | --- |
| `cd web && pnpm install --frozen-lockfile` | ok |
| `cd web && pnpm test` | 45 files, 522 tests, 0 failed |
| `cd web && pnpm check` | 1518 files, 0 errors, 0 warnings |
| `cd e2e && pnpm exec playwright test tests/datastore.spec.ts:128 tests/datastore-pg.spec.ts:36 --retries=0` (with the fix stashed) | 2 failed — `expect(locator).toBeVisible()` on `getByRole('cell', { name: email })` |
| same, whole two spec files, with the fix | 11 passed |
| `cd e2e && pnpm exec playwright test tests/pack-editor.spec.ts --retries=0` | 3 failed / 1 passed — the same three specs this ticket lists, still on the stage-2 locator drift (`#property-resource` does not exist since `260b277`; the picker copy since `8f20750`); no new failure |
| `cd e2e && pnpm exec playwright test tests/n8n-compare.spec.ts tests/node-coverage.spec.ts --retries=0` | 7 failed / 22 passed / 5 skipped — exactly the seven specs this ticket lists for those two files (`n8n-compare:192,363,599,653`, `node-coverage:211,400,696`), no new failure |
| `go build ./... && go vet ./...` | clean (0 Go files changed) |
| `go test ./internal/guardrails/... -race` | ok |

Environment: e2e against a scratch `pgvector/pgvector:pg17`
(`KILASFLOW_E2E_POSTGRES_DSN='postgres://kilas:hunter2@127.0.0.1:32781/kilasflow?sslmode=disable'`)
and `KILASFLOW_TEST_OLLAMA_BASE_URL='http://127.0.0.1:1/v1'` so the local-model
tests skip with an allowlisted reason.

The `options` branch is editor-wide, so the same run is the check that it broke
nothing that passed at HEAD: across the five spec files the failures are exactly
the set this ticket lists, minus the two datastore specs this stage fixed, and
every other spec in those files passed.

Still open for the later stages: `datastore.spec.ts:128` and
`datastore-pg.spec.ts:36` are green here; `pack-editor.spec.ts:76/146` need the
stage-2 locator fix before this stage's select can be exercised by them.

## Implementation notes (stage 2: the ten drifted specs follow the product changes)

Stage 2 of 4. No criterion is ticked by this stage alone: ten of the twelve specs
are green, the two catalogue ratchets still fail by design (stage 3), and the
"12 e2e specs pass" criterion needs all twelve. Only e2e spec files changed —
no product code, no Go, no web source — so the Go/web gates are not re-run here.

**Reproduced first.** All twelve specs from the ticket, with `--retries=0`,
against a scratch `pgvector/pgvector:pg17` (`kf-pg-rpkjpy-impl`) and
`KILASFLOW_TEST_OLLAMA_BASE_URL=http://127.0.0.1:1/v1`: **10 failed, 2 passed**.
The two that passed are `datastore.spec.ts:128` and `datastore-pg.spec.ts:36`,
fixed in stage 1. The ten failures are the ones this stage owns.

**What changed (spec drift after deliberate product changes, nothing loosened):**

- `datastore.spec.ts:154` — a cell's accessible name is now
  `Edit <column> in row <id>: <value>`, so the score cell is matched with
  `/^Edit score in row \d+: 87$/`. Stricter than the bare `'87'`, which also
  matched the email cell whenever `Date.now()` digits contained `87`
  (a strict-mode violation about one run in nine).
- `pack-editor.spec.ts` — the picker's pack row is a `role="option"` inside the
  `Registered node types` listbox (`node-picker.svelte:186/199`), and the
  "from this workspace's server registry" copy is gone (`8f20750`); the panel's
  property controls are addressed by role + accessible name because their ids
  became per-instance `property-${id}` (`260b277`); the toolbar button is
  `Execute`, not `Run` (`bf802ab`, `workflow-editor.svelte:994`). The
  `section[aria-label="… properties"]` locators were left as they are: they
  still match `properties-panel.svelte:146` and are not a cause of any failure.
- `n8n-compare.spec.ts` — the default HTTP output is n8n's parsed body, so the
  old `statusCode`/`body` assertions were replaced by a direct `toMatchObject`
  on the item, with a second `fullResponse: true` workflow proving the envelope
  (`{statusCode, statusMessage, body, headers}` with lower-case header names)
  still exists; the editor run uses `Execute`; and `kilasflow.chatModel` no
  longer demands a credential (`nodes/ai.go:640-648`), so the run is now
  accepted (202), the request leaves without an `Authorization` header, and a
  second workflow with an attached `httpBearerAuth` credential proves the header
  is applied (`[undefined, 'Bearer compare-key']`).
- `node-coverage.spec.ts` — the identical HTTP change (`answered` + a
  `Coverage HTTP Envelope` workflow) and the identical chat-model change
  (`Coverage Model Key`, token `coverage-key`, expected
  `[undefined, 'Bearer coverage-key']`).

**Verification.**

| command | outcome |
| --- | --- |
| 12 specs by `file:line`, `--retries=0` (before edits) | 10 failed / 2 passed |
| `pnpm exec playwright test tests/datastore-pg.spec.ts tests/datastore.spec.ts tests/n8n-compare.spec.ts tests/node-coverage.spec.ts tests/pack-editor.spec.ts --retries=0` (run 1) | 42 passed / 2 failed / 5 skipped |
| same command (run 2) | 42 passed / 2 failed / 5 skipped |

The only two failures in both runs are the ratchets this stage does not touch —
`n8n-compare.spec.ts:192` and `node-coverage.spec.ts:211` — which fail because
`kilasflow.errorTrigger@1`, `kilasflow.stopAndError@1` and
`kilasflow.formTrigger@1` are in the catalogue with no tier entry yet (stage 3).
Every other spec in the five files passes, twice, with `--retries=0`.

## Implementation notes (stage 3: the catalogue ratchets get real tests, and the suite proves stable)

Stage 3 of 4. The two ratchets were red by design: three catalogue entries —
`kilasflow.errorTrigger@1` and `kilasflow.stopAndError@1` (BUG-aede06, `1d8191e`)
and `kilasflow.formTrigger@1` (FEAT-nqpvf6, `4cc80ff`) — sat in no tier. They are
never listed as exclusions and never added to a tier without something that runs
them: the new `e2e/fixtures/error-form-nodes.ts` exercises all three against the
real binary and both spec files call it.

**The error pair.** An *activated* handler (`errorTrigger -> set`) receives the
failure of a run that `kilasflow.stopAndError` stopped: the failing execution
settles `failed` with `execute node "stop": <message>`, the handler starts as a
`subworkflow` execution and succeeds, and the Error Trigger item carries n8n's
payload — `execution.id`, `execution.error.message`, `execution.error.node`
`{id:'stop', type:'kilasflow.stopAndError'}`, `workflow.id` — with the downstream
`set` emitting `handled: 'yes'`. An inactive handler would run nothing, so the
activation is part of the proof.

**The hosted form.** `formTrigger -> set` on both sides of its minted route: one
binding (POST, `/webhook/<32 hex>`), GET returns the page (200, `text/html`,
the title and `name="Name"`), a POST missing the required field is refused with
400 and starts nothing, and `Name=Ada` is accepted (200) and starts the run,
whose trigger item is `{Name, submittedAt, formMode: 'production'}` and whose
`set` passes it through.

The tier entries (`STUB_EXECUTED` in `e2e/fixtures/n8n-live.ts`, `RUN_COVERAGE`
in `node-coverage.spec.ts`) were added **after** the exercise test ran green, and
the matrix line now reads `n8n-compare matrix: 59 catalogue entries (36
stub-executed, 23 editor-validated, 0 live-service, 0 excluded)`.

### The twelve specs, by cause

| spec | cause |
| --- | --- |
| `datastore.spec.ts:128` | **product regression** — `df6c87c` replaced the editable cell's accessible name with `Edit <col> in row <id>`, dropping the visible value; fixed in the product in stage 1, locator tightened to the full name |
| `datastore-pg.spec.ts:36` | the same regression, on PostgreSQL |
| `n8n-compare.spec.ts:192` | **catalogue ratchet** — `stopAndError`/`errorTrigger` (`1d8191e`) and `formTrigger` (`4cc80ff`) had no tier; the new tests are the tier |
| `n8n-compare.spec.ts:363` | spec drift — the HTTP node's default output is n8n's parsed body (`a6c2425`, BUG-kzkvv6); a `fullResponse` workflow now carries the envelope proof |
| `n8n-compare.spec.ts:599` | spec drift — the toolbar button is `Execute`, not `Run` (`bf802ab`) |
| `n8n-compare.spec.ts:653` | deliberate product change — `kilasflow.chatModel` accepts no credential and needs none (`047b8d1`, BUG-tcqkad); the run is accepted and a keyed workflow proves the header |
| `node-coverage.spec.ts:211` | **catalogue ratchet**, as `n8n-compare:192` |
| `node-coverage.spec.ts:400` | spec drift — the HTTP parsed-body output, as above |
| `node-coverage.spec.ts:696` | deliberate product change — the chat model's optional credential, as above |
| `pack-editor.spec.ts:43` | spec drift — the picker was rewritten (`8f20750`): rows are `role=option` inside the `Registered node types` listbox and the old registry copy is gone |
| `pack-editor.spec.ts:76` | **product regression** — `40cc8db` deleted the `options` branch of `property-field.svelte`, so the control was a free-text input; fixed in the product in stage 1. Also per-instance ids (`260b277`) and `Execute` (`bf802ab`) |
| `pack-editor.spec.ts:146` | the same regression and the same locator changes |

### Two more failures the full local run found (not among the twelve)

- `library-import.spec.ts:294`, whose identical locator also sits at
  `waha-migration.spec.ts:135` (that one is behind the WAHA-corpus skip): an
  empty workspace renders **two** `Import n8n` triggers — the header's and the
  empty state's (`workflows/+page.svelte`, added by `e9cbe7d`) — so the name
  alone matches two buttons and the click is a strict-mode violation. It failed
  2 of 3 local runs; CI's two retries hide it. Both now name the header's
  trigger (`.first()`, first in document order) with a comment; no dialog
  assertion changed.
- `pack-editor.spec.ts:183`: `toHaveCount(1)` sampled the loader's
  pre-response window. With the stage-1 select restored, the settled state after
  a resource change is the loader's new list **plus** the document's saved
  operation, which is not in it (`option "send"` beside `option "list"`, which
  is the point of that fix: a value not in the list must not read back as a
  different one). The assertion passed only before the loader answered and
  failed 1 of 4 flake runs; it now asserts the settled option set.

### Verification

| command | outcome |
| --- | --- |
| `pnpm exec playwright test <the five files> -g '<the 12 titles>' --retries=0`, plus `datastore-pg.spec.ts` by title | 12 passed / 0 failed |
| `cd e2e && pnpm test`, run A then run B | 96 passed / 22 skipped / 0 failed, both (118 tests) |
| `make e2e-skip-budget` (immediately after run B) | `22/24 skipped, every one of them explained (local model unavailable \| WAHA corpus unavailable \| live n8n unavailable)`, exit 0 |
| `pnpm exec playwright test tests/datastore-pg.spec.ts tests/datastore.spec.ts tests/n8n-compare.spec.ts tests/node-coverage.spec.ts tests/pack-editor.spec.ts --repeat-each=4 --retries=0` | 184 passed / 20 skipped / 0 failed — the 20 are the env-gated live-n8n cases (5 per pass) |
| the two ratchets, `--retries=0` | passed, with the 59/36/23 matrix line above |
| the new exercise test in both files, `--retries=0`, before the tier entries | 2 passed |
| `pnpm exec playwright test tests/library-import.spec.ts -g 'a sampled workflow opens in the editor' --repeat-each=4 --retries=0` | 4 passed (before the locator fix: 2 of 3 failed) |
| `go build ./... && go vet ./...` | clean (no Go file changed) |
| `go test ./internal/guardrails/... -race` | ok |

No `web/`, Go or config file changed in this stage, so `pnpm check` / `pnpm test`
were not re-run: stage 1's results (1518 files, 0 errors; 45 files, 522 tests)
stand.

Honest reading of the counts: the local suite is 118 tests (CI's 116 plus the two
new exercise tests), and the local-model, WAHA-corpus and live-n8n gates are the
only skips. The optional live-Ollama pass (step 5 of the stage brief) was not
run, so the recorded skip state is the one proved above.

Environment: scratch `pgvector/pgvector:pg17` named `kf-pg-rpkjpy-impl`
(`KILASFLOW_E2E_POSTGRES_DSN=postgres://kilas:hunter2@127.0.0.1:32792/kilasflow?sslmode=disable`)
and `KILASFLOW_TEST_OLLAMA_BASE_URL=http://127.0.0.1:1/v1` so the local-model
tests skip with an allowlisted reason; the container was removed afterwards.
`CI is green on main` stays unticked: it cannot be observed without a push, which
this workflow forbids, and the Go tests job is independently red in package
`nodes` — the e2e work cannot make that criterion true.

### Stage 4 (the Go tests job) — Implementation notes

The Go tests job was independently red in package `nodes`; three failures were
reproduced against a scratch `pgvector/pgvector:pg17` container and fixed in the
TEST SETUP, not in the product. The stage was interrupted mid-flight (the agent
harness hit a provider usage limit) and finished by the orchestrator.

1. `nodes/pgvector_test.go` — `openVectorPostgres` created the throwaway
   `kf_vector_test` database and migrated it without installing pgvector in it.
   The availability probe asks `pg_available_extensions`, which only says the
   server carries the binaries; migration 000006 gates on `pg_extension` in the
   database being migrated, so since f79cb9a it was recorded as skipped and every
   table it owns was missing (`42P01` on `kvtest_vector_collections`). The harness
   now runs `CREATE EXTENSION IF NOT EXISTS vector` in its own throwaway database
   before `Migrate`. That placement is deliberate: a test harness owns its
   database, while an operator's shared-database role very often cannot create an
   extension, which is why the migration keeps failing loudly instead.
2. `TestVectorSearchUsesTheANNIndex` then ran further and failed on its own
   premise: with no statistics the planner estimates ONE matching row from the
   tenant btree and serves the query with a sort, so no cosine ANN index appears
   in the captured plan. The test now runs `ANALYZE` on the vector table after the
   bulk insert (autovacuum would get there eventually; a test cannot wait for it),
   after which the plan uses `kvtest_idx_vdocs384_ivf_cos`.
3. `TestUnderIndependentlyAnItemThatNeverResolvesIsStillJustThatItem` — the
   expression it used (`{{ $json.bound.inner }}` with `bound` a string) yields
   undefined rather than failing, so the item DID resolve and the test's premise
   was not the property it asserted. The expression is now
   `{{ $json.bound.inner.trim() }}`, a structural error on undefined, so the item
   genuinely never resolves; the assertion is unchanged.

Commands and outcomes (scratch container `kf-pg-recover`, removed afterwards):

| Command | Outcome |
| --- | --- |
| `go test -count=1 -run 'TestVector\|TestUnderIndependently' ./nodes/` with the DSN | ok (11.1s) — the four previously failing tests now pass |
| `go test -count=1 ./nodes/...` with the DSN | ok |
| `gofmt -l nodes/`, `go build ./...`, `go vet ./...` | clean |

Honesty: `CI is green on main` still cannot be observed from here (this workflow
forbids pushing); what this stage proves is that the packages CI runs pass
locally against a scratch PostgreSQL, and it fixes the two test-side causes that
made them fail.
