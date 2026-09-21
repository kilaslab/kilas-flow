---
id: BUG-rpkjpy
title: 'CI has been red on main since at least 2026-09-11: every failing job, its cause and the fix'
status: done
priority: high
labels:
    - ci
    - testing
    - dx
created: "2026-09-20T04:29:14Z"
updated: "2026-09-21T00:33:27Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `61afc0be` (last commit at or before ticket created 2026-09-20)
- Commits (9):
  - `67ba2169` — BUG-rpkjpy: the e2e specs and the Go tests job go green again
  - `794adbb3` — chore(pine): record the CI repairs and close the wait-timer race
  - `8bbff74c` — BUG-rpkjpy: the sign-in throttle tests stop racing their own refill
  - `35a32bf2` — BUG-rpkjpy: the host SDK covers the operator surface, and the counts move to 73
  - `bff73803` — BUG-rpkjpy: a skipped migration may lose the race to record itself
  - `6bd5a96b` — BUG-rpkjpy: regenerate the three drifted artifacts, and map workflow-diagnostics
  - `e3d92c87` — chore(pine): close the module rename and the open-source readiness pass
  - `1357f6db` — BUG-rpkjpy: the config tests stop reading the developer's environment
  - `e5e5f9f6` — BUG-341sxn: the Go module path names the account that owns the repository
- Files changed (base → working tree):

```
 .github/actions/js-toolchain/action.yml            |   11 +-
 .github/workflows/ci.yml                           |   25 +-
 .github/workflows/release.yml                      |  120 +-
 .pine/MEMORY.md                                    |    2 +
 .pine/memory/e2e.md                                |    9 +
 .pine/memory/embedding.md                          |    8 +
 .pine/tickets/BUG-341sxn.md                        |  588 +++++++++-
 .pine/tickets/BUG-5gws7n.md                        |   97 ++
 .pine/tickets/BUG-fng4m2.md                        |   88 ++
 .pine/tickets/BUG-fvdz46.md                        |  400 +++++++
 .pine/tickets/BUG-p3t7yq.md                        |  212 ++++
 .pine/tickets/BUG-rpkjpy.md                        |  400 +++++++
 .pine/tickets/BUG-t9j2ek.md                        |  452 ++++++++
 .pine/tickets/BUG-vzzkg3.md                        |  598 ++++++++++
 .pine/tickets/BUG-w8h3km.md                        |  123 ++
 .pine/tickets/BUG-xmr673.md                        |  737 ++++++++++++
 .pine/tickets/EPIC-87t47t.md                       |   50 +
 .pine/tickets/EPIC-bkj6yf.md                       |   46 +
 .pine/tickets/EPIC-m0bne8.md                       |   57 +
 .pine/tickets/EPIC-r0yg5q.md                       |   27 +
 .pine/tickets/FEAT-2mth85.md                       |   57 +
 .pine/tickets/FEAT-3taswf.md                       | 1199 +++++++++++++++-----
 .pine/tickets/FEAT-41m8dj.md                       |   55 +
 .pine/tickets/FEAT-48hreg.md                       |   20 +-
 .pine/tickets/FEAT-4jns31.md                       |   26 +
 .pine/tickets/FEAT-4ve1bq.md                       |   67 ++
 .pine/tickets/FEAT-77rveq.md                       |   73 ++
 .pine/tickets/FEAT-7cg0cd.md                       |   20 +-
 .pine/tickets/FEAT-8mymac.md                       |    4 +-
 .pine/tickets/FEAT-a5fhjw.md                       |  323 +++++-
 .pine/tickets/FEAT-adyeh0.md                       |   53 +
 .pine/tickets/FEAT-bb4s6e.md                       |   27 +
 .pine/tickets/FEAT-bp59m4.md                       |  420 +++++++
 .pine/tickets/FEAT-c72set.md                       |   26 +
 .pine/tickets/FEAT-cwmw90.md                       |  513 ++++++++-
 .pine/tickets/FEAT-ds4e0m.md                       |   54 +
 .pine/tickets/FEAT-emf6k5.md                       |  573 ++++++++++
 .pine/tickets/FEAT-ew46cb.md                       |   26 +
 .pine/tickets/FEAT-fpqvwx.md                       |  936 +++++++++++++++
 .pine/tickets/FEAT-g07pj8.md                       |   67 ++
 .pine/tickets/FEAT-hj8pyx.md                       |  750 ++++++++++++
 .pine/tickets/FEAT-m4d2y1.md                       |   30 +
 .pine/tickets/FEAT-mha6a0.md                       |  259 +++++
 .pine/tickets/FEAT-p77zr3.md                       |   67 ++
 .pine/tickets/FEAT-qdedm0.md                       |  993 +++++++++++++++-
 .pine/tickets/FEAT-x5qqpm.md                       |   26 +
 .pine/tickets/FEAT-yxwyav.md                       |   26 +
 CHANGELOG.md                                       |   98 +-
 CONTRIBUTING.md                                    |    3 +
 Makefile                                           |   66 +-
 README.md                                          |   11 +-
 cmd/kilasflow/embed_issuer.go                      |   18 +
 cmd/kilasflow/embed_issuer_test.go                 |  134 +++
 cmd/kilasflow/fleet.go                             |   92 ++
 cmd/kilasflow/fleet_test.go                        |  395 +++++++
 cmd/kilasflow/idempotency_test.go                  |  225 ++++
 cmd/kilasflow/main.go                              |  248 +++-
 cmd/kilasflow/main_test.go                         |    6 +-
 cmd/kilasflow/retention_test.go                    |    8 +-
 cmd/kilasflow/secrets_boot_test.go                 |    4 +-
 cmd/kilasflow/webhook_wiring_test.go               |  138 +++
 cmd/nodepackgen/authorcmd.go                       |    2 +-
 cmd/nodepackgen/generate.go                        |    8 +-
 cmd/nodepackgen/generate_test.go                   |   12 +-
 config.example.yaml                                |  110 +-
 docs/astro.config.mjs                              |    6 +-
 docs/src/content/docs/concepts/credentials.md      |   21 +-
 docs/src/content/docs/concepts/execution-model.md  |    1 +
 docs/src/content/docs/concepts/node-registry.md    |   40 +
 .../content/docs/concepts/tenancy-and-embedding.md |   27 +-
 docs/src/content/docs/concepts/webhooks.md         |  107 +-
 docs/src/content/docs/guides/community-nodes.md    |  133 ++-
 docs/src/content/docs/guides/embedding.md          |   32 +-
 docs/src/content/docs/guides/idempotency.md        |  197 ++++
 docs/src/content/docs/guides/node-authoring.md     |    6 +
 .../src/content/docs/guides/tenant-scoped-nodes.md |  183 +++
 .../docs/operate/configuration-reference.md        |  178 ++-
 docs/src/content/docs/operate/deployment.md        |   10 +-
 docs/src/content/docs/operate/security.md          |   23 +-
 docs/src/content/docs/operate/tenant-deletion.md   |  194 ++++
 docs/src/content/docs/operate/upgrades.md          |   61 +-
 docs/src/content/docs/reference/api-contract.md    |   34 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/credentials.md |    7 +
 docs/src/content/docs/reference/api/datastores.md  |    6 +-
 docs/src/content/docs/reference/api/errors.md      |   13 +-
 docs/src/content/docs/reference/api/nodes.md       |   16 +-
 docs/src/content/docs/reference/api/system.md      |    3 +-
 docs/src/content/docs/reference/api/tenants.md     |   21 +
 docs/src/content/docs/reference/api/workflows.md   |   46 +-
 docs/src/content/docs/reference/cli.md             |  628 ++++++++++
 docs/src/content/docs/reference/node-packs.md      |   15 +-
 docs/src/content/docs/start/install.md             |   12 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   11 +-
 .../specs/2026-09-20-agent-surface-design.md       |  519 +++++++++
 e2e/fixtures/error-form-nodes.ts                   |  218 ++++
 e2e/fixtures/live-backend.ts                       |  263 +++++
 e2e/fixtures/n8n-live.ts                           |    5 +
 e2e/fixtures/pack-convert-driver.go                |    2 +-
 e2e/helpers/stub.ts                                |    8 +
 e2e/tests/dashboard-lists.spec.ts                  |  187 +++
 e2e/tests/datastore.spec.ts                        |    2 +-
 e2e/tests/library-import.spec.ts                   |    6 +-
 e2e/tests/live-backend-api.spec.ts                 |  386 +++++++
 e2e/tests/live-backend-datastore.spec.ts           |  282 +++++
 e2e/tests/live-backend-http-auth.spec.ts           |   94 ++
 e2e/tests/live-backend-queue.spec.ts               |  213 ++++
 e2e/tests/live-backend-webhook.spec.ts             |  401 +++++++
 e2e/tests/n8n-compare.spec.ts                      |   53 +-
 e2e/tests/node-coverage.spec.ts                    |   56 +-
 e2e/tests/pack-editor.spec.ts                      |   39 +-
 e2e/tests/waha-migration.spec.ts                   |    5 +-
 go.mod                                             |    4 +-
 go.sum                                             |    4 +
 internal/ai/agent_output_test.go                   |    2 +-
 internal/ai/ai_test.go                             |    2 +-
 internal/ai/fromai_test.go                         |    2 +-
 internal/ai/maf/runtime.go                         |    2 +-
 internal/ai/maf/runtime_test.go                    |    2 +-
 internal/ai/openai_test.go                         |    2 +-
 internal/api/auth_test.go                          |   28 +-
 internal/api/cors_test.go                          |    8 +-
 internal/api/credentials_pagination_test.go        |    2 +-
 internal/api/credentials_test.go                   |   16 +-
 internal/api/datastores_csv_test.go                |    2 +-
 internal/api/datastores_test.go                    |   12 +-
 internal/api/embed_confinement_test.go             |    2 +-
 internal/api/embed_datastore_test.go               |    4 +-
 internal/api/embed_defaults_test.go                |  150 +++
 internal/api/embed_test.go                         |    8 +-
 internal/api/events_test.go                        |   14 +-
 internal/api/handlers/admin.go                     |  158 ++-
 internal/api/handlers/admin_admin_test.go          |  271 ++++-
 internal/api/handlers/auth.go                      |    6 +-
 internal/api/handlers/auth_test.go                 |  110 +-
 internal/api/handlers/credentials.go               |    8 +-
 internal/api/handlers/datastores.go                |  187 ++-
 internal/api/handlers/datastores_csv.go            |    2 +-
 internal/api/handlers/datastores_csv_test.go       |    2 +-
 internal/api/handlers/embed.go                     |    6 +-
 internal/api/handlers/embedscope.go                |    8 +-
 internal/api/handlers/executions.go                |   10 +-
 internal/api/handlers/idempotency.go               |  115 ++
 internal/api/handlers/interop.go                   |   14 +-
 internal/api/handlers/nodes.go                     |   90 +-
 internal/api/handlers/problem.go                   |   21 +-
 internal/api/handlers/resume.go                    |    8 +-
 internal/api/handlers/resume_test.go               |   12 +-
 internal/api/handlers/schedules.go                 |    4 +-
 internal/api/handlers/system.go                    |  142 ++-
 internal/api/handlers/tenants.go                   |    6 +-
 internal/api/handlers/workflows.go                 |  182 ++-
 internal/api/handlers/workflows_conflict_test.go   |    4 +-
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/idempotency_test.go                   | 1004 ++++++++++++++++
 internal/api/list_pagination_test.go               |   12 +-
 internal/api/middleware/auth.go                    |    4 +-
 internal/api/middleware/auth_test.go               |    4 +-
 internal/api/middleware/cors.go                    |   13 +-
 internal/api/middleware/cors_test.go               |    2 +-
 internal/api/middleware/embed.go                   |    5 +-
 internal/api/middleware/embed_test.go              |    2 +-
 internal/api/middleware/loginlimit.go              |   13 +
 internal/api/middleware/loginlimit_test.go         |    3 +-
 internal/api/middleware/sessioncache.go            |    2 +-
 internal/api/node_types_test.go                    |   24 +-
 internal/api/node_visibility_test.go               |  544 +++++++++
 internal/api/openapi_security_test.go              |    4 +-
 internal/api/ready_fleet_test.go                   |  358 ++++++
 internal/api/routes.go                             |   24 +-
 internal/api/server.go                             |   37 +-
 internal/api/server_test.go                        |    4 +-
 internal/api/tenant_delete_test.go                 |  386 +++++++
 internal/api/workflow_history_test.go              |    2 +-
 internal/api/workflows_test.go                     |   99 +-
 internal/auth/auth_test.go                         |    2 +-
 internal/binary/binary.go                          |  119 +-
 internal/binary/binary_test.go                     |  219 +++-
 internal/cli/api_prefix.go                         |   43 +
 internal/cli/cli.go                                |  382 +++++++
 internal/cli/cli_test.go                           |  248 ++++
 internal/cli/client.go                             |  397 +++++++
 internal/cli/client_test.go                        |  320 ++++++
 internal/cli/command.go                            |  139 +++
 internal/cli/command_test.go                       |  302 +++++
 internal/cli/config.go                             |  258 +++++
 internal/cli/config_test.go                        |  749 ++++++++++++
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
 internal/cli/verbs_api.go                          |  315 +++++
 internal/cli/verbs_api_test.go                     |  820 +++++++++++++
 internal/cli/verbs_auth.go                         |  289 +++++
 internal/cli/verbs_credential.go                   |  146 +++
 internal/cli/verbs_credential_test.go              |  119 ++
 internal/cli/verbs_datastore.go                    |  209 ++++
 internal/cli/verbs_datastore_test.go               |  215 ++++
 internal/cli/verbs_exec.go                         |  413 +++++++
 internal/cli/verbs_exec_test.go                    |  436 +++++++
 internal/cli/verbs_node.go                         |  292 +++++
 internal/cli/verbs_node_test.go                    |  196 ++++
 internal/cli/verbs_pack.go                         |  143 +++
 internal/cli/verbs_pack_test.go                    |  195 ++++
 internal/cli/verbs_run.go                          |  257 +++++
 internal/cli/verbs_run_test.go                     |  314 +++++
 internal/cli/verbs_schedule.go                     |   36 +
 internal/cli/verbs_schedule_test.go                |   46 +
 internal/cli/verbs_system.go                       |  282 +++++
 internal/cli/verbs_system_test.go                  |  182 +++
 internal/cli/verbs_tenant.go                       |   86 ++
 internal/cli/verbs_tenant_test.go                  |  116 ++
 internal/cli/verbs_workflow.go                     |  592 ++++++++++
 internal/cli/verbs_workflow_test.go                |  425 +++++++
 internal/conditions/conditions.go                  |    2 +-
 internal/conditions/conditions_test.go             |    4 +-
 internal/config/config.go                          |  164 ++-
 internal/config/config_test.go                     |  104 +-
 internal/config/embed_branding_test.go             |  192 ++++
 internal/config/embed_validate.go                  |   34 +
 internal/config/packs_visibility.go                |   83 ++
 internal/config/packs_visibility_test.go           |  168 +++
 internal/config/webhook_require_auth_test.go       |   50 +
 internal/credentials/builtin.go                    |   70 +-
 internal/credentials/credentials_test.go           |   62 +-
 internal/credentials/external.go                   |    4 +-
 internal/credentials/external_test.go              |    6 +-
 internal/credentials/redirect_test.go              |    6 +-
 internal/credentials/registry.go                   |   67 +-
 internal/credentials/vault.go                      |    2 +-
 internal/database/database.go                      |    2 +-
 internal/database/database_test.go                 |    2 +-
 internal/database/migrate.go                       |   37 +-
 internal/database/migrate_test.go                  |  103 +-
 internal/database/prefix_test.go                   |    4 +-
 internal/database/tenant_columns_test.go           |  309 +++++
 internal/database/webhook_route_backfill_test.go   |  491 ++++++++
 internal/datastore/catalogue.go                    |   16 +-
 internal/datastore/column_tenant_test.go           |  152 +++
 internal/datastore/concurrency.go                  |    5 +-
 internal/datastore/config_bind_test.go             |    2 +-
 internal/datastore/doc.go                          |    4 +-
 internal/datastore/engine.go                       |   31 +-
 internal/datastore/engine_test.go                  |   57 +-
 internal/datastore/fleet.go                        |  366 +++++-
 internal/datastore/fleet_engine_test.go            |  711 ++++++++++++
 internal/datastore/idents_test.go                  |    2 +-
 internal/datastore/isolation.go                    |   96 +-
 internal/datastore/isolation_test.go               |  243 +++-
 internal/datastore/migrate_test.go                 |    1 +
 internal/datastore/model.go                        |    9 +-
 internal/datastore/rows.go                         |   25 +-
 internal/datastore/trace_test.go                   |    2 +-
 internal/datetime/datetime_test.go                 |    2 +-
 internal/embed/embed.go                            |  121 +-
 internal/embed/embed_branding_test.go              |  164 +++
 internal/embed/embed_lifetime_test.go              |  146 +++
 internal/embed/embed_test.go                       |    2 +-
 internal/engine/approval.go                        |    2 +-
 internal/engine/approval_test.go                   |    4 +-
 internal/engine/authenticate.go                    |    8 +-
 internal/engine/authenticate_test.go               |    6 +-
 internal/engine/checkpoint.go                      |    4 +-
 internal/engine/error_workflow_test.go             |   10 +-
 internal/engine/export_test.go                     |   20 +
 internal/engine/expression_context_test.go         |    6 +-
 internal/engine/lease_test.go                      |   22 +-
 internal/engine/live_progress_test.go              |    8 +-
 internal/engine/loopstate_test.go                  |   14 +-
 internal/engine/multiproc.go                       |    4 +-
 internal/engine/multiprocess_test.go               |   26 +-
 internal/engine/runner.go                          |    4 +-
 internal/engine/runner_test.go                     |   16 +-
 internal/engine/service.go                         |   42 +-
 internal/engine/service_test.go                    |   22 +-
 internal/engine/subworkflow_test.go                |   22 +-
 internal/engine/tenant_visibility_test.go          |  379 +++++++
 internal/engine/trace.go                           |    2 +-
 internal/engine/trace_persist_test.go              |   10 +-
 internal/engine/trace_test.go                      |   20 +-
 internal/engine/wait_service.go                    |   57 +-
 internal/engine/wait_service_test.go               |  245 +++-
 internal/engine/worker_test.go                     |    8 +-
 internal/events/events.go                          |    2 +-
 internal/events/events_test.go                     |    2 +-
 internal/execution/redact_datastore_test.go        |    2 +-
 internal/execution/redact_test.go                  |    2 +-
 internal/expression/expression_test.go             |    2 +-
 internal/expression/parity_test.go                 |    2 +-
 internal/guardrails/compile_scope_test.go          |  440 +++++++
 internal/guardrails/licence_boundary_test.go       |    4 +-
 internal/idempotency/hash.go                       |   64 ++
 internal/idempotency/hash_test.go                  |  142 +++
 internal/idempotency/idempotency.go                |  432 +++++++
 internal/idempotency/idempotency_test.go           | 1120 ++++++++++++++++++
 internal/idempotency/sweeper.go                    |   94 ++
 internal/idempotency/sweeper_test.go               |  146 +++
 internal/interop/n8n/corpus/scoreboard_test.go     |   36 +-
 internal/interop/n8n/gowa.go                       |    2 +-
 internal/interop/n8n/gowa_test.go                  |    2 +-
 internal/interop/n8n/importer_tail_test.go         |    4 +-
 internal/interop/n8n/n8n.go                        |    2 +-
 internal/interop/n8n/n8n_test.go                   |   26 +-
 internal/interop/n8n/parameters.go                 |   13 +-
 internal/interop/n8n/sqlfidelity_test.go           |    8 +-
 internal/interop/n8n/waitsubworkflow_test.go       |    4 +-
 internal/loadoptions/datastores.go                 |    2 +-
 internal/loadoptions/datastores_test.go            |    6 +-
 internal/loadoptions/loadoptions.go                |    6 +-
 internal/loadoptions/loadoptions_test.go           |   10 +-
 internal/loadoptions/redirect_test.go              |    8 +-
 internal/loadoptions/schema.go                     |    2 +-
 internal/loadoptions/sql.go                        |    4 +-
 internal/loadoptions/sql_test.go                   |   10 +-
 internal/loadoptions/workflows.go                  |    2 +-
 internal/node/registry.go                          |   35 +-
 internal/node/registry_bench_test.go               |  112 ++
 internal/node/registry_test.go                     |    6 +-
 internal/node/visibility.go                        |  347 ++++++
 internal/node/visibility_test.go                   |  796 +++++++++++++
 internal/nodepack/author.go                        |    6 +-
 internal/nodepack/author_test.go                   |   47 +-
 internal/nodepack/convert.go                       |    8 +-
 internal/nodepack/convert_test.go                  |   10 +-
 internal/nodepack/loaddir.go                       |    8 +-
 internal/nodepack/loaddir_test.go                  |   16 +-
 internal/nodepack/nodepack.go                      |   30 +-
 internal/nodepack/startcase_test.go                |    2 +-
 internal/nodepack/trigger.go                       |   18 +-
 internal/nodepack/trigger_require_auth_test.go     |   43 +
 internal/nodepack/validate.go                      |   25 +-
 internal/nodepack/visibility_test.go               |  262 +++++
 internal/property/locator_test.go                  |    4 +-
 internal/property/mapper_test.go                   |    2 +-
 internal/property/visibility_test.go               |    2 +-
 internal/repository/auth.go                        |    4 +-
 internal/repository/auth_admin_test.go             |    4 +-
 internal/repository/auth_test.go                   |    8 +-
 internal/repository/claim_lease_test.go            |   10 +-
 internal/repository/claim_wake_test.go             |   10 +-
 internal/repository/credentials.go                 |    4 +-
 internal/repository/credentials_external_test.go   |    8 +-
 internal/repository/execution_retention.go         |    2 +-
 internal/repository/execution_retention_test.go    |   12 +-
 internal/repository/executions.go                  |    4 +-
 internal/repository/idempotency.go                 |  360 ++++++
 internal/repository/idempotency_test.go            |  615 ++++++++++
 internal/repository/import_diagnostics.go          |    2 +-
 internal/repository/import_diagnostics_test.go     |    8 +-
 internal/repository/models.go                      |   23 +-
 internal/repository/models_test.go                 |   10 +-
 internal/repository/postgres_execution_test.go     |   26 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |    4 +-
 internal/repository/schedules.go                   |    2 +-
 internal/repository/subworkflow_activation_test.go |    6 +-
 internal/repository/table_names_test.go            |    1 +
 internal/repository/tenant_purge.go                |   48 +-
 internal/repository/tenant_purge_test.go           |   56 +-
 internal/repository/tenant_rows.go                 |  283 +++++
 internal/repository/tenant_rows_test.go            |  420 +++++++
 internal/repository/waits.go                       |    2 +-
 internal/repository/waits_test.go                  |   10 +-
 internal/repository/webhooks.go                    |  126 +-
 internal/repository/webhooks_delivery_test.go      |  147 +++
 internal/repository/webhooks_test.go               |  190 +++-
 internal/repository/workflow_history.go            |    2 +-
 internal/repository/workflow_history_test.go       |   10 +-
 internal/repository/workflow_list_test.go          |    4 +-
 internal/repository/workflows.go                   |   24 +-
 internal/routing/executor.go                       |   12 +-
 internal/routing/request.go                        |    8 +-
 internal/routing/response.go                       |    4 +-
 internal/routing/routing.go                        |    2 +-
 internal/routing/routing_test.go                   |   10 +-
 internal/runcode/runcode_test.go                   |    2 +-
 internal/safehttp/safehttp.go                      |    2 +-
 internal/safehttp/safehttp_test.go                 |    2 +-
 internal/scheduler/extract.go                      |    4 +-
 internal/scheduler/item.go                         |    2 +-
 internal/scheduler/rule_test.go                    |    2 +-
 internal/scheduler/scheduler.go                    |    2 +-
 internal/scheduler/scheduler_test.go               |   14 +-
 internal/sqlbuild/sqlbuild.go                      |    2 +-
 internal/sqlbuild/sqlbuild_test.go                 |    6 +-
 internal/sqlguard/attack_test.go                   |    2 +-
 internal/sqlguard/sqlguard_test.go                 |    2 +-
 internal/sqlnode/guard_test.go                     |    4 +-
 internal/sqlnode/internal_test.go                  |    4 +-
 internal/sqlnode/policy_test.go                    |    4 +-
 internal/sqlnode/sqlnode.go                        |    6 +-
 internal/sqlnode/sqlnode_test.go                   |    2 +-
 internal/tenantpurge/completeness_test.go          |  368 ++++++
 internal/tenantpurge/doc.go                        |  120 ++
 internal/tenantpurge/docs_test.go                  |  115 ++
 internal/tenantpurge/harness_test.go               |  614 ++++++++++
 internal/tenantpurge/purge.go                      |  412 +++++++
 internal/tenantpurge/purge_test.go                 |  507 +++++++++
 internal/web/embed.go                              |    2 +-
 internal/webhook/export_test.go                    |    2 +-
 internal/webhook/form_test.go                      |    2 +-
 internal/webhook/jwt.go                            |  144 +++
 internal/webhook/jwt_test.go                       |  212 ++++
 internal/webhook/lifecycle.go                      |    6 +-
 internal/webhook/lifecycle_test.go                 |   10 +-
 internal/webhook/request_lifecycle.go              |    6 +-
 internal/webhook/request_lifecycle_test.go         |    8 +-
 internal/webhook/require_auth.go                   |   74 ++
 internal/webhook/require_auth_test.go              |  367 ++++++
 internal/webhook/route_label_test.go               |  172 +++
 internal/webhook/shape.go                          |   29 +-
 internal/webhook/shape_test.go                     |   34 +-
 internal/webhook/webhook.go                        |   99 +-
 internal/webhook/webhook_test.go                   |  222 +++-
 internal/workflow/catalog_scope.go                 |   39 +
 internal/workflow/compiler.go                      |   24 +-
 internal/workflow/compiler_test.go                 |    2 +-
 internal/workflow/compiler_visibility_test.go      |  280 +++++
 internal/workflow/document_test.go                 |    8 +-
 internal/workflow/typeversion_test.go              |    2 +-
 .../000014_webhook_route_backfill.down.sql         |   14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |   62 +
 .../postgres/000015_idempotency_keys.down.sql      |    6 +
 migrations/postgres/000015_idempotency_keys.up.sql |   49 +
 .../000016_webhook_deliveries_tenant.down.sql      |   14 +
 .../000016_webhook_deliveries_tenant.up.sql        |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   14 +
 .../000017_datastore_columns_tenant.up.sql         |   39 +
 .../sqlite/000014_webhook_route_backfill.down.sql  |   14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |   58 +
 migrations/sqlite/000015_idempotency_keys.down.sql |    6 +
 migrations/sqlite/000015_idempotency_keys.up.sql   |   48 +
 .../000016_webhook_deliveries_tenant.down.sql      |   13 +
 .../sqlite/000016_webhook_deliveries_tenant.up.sql |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   12 +
 .../sqlite/000017_datastore_columns_tenant.up.sql  |   38 +
 nodes/ai.go                                        |   14 +-
 nodes/ai_mcp_test.go                               |   10 +-
 nodes/ai_ollama_test.go                            |   16 +-
 nodes/ai_test.go                                   |   18 +-
 nodes/ai_tools_test.go                             |    8 +-
 nodes/annotation.go                                |    6 +-
 nodes/apostrophe_live_test.go                      |    4 +-
 nodes/assignments.go                               |    6 +-
 nodes/bindings_test.go                             |   10 +-
 nodes/code.go                                      |    8 +-
 nodes/code_test.go                                 |   10 +-
 nodes/conditions.go                                |    4 +-
 nodes/core.go                                      |    9 +-
 nodes/database.go                                  |   10 +-
 nodes/database_test.go                             |   12 +-
 nodes/datastore.go                                 |   16 +-
 nodes/datastore_test.go                            |   20 +-
 nodes/datastore_tool_test.go                       |   12 +-
 nodes/datetime.go                                  |   10 +-
 nodes/datetime_test.go                             |    8 +-
 nodes/embedscope.go                                |    6 +-
 nodes/embedscope_test.go                           |    4 +-
 nodes/error_workflow.go                            |   12 +-
 nodes/error_workflow_test.go                       |    8 +-
 nodes/executors.go                                 |   20 +-
 nodes/executors_test.go                            |   12 +-
 nodes/flow.go                                      |   10 +-
 nodes/flow_test.go                                 |   10 +-
 nodes/http.go                                      |   14 +-
 nodes/http_test.go                                 |   12 +-
 nodes/jscode.go                                    |    6 +-
 nodes/jscode_test.go                               |    6 +-
 nodes/loop.go                                      |    6 +-
 nodes/mysql_v2.go                                  |   10 +-
 nodes/mysql_v2_test.go                             |   10 +-
 nodes/pgvector.go                                  |    8 +-
 nodes/pgvector_test.go                             |   54 +-
 nodes/postgres_v2.go                               |   16 +-
 nodes/postgres_v2_test.go                          |   10 +-
 nodes/presentation_test.go                         |   39 +-
 nodes/routing.go                                   |    6 +-
 nodes/sql_options.go                               |    4 +-
 nodes/sql_options_live_test.go                     |   31 +-
 nodes/sql_options_test.go                          |    6 +-
 nodes/sqlite_attach_test.go                        |    8 +-
 nodes/subworkflow.go                               |   12 +-
 nodes/subworkflow_calls_test.go                    |    4 +-
 nodes/telegram.go                                  |    6 +-
 nodes/telegram_download.go                         |    8 +-
 nodes/telegram_lifecycle.go                        |    6 +-
 nodes/telegram_test.go                             |   18 +-
 nodes/transform.go                                 |    6 +-
 nodes/transform_test.go                            |    6 +-
 nodes/unsupported.go                               |    6 +-
 nodes/wait.go                                      |   10 +-
 nodes/webhook.go                                   |   30 +-
 packs/telegram/telegram.go                         |    8 +-
 packs/telegram/telegram_test.go                    |   20 +-
 packs/waha/waha.go                                 |   10 +-
 packs/waha/waha_test.go                            |   38 +-
 pkg/sdk/example/echo/main.go                       |    2 +-
 pkg/sdk/sdk_test.go                                |    2 +-
 pkg/sdk/wasm_exec_test.go                          |    2 +-
 scripts/check-coordinates.sh                       |   73 +-
 scripts/config-reference.go                        |    2 +-
 scripts/config-reference_test.go                   |    2 +-
 scripts/generate-api-reference.mjs                 |   19 +-
 scripts/smoke-cli.sh                               |  228 ++++
 sdk/CHANGELOG.md                                   |   23 +-
 sdk/LICENSE                                        |  202 ++++
 sdk/README.md                                      |   99 +-
 sdk/RELEASING.md                                   |  188 +++
 sdk/examples/host-page/README.md                   |   64 +-
 sdk/examples/host-page/server.mjs                  |   68 +-
 sdk/package.json                                   |   11 +-
 sdk/pnpm-lock.yaml                                 |    3 +
 sdk/scripts/check-example.mjs                      |  320 ++++++
 sdk/scripts/check-package.mjs                      |  315 +++++
 sdk/scripts/lib/pack.mjs                           |   77 ++
 sdk/scripts/lib/release.mjs                        |  266 +++++
 sdk/scripts/release.mjs                            |  149 +++
 sdk/src/generated/models.ts                        | 1115 ++++++++++++++++--
 sdk/src/http.ts                                    |   35 +-
 sdk/src/server.ts                                  |  271 ++++-
 sdk/test/operation-coverage.test.mjs               |   38 +-
 sdk/test/operations.test.ts                        |   31 +
 sdk/test/release-workflow.test.mjs                 |  223 ++++
 sdk/test/release.test.mjs                          |  390 +++++++
 sdk/test/server.test.ts                            |  108 ++
 web/src/lib/api/generated/admin/admin.ts           |   94 ++
 .../api/generated/datastore-rows/datastore-rows.ts |    4 +-
 web/src/lib/api/generated/models/binaryRemoval.ts  |   16 +
 .../generated/models/executionNodeRunResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |    6 +
 .../lib/api/generated/models/notReadyProblem.ts    |   31 +
 .../lib/api/generated/models/readyDatastores.ts    |   19 +
 .../api/generated/models/readyDatastoresSpread.ts  |   12 +
 .../lib/api/generated/models/readyOutputBody.ts    |    3 +
 .../api/generated/models/tenantDeletionResource.ts |   24 +
 .../models/tenantDeletionResourceRemoved.ts        |   12 +
 web/src/lib/api/generated/nodes/nodes.ts           |    8 +-
 web/src/lib/api/generated/system/system.ts         |   18 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   97 ++
 .../workflow-editor/property-field.svelte          |   19 +
 .../workflow-editor/property-field.test.ts         |   81 ++
 web/src/lib/dashboard/cursor-page.test.ts          |  285 ++++-
 web/src/lib/dashboard/cursor-page.ts               |   92 +-
 web/src/lib/dashboard/execution-list.test.ts       |  148 ++-
 web/src/lib/dashboard/execution-list.ts            |   98 +-
 web/src/lib/dashboard/workflow-list.test.ts        |   20 +-
 web/src/lib/dashboard/workflow-list.ts             |   21 +-
 web/src/lib/datastore/columns.test.ts              |   41 +
 web/src/lib/datastore/columns.ts                   |   22 +
 .../routes/(dashboard)/app/workflows/+page.svelte  |    9 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |    5 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  143 ++-
 563 files changed, 53484 insertions(+), 2281 deletions(-)
```
