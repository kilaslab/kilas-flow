---
id: FEAT-vntngh
title: Google Sheets node, trigger and tool (the most-used n8n app node)
status: done
priority: critical
labels:
    - n8n
    - parity
    - node-catalog
    - google
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-10-16T15:25:46Z"
---

# Description

Google Sheets appears in 33.5% of the 998 sampled n8n templates (45.7% of the newest). It is the #1 app node, and after Code it unlocks the most templates. KilasFlow has no Sheets node, and because HTTP Request cannot attach an OAuth2 credential, there is no workaround either.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-2). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** `n8n-nodes-base.googleSheets` appears in 334/998 templates (33.5%; 45.7% of the newest), #6 overall and #1 among app nodes. The Sheets family (node, Trigger, Tool) together appears in 35.0% of templates (48.1% of the newest). It is #1 on n8n.io/integrations and #7 on n8n Pulse.

# Steps to Reproduce

1. Import Manual → Google Sheets v4.7. I used a real instance from template 19200. Correction (2026-10-09): 19200 has no `appendOrUpdate` node. Its 8 Sheets nodes are 2 × read (no `operation`, so n8n's default `read`), 3 × append (`columns` defineBelow) and 3 × update (`matchingColumns: ["row_number"]`), with `documentId`/`sheetName` locators and the `columns` resourceMapper. `appendOrUpdate` is still an n8n operation we implement; it just is not in this template.
2. Run it.
3. Repeat with `googleSheetsTrigger` and `googleSheetsTool`.

# Expected

Sheets read / append / appendOrUpdate / update / delete / clear, plus the trigger (events `rowAdded`, `rowUpdate`, `anyUpdate`; n8n spells the update event `rowUpdate`, not `rowUpdated`) and the tool variant, import and run.

# Actual

- Import issue: blocking "KilasFlow has no equivalent of the n8n node \"n8n-nodes-base.googleSheets\". It was imported as an unsupported placeholder: the workflow can be edited, but it cannot run until this node is replaced." The parameters survive only inside the `original` capsule.
- The run fails with 422: "this node was imported from n8n-nodes-base.googleSheets, which KilasFlow does not support."
- There is also no workaround. `GET /api/v1/credential-types` has no Sheets or generic OAuth2 type, and the HTTP Request node cannot attach an OAuth2 credential (see NG-19).

# Acceptance Criteria
- [x] `n8n-nodes-base.googleSheets` imports onto a native `kilasflow.googleSheets` that implements read/append/appendOrUpdate/update/delete/clear, using n8n's operation and value strings and the `columns` resourceMapper. Evidence: `TestSheetsOperationsImportVerbatimWithTheirLocatorsAndMapper`, `TestSheetsVersionDependentDefaultsAreWrittenDown`, `TestSheetsNodesGoBackToN8NAsTheyCame` (internal/interop/n8n); the executor against an in-memory Sheets in `nodes/google_sheets_test.go` and `google_sheets_write_test.go`; the pickers in `internal/loadoptions/googlesheets_test.go`.
- [x] `googleSheetsTrigger` (rowAdded/rowUpdate/anyUpdate) runs on the leased poll framework, and `googleSheetsTool` is usable by the AI Agent. **Trigger half met in wave 1** (`nodes/google_sheets_trigger_test.go`: baseline tick, the three events, columnsToWatch, deleted rows, row cap, `TestPollHandlersCoverEveryTriggerExtractPollsRecognises`). **Tool half met in wave 2** (2026-10-16): `kilasflow.googleSheetsTool` is one `toolForms` row. Evidence: `nodes/google_sheets_tool_test.go` (operations, author-only keys, automatic mapping refused at validation and at run time, no header from the agent's item, the header row left alone), `nodes/google_sheets_tool_security_test.go` (every tool write is RAW, the column map, the filter list and the schema stay the author's, no column deletes), `nodes/google_sheets_tool_run_test.go` (an agent with a scripted model against the httptest Sheets stub: a read with a `$fromAI` filter, an append and an upsert with `$fromAI` column values, all reaching the stub as data), `nodes/google_sheets_literal_test.go` and the tool half of `google_sheets_tool_run_test.go` (a cell holding `{{ $env.X }}` through `={{ $json.x }}` into `columns.value` and `filtersUI.lookupValue` is written and compared literally, for the node and the tool; the tests fail if the parameters are evaluated twice), and in `internal/interop/n8n`: `sheets_tool_import_test.go`, `sheets_tool_fixture_test.go` over the hand-authored `testdata/n8n_sheets_tool_19348.json` (no blocking issue apart from credentials, compiles on an agent, export then import gives the same tools) and `sheets_tool_security_test.go`. Verified 2026-10-16 on the branch: `go test ./... -skip TestLuxonMatchesTheRecordedNodeGoldens -count=1`, `make generate-docs-facts-check generate-api-check generate-types-check generate-skills-index-check`, `cd web && pnpm check && pnpm test`, and `cd e2e && KILASFLOW_E2E_PREBUILT=1 pnpm exec playwright test tests/node-coverage.spec.ts` (18 passed) on a fresh `make build-all`.
- [x] Uses the existing Google OAuth2 credential flow from Drive/Gmail. Evidence: `TestTheSheetsCredentialTypesAreN8NsOwnIDs`, `TestEveryGoogleCredentialTypeIsRecognisedAsGoogleOAuth`, `TestGoogleDefaultScopeIsThePerTypeScope` (internal/credentials), `TestConnectAsksForTheSheetsScopeWhenTheCredentialSavedNone` (internal/api), `TestRefreshingCredentialStoreRefreshesAnExpiringSheetsToken` (internal/repository); the credentials page shows Connect for both types.
- [x] Template 19200 imports with no blocking issue and runs its Sheets step against a stub or real sheet. Evidence: the hand-authored fixture `internal/interop/n8n/testdata/n8n_sheets_19200.json` imports with zero blocking issues and compiles once a credential is bound (`TestTheSheets19200ShapeImportsWithNoBlockingIssue`, `TestTheSheets19200ShapeCompilesOnceACredentialIsBound`), and runs end to end against an in-memory Sheets (`TestAnImportedSheetsWorkflowRunsAgainstTheStub`). The real template, fetched on 2026-10-09 into a scratch directory (never the repo): 21 nodes, 8 of them Sheets, imported with 0 blocking issues and no placeholder, and compiled with stand-in credentials (`TestARealSheetsTemplateImportsWithNoBlockingIssue`); its first Sheets step (the filtered read of the Accounts tab by gid) ran against the stub and returned the NOT DONE rows with their sheet row numbers (`TestTheRealTemplateRunsItsFirstSheetsStepAgainstTheStub`). Both are gated on `KILASFLOW_TEST_SHEETS_TEMPLATE` and skip without it.

# Implementation Plan

Build on the Google OAuth2 (FEAT-mc6s65) and leased poll trigger (FEAT-wcmk3r) that Drive and Gmail already use. Ship `kilasflow.googleSheets` with n8n's operation and value strings, the resourceMapper `columns`, a Sheets trigger and a tool variant, and add the three importer mappings.

# Notes

Related tickets: FEAT-gcq50s, FEAT-mc6s65, FEAT-wcmk3r

Related (from the audit): FEAT-gcq50s explicitly marks Google Sheets "out of scope". No open ticket covers it.

## Scope split (2026-10-09)

- Wave 1 (this branch): node, trigger, the two credential types, option/schema loaders, importer mappings, a hand-authored 19200-shaped fixture and an end-to-end run test.
- Wave 2: `googleSheetsTool`, after the generic base-executor-as-tool mechanism from the AI-tools track lands. Acceptance criterion 2 stays half open until then.

## Behaviour notes (written in our own words, 2026-10-09)

Wire-format and behaviour facts only, read as a behaviour reference under memory/licensing.md (2026-09-24). Nothing here is copied from n8n.

Node `n8n-nodes-base.googleSheets` (published versions 3, 4, 4.1 to 4.7; we write 4.7 and read 4 to 4.7, and refuse the v3 shape):
- `authentication` is `oAuth2` (default) or `serviceAccount`. The credential is `googleSheetsOAuth2Api`, or `googleApi` for a service account.
- `resource` is `sheet` (default) or `spreadsheet`. Sheet operations: `read` (default), `append`, `appendOrUpdate`, `update`, `delete`, `clear`, `create`, `remove`. We run the six data operations and refuse `create`, `remove` and the `spreadsheet` resource at import.
- `documentId` is a resource locator with modes `list`, `url` and `id`. `sheetName` has `list`, `url`, `id` (a number or `gid=N`) and `name`. In `list` mode the sheet value is the numeric gid.
- From v4 the `columns` parameter is a resourceMapper value: `{mappingMode: defineBelow | autoMapInputData, value, matchingColumns, schema, attemptToConvertTypes, convertFieldsToString}`. Only one matching column is allowed. For `update`, the pseudo column `row_number` can be the match.
- Read: `filtersUI.values[]{lookupColumn, lookupValue}` with `combineFilters` AND or OR. The default is OR below 4.3 and AND from 4.3. Below 4.5 the option is `returnAllMatches` with the strings `returnFirstMatch` (default) or `returnAllMatches`; from 4.5 it is a boolean `returnFirstMatch`, default false. `options.dataLocationOnSheet.values` carries `rangeDefinition`, `readRowsUntil`, `headerRow` (1), `firstDataRow` (2) and `range`. `options.outputFormatting.values` carries `general` (UNFORMATTED_VALUE, FORMATTED_VALUE, FORMULA) and `date` (FORMATTED_STRING, SERIAL_NUMBER).
- Read output: one item per data row, keyed by the header row, plus `row_number` (the real sheet row). Missing cells are the empty string.
- Write options: `cellFormat` USER_ENTERED (default) or RAW, `locationDefine.values{headerRow, firstDataRow}`, `handlingExtraData` insertInNewColumn (default), ignoreIt or error, `useAppend` bool.
- `delete`: `toDelete` is rows or columns. `startIndex` is a 1-based row number (default 2) for rows and a column letter (default "A") for columns. `numberToDelete` defaults to 1. It emits one `{success: true}` item.
- `clear`: `clear` is wholeSheet, specificRows, specificColumns or specificRange, plus `keepFirstRow`. `range` defaults to "A:F". It passes its input items through.
- `alwaysOutputData` (a node setting) matters here because a read with no match returns zero items.

Node `n8n-nodes-base.googleSheetsTrigger` (version 1):
- `authentication` is `triggerOAuth2` (default) or `serviceAccount`; credential `googleSheetsTriggerOAuth2Api`.
- `event` is `rowAdded`, `rowUpdate` or `anyUpdate` (default). `includeInOutput` is new (default), old or both. `options`: `columnsToWatch`, `dataLocationOnSheet`, `valueRender`, `dateTimeRenderOption`. `pollTimes` as the other poll triggers.
- The first non-manual poll stores a baseline and emits nothing.

`googleSheetsTool` exports as the base parameters plus `descriptionType` and `toolDescription` (wave 2).

Open to confirm before closing: the `update`/`appendOrUpdate` match rule (first row whose matching column text equals the input value) and the output shapes of the write operations. Recorded under Progress once checked.

# Related Files

- `probes/Google_Sheets.json`
- `results-extra.json` (Google Sheets Trigger)
- `results-ai.json` (Google Sheets Tool)
- `greedy-unlock.json`: Sheets is the #2 unlock (+85 templates).
- `internal/interop/n8n/n8n.go:803-816`

# Attachments

# Progress (2026-10-09)

Wave 1 is done: node, trigger, the two credential types, the pickers, the importer mappings, the hand-authored fixture and the end-to-end run. The tool is wave 2 and is not started.

## What changed

- **Credentials.** `googleSheetsOAuth2Api` and `googleSheetsTriggerOAuth2Api` (n8n's IDs, two types as the orchestrator decided), registered with `googleOAuthType`, covered by `IsGoogleOAuth` (so Connect and the token refresh work), with the least-privilege default scopes set in the review round below (they replaced a first version that mirrored n8n's wider lists). New `credentials.GoogleDefaultScope`; the OAuth start handler uses it instead of a Gmail-or-Drive guess, which would have asked a Sheets credential for Drive access. The credentials page shows Connect for both. `docs/.../concepts/credentials.md` has the two IDs in the empty-allowed-hosts table and in the file list; its catalogue table is already stale (it says thirteen types and lacks Drive and Gmail) and is left to the docs track.
- **Node** (`nodes/google_sheets*.go`): `kilasflow.googleSheets` and `kilasflow.googleSheetsTrigger`, executors `core.googleSheets` and `core.googleSheetsTrigger`, registered at version 1 like Drive and Gmail (the importer keeps n8n's 4.x typeVersion and the registry resolves down). `Validate` refuses service-account sign-in, the spreadsheet resource, create and remove, an unset document or tab, and an update or upsert without exactly one matching column. The node is `WholeBatch`: see the deviations.
- **Trigger**: `GoogleSheetsPoller` keeps `{v, doc, gid, lastRow, rows: {row: fingerprint}}`. The first tick stores a baseline; rowAdded emits rows past `lastRow` (keyed by header, no `row_number`, as n8n's does); rowUpdate emits rows whose fingerprint changed; anyUpdate emits both with `change_type` added or updated; `columnsToWatch` narrows the fingerprint; a different tab starts a new baseline; a sheet over 20,000 data rows is refused by name. `nodes.PollHandlers` is the one handler list main wires, and a test holds it equal to what `ExtractPolls` recognises.
- **Pickers** (`internal/loadoptions/googlesheets.go`): `googleSheets.documents` (Drive list), `googleSheets.sheets` (tabs valued by gid), schema loader `googleSheets.columns` (the header row; `row_number` as a read-only match column for update only). Each call applies the egress policy, the credential's allowed hosts, the redirect scope and the credential's own authentication.
- **Importer** (`internal/interop/n8n/googlesheets.go`): both mappings; the parameter tree is converted with expressions marked, the mapper's schema rows are left untouched (a label starting with `=` is not an expression); the version-dependent defaults are written down (`combineFilters` OR below 4.3, AND from 4.3; `options.returnAllMatches` below 4.5 becomes `returnFirstMatch`; a missing `operation` is `read`). Export goes back at the version the node came from and restores the old spelling below 4.5. Blocking: version 3, service account (setting or `googleApi` credential), resource spreadsheet, create, remove, unknown operation. Lossy: trigger `includeInOutput` old or both, imported as new. No rebind issue on `documentId`: a Google ID is valid on any instance.
- **Fix found on the way**: BUG-8xz9t1 (nested expression markers exported as objects), fixed test-first in its own commit before anything depended on it.
- **Glyph and inventories**: `sheet` in `glyphs.json` and `node-visual.ts`; both types in the e2e validation inventories, each checked to refuse a run without its own credential.
- **Catalogue size** in `docs/.../node-registry.md` and `start/what-kilasflow-is.md` goes from 62 to 64 types (test `TestTheDocumentedCatalogueSizeIsTheShippedOne`).

## Deviations from the plan

- **WholeBatch.** The plan left the node per-item. Under `onError` other than stop the runner then split it into one-item calls, each re-reading the sheet: the fixture's update node (it has `continueErrorOutput`, as template 19200's does) made 2 reads and 2 writes for 2 items, and a hundred rows would spend Google's quota. n8n's node runs the whole operation in one try and gives one error item for it under Continue on fail, so the node is `WholeBatch` and a tolerated failure is an `engine.BatchFailure`. `TestASheetsFailureUnderTheErrorOutputIsOneErrorItem` and the request counts in `TestAnImportedSheetsWorkflowRunsAgainstTheStub` pin it. `batch_shape_test.go` lists the node as batch-shaped.
- **Append writes at an exact range.** The plan said `values:append` with `INSERT_ROWS`. n8n's default counts the last used row and writes the rows there (Google's append finds "the table" and can insert rows after the first gap), growing the grid by exactly what is needed; `useAppend` opts into Google's call, as in n8n. Header cells are always written RAW.
- **`row_number` in an A1-range read is the sheet's real row**, not the position inside the fetched range (n8n numbers from the fetched data, which is wrong for any range that does not start at row 1).
- **Short rows are padded with empty text in every read mode**; n8n pads only in detect-automatically mode.
- **New columns in automatic mapping are added in sorted order** (items are unordered maps here; n8n uses key insertion order).
- **OR filters with only the first match wanted stop at the first match overall** (n8n 4.6 and later). Imports below 4.6 get that too; the registry resolves every Sheets node to version 1 at run time, so a version-dependent run-time rule cannot exist.
- **Keep First Row on Clear** clears rows 2 to the end of the grid instead of clearing and rewriting the first row (n8n rewrites it as RAW from its formatted text, which flattens formulas).
- **The trigger diffs fingerprints, not Drive revisions** (needs `spreadsheets` only; `includeInOutput` old and both are lossy). Added rows count when no fingerprint existed for the row, so the three events differ slightly from n8n's revision diff in corner cases (a gap row that is filled later is "added" for anyUpdate, and an addition for rowAdded only past the last row).
- The fixture's `lookupValue` is `={{ $json.status }}`, fed by the run test's trigger input, and all four Sheets nodes share one placeholder document (the stub serves one). The real template uses two documents.
- Plan step 6 also asked to extend `TestMirroredNodeTypesMatchTheNodePack`; the Sheets constants live in the new importer file and `TestSheetsTypesMatchTheNodePack` pins them, so the shared test table is untouched.

## Behaviour confirmed against n8n's public source (behaviour only, memory/licensing.md 2026-09-24)

Read on 2026-10-09 from the n8n repository on GitHub (packages/nodes-base/nodes/Google/Sheet), in the Sheets v2 operation files, the sheet helper and the router, written down here in our own words; nothing was ported.

- **Update and appendOrUpdate match rule: confirmed.** The sheet is read once as formatted text; the matching column's cells are compared as text with the input value, and the first equal one wins. Every item is matched against that snapshot, so two items with a new key both append. The matching cell itself is not rewritten. Only columns present on the item and not null are written, one cell per range, in one batch update; with `row_number` as the match the row is addressed directly. A hand-mapped null becomes empty text before writing, and an empty match value is an error.
- **Output shapes: confirmed.** Append, appendOrUpdate and update emit the input items unchanged under automatic mapping and the mapped values per item under hand mapping (update returns nothing when it wrote nothing); delete emits one `{success: true}`; clear returns its input items; read emits header-keyed rows plus `row_number`.
- **Document and tab are read at item 0, and the whole operation sits in one try**, with one error item under Continue on fail (the WholeBatch deviation above).
- **Read:** default `readRowsUntil` is last row in sheet; the "first empty row" mode cuts at the first blank row, any other value drops every blank row; leading blank rows are skipped and the header is the first non-empty row; a column with an empty heading and no data is dropped, and a heading that is empty but has data is named by its position; filters compare text and an unknown filter column is an error; with no filters every row is returned and "return only first matching row" is not applied (the option's description says otherwise; the code does not).
- **Delete** defaults to row 2 (rows) or column A, one row or column; **clear** defaults to the whole sheet, row 1 or column A and range `A:F`, and keeping the first row is offered only for the whole sheet.
- **Trigger output:** rowAdded rows are header-keyed with no `row_number`; update events add `row_number` (the real row), and anyUpdate adds `change_type`; the first non-manual poll only records a baseline.

**Unverified:** the trigger's behaviour when run manually from the editor (n8n returns the rows already there; ours has no manual poll), the exact effect of Google's `USER_ENTERED` parsing on values (the stub does not parse), and how the real API treats a `null` cell in an update (we rely on Google documenting null as "leave the cell alone"; the stub models that). No live Google account was used.

## Risks and follow-ups

- Quota: about 60 reads a minute per user. The node reads the tab list once and the sheet once per run, and writes in one request each; a trigger tick is two reads.
- `drive.metadata.readonly` (restricted by Google) is in both default scopes, for the spreadsheet picker. Without Google's verification of the platform client, edit the credential's Scope field; the url and id locator modes need no Drive scope. `spreadsheets` is a sensitive scope.
- The Drive and Gmail credential types still share the OAuth userinfo Test probe, which a token without a profile scope may refuse. The Sheets types now probe Drive's about endpoint instead; a fix for the other two is outside this ticket.
- The shared `resolveGoogleIR` also evaluates `{{ }}` inside already-resolved values (Track C's fix is in flight). A Sheets-level regression test for a cell value that contains `{{ }}` should be added after the rebase onto it.
- Wave 2: `googleSheetsToolNode()` over `toolVariantOf(...)`, `toolVariants["kilasflow.googleSheets"]`, the glyph variant, the e2e inventories, a fixture shaped like template 19348 and an agent run against the stub.

## Verification (2026-10-09)

- `go vet ./...` and `gofmt -l .`: clean.
- `go test ./... -race -skip TestLuxonMatchesTheRecordedNodeGoldens`: 54 packages ok; three failed, none in code this change touches. `nodes` fails under `-race` on a data race inside the test helper `stubCredentials` (`nodes/http_test.go:30`, appended to from parallel subtests): `TestAnOpenAIChatModelFallsBackToItsCredentialsBaseURL` fails alone under `-race` and the Sheets tests are race-clean (`go test ./nodes -race -run 'TestSheets|TestGoogleSheets|TestAnImportedSheets|TestASheetsFailure|TestPollHandlers'` passes). `jsrun` (two tests over their 10s limit) and `runcode` (10m package timeout) failed on a loaded machine under `-race`. Without `-race`: `go test ./nodes ./internal/jsrun ./internal/runcode -skip TestLuxonMatchesTheRecordedNodeGoldens` all ok.
- `make generate-api-check generate-types-check generate-api-reference-check generate-config-reference-check generate-skills-index-check generate-skills-command-reference-check corpus-check`: every generated artefact is fresh (no new endpoint, key, verb or skill); `corpus-check` skipped its scoreboard as "no corpus materialised".
- `pnpm check` (0 errors) and `pnpm test` (810 tests) from `web/`.
- e2e `tests/node-coverage.spec.ts` against a freshly built binary: 16 of 18 pass, including the Google nodes refusing to run without their own credential. The two failures (`the error workflow pair and the hosted form run against the binary`, `a sticky note saves and runs without affecting the result`) are in nodes this change does not touch (a node name where the test expects its id; a missing sticky-note trace row) and were not re-run on a clean main.

# Review round (2026-10-09)

Done after the first review, on top of main merged in (docs catalogue count and credential-type count come from `make generate-docs-facts`; the credentials catalogue table has rows for the two types).

- **Scopes, least privilege.** The node defaults to `spreadsheets` and `drive.metadata.readonly`; the trigger to `spreadsheets.readonly` and `drive.metadata.readonly`. n8n's wider lists are not needed: an import never carries a credential. Each call was checked against the scopes Google's reference lists for it (read 2026-10-09), and `TestEveryCallTheSheetsNodesMakeIsCoveredByTheirDefaultScope` pins it:
  - spreadsheets.get and values.get accept `spreadsheets` and `spreadsheets.readonly`, so the node, the trigger and the tab and column pickers are covered;
  - values.update, append, batchUpdate, clear and spreadsheets.batchUpdate accept `drive`, `drive.file` and `spreadsheets`, so the node's writes are covered by `spreadsheets` (the trigger makes none);
  - Drive files.list (the spreadsheet picker) accepts `drive.metadata.readonly`;
  - **one call needed a change:** the credential Test button used the OAuth userinfo endpoint, which needs a profile scope neither type asks for. The two Sheets types now call Drive's about endpoint (`about.get` accepts `drive.metadata.readonly`). Nothing else needed more than the default.
- **Wording.** Every error string that echoed n8n's is reworded in our own terms, and the header-row guard is two plain checks (the header row is among the rows read; the first data row is not above it). Covered by `TestSheetsErrorsSayWhatToFix`.
- **Trigger after an edit.** The cursor carries a fingerprint of the watched columns, how values and dates are rendered, the range and the header and first data rows; any change starts a new baseline and emits nothing (`TestSheetsTriggerStartsAgainWhenItsConfigurationChanges`). A tick that finds nothing hands the stored cursor back unchanged (`TestSheetsTriggerLeavesTheCursorAloneWhenNothingChanged`); the poll service still stamps the lease fields when it completes a tick, and `Complete` writes the cursor column with them, so the saving is the re-encoding, not the row write.
- **Column names are the heading as written** (spaces included) everywhere: the picker, a mapped value, a read row's keys, a filter, Columns to Watch (`TestSheetsColumnNamesAreTheHeadingAsWritten`, `TestGoogleSheetsColumnsKeepAHeadingAsWritten`, `TestSheetsTriggerWatchesAColumnByItsHeadingAsWritten`).
- **A1 helpers** live in `internal/sheetsa1`: a range's sheet part is cut at the last `!` outside quotes (a tab may be called `a!b`), shared by the node, the trigger and the pickers.
- **Pickers.** The columns loader reads the header row the node names (Options > Data Location on Sheet for writes, the trigger's rows or A1 range); it needs the node's Options collection, which the node-types endpoint now sends as JSON for a structured dependency (it was Go's `map[...]` text). A tab arriving as a link is found by its `#gid=`. New loader `googleSheets.headings` gives Columns to Watch a list of the tab's columns; for that, the load-options endpoint finds a property inside a collection or a fixed-collection group by its own key (a top-level property of the same key still wins), and the editor's multi-select shows a loaded list.
- **Smaller.** `includeInOutput` old and both are refused by Validate (still imported as new with a lossy note; the select offers only New). An automatic-mapping item whose match value is empty text matches no row. Rows are compared by position, so deleting a row reports the rows below it as updated: said in the trigger's description and pinned by `TestSheetsTriggerReportsTheRowsBelowADeletedRowAsUpdated`. A loader redirect-scope test mirrors the HTTP loader's. `TestPollHandlersCoverEveryTriggerExtractPollsRecognises` asks the catalogue which types poll.
- **Not done on purpose:** the `{{ }}` regression test and `googleSheetsTool`, which wait for Track C.

## Second review round (2026-10-09)

- **Load-options lookup.** The lookup is by key: a node's own parameter first, then the first declared property inside a collection or a fixed collection's group. Tests pin both orders (`TestAKeyThatIsBothTopLevelAndNestedIsAnsweredByTheTopLevelProperty`, `TestTwoNestedPropertiesWithOneKeyAreAnsweredByTheFirstDeclared`), the `property` field's API doc says so, and `TestNoShippedNodeHasTwoLoaderBearingPropertiesWithOneKey` holds the whole shipped catalogue to having none.
- **Multi-select.** A loader-backed multi-select draws every saved value the loaded list lacks as a checked row labelled by its value, so a saved choice stays visible and can be unticked while the list is loading, has failed, or answers with only a reason (a node just imported from n8n has no credential to load with). SSR tests in `property-field.test.ts`.
- **Header row as an expression.** The picker answers "the header row depends on an expression, which cannot be resolved while editing" and makes no Google call, instead of falling back to row 1. The empty-first-row case is a returned flag now, not a match on the reason's wording.
- **Trigger headings.** The trigger names columns by the heading as the sheet shows it, as the picker lists them. When the values are read as numbers and serial dates and a heading is not plain text (or the rendering is formulas), it reads the heading row once more as shown; text headings cost no extra read. `sheetsa1.HeaderRange` finds the row across the range's columns. Test with a date heading.
- **Tab rename.** The config digest holds the cell range, not the tab's title, so a rename keeps the baseline (the gid identifies the tab).
- **Test probe.** A 403 from Drive's about call on a Sheets credential reads as a pass that says the token is valid and the Drive scope is not granted (`TestRequest.ForbiddenNote`); a 401 is still a failure, and no other credential type has the note.

Risks added:

- **Picker cost.** The Columns to Watch list and the columns mapper depend on the node's whole Options collection, because the loader framework's dependencies are top-level parameter keys, not paths. Ticking a column in Columns to Watch changes that collection and so loads the list again: two Google reads (the tab list and the header row), about 3% of a minute's per-user read quota each time. A path-style dependency (`options.dataLocationOnSheet`) would stop it but changes the framework in both the editor and the endpoint; not done here.

# Plan (wave 2, 2026-10-09)

Track C's generic tool-form mechanism is on main (`nodes/tool_form.go`, `internal/interop/n8n/tool_forms.go`), and so is the `resolveGoogleIR` fix (a resolved value is never evaluated again). Wave 2 is `kilasflow.googleSheetsTool` as one row of that table, with the importer line, a fixture, an agent run against the Sheets stub, and the literal-value regression test the first review asked for.

**Shape of the tool.** One `toolForms` row: tool type `kilasflow.googleSheetsTool` over `kilasflow.googleSheets` v1. The agent runs the node's own executor (egress policy, credential check, batching and writes inherited), on the agent's current item, with the model's values reaching it only through `$fromAI` inside expressions.

**Operations the tool allows: read (the default), append, appendOrUpdate, update and delete. Not clear.**

- read, append, appendOrUpdate and update are what an assistant does with a table: look a row up, add one, change one. n8n template 19348 (read on 2026-10-09 into a scratch directory, never the repo) is an agent whose Sheets tool upserts an order by its ID with every column a `$fromAI` value.
- delete is allowed because its reach is bounded by what the author fixes: whether rows or columns (`toDelete`) and how many (`numberToDelete`) are author-only, so one call removes a span of known size, and the model picks only where it starts.
- clear is refused: it empties a whole tab or a range in one call, has no row-level scope to bound, and no assistant needs it. Validation and the importer both say so by name.

**Author-only keys (a `$fromAI`, an expression or `{{ }}` in them is refused).** `documentId` and `sheetName` (the model never picks which spreadsheet or tab: a credential can see many, and a model steered by text in a cell must not roam across them), `options` (cell format, extra-field handling, the header row and the A1 range a read uses), `combineFilters`, `toDelete`, `numberToDelete`. Filter column names (`filtersUI` `lookupColumn`) are author-only too, and so are the keys of `columns.value` (a key holds no `{{`); only the values are the model's. The generic rules apply on top: `resource` and `operation` are the author's, no `$fromAI` in plain text, and `$fromAI` calls must agree.

**Automatic column mapping is refused in the tool form**, as the PostgreSQL tool does and for the same reason: automatic mapping writes the running item's fields as the row, and a tool runs on the agent's input item, which is the conversation. At validation (`validateGoogleSheetsTool`, via the row's `validate`) for append, appendOrUpdate and update; at run time in the executor, whenever `request.FromAIArguments` is set, before any Google call. Two related guards in the same place: the "empty sheet falls back to automatic mapping" rule is off for a tool (it would make the item's keys the header), and the executor reads no item fields for a tool.

**Importer.** One `toolVariants[GoogleSheetsNodeType]` line, with a translator that is the generic tool-form one plus the Sheets rules as blocking issues at import: `clear`, automatic mapping on a write, and a document, tab, option or filter column that is an expression. `descriptionType` and `toolDescription` cross as for every tool form. Export is the derived one.

**Tests, written first.**

1. `nodes`: the tool-form table row and the shared derivation test (`toolFormRows`); `TestTheGoogleSheetsToolKeepsItsCallsTheAuthors` (operations, author-only keys, filter columns, `columns.value` keys); `TestTheGoogleSheetsToolRefusesAutomaticMapping` at validation and at run time against the stub, with the ordinary node still mapping automatically.
2. A scripted-model agent run against the Sheets stub: a read whose filter value is a `$fromAI` and an append whose column values are `$fromAI`, asserting what reached the stub and that a model-supplied `{{ $env.SECRET }}` arrives as text.
3. The regression test: a cell holding `{{ $env.X }}` that flows through `={{ $json.x }}` into `columns.value` and into `filtersUI.lookupValue` is written and compared literally, for the plain node and for the tool.
4. `internal/interop/n8n`: a hand-authored fixture shaped like template 19348 (an agent, a chat model, and Sheets tools whose columns are `$fromAI`, with n8n's override comment and backtick descriptions), imported with no blocking issue apart from credentials, compiled on the agent, exported and imported again.
5. Glyph variant, the e2e node-coverage tier, the catalogue counts through `make generate-docs-facts` (never typed), `shippedcatalogue` unchanged unless a registration call changes.
6. Skills: a validated query-side example in `skills/kilasflow-ai-agents/references/RAG.md` (Agent, chat model, `kilasflow.vectorStoreTool`, retrieve-mode store, embeddings), tool discovery left generic.

Out of scope: a `clear` tool, a tool that picks its spreadsheet at run time.

# Progress, wave 2 (2026-10-09, stopped at the time box)

Wave 2 is part done. Both commits below are green (`go test ./nodes/` passes, `go build ./...`, `generate-docs-facts-check`, `generate-types-check`, `generate-api-reference-check`, `generate-skills-index-check` and `generate-config-reference-check` are clean). AC2's tool half is NOT ticked yet: the importer, the agent runs and the skills example are still to do.

## Done, with evidence

- `98f0363d` the tool form. `kilasflow.googleSheetsTool` is one `toolForms` row (`nodes/tool_form.go`), with its rules in `nodes/google_sheets_tool.go` and the constant `GoogleSheetsToolNodeType` in `nodes/google_sheets.go`. Operations: read (default), append, appendOrUpdate, update, delete; `clear` is refused by name (it empties a tab or range in one call, with nothing to bound it). Author-only keys: `documentId`, `sheetName`, `options`, `combineFilters`, `toDelete`, `numberToDelete`, the filter column (`lookupColumn`), the whole `columns` parameter, `mappingMode`, `matchingColumns` and the column names; the model supplies values and where a delete starts. Automatic column mapping is refused at validation and again in the executor, before any Google call (`refuseSheetsToolRun`), including a mode an expression computes. Two further tool-run guards, which the plan did not name: an empty sheet does not take its header from the agent's item (the red run wrote `chatInput`/`sessionId` as a header and a row), and a tool call neither updates by a `row_number` above the first data row nor deletes row 1. Tests, written first and seen failing: `nodes/google_sheets_tool_test.go` (operations, author-only keys, automatic mapping at validation and run time, the empty-sheet header, the header-row guard), the Sheets row in `toolFormRows` (`nodes/tool_form_test.go`, shared derivation and refusal cases) and the glyph variant in `nodes/glyphs_test.go`. The fake-Sheets tests are not `t.Parallel()` because `newFakeSheets` swaps a package-level API root.
- `84529e42` catalogue counts regenerated with `make generate-docs-facts` (69 types, 76 pairs); nothing typed by hand.

## Left, in order

1. Importer. Add `GoogleSheetsToolNodeType = "kilasflow.googleSheetsTool"` to the mirrored constants in `internal/interop/n8n/tool_forms.go` and to the SupportedMappings list and the mirrored-types table in `internal/interop/n8n/n8n_test.go`. Add `GoogleSheetsNodeType: {kilasType: GoogleSheetsToolNodeType, toKilas: googleSheetsToolToKilas}` to `toolVariants` (`internal/interop/n8n/n8n.go`; leave `publishedVersions` nil so the base's 4 to 4.7 apply). `googleSheetsToolToKilas` is `toolFormToKilas(mapping{toKilas: googleSheetsToKilas})`, then restores `columns` with `sheetsMapper(original, fromN8NTree)` so the generic deep `=` pass cannot turn a schema label such as `=total` into an expression, then adds blocking issues. Plan for the issues: export `nodes.CheckToolForm(toolType, parameters)` with a typed `*nodes.ToolFormRefusal{Key, Reason}` (the validators and the structural-key check return it) so the import report and the validator can never drift; one blocking issue per node with `Field` the refused key. Export is the derived `toolFormToN8N`; check `googleSheetsToN8N` works on a tool node (it reads only `TypeVersion`). Tests first: blocking for clear, automatic mapping on a write, and an expression in documentId, sheetName, options or a filter column.
2. Fixture and tests: hand-author `internal/interop/n8n/testdata/n8n_sheets_tool_19348.json` (`_source` saying "hand-authored KilasFlow fixture", never the real template): a manual trigger, an agent 3.1, `lmChatOpenRouter` 1, `memoryBufferWindow` 1.3, a `googleSheetsTool` 4.7 `appendOrUpdate` with `defineBelow`, `matchingColumns: ["Order ID"]` and `$fromAI` column values written with n8n's `/*n8n-auto-generated-fromAI-override*/` comment, a second read tool with a `$fromAI` filter value and `toolDescription`, and a `gmailTool` 2.2. Assert: no blocking issue apart from credentials (`blockingApartFromCredentials`), a lossy note for the generated description, `compileImported` on the agent, export as `n8n-nodes-base.googleSheetsTool` v4.7 with `descriptionType: manual`, and import of the export equal to the first import.
3. `docs/src/content/docs/guides/n8n-migration.md`: AI table row `n8n-nodes-base.googleSheetsTool` to `kilasflow.googleSheetsTool`; the credentials and import docs only where a list already names node types. Then `make generate-docs-facts` again if a count moves.
4. e2e: add `'kilasflow.googleSheetsTool@1'` beside `'kilasflow.gmailTool@1'` (about line 111) and an entry in the tool-form loop (about line 927) that expects `toolDescription is required`, in `e2e/tests/node-coverage.spec.ts`; run `cd e2e && pnpm exec playwright test tests/node-coverage.spec.ts`.
5. Agent run against the Sheets stub (`nodes/google_sheets_tool_run_test.go`, not parallel): `formHarness` plus `newFakeSheets`, a credential map holding `cred-key` and a `googleCreds`-style Google credential for `nodes.GoogleSheetsCredentialType`, a scripted model that calls the tool with (a) a read whose filter value is a `$fromAI` and (b) an append whose column values are `$fromAI`; assert what reached the stub (`fake.snapshot`, `callsMatching`) and that a model value `{{ $env.SECRET }}` is stored as text.
6. The regression test the first review asked for: a sheet cell holding `{{ $env.X }}` flows through `={{ $json.x }}` into `columns.value` and into `filtersUI.lookupValue`, is written and compared literally and never evaluated, for the plain node and for the tool (put it in the same test file as 5).
7. Skills: in `skills/kilasflow-ai-agents/references/RAG.md` add a query-side example (Agent, chat model, `kilasflow.vectorStoreTool`, a retrieve-mode store, embeddings) in a ```json kilasflow-workflow fence so `TestEverySkillExampleValidates` checks it; keep tool discovery generic. `make skills-check`.
8. Tick AC2's tool half with that evidence, then run: `go test ./... -skip TestLuxonMatchesTheRecordedNodeGoldens`, every `make generate-*-check` (including `generate-api-check`, not run here because it needs `web-sync`), `make skills-check`, `pnpm check`, `pnpm test`, the e2e node-coverage spec, `go vet ./...`, `gofmt -l`.

Follow-up worth its own ticket: the generic tool-form importer reports no blocking issue when a Gmail, Postgres or Telegram tool breaks a tool-form rule (it fails at validation instead); the typed refusal in step 1 would let `toolFormToKilas` do it for every form.

## Where wave 2 stopped (2026-10-09, 21:00)

The session stopped at the owner's time box. Wave 1 (node, trigger, credentials, loaders, importer, 19200-shaped fixture) is on main. The wave-2 commits — the plan, `kilasflow.googleSheetsTool` as one tool-form row (`98f0363d`) and the regenerated counts (`84529e42`) — are on branch `worktree-agent-a3294cd4f4ee7e00c` and are NOT yet reviewed or merged. Resume there: steps 1–8 of "Progress, wave 2" above, then a review, then merge. Follow-up worth its own ticket: the generic tool-form importer reports no blocking issue when a Gmail, Postgres or Telegram tool breaks a tool-form rule (they fail at validation instead); step 1's typed `ToolFormRefusal` would let `toolFormToKilas` report it for every form.


## Resumed window (2026-10-09, ended 20:59)

Main was merged into the branch (`355b4d63`; the one conflict was this ticket, where both Progress notes are kept). After the merge `go build ./...`, `go test ./nodes/ ./internal/interop/n8n/` and `make generate-docs-facts-check` pass. The window was too short to take step 1 (the importer) to green, so nothing of steps 1 to 8 above is started: they stand exactly as listed, in the same order.

### Update at 20:57: step 1 is done in a reduced form

`caa8fd22` is the importer (green: `go test ./internal/interop/n8n/`, `go vet`, `gofmt -l` clean). Done: the mirrored constant `GoogleSheetsToolNodeType` (`tool_forms.go`), the `toolVariants[GoogleSheetsNodeType]` line (`n8n.go`, base versions 4 to 4.7 apply), `googleSheetsToolToKilas` (restores `columns` from the original so a schema label such as `=total` stays plain text; blocks `clear` on `operation` and automatic mapping on a write on `columns`), the SupportedMappings and mirrored-types entries in `n8n_test.go`, and `internal/interop/n8n/sheets_tool_import_test.go` (import with no blocking issue apart from credentials and compile on an agent, the export as `n8n-nodes-base.googleSheetsTool` 4.7 with `descriptionType: manual`, and the two blocking cases). The tool imports at n8n's own typeVersion (4.7), as the Gmail tool does.

Not done from step 1: the planned `nodes.CheckToolForm` with a typed `*ToolFormRefusal{Key}`, which would also block at import a documentId, sheetName, options or filter column that is an expression (today such a tool imports and is refused at validation). Not done from step 2: the `testdata/n8n_sheets_tool_19348.json` fixture file, the lossy-note assertion for a generated description, and the import-of-the-export equality; the tests use an inline node shaped like the template. Steps 3 to 8 are untouched and stand as listed above, with `go test ./nodes/` last green at `98f0363d` plus the main merge.

### Update at 20:59: steps 3 and 4 are done

Step 3: the `n8n-nodes-base.googleSheetsTool` row and a sentence on the blocking cases (clear, automatic mapping) in `docs/src/content/docs/guides/n8n-migration.md`; `make generate-docs-facts-check` is clean (no count moved). Step 4: `'kilasflow.googleSheetsTool@1'` in the coverage list and `{ type: 'kilasflow.googleSheetsTool', version: 1 }` in the tool-form loop of `e2e/tests/node-coverage.spec.ts` (it expects `toolDescription is required`); the file type-checks with `web/node_modules/.bin/tsc` (no error in it), but the Playwright spec itself was NOT run, so run `cd e2e && pnpm exec playwright test tests/node-coverage.spec.ts` before ticking. Still to do, in order: the rest of steps 1 and 2 (the typed refusal, the 19348 fixture file), then steps 5, 6, 7 and 8 as listed above.

# Progress, wave 2 closed (2026-10-16)

Done since the time-boxed update, each step test first and green: the 19348-shaped fixture and its import, compile and round-trip tests (`688f4e81`); the agent runs against the Sheets stub and the `{{ $env.X }}` literal-value regression tests for the node and the tool (`2da156b5`; they fail when the resolved parameters are evaluated a second time); the security review's findings (`09fd4b7f`, `e1add81e`): H1 a tool call always writes RAW (`valueInputOption`) so a model's `=IMPORTRANGE(...)` stays text, and the importer reports it as a lossy note on the cell format; H2 `columns.value` must be a plain map of column names to values (not an expression, text holding `{{`, or a list) and the schema is held to the same author-only rule; M1 `filtersUI`, its `values` list and each entry must be plain, and only `lookupValue` may hold an expression; M2 a tool does not delete columns (refused in the validator and again in `refuseSheetsToolRun`, and blocked at import). The executor now also runs the form's own validator over the authored parameters before it calls Google, so a forged descriptor gets the validator's refusal and no request is sent. AC2 is ticked above.

Left, not done: the typed `nodes.CheckToolForm` / `ToolFormRefusal{Key}` (a documentId, sheetName, options or filter column that is an expression still imports without a blocking issue and is refused at validation instead); the query-side example in `skills/kilasflow-ai-agents/references/RAG.md` (taken by another agent on main); the review of this branch and the merge. Not run in this window: `make skills-check`, `generate-config-reference-check` and `generate-api-reference-check` after the final commits (they were clean at `75b86f13`; nothing since touches their inputs but the node catalogue, whose counts `generate-docs-facts-check` confirms are unchanged).

## Work Evidence

Closed by `pine close --evidence` on 2026-10-16.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (46):
  - `e0ec56a0` — FEAT-vntngh: the n8n-compare matrix names the Google Sheets tool
  - `7e4efb74` — merge: n8n's googleSheetsTool runs as a Google Sheets agent tool whose spreadsheet, tab, columns and filters stay the author's and whose writes are always RAW (FEAT-vntngh wave 2)
  - `c1ae7dd0` — chore(pine): FEAT-vntngh ticks AC2's tool half with its evidence and records what wave 2 left
  - `e1add81e` — FEAT-vntngh: importing a Sheets tool notes that it writes RAW and blocks one that deletes columns
  - `09fd4b7f` — FEAT-vntngh: a Sheets tool call writes RAW, keeps the column map, the filter list and the schema the author's, and does not delete columns
  - `7cb4278a` — merge: the AI-agents skill shows a validated Vector Store Tool query workflow (FEAT-vntngh step 7)
  - `2da156b5` — FEAT-vntngh: an agent's Sheets tool reads and writes the model's values as data, and template text in a cell is never evaluated
  - `de36d9fc` — FEAT-vntngh: the AI-agents skill shows a validated Vector Store Tool query workflow
  - `688f4e81` — FEAT-vntngh: a hand-authored fixture shaped like template 19348 imports, runs on an agent and survives export and import
  - `59f3ce6c` — chore(pine): FEAT-vntngh records wave-2 steps 3 and 4 done on its branch at the 21:00 stop
  - `3f53313c` — chore(pine): FEAT-vntngh marks wave-2 steps 3 and 4 done
  - `cb3352e8` — FEAT-vntngh: the migration guide and the e2e node-coverage tier name the Google Sheets tool
  - `2315beaf` — chore(pine): FEAT-vntngh records the wave-2 steps done on its branch at the 21:00 stop
  - `f817b244` — chore(pine): FEAT-vntngh marks the importer done in a reduced form and lists what is still to do
  - `caa8fd22` — FEAT-vntngh: n8n's googleSheetsTool imports and exports as the Sheets tool form
  - `cf75dd1b` — chore(pine): FEAT-vntngh drops the merge markers and records that steps 1 to 8 are still to do
  - `355b4d63` — merge: main into the Track B wave 2 branch (editor slot and stale-wire fixes; the ticket keeps both Progress notes)
  - `312baef0` — chore(pine): FEAT-vntngh records where wave 2 stopped and which branch holds the unreviewed tool form
  - `75b86f13` — chore(pine): FEAT-vntngh records wave 2 progress and the remaining steps in order
  - `84529e42` — FEAT-vntngh: the catalogue counts follow the Google Sheets tool (make generate-docs-facts)
  - `98f0363d` — FEAT-vntngh: the Google Sheets tool is one tool-form row that keeps the spreadsheet, the columns and the header the author's
  - `3fd4bbfa` — chore(pine): FEAT-vntngh plans wave 2, the Google Sheets tool, on Track C's tool-form table
  - `dfb6b3d3` — merge: n8n's Google Sheets node and trigger import onto native nodes that read, append, update, upsert, delete and clear rows and poll for row changes, on least-privilege Google credentials (FEAT-vntngh wave 1, BUG-8xz9t1)
  - `35a0afbb` — FEAT-vntngh: the second review round is written back to the ticket, with the picker's reload cost recorded as a risk
  - `0d859ea9` — FEAT-vntngh: a saved multi-select choice shows while its list loads, nested loader lookups are pinned, and the Sheets pickers, trigger and Test button settle the second review's points
  - `2962e828` — FEAT-vntngh: the review round is written back to the ticket, with the scope check for every call the node, trigger and pickers make
  - `e9e13389` — FEAT-vntngh: the Sheets pickers read the header row the node names, offer headings as written, give Columns to Watch a list of the tab's columns, and refuse to follow a redirect off the credential's hosts
  - `06220a84` — FEAT-vntngh: a loader below the top level is found by its own key, a structured dependency reaches it as JSON, and the Sheets A1 helpers live in one small package
  - `6c9338c0` — FEAT-vntngh: the Sheets Trigger starts a new baseline when its settings change and leaves its cursor alone when nothing did
  - `7bbd4f87` — FEAT-vntngh: the Sheets node words its own errors, names columns by their heading as written, cuts a range at the right bang and refuses a trigger that wants the old row
  - `90c642d1` — FEAT-vntngh: the Sheets credential types ask for the least scope their node needs, and their Test button calls a Drive endpoint that scope covers
  - `7946562b` — FEAT-vntngh: wave 1 is written back to the ticket, with the behaviour notes confirmed against n8n's public source, the changelog entry and one durable memory note
  - `c4d2b6ef` — FEAT-vntngh: a real template fetched outside the repository can be pointed at for the import and first-step run checks
  - `b80e8c87` — FEAT-vntngh: the e2e inventories list the Sheets node and trigger and check each refuses to run without its own credential
  - `9db8bfea` — FEAT-vntngh: the Sheets node runs as one batch, so a failure under Continue on fail is one error item and the sheet is read once, not once per item
  - `bf8890a1` — FEAT-vntngh: a hand-authored fixture shaped like template 19200's Sheets steps imports with no blocking issue and compiles once a credential is bound
  - `5f7cb593` — FEAT-vntngh: n8n's Google Sheets node and trigger import onto the native pair with n8n's strings, the version-dependent defaults written down, and export back at the version they came from
  - `aba6e149` — FEAT-vntngh: the Sheets pickers list spreadsheets, tabs by gid and header columns, each call held to the egress policy and the credential's allowed hosts
  - `e8e67ea5` — FEAT-vntngh: the Google Sheets Trigger polls on the leased cursor, comparing row fingerprints, and the poll handlers are one list main and ExtractPolls agree on
  - `7c1d5df9` — FEAT-vntngh: the Google Sheets node reads, appends, updates, upserts, deletes and clears rows against an in-memory Sheets, batching the reads and writes of a run
  - `64919e4f` — FEAT-vntngh: the Google Sheets node and trigger are registered with n8n's operation, event and locator strings and refuse what they do not implement
  - `733c4855` — FEAT-vntngh: two Google Sheets credential types, googleSheetsOAuth2Api and googleSheetsTriggerOAuth2Api, join the Google Connect and refresh flow with their own default scopes
  - `d25f2193` — chore(pine): BUG-8xz9t1 files the nested-expression export bug found while planning FEAT-vntngh
  - `76b48d45` — chore(pine): FEAT-vntngh corrects the 19200 operation list and the trigger event name, and records the behaviour notes in our own words
  - `ce0645fb` — chore(pine): the 2026-10-09 parallel wave moves its tickets to doing
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .env.example                                       |    8 +-
 .github/assets/editor.png                          |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml                           |   82 +-
 .github/workflows/code-corpus.yml                  |   54 +
 .gitignore                                         |    4 +
 .pine/MEMORY.md                                    |    2 +
 .pine/memory/code-node.md                          |   30 +-
 .pine/memory/docs.md                               |    5 +-
 .pine/memory/e2e.md                                |    3 +
 .pine/memory/licensing.md                          |   12 +-
 .pine/memory/n8n-reference.md                      |   10 +-
 .pine/memory/persistence.md                        |    5 +-
 .pine/memory/testing.md                            |    3 +-
 .pine/memory/web-editor.md                         |    7 +-
 .pine/roadmap.md                                   |    8 +-
 .pine/tickets/BUG-0592hz.md                        |   62 +
 .pine/tickets/BUG-0bzsa1.md                        |  464 +++
 .pine/tickets/BUG-0grt9g.md                        |  427 +++
 .pine/tickets/BUG-0xv7bg.md                        |   68 +
 .pine/tickets/BUG-14gp8r.md                        |  198 +
 .pine/tickets/BUG-15st2k.md                        |  198 +
 .pine/tickets/BUG-2d1pdx.md                        |   49 +
 .pine/tickets/BUG-2eryxn.md                        |   95 +
 .pine/tickets/BUG-2mes2k.md                        |   83 +
 .pine/tickets/BUG-2n4rfz.md                        |   58 +
 .pine/tickets/BUG-2vcwjf.md                        |  126 +
 .pine/tickets/BUG-2xrz6c.md                        |   51 +
 .pine/tickets/BUG-2z8geh.md                        |  136 +
 .pine/tickets/BUG-3k12ky.md                        |  187 +
 .pine/tickets/BUG-3mem9s.md                        |  105 +
 .pine/tickets/BUG-3qxx0j.md                        |   70 +
 .pine/tickets/BUG-46g75c.md                        |  219 ++
 .pine/tickets/BUG-49vf3j.md                        |  428 +++
 .pine/tickets/BUG-4ch186.md                        |   85 +
 .pine/tickets/BUG-548bk9.md                        |  179 +
 .pine/tickets/BUG-56qqgx.md                        |   88 +
 .pine/tickets/BUG-5bgx5c.md                        |   81 +
 .pine/tickets/BUG-5dn8hr.md                        |   39 +
 .pine/tickets/BUG-5fhcx7.md                        |   50 +
 .pine/tickets/BUG-5xkexq.md                        |   63 +
 .pine/tickets/BUG-605n21.md                        |  154 +
 .pine/tickets/BUG-66fhea.md                        |  112 +
 .pine/tickets/BUG-6d6wbg.md                        |  115 +
 .pine/tickets/BUG-6gkd12.md                        | 1183 ++++++
 .pine/tickets/BUG-719gaz.md                        |  117 +
 .pine/tickets/BUG-8xz9t1.md                        |  511 +++
 .pine/tickets/BUG-9296bf.md                        |  532 +++
 .pine/tickets/BUG-9dw5me.md                        |   66 +
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-9pmv8y.md                        | 1148 ++++++
 .pine/tickets/BUG-a7p6c8.md                        |  487 +++
 .pine/tickets/BUG-a9d2hb.md                        |  212 ++
 .pine/tickets/BUG-argnka.md                        |   34 +
 .pine/tickets/BUG-asdh5q.md                        |  607 +++
 .pine/tickets/BUG-b3p8va.md                        |  120 +
 .pine/tickets/BUG-b4cb1c.md                        | 1252 +++++++
 .pine/tickets/BUG-b8bwhw.md                        | 1338 +++++++
 .pine/tickets/BUG-bcahaj.md                        |  113 +
 .pine/tickets/BUG-bw2zc1.md                        |   93 +
 .pine/tickets/BUG-c19kyx.md                        |  149 +
 .pine/tickets/BUG-c3fgw5.md                        |   60 +
 .pine/tickets/BUG-c73h98.md                        |   51 +
 .pine/tickets/BUG-c7s5ss.md                        |   33 +
 .pine/tickets/BUG-cmnsfz.md                        |  457 +++
 .pine/tickets/BUG-cnhq1f.md                        |   54 +
 .pine/tickets/BUG-cqbq25.md                        |   41 +
 .pine/tickets/BUG-d2t3kp.md                        |   73 +
 .pine/tickets/BUG-djp647.md                        |  130 +
 .pine/tickets/BUG-dstsg9.md                        |   69 +
 .pine/tickets/BUG-e7dwpk.md                        |  116 +
 .pine/tickets/BUG-e8ytyq.md                        |   58 +
 .pine/tickets/BUG-ecbq28.md                        |  132 +
 .pine/tickets/BUG-epy2se.md                        |  131 +
 .pine/tickets/BUG-f85r5h.md                        |  412 +++
 .pine/tickets/BUG-f8b8gh.md                        |   47 +
 .pine/tickets/BUG-fthahg.md                        |  244 ++
 .pine/tickets/BUG-g0hp55.md                        |   31 +
 .pine/tickets/BUG-g7ffj1.md                        |   92 +
 .pine/tickets/BUG-g9zf51.md                        |  480 +++
 .pine/tickets/BUG-gk7mf5.md                        |   69 +
 .pine/tickets/BUG-h6tj4e.md                        |   93 +
 .pine/tickets/BUG-hejyb9.md                        |  109 +
 .pine/tickets/BUG-hmp85t.md                        |  116 +
 .pine/tickets/BUG-hnvn3r.md                        |   55 +
 .pine/tickets/BUG-j7qrp2.md                        |   71 +
 .pine/tickets/BUG-jwhj6y.md                        |  122 +
 .pine/tickets/BUG-jx2g0k.md                        |  527 +++
 .pine/tickets/BUG-jx94q9.md                        |  417 +++
 .pine/tickets/BUG-k4tzc8.md                        |   46 +
 .pine/tickets/BUG-k99658.md                        |  213 ++
 .pine/tickets/BUG-kkkrmx.md                        |  158 +
 .pine/tickets/BUG-kvpx6x.md                        |  144 +
 .pine/tickets/BUG-mzk0xn.md                        |   63 +
 .pine/tickets/BUG-n4e97f.md                        |   51 +
 .pine/tickets/BUG-n6p7qy.md                        | 1314 +++++++
 .pine/tickets/BUG-n9a6bz.md                        |   67 +
 .pine/tickets/BUG-namghh.md                        |  118 +
 .pine/tickets/BUG-nbymq4.md                        |   82 +
 .pine/tickets/BUG-ngt25j.md                        |  739 ++++
 .pine/tickets/BUG-nn74ph.md                        |   60 +
 .pine/tickets/BUG-nzy3pa.md                        |  228 ++
 .pine/tickets/BUG-p334yw.md                        |   65 +
 .pine/tickets/BUG-p3j233.md                        |   71 +
 .pine/tickets/BUG-pc284d.md                        |  587 +++
 .pine/tickets/BUG-pder07.md                        |  430 +++
 .pine/tickets/BUG-pdmkn7.md                        |  419 +++
 .pine/tickets/BUG-pdsydm.md                        |  125 +
 .pine/tickets/BUG-phv0r9.md                        |   67 +
 .pine/tickets/BUG-ppvyzr.md                        |   65 +
 .pine/tickets/BUG-prhxdy.md                        |  479 +++
 .pine/tickets/BUG-pzkpfr.md                        |   76 +
 .pine/tickets/BUG-q6b75c.md                        |   70 +
 .pine/tickets/BUG-qe71kf.md                        |  142 +
 .pine/tickets/BUG-qfp7rf.md                        |  122 +
 .pine/tickets/BUG-r1m83f.md                        |   92 +
 .pine/tickets/BUG-rbask0.md                        |  158 +
 .pine/tickets/BUG-rh7mpa.md                        |   62 +
 .pine/tickets/BUG-rs0xq1.md                        |   52 +
 .pine/tickets/BUG-rytwy7.md                        |  118 +
 .pine/tickets/BUG-sgrxhh.md                        |   68 +
 .pine/tickets/BUG-t12ffz.md                        |  196 +
 .pine/tickets/BUG-t3p92b.md                        |  107 +
 .pine/tickets/BUG-t9e3k5.md                        |   34 +
 .pine/tickets/BUG-tpyg0q.md                        |   65 +
 .pine/tickets/BUG-txafja.md                        |  762 ++++
 .pine/tickets/BUG-v8ksv8.md                        |   79 +
 .pine/tickets/BUG-vbw239.md                        |  197 +
 .pine/tickets/BUG-vqghpm.md                        |  329 ++
 .pine/tickets/BUG-vsmnby.md                        |  152 +
 .pine/tickets/BUG-w18vn3.md                        |   62 +
 .pine/tickets/BUG-w1jp70.md                        |  120 +
 .pine/tickets/BUG-w9k234.md                        |   61 +
 .pine/tickets/BUG-x28fsx.md                        | 1369 +++++++
 .pine/tickets/BUG-x2sxzt.md                        |  525 +++
 .pine/tickets/BUG-x6gyc1.md                        |   77 +
 .pine/tickets/BUG-x94b3d.md                        |   46 +
 .pine/tickets/BUG-xam6t8.md                        |  179 +
 .pine/tickets/BUG-xkz7qx.md                        |  499 +++
 .pine/tickets/BUG-xpr3jj.md                        |  416 +++
 .pine/tickets/BUG-y38bss.md                        |   90 +
 .pine/tickets/BUG-ywbvfa.md                        | 1316 +++++++
 .pine/tickets/BUG-z0s4zg.md                        |  100 +
 .pine/tickets/BUG-z26b0j.md                        |   39 +
 .pine/tickets/BUG-zf4pnj.md                        |  115 +
 .pine/tickets/EPIC-3en6xr.md                       |   88 +
 .pine/tickets/EPIC-62zt4j.md                       |  110 +
 .pine/tickets/EPIC-7c3ry9.md                       |   66 +
 .pine/tickets/EPIC-8rbys7.md                       |  192 +
 .pine/tickets/EPIC-brpz48.md                       |   80 +
 .pine/tickets/EPIC-hkypt5.md                       |    8 +-
 .pine/tickets/EPIC-m42s3g.md                       |    2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 1234 +++++++
 .pine/tickets/FEAT-02cj1g.md                       |  102 +
 .pine/tickets/FEAT-02zdcq.md                       |  169 +
 .pine/tickets/FEAT-0hdfzd.md                       |  106 +
 .pine/tickets/FEAT-0xsc1s.md                       |   43 +
 .pine/tickets/FEAT-0ynje5.md                       |   35 +
 .pine/tickets/FEAT-1ge0xc.md                       |   38 +
 .pine/tickets/FEAT-1mxtsn.md                       |  104 +
 .pine/tickets/FEAT-21h6xp.md                       |  254 ++
 .pine/tickets/FEAT-274c4p.md                       |   68 +
 .pine/tickets/FEAT-27g2za.md                       |   40 +
 .pine/tickets/FEAT-2kx0hx.md                       |  260 ++
 .pine/tickets/FEAT-2m24nh.md                       |   53 +
 .pine/tickets/FEAT-2m4yvz.md                       |  101 +
 .pine/tickets/FEAT-2npfgy.md                       |   46 +
 .pine/tickets/FEAT-38je8w.md                       |   35 +
 .pine/tickets/FEAT-39ttf6.md                       |   39 +
 .pine/tickets/FEAT-3kwr8j.md                       |   38 +
 .pine/tickets/FEAT-3t112f.md                       |   53 +
 .pine/tickets/FEAT-3taswf.md                       |   16 +-
 .pine/tickets/FEAT-3ykb4v.md                       |   37 +
 .pine/tickets/FEAT-4bjfny.md                       |  100 +
 .pine/tickets/FEAT-4bvcrb.md                       |   38 +
 .pine/tickets/FEAT-4e376e.md                       |   56 +
 .pine/tickets/FEAT-4fp51b.md                       |   42 +
 .pine/tickets/FEAT-4jhtny.md                       |   35 +
 .pine/tickets/FEAT-4pz9fn.md                       |   37 +
 .pine/tickets/FEAT-53pa9a.md                       |   52 +
 .pine/tickets/FEAT-5fhj6p.md                       |    2 +-
 .pine/tickets/FEAT-5fx926.md                       |   57 +
 .pine/tickets/FEAT-5g42rz.md                       |   38 +
 .pine/tickets/FEAT-5kv1jq.md                       |    8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |   99 +
 .pine/tickets/FEAT-6m295t.md                       |   38 +
 .pine/tickets/FEAT-6qzza1.md                       |   58 +
 .pine/tickets/FEAT-6r663e.md                       |   40 +
 .pine/tickets/FEAT-70j6dn.md                       |   55 +
 .pine/tickets/FEAT-7cg0cd.md                       |    6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  385 ++
 .pine/tickets/FEAT-7t0xks.md                       |   39 +
 .pine/tickets/FEAT-83rcve.md                       |   36 +
 .pine/tickets/FEAT-8752vx.md                       |   70 +
 .pine/tickets/FEAT-8zgwp6.md                       |   37 +
 .pine/tickets/FEAT-9ep5pw.md                       |   38 +
 .pine/tickets/FEAT-9we7kw.md                       | 1003 +++++
 .pine/tickets/FEAT-9yr3tt.md                       |   40 +
 .pine/tickets/FEAT-a3dwj2.md                       |   52 +
 .pine/tickets/FEAT-afkx3k.md                       |  118 +
 .pine/tickets/FEAT-bfrkyk.md                       |   54 +
 .pine/tickets/FEAT-bvyc0b.md                       |   52 +
 .pine/tickets/FEAT-c81kp3.md                       |   59 +
 .pine/tickets/FEAT-cf5n4j.md                       |  564 +++
 .pine/tickets/FEAT-cgm1y3.md                       |    2 +-
 .pine/tickets/FEAT-csqgg5.md                       |    6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |   59 +
 .pine/tickets/FEAT-e898nz.md                       |  210 ++
 .pine/tickets/FEAT-edzr73.md                       |  121 +
 .pine/tickets/FEAT-egm8bf.md                       |   39 +
 .pine/tickets/FEAT-eqzpzq.md                       |  136 +
 .pine/tickets/FEAT-ez6xtm.md                       |   55 +
 .pine/tickets/FEAT-f045nj.md                       |  131 +
 .pine/tickets/FEAT-f3hx3a.md                       |   37 +
 .pine/tickets/FEAT-f40kg4.md                       |  142 +
 .pine/tickets/FEAT-fpqg78.md                       |   52 +
 .pine/tickets/FEAT-fqmh01.md                       |   97 +
 .pine/tickets/FEAT-fs3pjr.md                       |  205 ++
 .pine/tickets/FEAT-g33qf6.md                       |   42 +
 .pine/tickets/FEAT-g6k3y9.md                       |  188 +
 .pine/tickets/FEAT-gcq50s.md                       |   17 +-
 .pine/tickets/FEAT-gzd32h.md                       |   36 +
 .pine/tickets/FEAT-hxztwz.md                       |   45 +
 .pine/tickets/FEAT-je4f4t.md                       |    4 +-
 .pine/tickets/FEAT-jrrqs2.md                       |   21 +
 .pine/tickets/FEAT-jwhdsy.md                       |    2 +-
 .pine/tickets/FEAT-kcdrcy.md                       |  130 +
 .pine/tickets/FEAT-kfmq1z.md                       |   55 +
 .pine/tickets/FEAT-kpn0m3.md                       |   37 +
 .pine/tickets/FEAT-ktasef.md                       |  103 +
 .pine/tickets/FEAT-ky75b5.md                       |   52 +
 .pine/tickets/FEAT-m1fdn4.md                       |   56 +
 .pine/tickets/FEAT-m7aw75.md                       |   54 +
 .pine/tickets/FEAT-mammrz.md                       |  107 +
 .pine/tickets/FEAT-mccadj.md                       |   44 +
 .pine/tickets/FEAT-mh4e8g.md                       |   38 +
 .pine/tickets/FEAT-mj2nek.md                       |   98 +
 .pine/tickets/FEAT-mngmn1.md                       |   39 +
 .pine/tickets/FEAT-mq412g.md                       |   58 +
 .pine/tickets/FEAT-mxmjt7.md                       |  129 +
 .pine/tickets/FEAT-n010f0.md                       |   33 +
 .pine/tickets/FEAT-n12211.md                       |   40 +
 .pine/tickets/FEAT-nch9dg.md                       |    6 +-
 .pine/tickets/FEAT-npc3ge.md                       |   37 +
 .pine/tickets/FEAT-nq1vsx.md                       |   53 +
 .pine/tickets/FEAT-p01rcw.md                       |   98 +
 .pine/tickets/FEAT-p75n7j.md                       | 1319 +++++++
 .pine/tickets/FEAT-pfwjzk.md                       |   30 +
 .pine/tickets/FEAT-ppnetz.md                       |  141 +
 .pine/tickets/FEAT-pqnxx4.md                       |   37 +
 .pine/tickets/FEAT-prw1hw.md                       |   56 +
 .pine/tickets/FEAT-pt6ge9.md                       |   44 +
 .pine/tickets/FEAT-pxcbqj.md                       |  479 +++
 .pine/tickets/FEAT-q80xe3.md                       |   65 +
 .pine/tickets/FEAT-q81bq4.md                       |    2 +-
 .pine/tickets/FEAT-qae4sh.md                       |  969 +++++
 .pine/tickets/FEAT-qdwm1k.md                       | 1217 +++++++
 .pine/tickets/FEAT-qf0hsa.md                       |   53 +
 .pine/tickets/FEAT-r267jj.md                       |   41 +
 .pine/tickets/FEAT-r87gtj.md                       |  970 +++++
 .pine/tickets/FEAT-r8ph93.md                       |   41 +
 .pine/tickets/FEAT-rdfjh1.md                       |   42 +
 .pine/tickets/FEAT-re138f.md                       |   54 +
 .pine/tickets/FEAT-rkj8ry.md                       |  319 ++
 .pine/tickets/FEAT-s3sfx5.md                       |   36 +
 .pine/tickets/FEAT-s99vdp.md                       |  155 +
 .pine/tickets/FEAT-sc3qrq.md                       |   54 +
 .pine/tickets/FEAT-sz4ddp.md                       |   57 +
 .pine/tickets/FEAT-t26rt7.md                       |    2 +-
 .pine/tickets/FEAT-t38djq.md                       |   56 +
 .pine/tickets/FEAT-t58m89.md                       |   37 +
 .pine/tickets/FEAT-t672pv.md                       |   57 +
 .pine/tickets/FEAT-tjcr13.md                       |   52 +
 .pine/tickets/FEAT-v2nenc.md                       |   58 +
 .pine/tickets/FEAT-v8md2e.md                       |   66 +
 .pine/tickets/FEAT-vjjs8t.md                       |  951 +++++
 .pine/tickets/FEAT-vntngh.md                       |  257 ++
 .pine/tickets/FEAT-vvwpjw.md                       |    2 +-
 .pine/tickets/FEAT-w7n7x6.md                       |  131 +
 .pine/tickets/FEAT-w9kqeg.md                       |   10 +-
 .pine/tickets/FEAT-wcr6en.md                       |   52 +
 .pine/tickets/FEAT-wdxkvw.md                       |   43 +
 .pine/tickets/FEAT-wzfz3d.md                       |   59 +
 .pine/tickets/FEAT-x9gq0s.md                       |  320 ++
 .pine/tickets/FEAT-xj5tv6.md                       |   43 +
 .pine/tickets/FEAT-xr75b9.md                       |   58 +
 .pine/tickets/FEAT-xzdn35.md                       |   56 +
 .pine/tickets/FEAT-ybm2pd.md                       |    2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |   37 +
 .pine/tickets/FEAT-ys734v.md                       |   36 +
 .pine/tickets/FEAT-yxhgeh.md                       |  491 +++
 .pine/tickets/FEAT-yyjfjq.md                       |    2 +-
 .pine/tickets/FEAT-z90r5a.md                       |   38 +
 .pine/tickets/FEAT-zhdxc4.md                       |   38 +
 .pine/tickets/FEAT-zjrw76.md                       |  454 +++
 .pine/tickets/FEAT-zm3wh2.md                       | 1209 ++++++
 .pine/tickets/FEAT-zn5rqy.md                       |  103 +
 .pine/tickets/FEAT-zwpvbf.md                       |   60 +
 CHANGELOG.md                                       |  588 ++-
 CONTRIBUTING.md                                    |   35 +
 Makefile                                           |   76 +
 README.md                                          |  450 +--
 cmd/kilasflow/boot_logger_test.go                  |   30 +
 cmd/kilasflow/catalogue_docs_test.go               |   84 +
 cmd/kilasflow/javascript_test.go                   |   29 +
 cmd/kilasflow/main.go                              |  417 ++-
 cmd/kilasflow/reseal_test.go                       |  219 ++
 cmd/kilasflow/secrets_boot_test.go                 |  102 +-
 cmd/kilasflow/shipped_catalogue_drift_test.go      |  228 ++
 cmd/kilasflow/sqlite_files_test.go                 |   50 +
 config.example.yaml                                |  126 +-
 docs/src/content/docs/concepts/architecture.md     |   86 +
 docs/src/content/docs/concepts/credentials.md      |  385 +-
 docs/src/content/docs/concepts/execution-model.md  |   52 +-
 docs/src/content/docs/concepts/expressions.md      |   20 +-
 .../src/content/docs/concepts/items-and-lineage.md |   22 +-
 docs/src/content/docs/concepts/node-registry.md    |   15 +-
 .../src/content/docs/concepts/safety-boundaries.md |  277 +-
 .../content/docs/concepts/tenancy-and-embedding.md |  100 +-
 docs/src/content/docs/concepts/webhooks.md         |  109 +-
 docs/src/content/docs/guides/code-javascript.md    |  426 +++
 docs/src/content/docs/guides/community-nodes.md    |    7 +-
 docs/src/content/docs/guides/embedding.md          |  113 +-
 docs/src/content/docs/guides/n8n-migration.md      |  292 +-
 docs/src/content/docs/guides/node-authoring.md     |  183 +-
 .../src/content/docs/guides/tenant-scoped-nodes.md |    6 +
 .../content/docs/operate/acceptance-capstone.md    |    3 +-
 .../docs/operate/configuration-reference.md        |  220 +-
 docs/src/content/docs/operate/configuration.md     |   24 +-
 docs/src/content/docs/operate/deployment.md        |   27 +
 docs/src/content/docs/operate/key-rotation.md      |  158 +
 docs/src/content/docs/operate/security.md          |   66 +-
 docs/src/content/docs/operate/tenant-deletion.md   |    5 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +
 docs/src/content/docs/reference/api-contract.md    |   25 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/credentials.md |    9 +-
 docs/src/content/docs/reference/api/events.md      |   14 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |   17 +
 docs/src/content/docs/reference/api/workflows.md   |   11 +-
 docs/src/content/docs/reference/cli.md             |  361 +-
 .../content/docs/reference/expression-grammar.md   |   13 +-
 docs/src/content/docs/reference/node-packs.md      |   13 +
 docs/src/content/docs/start/install.md             |    5 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   20 +-
 docs/src/styles/kilasflow.css                      |    2 +-
 e2e/.gitignore                                     |    4 +
 e2e/fixtures/epic-code.ts                          |  271 ++
 e2e/fixtures/epic-proofs.ts                        |   23 +
 e2e/fixtures/error-form-nodes.ts                   |    7 +-
 e2e/fixtures/n8n-live.ts                           |    8 +
 e2e/global-setup.ts                                |   26 +-
 e2e/helpers/focus-ring.ts                          |  125 +
 e2e/helpers/seed.ts                                |   27 +
 e2e/helpers/server.ts                              |   12 +
 e2e/helpers/stub.ts                                |   11 +-
 e2e/package.json                                   |    3 +-
 e2e/playwright.visual.config.ts                    |   62 +
 e2e/tests/attachment-slot-picker.spec.ts           |   55 +
 e2e/tests/code-editor.spec.ts                      |  154 +
 e2e/tests/datastore-pg.spec.ts                     |    4 +-
 e2e/tests/datastore.spec.ts                        |  115 +-
 e2e/tests/editor-chat.spec.ts                      |   68 +-
 e2e/tests/embed-branding.spec.ts                   |  163 +
 e2e/tests/focus-tab-order.spec.ts                  |  155 +
 e2e/tests/google-oauth.spec.ts                     |   28 +
 e2e/tests/js-code.spec.ts                          |  702 ++++
 e2e/tests/live-backend-api.spec.ts                 |   72 +-
 e2e/tests/n8n-compare.spec.ts                      |   50 +-
 e2e/tests/n8n-paste.spec.ts                        |  116 +
 e2e/tests/node-coverage.spec.ts                    |  129 +-
 e2e/tests/smoke.spec.ts                            |   86 +
 e2e/tests/vector-store-ports.spec.ts               |  105 +
 e2e/tests/waha-migration.spec.ts                   |   14 +-
 e2e/tests/workflow-tool-inputs.spec.ts             |  406 +++
 e2e/visual/fixtures.ts                             |   71 +
 e2e/visual/focus-ring.spec.ts                      |   50 +
 e2e/visual/force-state.ts                          |   34 +
 e2e/visual/state-matrix.spec.ts                    |   25 +
 e2e/visual/theme-subtree.spec.ts                   |   47 +
 gflow-prd-v1.md                                    |    8 +-
 go.mod                                             |   12 +-
 go.sum                                             |   22 +-
 internal/ai/agent.go                               |   64 +-
 internal/ai/ai.go                                  |   30 +-
 internal/ai/ai_test.go                             |  163 +
 internal/ai/fromai.go                              |  159 +-
 internal/ai/fromai_test.go                         |  121 +-
 internal/ai/fromai_whole_test.go                   |   47 +
 internal/ai/memory.go                              |   26 +-
 internal/ai/openai.go                              |  247 +-
 internal/ai/openai_test.go                         |  156 +
 internal/ai/outputschema.go                        |  587 ++-
 internal/ai/outputschema_test.go                   |  229 ++
 internal/ai/reasoning_test.go                      |  245 ++
 internal/ai/toolusage.go                           |   46 +
 internal/ai/toolusage_test.go                      |   60 +
 internal/api/auth_test.go                          |   53 +-
 internal/api/convert_fragment_test.go              |   82 +
 internal/api/credential_audit_test.go              |  271 ++
 internal/api/credential_confinement_test.go        |  273 ++
 internal/api/credential_testable_test.go           |   62 +
 internal/api/credential_usage_test.go              |  169 +
 internal/api/credentials_pagination_test.go        |   58 +
 internal/api/credentials_probe_scope_test.go       |  235 ++
 internal/api/credentials_test.go                   |   81 +-
 internal/api/credentials_update_test.go            |  206 ++
 internal/api/datastores_csv_test.go                |   34 +
 internal/api/datastores_test.go                    |   57 +
 internal/api/debug_ops_test.go                     |   45 +-
 internal/api/docs.go                               |   10 +-
 internal/api/embed_confinement_test.go             |    9 +-
 internal/api/embed_publish_test.go                 |  160 +
 internal/api/embed_test.go                         |  106 +-
 internal/api/events_test.go                        |  104 +-
 internal/api/handlers/actor.go                     |   33 +
 internal/api/handlers/admin.go                     |    9 +-
 internal/api/handlers/admin_admin_test.go          |    2 +-
 internal/api/handlers/auth.go                      |    6 +-
 internal/api/handlers/credentials.go               |  526 ++-
 internal/api/handlers/credentials_probe_test.go    |   75 +
 internal/api/handlers/datastores.go                |   13 +-
 internal/api/handlers/datastores_csv.go            |   27 +-
 internal/api/handlers/embed.go                     |    2 +-
 internal/api/handlers/embedscope.go                |  156 +-
 internal/api/handlers/execution_console_test.go    |   62 +
 internal/api/handlers/execution_retry.go           |   59 +-
 internal/api/handlers/execution_waiting_test.go    |   82 +
 internal/api/handlers/executions.go                |  139 +-
 internal/api/handlers/executions_events_test.go    |   65 +
 internal/api/handlers/interop.go                   |   59 +
 internal/api/handlers/nodes.go                     |   76 +-
 internal/api/handlers/oauth.go                     |  236 +-
 internal/api/handlers/problem.go                   |   16 +
 internal/api/handlers/resume_test.go               |    2 +-
 internal/api/handlers/system.go                    |   22 +-
 internal/api/handlers/workflow_validate.go         |   95 +-
 internal/api/handlers/workflow_validate_test.go    |  171 +
 internal/api/handlers/workflows.go                 |  145 +-
 internal/api/handlers/workflows_delete_test.go     |   81 +-
 internal/api/middleware/embed.go                   |   54 +-
 internal/api/middleware/embed_publish_test.go      |   87 +
 internal/api/middleware/embed_sentences_test.go    |   24 +-
 internal/api/middleware/embed_test.go              |   88 +
 internal/api/middleware/scope.go                   |   48 +-
 internal/api/middleware/scope_test.go              |    3 +
 internal/api/node_types_lookup_test.go             |  158 +
 internal/api/node_types_nested_test.go             |  114 +
 internal/api/node_types_schema_expression_test.go  |   81 +
 internal/api/oauth_external_test.go                |   69 +
 internal/api/oauth_sheets_test.go                  |   48 +
 internal/api/oauth_test.go                         |  364 +-
 internal/api/problems.go                           |   97 +
 internal/api/problems_test.go                      |  171 +
 internal/api/routes.go                             |   14 +-
 internal/api/schedules_test.go                     |   28 +
 internal/api/server.go                             |    8 +-
 internal/api/server_test.go                        |   40 +
 internal/api/subworkflow_activation_test.go        |  219 ++
 internal/api/workflow_tags_test.go                 |  342 ++
 internal/api/workflows_test.go                     |    8 +
 internal/cli/cli.go                                |   22 +
 internal/cli/client.go                             |  143 +-
 internal/cli/command_test.go                       |    4 +-
 internal/cli/docs_examples_test.go                 |  122 +
 internal/cli/document_flag_help_test.go            |   31 +
 internal/cli/exit.go                               |  136 +
 internal/cli/exit_reference_test.go                |  286 ++
 internal/cli/exit_test.go                          |   26 +
 internal/cli/guard_test.go                         |  144 +-
 internal/cli/mcp.go                                |   44 +-
 internal/cli/mcp_test.go                           |  317 +-
 internal/cli/openapi.go                            |  134 +-
 internal/cli/openapi_contract_test.go              |   43 +-
 internal/cli/skills_not_shipped_test.go            |  410 +++
 internal/cli/sse.go                                |   27 +-
 internal/cli/verbs_api.go                          |   83 +-
 internal/cli/verbs_api_test.go                     |  324 +-
 internal/cli/verbs_credential.go                   |   39 +-
 internal/cli/verbs_credential_test.go              |  126 +-
 internal/cli/verbs_datastore.go                    |    5 +-
 internal/cli/verbs_debug.go                        |   67 +-
 internal/cli/verbs_debug_test.go                   |   82 +
 internal/cli/verbs_exec.go                         |   16 +
 internal/cli/verbs_exec_test.go                    |   43 +
 internal/cli/verbs_node.go                         |   91 +-
 internal/cli/verbs_node_options_test.go            |  333 ++
 internal/cli/verbs_skills.go                       |   94 +-
 internal/cli/verbs_skills_test.go                  |  119 +
 internal/cli/verbs_workflow.go                     |  110 +-
 internal/cli/verbs_workflow_subworkflow_test.go    |  144 +
 internal/cli/verbs_workflow_tag_test.go            |   84 +
 internal/cli/verbs_workflow_test.go                |   59 +
 internal/config/boot_strictness_test.go            |  292 ++
 internal/config/config.go                          |  413 ++-
 internal/config/config_test.go                     |  142 +
 internal/config/sqlite_root_test.go                |   51 +
 internal/credentials/binding_guard_test.go         |   79 +
 internal/credentials/builtin.go                    |   76 +-
 internal/credentials/cipher.go                     |  330 ++
 internal/credentials/cipher_internal_test.go       |   35 +
 internal/credentials/cipher_test.go                |  388 ++
 internal/credentials/credentials.go                |  221 +-
 internal/credentials/credentials_test.go           |  194 +-
 internal/credentials/external.go                   |   54 +-
 internal/credentials/google_sheets_probe_test.go   |   68 +
 internal/credentials/google_sheets_test.go         |  168 +
 internal/credentials/keysource.go                  |   63 +-
 internal/credentials/keysource_test.go             |  103 +
 internal/credentials/oauth.go                      |  168 +-
 internal/credentials/oauth_redirect_test.go        |   41 +
 internal/credentials/oauth_test.go                 |   95 +-
 internal/credentials/probe_test.go                 |  181 +
 internal/credentials/registry.go                   |  107 +-
 internal/credentials/scope.go                      |  102 +
 internal/credentials/scope_test.go                 |  217 ++
 internal/credentials/scrub.go                      |  107 +
 internal/credentials/scrub_test.go                 |   61 +
 internal/credentials/vault.go                      |   10 +-
 .../database/credential_events_migration_test.go   |   61 +
 .../datastore_unique_names_migration_test.go       |  212 ++
 internal/database/migrate_test.go                  |    3 +
 ...webhook_route_lifecycle_state_migration_test.go |   78 +
 internal/database/workflow_actor_migration_test.go |    7 +-
 internal/datastore/catalogue.go                    |   46 +-
 internal/datastore/engine.go                       |   26 +-
 internal/datastore/engine_test.go                  |   13 +-
 internal/datastore/names.go                        |  132 +
 internal/datastore/names_test.go                   |  271 ++
 internal/docexamples/docexamples.go                |  318 ++
 internal/docexamples/docexamples_test.go           |  209 ++
 internal/embed/confinement.go                      |   12 -
 internal/embed/confinement_test.go                 |   14 +-
 internal/embed/embed.go                            |   87 +-
 internal/embed/embed_publish_test.go               |  113 +
 internal/embed/embed_test.go                       |   60 +
 internal/engine/always_output_test.go              |  511 +++
 internal/engine/approval.go                        |    2 +-
 internal/engine/authenticate.go                    |  158 +-
 internal/engine/authenticate_test.go               |   74 +
 internal/engine/batch_failure_test.go              |  144 +
 internal/engine/batch_nodes_tolerance_test.go      |  108 +
 internal/engine/checkpoint.go                      |   94 +-
 internal/engine/checkpoint_test.go                 |  101 +
 internal/engine/console_test.go                    |  397 ++
 internal/engine/error_workflow_test.go             |  312 +-
 internal/engine/eval.go                            |  287 +-
 internal/engine/eval_test.go                       |  269 +-
 internal/engine/export_test.go                     |   13 +-
 internal/engine/expression_context_test.go         |   24 +
 internal/engine/fanout_lineage_test.go             |  241 ++
 internal/engine/item_outcomes.go                   |  131 +
 internal/engine/item_outcomes_test.go              |  201 +
 internal/engine/lineage_internal_test.go           |   74 +
 internal/engine/missing_credential_test.go         |   49 +
 internal/engine/node_branch_test.go                |  157 +
 internal/engine/node_branches.go                   |   87 +
 internal/engine/node_timing_test.go                |  211 ++
 internal/engine/per_item_suspend_test.go           |  438 +++
 internal/engine/required_inputs_test.go            |  198 +
 internal/engine/resume_failure_test.go             |  174 +
 internal/engine/run_presentation_test.go           |   77 +
 internal/engine/runindex_skip_test.go              |  299 ++
 internal/engine/runindex_test.go                   |   70 +
 internal/engine/runner.go                          | 1212 +++++-
 internal/engine/runner_test.go                     |  968 +++++
 internal/engine/secret_scrub.go                    |  159 +
 internal/engine/secret_scrub_test.go               |  134 +
 internal/engine/service.go                         |  235 +-
 internal/engine/static_data.go                     |  136 +
 internal/engine/static_data_service_test.go        |  268 ++
 internal/engine/static_data_test.go                |   69 +
 internal/engine/subworkflow_test.go                |  259 +-
 internal/engine/wait_error_output_test.go          |  181 +
 internal/engine/wait_links_test.go                 |  159 +
 internal/engine/wait_service.go                    |   87 +-
 internal/engine/wait_service_test.go               |  641 +++-
 internal/engine/worker_test.go                     |    8 +
 internal/events/events.go                          |   37 +-
 internal/events/events_test.go                     |   68 +
 internal/execution/records.go                      |    7 +
 internal/expression/doc.go                         |    2 +-
 internal/expression/expression.go                  |   38 +
 internal/expression/expression_test.go             |   66 +
 internal/expression/globals.go                     |   22 +-
 internal/expression/parity_test.go                 |   48 +
 internal/expression/roots.go                       |  178 +-
 internal/guardrails/compile_scope_test.go          |    7 +-
 internal/guardrails/jsruntime_boundary_test.go     |  321 ++
 internal/idempotency/once.go                       |   64 +
 internal/idempotency/once_test.go                  |   56 +
 internal/interop/n8n/chatmodel_import_test.go      |  333 ++
 internal/interop/n8n/code_import_test.go           |  115 +
 internal/interop/n8n/connection_order_test.go      |   52 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   29 +-
 internal/interop/n8n/cycle_import_test.go          |  178 +
 internal/interop/n8n/export_expression_test.go     |   79 +
 internal/interop/n8n/export_test.go                |    7 +
 internal/interop/n8n/extract_import_test.go        |   83 +
 internal/interop/n8n/fragment_import_test.go       |  134 +
 internal/interop/n8n/googlesheets.go               |  243 ++
 internal/interop/n8n/googlesheets_import_test.go   |  415 +++
 internal/interop/n8n/imported_ports_golden_test.go |  186 +
 internal/interop/n8n/importer_tail_test.go         |   79 +-
 internal/interop/n8n/legacy_agent_test.go          |  120 +
 internal/interop/n8n/loose.go                      |  252 ++
 internal/interop/n8n/loose_json_test.go            |  239 ++
 internal/interop/n8n/n8n.go                        |  654 +++-
 internal/interop/n8n/n8n_test.go                   |  711 +++-
 internal/interop/n8n/parameters.go                 | 1130 ++++--
 internal/interop/n8n/rag_import_test.go            |  215 +-
 internal/interop/n8n/roundtrip_fidelity_test.go    |  171 +
 internal/interop/n8n/set_null_import_test.go       |  150 +
 internal/interop/n8n/sheets_fixture_test.go        |   98 +
 internal/interop/n8n/sheets_template_test.go       |   72 +
 internal/interop/n8n/sheets_tool_fixture_test.go   |  162 +
 internal/interop/n8n/sheets_tool_import_test.go    |   99 +
 internal/interop/n8n/sheets_tool_security_test.go  |   41 +
 internal/interop/n8n/switch_import_test.go         |  371 ++
 internal/interop/n8n/tags.go                       |  182 +
 internal/interop/n8n/tags_test.go                  |  312 ++
 internal/interop/n8n/template6270_shape_test.go    |  305 ++
 .../n8n/testdata/imported_ports.golden.json        | 1496 ++++++++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   24 +
 internal/interop/n8n/testdata/n8n_rag_16706.json   |   12 +-
 internal/interop/n8n/testdata/n8n_rag_3647.json    |    2 +-
 internal/interop/n8n/testdata/n8n_rag_4799.json    |    2 +-
 internal/interop/n8n/testdata/n8n_rag_5908.json    |    2 +-
 .../interop/n8n/testdata/n8n_sheets_19200.json     |  185 +
 .../n8n/testdata/n8n_sheets_tool_19348.json        |  172 +
 internal/interop/n8n/tool_form_import_test.go      |  559 +++
 internal/interop/n8n/tool_forms.go                 |  447 +++
 internal/interop/n8n/tool_variant_import_test.go   |  111 +
 internal/interop/n8n/topology_import_test.go       |  147 +
 internal/interop/n8n/vectorstore.go                |  153 +
 internal/interop/n8n/vectorstore_import_test.go    |  281 ++
 internal/interop/n8n/workflow_tool_inputs.go       |  241 ++
 internal/interop/n8n/workflow_tool_inputs_test.go  |  319 ++
 internal/interop/n8n/workflow_tool_self_test.go    |   93 +
 internal/jsrun/analyze.go                          |  666 ++++
 internal/jsrun/analyze_errors.go                   |  187 +
 internal/jsrun/analyze_html.go                     |  575 +++
 internal/jsrun/analyze_html_test.go                |   63 +
 internal/jsrun/analyze_test.go                     |  184 +
 internal/jsrun/bounds_test.go                      |  364 ++
 internal/jsrun/buffer_test.go                      |   78 +
 internal/jsrun/clock.go                            |   68 +
 internal/jsrun/codec.go                            |  330 ++
 internal/jsrun/codec_internal_test.go              |  103 +
 internal/jsrun/comparator_test.go                  |  221 ++
 internal/jsrun/console.go                          |   64 +
 internal/jsrun/console_test.go                     |  109 +
 internal/jsrun/corpus/BASELINE.md                  |  412 +++
 internal/jsrun/corpus/MANIFEST.json                |  769 ++++
 internal/jsrun/corpus/baseline.json                | 3464 ++++++++++++++++++
 internal/jsrun/corpus/corpus_test.go               |  388 ++
 internal/jsrun/corpus/doc.go                       |   32 +
 internal/jsrun/corpus/jsdiff_test.go               |  317 ++
 internal/jsrun/corpus/scoreboard_test.go           |  710 ++++
 internal/jsrun/corpus/testdata/control/orders.json |  141 +
 internal/jsrun/crypto.go                           |  859 +++++
 internal/jsrun/crypto_test.go                      |  792 ++++
 internal/jsrun/doc.go                              |   99 +
 internal/jsrun/engine.go                           |  739 ++++
 internal/jsrun/engine_host.go                      |  174 +
 internal/jsrun/engine_nodejs.go                    |   34 +
 internal/jsrun/engine_rejections.go                |   94 +
 internal/jsrun/engine_timers.go                    |   85 +
 internal/jsrun/errors.go                           |  230 ++
 internal/jsrun/export_test.go                      |   80 +
 internal/jsrun/guards_test.go                      |  172 +
 internal/jsrun/helpers.go                          |  298 ++
 internal/jsrun/helpers_test.go                     |  600 +++
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/inline.go                           |  149 +
 internal/jsrun/intl.go                             | 3853 ++++++++++++++++++++
 internal/jsrun/intl_internal_test.go               |  204 ++
 internal/jsrun/intl_test.go                        |  780 ++++
 internal/jsrun/items.go                            |  248 ++
 internal/jsrun/js/modules/buffer.js                |  296 ++
 internal/jsrun/js/modules/crypto.js                |  808 ++++
 internal/jsrun/js/modules/errors.js                |  298 ++
 internal/jsrun/js/modules/helpers.js               |  342 ++
 internal/jsrun/js/modules/intl.js                  |  538 +++
 internal/jsrun/js/modules/luxon.js                 |  110 +
 internal/jsrun/js/modules/timers.js                |   75 +
 internal/jsrun/js/modules/url.js                   |   10 +
 internal/jsrun/js/modules/util.js                  |   62 +
 internal/jsrun/js/modules/web.js                   |  295 ++
 internal/jsrun/js/runtime.js                       | 1726 +++++++++
 internal/jsrun/jsrun.go                            |  247 ++
 internal/jsrun/jsrun_test.go                       |  487 +++
 internal/jsrun/libraries.go                        |   86 +
 internal/jsrun/luxon_test.go                       |  163 +
 internal/jsrun/modules.go                          |   68 +
 internal/jsrun/modules_test.go                     |   50 +
 internal/jsrun/natives.go                          |  113 +
 internal/jsrun/norace_test.go                      |    7 +
 internal/jsrun/programs.go                         |  145 +
 internal/jsrun/race_test.go                        |    8 +
 internal/jsrun/rejections_test.go                  |  190 +
 internal/jsrun/roots.go                            |  152 +
 internal/jsrun/roots_test.go                       |  584 +++
 internal/jsrun/run.go                              |  484 +++
 internal/jsrun/security_test.go                    |  284 ++
 internal/jsrun/surface_internal_test.go            |  596 +++
 internal/jsrun/testdata/parity/collation.json      |   10 +
 internal/jsrun/testdata/parity/currencies.json     |   59 +
 internal/jsrun/testdata/parity/date-options.json   | 3598 ++++++++++++++++++
 internal/jsrun/testdata/parity/dates.json          |   33 +
 internal/jsrun/testdata/parity/errors.json         |  304 ++
 internal/jsrun/testdata/parity/html-comments.json  |   37 +
 internal/jsrun/testdata/parity/luxon.json          |   68 +
 internal/jsrun/testdata/parity/numbers.json        |  317 ++
 internal/jsrun/testdata/parity/utf8.json           | 2598 +++++++++++++
 internal/jsrun/testdata/parity/weeks.json          |   28 +
 internal/jsrun/testdata/parity/zones.json          |  832 +++++
 internal/jsrun/testdata/surface.txt                |  512 +++
 internal/jsrun/watchdog.go                         |  117 +
 internal/jsrun/web_test.go                         |  304 ++
 internal/jsrun/wire.go                             |  213 ++
 internal/jsrun/wire_test.go                        |   57 +
 internal/jsrun/wording.go                          |  150 +
 internal/jsrun/wording_internal_test.go            |   48 +
 internal/jsrun/wording_test.go                     |  327 ++
 internal/jsrun/wrapper.go                          |  111 +
 internal/jsworker/confine.go                       |  192 +
 internal/jsworker/confine_linux.go                 |  234 ++
 internal/jsworker/confine_linux_test.go            |  501 +++
 internal/jsworker/confine_test.go                  |  250 ++
 internal/jsworker/doc.go                           |   78 +
 internal/jsworker/helpers_test.go                  |  275 ++
 internal/jsworker/jsworker_test.go                 |  595 +++
 internal/jsworker/limits_linux.go                  |   93 +
 internal/jsworker/limits_other.go                  |   45 +
 internal/jsworker/load_test.go                     |  144 +
 internal/jsworker/norace.go                        |    6 +
 internal/jsworker/pool.go                          |  952 +++++
 internal/jsworker/probe_other_test.go              |    6 +
 internal/jsworker/protocol.go                      |  158 +
 internal/jsworker/race.go                          |    6 +
 internal/jsworker/security_test.go                 |   52 +
 internal/jsworker/tenants_test.go                  |  194 +
 internal/jsworker/worker.go                        |  326 ++
 internal/loadoptions/datastores.go                 |   31 +-
 internal/loadoptions/datastores_test.go            |   35 +
 internal/loadoptions/googlesheets.go               |  463 +++
 internal/loadoptions/googlesheets_export_test.go   |   15 +
 internal/loadoptions/googlesheets_headers_test.go  |  240 ++
 internal/loadoptions/googlesheets_test.go          |  315 ++
 internal/loadoptions/loadoptions.go                |   14 +-
 internal/loadoptions/loadoptions_test.go           |   30 +
 internal/loadoptions/redirect_test.go              |   30 +
 internal/loadoptions/sql.go                        |    4 +-
 internal/loadoptions/sql_test.go                   |   21 +-
 internal/loadoptions/workflows.go                  |   79 +
 internal/loadoptions/workflows_test.go             |  158 +
 internal/mcp/server.go                             |   32 +-
 internal/node/registry.go                          |   75 +-
 internal/node/tool_form_scope_test.go              |   99 +
 internal/node/visibility.go                        |   75 +-
 internal/nodepack/trigger.go                       |   77 +-
 internal/nodepack/trigger_capture_test.go          |  221 ++
 internal/property/editor_test.go                   |   51 +
 internal/property/mapper.go                        |    5 +
 internal/property/mapper_test.go                   |   22 +
 internal/property/property.go                      |   52 +
 internal/repository/auth.go                        |    9 +-
 internal/repository/credential_events.go           |  130 +
 internal/repository/credential_events_test.go      |  122 +
 internal/repository/credential_reseal_test.go      |  218 ++
 internal/repository/credential_seal_test.go        |  133 +
 internal/repository/credential_usage.go            |  204 ++
 internal/repository/credential_usage_test.go       |  194 +
 internal/repository/credentials.go                 |  159 +-
 internal/repository/credentials_external_test.go   |    8 +-
 internal/repository/credentials_page_test.go       |   69 +
 internal/repository/executions.go                  |  262 +-
 internal/repository/export_reseal_test.go          |    7 +
 internal/repository/models.go                      |   38 +-
 internal/repository/node_run_console_test.go       |  117 +
 internal/repository/polls_test.go                  |    8 +
 internal/repository/postgres_execution_test.go     |    2 +
 internal/repository/prefix_test.go                 |   40 +
 internal/repository/refresh.go                     |    6 +-
 internal/repository/refresh_external_test.go       |   86 +
 internal/repository/refresh_sheets_test.go         |   86 +
 internal/repository/reseal.go                      |  153 +
 internal/repository/schedules.go                   |   43 +-
 internal/repository/static_data.go                 |   81 +
 internal/repository/static_data_test.go            |   50 +
 internal/repository/subworkflow_activation_test.go |  116 +
 internal/repository/table_names_test.go            |   23 +
 internal/repository/tenant_rows.go                 |    7 +-
 internal/repository/waits.go                       |    4 +
 internal/repository/waits_test.go                  |   68 +
 internal/repository/webhook_state.go               |  109 +
 internal/repository/webhook_state_test.go          |  148 +
 internal/repository/webhooks.go                    |   18 +-
 internal/repository/workflow_history.go            |    4 +
 internal/repository/workflow_tags.go               |   87 +
 internal/repository/workflow_tags_test.go          |  283 ++
 internal/repository/workflows.go                   |  144 +-
 internal/routing/executor.go                       |   13 +-
 internal/runcode/diagnostic_internal_test.go       |   38 +
 internal/runcode/runcode.go                        |   58 +-
 internal/runcode/runcode_test.go                   |   80 +
 internal/runcode/sandbox.go                        |   73 +
 internal/safehttp/path.go                          |   15 +
 internal/safehttp/redact.go                        |   82 +
 internal/safehttp/redact_test.go                   |  108 +
 internal/safehttp/redirect_scope_test.go           |  141 +
 internal/safehttp/safehttp.go                      |   62 +-
 internal/safehttp/safehttp_test.go                 |   20 +
 internal/scheduler/scheduler.go                    |   51 +-
 internal/scheduler/scheduler_test.go               |   83 +-
 internal/sheetsa1/a1.go                            |   83 +
 internal/sheetsa1/a1_test.go                       |   79 +
 internal/shippedcatalogue/shippedcatalogue.go      |  167 +
 internal/shippedcatalogue/shippedcatalogue_test.go |  132 +
 internal/skills/bundle_test.go                     |   35 +
 internal/skills/check.go                           |  146 +
 internal/skills/check_test.go                      |   59 +
 internal/skills/examples.go                        |  287 ++
 internal/skills/examples_coverage_test.go          |   49 +
 internal/skills/examples_shape_test.go             |  121 +
 internal/skills/examples_test.go                   |  642 ++++
 internal/skills/skill.go                           |   35 +
 internal/skills/skills_test.go                     |  254 +-
 .../skills/kilasflow-fixture/SKILL.md              |   32 +
 internal/sqlbuild/sqlbuild_test.go                 |    5 +-
 internal/sqlnode/export_test.go                    |   32 +-
 internal/sqlnode/guard_test.go                     |    2 +-
 internal/sqlnode/internal_test.go                  |    1 +
 internal/sqlnode/sqlite.go                         |  302 ++
 internal/sqlnode/sqlite_bounded_test.go            |  242 ++
 internal/sqlnode/sqlite_confinement_test.go        |  300 ++
 internal/sqlnode/sqlite_unix_test.go               |  116 +
 internal/sqlnode/sqlnode.go                        |  100 +-
 internal/sqlnode/sqlnode_test.go                   |   37 +-
 internal/sqlnode/tls_test.go                       |  200 +
 internal/tenantpurge/harness_test.go               |   52 +-
 internal/tenantpurge/purge.go                      |    3 +-
 internal/tenantpurge/purge_test.go                 |    4 +-
 internal/wasmpack/hostcalls_http.go                |   24 +-
 internal/wasmpack/probe_test.go                    |    4 +-
 internal/web/embed.go                              |   43 +-
 internal/web/embed_test.go                         |   95 +
 internal/webhook/header_auth_redaction_test.go     |   72 +
 internal/webhook/lifecycle.go                      |   74 +-
 internal/webhook/lifecycle_redaction_test.go       |   74 +
 internal/webhook/lifecycle_scope_test.go           |  129 +
 internal/webhook/lifecycle_test.go                 |  114 +
 internal/webhook/request_lifecycle.go              |  581 ++-
 internal/webhook/request_lifecycle_capture_test.go |  401 ++
 internal/webhook/request_lifecycle_test.go         |  275 ++
 internal/webhook/route_state_test.go               |   75 +
 internal/webhook/sandbox.go                        |   90 +
 internal/webhook/sandbox_test.go                   |  231 ++
 internal/webhook/secret_record_test.go             |   66 +
 internal/webhook/webhook.go                        |   69 +-
 internal/webhook/webhook_test.go                   |   86 +
 internal/workflow/compiler.go                      |  247 +-
 internal/workflow/compiler_test.go                 |   85 +
 internal/workflow/cycle.go                         |  192 +
 internal/workflow/cycle_test.go                    |  161 +
 internal/workflow/document.go                      |   18 +
 internal/workflow/document_test.go                 |   44 +
 internal/workflow/shape.go                         |   43 +
 internal/workflow/tags.go                          |  113 +
 internal/workflow/tags_test.go                     |  188 +
 .../postgres/000021_node_run_console.down.sql      |    8 +
 migrations/postgres/000021_node_run_console.up.sql |   25 +
 .../000022_datastore_unique_names.down.sql         |    8 +
 .../postgres/000022_datastore_unique_names.up.sql  |   64 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../postgres/000024_workflow_static_data.down.sql  |    4 +
 .../postgres/000024_workflow_static_data.up.sql    |   25 +
 migrations/postgres/000025_workflow_tags.down.sql  |    5 +
 migrations/postgres/000025_workflow_tags.up.sql    |   36 +
 .../postgres/000026_credential_events.down.sql     |    4 +
 .../postgres/000026_credential_events.up.sql       |   36 +
 migrations/sqlite/000021_node_run_console.down.sql |    8 +
 migrations/sqlite/000021_node_run_console.up.sql   |   21 +
 .../sqlite/000022_datastore_unique_names.down.sql  |    8 +
 .../sqlite/000022_datastore_unique_names.up.sql    |   57 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../sqlite/000024_workflow_static_data.down.sql    |    4 +
 .../sqlite/000024_workflow_static_data.up.sql      |   25 +
 migrations/sqlite/000025_workflow_tags.down.sql    |    5 +
 migrations/sqlite/000025_workflow_tags.up.sql      |   35 +
 .../sqlite/000026_credential_events.down.sql       |    4 +
 migrations/sqlite/000026_credential_events.up.sql  |   35 +
 nodes/ai.go                                        |  799 +++-
 nodes/ai_fromai_data_test.go                       |  392 ++
 nodes/ai_scope_test.go                             |   52 +
 nodes/ai_test.go                                   |  418 ++-
 nodes/ai_tools_test.go                             |  334 ++
 nodes/annotation.go                                |    1 +
 nodes/batch_shape_test.go                          |  158 +
 nodes/chat.go                                      |    2 +-
 nodes/code.go                                      |   46 +-
 nodes/code_test.go                                 |   48 +
 nodes/core.go                                      |   32 +-
 nodes/credential_redaction_test.go                 |   76 +
 nodes/credential_redirect_test.go                  |  145 +
 nodes/credentialscope_test.go                      |  131 +
 nodes/database.go                                  |   14 +-
 nodes/database_test.go                             |   69 +-
 nodes/datastore.go                                 |  626 +++-
 nodes/datastore_byname_test.go                     |   76 +
 nodes/datastore_test.go                            |  127 +-
 nodes/datastore_tool_test.go                       | 1366 ++++++-
 nodes/embedscope.go                                |  141 +-
 nodes/embedscope_test.go                           |   56 +-
 nodes/error_workflow.go                            |   81 +-
 nodes/executors.go                                 |   53 +-
 nodes/executors_test.go                            |    2 +
 nodes/extract.go                                   |  250 +-
 nodes/extract_test.go                              |   90 +
 nodes/flow.go                                      |   21 +-
 nodes/glyphs_test.go                               |  157 +
 nodes/gmail.go                                     |   40 +-
 nodes/google.go                                    |   56 +-
 nodes/google_sheets.go                             |  526 +++
 nodes/google_sheets_def_test.go                    |  233 ++
 nodes/google_sheets_exec.go                        |  644 ++++
 nodes/google_sheets_export_test.go                 |   14 +
 nodes/google_sheets_fake_test.go                   |  534 +++
 nodes/google_sheets_literal_test.go                |   72 +
 nodes/google_sheets_review_test.go                 |  258 ++
 nodes/google_sheets_run_test.go                    |  203 ++
 nodes/google_sheets_template_run_test.go           |   96 +
 nodes/google_sheets_test.go                        |  526 +++
 nodes/google_sheets_tool.go                        |  209 ++
 nodes/google_sheets_tool_run_test.go               |  212 ++
 nodes/google_sheets_tool_security_test.go          |  180 +
 nodes/google_sheets_tool_test.go                   |  294 ++
 nodes/google_sheets_trigger.go                     |  386 ++
 nodes/google_sheets_trigger_review_test.go         |  192 +
 nodes/google_sheets_trigger_test.go                |  386 ++
 nodes/google_sheets_write.go                       |  616 ++++
 nodes/google_sheets_write_test.go                  |  523 +++
 nodes/google_template_test.go                      |  191 +
 nodes/http.go                                      |   96 +-
 nodes/http_error_test.go                           |  235 ++
 nodes/item_inputs_test.go                          |  112 +
 nodes/jscode.go                                    |  319 +-
 nodes/jscode_helpers.go                            |  272 ++
 nodes/jscode_helpers_test.go                       |  270 ++
 nodes/jscode_lineage_test.go                       |  207 ++
 nodes/jscode_roots.go                              |   49 +
 nodes/jscode_roots_test.go                         |  177 +
 nodes/jscode_run_test.go                           |  361 ++
 nodes/jscode_test.go                               |   41 +-
 nodes/model_redirect_internal_test.go              |  189 +
 nodes/mysql_v2.go                                  |    2 +-
 nodes/openai_credential_url_test.go                |   54 +
 nodes/pgvector.go                                  |   54 +-
 nodes/pgvector_customer.go                         |   30 +-
 nodes/pgvector_test.go                             |   13 +-
 nodes/poll.go                                      |   20 +-
 nodes/ports_for_golden_test.go                     |  309 ++
 nodes/postgres_v2.go                               |  162 +-
 nodes/rag.go                                       |   13 +
 nodes/rag_test.go                                  |    6 +-
 nodes/sidecar_egress.go                            |   28 +-
 nodes/sort_code_test.go                            |  206 ++
 nodes/sqlite_attach_test.go                        |    4 +-
 nodes/stop_and_error_leak_test.go                  |  115 +
 nodes/subworkflow.go                               |  102 +-
 nodes/subworkflow_inputs_test.go                   |   54 +
 nodes/telegram_download.go                         |   15 +-
 nodes/telegram_lifecycle.go                        |   92 +-
 nodes/testdata/n8n_chat_model_options.json         |    3 +-
 nodes/testdata/ports_for.golden.json               | 1826 ++++++++++
 nodes/tool_form.go                                 |  494 +++
 nodes/tool_form_postgres_live_test.go              |   96 +
 nodes/tool_form_run_test.go                        |  977 +++++
 nodes/tool_form_test.go                            |  246 ++
 nodes/tool_fromai.go                               |  151 +
 nodes/transform.go                                 |  127 +-
 nodes/vectorstore_retrieve.go                      |   84 +
 nodes/vectorstore_retrieve_test.go                 |  250 ++
 nodes/vectorstore_tool.go                          |  325 ++
 nodes/vectorstore_tool_export_test.go              |   20 +
 nodes/vectorstore_tool_security_test.go            |  208 ++
 nodes/vectorstore_tool_test.go                     |  565 +++
 nodes/webhook.go                                   |    3 +
 nodes/workflow_tool_inputs_test.go                 |  263 ++
 nodes/workflow_tool_locator_test.go                |   50 +
 packs/telegram/default_base_url_test.go            |   32 +
 packs/telegram/redaction_test.go                   |   43 +
 packs/waha/waha_test.go                            |   55 +-
 schemas/workflow-v1.schema.json                    |   24 +-
 scripts/cli-examples/main.go                       |  583 +++
 scripts/cli-examples/main_test.go                  |  215 ++
 scripts/cli-examples/process_other.go              |   14 +
 scripts/cli-examples/process_test.go               |  110 +
 scripts/cli-examples/process_unix.go               |   35 +
 scripts/cli-examples/seed.go                       |  330 ++
 scripts/cli-examples/server.go                     |  183 +
 scripts/cli-examples/testdata/credential.json      |    5 +
 scripts/cli-examples/testdata/patch.json           |   19 +
 scripts/cli-examples/testdata/payload.json         |    1 +
 scripts/cli-examples/testdata/wf.json              |    9 +
 scripts/cli-examples/testdata/workflow.json        |   28 +
 scripts/code-corpus-sync.sh                        |  312 ++
 scripts/docs-facts/main.go                         |  177 +
 scripts/docs-facts/main_test.go                    |  278 ++
 scripts/generate-api-reference.mjs                 |   51 +-
 scripts/internal/splice/splice.go                  |  149 +
 scripts/internal/splice/splice_test.go             |  112 +
 scripts/js-diff/harness.mjs                        |  310 ++
 scripts/js-parity/record-engine.mjs                |  341 ++
 scripts/js-parity/record.mjs                       |  756 ++++
 scripts/skills-command-reference/main.go           |  130 +-
 scripts/smoke-cli.sh                               |   10 +-
 sdk/CHANGELOG.md                                   |   27 +
 sdk/README.md                                      |   20 +-
 sdk/examples/reference-host/server.mjs             |   12 +-
 sdk/src/generated/models.ts                        |  546 ++-
 sdk/src/server.ts                                  |   37 +-
 sdk/test/operation-coverage.test.mjs               |   21 +-
 sdk/test/operations.test.ts                        |   19 +
 sidecar/doc.go                                     |    2 +-
 sidecar/runner_test.go                             |   15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |    2 +-
 skills/index.json                                  |  164 +-
 skills/kilasflow-ai-agents/SKILL.md                |  115 +
 skills/kilasflow-ai-agents/references/EXAMPLE.md   |  146 +
 skills/kilasflow-ai-agents/references/MODELS.md    |  105 +
 skills/kilasflow-ai-agents/references/RAG.md       |  374 ++
 skills/kilasflow-credentials/SKILL.md              |   47 +-
 .../references/CREDENTIAL_TYPES.md                 |   48 +-
 skills/kilasflow-credentials/references/EXAMPLE.md |   97 +
 skills/kilasflow-datastore/SKILL.md                |   51 +-
 skills/kilasflow-datastore/references/EXAMPLE.md   |  130 +
 skills/kilasflow-datastore/references/FILTERS.md   |   42 +-
 skills/kilasflow-debugging/SKILL.md                |   38 +-
 skills/kilasflow-debugging/references/EXAMPLE.md   |   70 +
 .../references/TRACE_READING.md                    |   43 +-
 skills/kilasflow-embedding/SKILL.md                |   56 +-
 .../references/EMBED_HANDSHAKE.md                  |   17 +-
 skills/kilasflow-embedding/references/EXAMPLE.md   |   82 +
 .../references/SESSION_AUTHORITY.md                |   10 +-
 skills/kilasflow-error-handling/SKILL.md           |   39 +-
 .../kilasflow-error-handling/references/EXAMPLE.md |  117 +
 skills/kilasflow-expressions/SKILL.md              |   50 +-
 skills/kilasflow-expressions/references/EXAMPLE.md |  134 +
 .../references/EXPRESSION_ROOTS.md                 |   63 +-
 skills/kilasflow-flow-patterns/SKILL.md            |  105 +
 .../references/BINARY_DATA.md                      |  116 +
 .../kilasflow-flow-patterns/references/EXAMPLE.md  |  134 +
 .../references/SUB_WORKFLOWS.md                    |  173 +
 skills/kilasflow-import-export/SKILL.md            |   41 +-
 .../kilasflow-import-export/references/EXAMPLE.md  |  108 +
 .../references/MAPPING_LIMITS.md                   |  102 +-
 skills/kilasflow-node-configuration/SKILL.md       |   44 +-
 .../references/EXAMPLE.md                          |  141 +
 .../references/LOAD_OPTIONS.md                     |   65 +-
 .../references/PROPERTY_KINDS.md                   |   67 +-
 skills/kilasflow-node-packs/SKILL.md               |   38 +-
 skills/kilasflow-node-packs/references/EXAMPLE.md  |   75 +
 .../references/MODULE_AND_SIDECAR.md               |   32 +-
 .../kilasflow-node-packs/references/PACK_FORMAT.md |   39 +-
 skills/kilasflow-operations/SKILL.md               |   17 +-
 skills/kilasflow-operations/references/EXAMPLE.md  |   86 +
 .../references/UPGRADE_ORDER.md                    |   15 +-
 skills/kilasflow-triggers/SKILL.md                 |   48 +-
 skills/kilasflow-triggers/references/EXAMPLE.md    |   80 +
 .../references/WEBHOOK_DELIVERY.md                 |   33 +-
 skills/kilasflow-workflow-lifecycle/SKILL.md       |   36 +-
 .../references/EXAMPLE.md                          |  111 +
 .../references/NAMING_AND_DESCRIPTIONS.md          |   25 +-
 .../references/VALIDATION_CHECKLIST.md             |   29 +-
 skills/using-kilasflow-skills/SKILL.md             |   45 +-
 .../using-kilasflow-skills/references/EXAMPLE.md   |   65 +
 third_party/lodash/LICENSE                         |   47 +
 third_party/lodash/PROVENANCE.md                   |   32 +
 third_party/lodash/embed.go                        |   15 +
 third_party/lodash/lodash.min.js                   |  136 +
 third_party/luxon/LICENSE                          |    7 +
 third_party/luxon/PROVENANCE.md                    |   34 +
 third_party/luxon/embed.go                         |   15 +
 third_party/luxon/luxon.min.js                     |    1 +
 third_party/waha/PROVENANCE.md                     |    5 +-
 web/messages/en/auth.json                          |    2 +
 web/messages/en/credentials.json                   |    8 +
 web/messages/en/datastores.json                    |   48 +-
 web/messages/en/editor.json                        |   53 +-
 web/messages/en/executions.json                    |  193 +-
 web/messages/en/nav.json                           |    2 +-
 web/messages/en/properties.json                    |   18 +-
 web/messages/en/settings.json                      |    9 +-
 web/messages/en/workflows.json                     |   25 +-
 web/messages/id/auth.json                          |    2 +
 web/messages/id/credentials.json                   |    8 +
 web/messages/id/datastores.json                    |   48 +-
 web/messages/id/editor.json                        |   58 +-
 web/messages/id/executions.json                    |  193 +-
 web/messages/id/nav.json                           |    2 +-
 web/messages/id/properties.json                    |   18 +-
 web/messages/id/settings.json                      |    9 +-
 web/messages/id/workflows.json                     |   25 +-
 web/package.json                                   |   11 +
 web/pnpm-lock.yaml                                 |  160 +-
 web/src/app.css                                    |  704 +++-
 web/src/app.d.ts                                   |    3 +
 .../lib/api/generated/credentials/credentials.ts   |   32 +-
 web/src/lib/api/generated/executions/executions.ts |    4 +-
 web/src/lib/api/generated/interop/interop.ts       |  103 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIAgentFailedEvent.ts |   24 +
 .../api/generated/models/aIModelCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |   24 +
 .../api/generated/models/aIModelStartedEvent.ts    |   24 +
 .../api/generated/models/aIToolCompletedEvent.ts   |   24 +
 .../lib/api/generated/models/aIToolFailedEvent.ts  |   24 +
 .../lib/api/generated/models/aIToolStartedEvent.ts |   24 +
 .../lib/api/generated/models/codeConsoleEvent.ts   |   24 +
 .../generated/models/convertFragmentInputBody.ts   |   16 +
 .../generated/models/convertedFragmentResource.ts  |   21 +
 web/src/lib/api/generated/models/credentialBody.ts |    4 +-
 .../api/generated/models/credentialBodyFields.ts   |    2 +-
 .../lib/api/generated/models/credentialResource.ts |   11 +-
 .../api/generated/models/credentialTypeResource.ts |    9 +
 .../generated/models/credentialUsageResource.ts    |   19 +
 .../api/generated/models/deleteCredentialParams.ts |   14 +
 web/src/lib/api/generated/models/document.ts       |    2 +
 .../lib/api/generated/models/embedSessionBody.ts   |    2 +-
 .../generated/models/evalExpressionInputBody.ts    |   12 +-
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/executionResource.ts  |    3 +
 .../lib/api/generated/models/healthOutputBody.ts   |    2 +
 web/src/lib/api/generated/models/index.ts          |   18 +
 .../api/generated/models/listWorkflowsParams.ts    |    5 +
 .../api/generated/models/loadOptionsInputBody.ts   |    2 +-
 web/src/lib/api/generated/models/otherEvent.ts     |   24 +
 .../generated/models/resourceMapperDeclaration.ts  |    1 +
 .../models/streamExecutionEvents200Item.ts         |   99 +
 .../lib/api/generated/models/testPayloadBody.ts    |    4 +-
 web/src/lib/api/generated/models/typeOptions.ts    |    4 +
 .../lib/api/generated/models/typeOptionsEditor.ts  |   14 +
 .../generated/models/typeOptionsEditorLanguage.ts  |   17 +
 .../generated/models/validateWorkflowResource.ts   |    6 +
 .../lib/api/generated/models/validationWarning.ts  |   13 +
 .../api/generated/models/webhookResponseEvent.ts   |   24 +
 .../api/generated/models/workflowDocumentInput.ts  |    6 +
 .../lib/api/generated/models/workflowSummary.ts    |    2 +
 web/src/lib/api/http.ts                            |   32 +-
 .../lib/components/dashboard/dashboard-nav.svelte  |   10 +-
 .../components/dashboard/workflow-tag-chips.svelte |   27 +
 web/src/lib/components/ui/button/button.svelte     |   14 +-
 web/src/lib/components/ui/checkbox/checkbox.svelte |    2 +-
 .../lib/components/ui/dialog/dialog-content.svelte |    2 +-
 .../dropdown-menu-checkbox-item.svelte             |    2 +-
 .../ui/dropdown-menu/dropdown-menu-content.svelte  |    2 +-
 .../ui/dropdown-menu/dropdown-menu-item.svelte     |    2 +-
 .../dropdown-menu/dropdown-menu-radio-item.svelte  |    2 +-
 .../dropdown-menu/dropdown-menu-sub-content.svelte |    2 +-
 .../dropdown-menu/dropdown-menu-sub-trigger.svelte |    2 +-
 web/src/lib/components/ui/input/input.svelte       |    4 +-
 web/src/lib/components/ui/list-row/index.ts        |    8 +
 web/src/lib/components/ui/list-row/list-row.svelte |   29 +
 .../components/ui/popover/popover-content.svelte   |    2 +-
 .../components/ui/scroll-area/scroll-area.svelte   |    2 +-
 .../lib/components/ui/select/select-content.svelte |    2 +-
 .../lib/components/ui/select/select-item.svelte    |    2 +-
 .../lib/components/ui/select/select-trigger.svelte |    2 +-
 .../lib/components/ui/sheet/sheet-content.svelte   |    2 +-
 web/src/lib/components/ui/sonner/sonner.svelte     |    2 +-
 web/src/lib/components/ui/switch/switch.svelte     |    4 +-
 web/src/lib/components/ui/tabs/tabs-content.svelte |    2 +-
 web/src/lib/components/ui/tabs/tabs-trigger.svelte |    2 +-
 web/src/lib/components/ui/textarea/textarea.svelte |    2 +-
 .../workflow-editor/canvas-bridge.svelte           |    3 +-
 .../workflow-editor/canvas-chat-panel.svelte       |  381 +-
 .../components/workflow-editor/canvas-node.svelte  |  125 +-
 .../workflow-editor/chat-markdown.svelte           |   38 +
 .../components/workflow-editor/code-editor.svelte  |   92 +
 .../workflow-editor}/diagnostics-section.svelte    |    0
 .../workflow-editor/execution-canvas-node.svelte   |   46 +-
 .../workflow-editor/execution-canvas.svelte        |    6 +-
 .../workflow-editor}/import-report.svelte          |    0
 .../components/workflow-editor/node-console.svelte |   27 +
 .../workflow-editor/node-console.test.ts           |   70 +
 .../components/workflow-editor/node-picker.svelte  |   28 +-
 .../workflow-editor/panel-query-harness.svelte     |   19 +
 .../workflow-editor/paste-report-sheet.svelte      |   45 +
 .../workflow-editor/properties-panel.svelte        |  207 +-
 .../workflow-editor/properties-panel.test.ts       |  254 ++
 .../workflow-editor/property-field.svelte          |  275 +-
 .../workflow-editor/property-field.test.ts         |  251 ++
 .../workflow-editor/version-panel.svelte           |   17 +-
 .../workflow-editor/webhook-address.svelte         |   91 +
 .../workflow-editor/webhook-address.test.ts        |   63 +
 .../workflow-editor/workflow-editor.svelte         |  537 ++-
 web/src/lib/dashboard/auth-mode.test.ts            |   65 +
 web/src/lib/dashboard/auth-mode.ts                 |   79 +
 web/src/lib/dashboard/credential-scope.test.ts     |   34 +
 web/src/lib/dashboard/credential-scope.ts          |   31 +
 web/src/lib/dashboard/credential-test.test.ts      |   45 +
 web/src/lib/dashboard/credential-test.ts           |   27 +
 web/src/lib/dashboard/credential-usage.test.ts     |   46 +
 web/src/lib/dashboard/credential-usage.ts          |   38 +
 web/src/lib/dashboard/sidebar.test.ts              |   34 +
 web/src/lib/dashboard/sidebar.ts                   |   35 +
 web/src/lib/dashboard/workflow-list.test.ts        |  138 +-
 web/src/lib/dashboard/workflow-list.ts             |   99 +-
 web/src/lib/datastore/columns.test.ts              |   36 +
 web/src/lib/datastore/columns.ts                   |   30 +
 web/src/lib/datastore/transfer.test.ts             |   33 +
 web/src/lib/datastore/transfer.ts                  |   39 +-
 web/src/lib/design-system/app-css.ts               |   15 +
 web/src/lib/design-system/class-scan.ts            |  245 ++
 web/src/lib/design-system/color.test.ts            |  120 +
 web/src/lib/design-system/color.ts                 |  513 +++
 web/src/lib/design-system/contrast-pairs.ts        |   84 +
 web/src/lib/design-system/css-tokens.test.ts       |  110 +
 web/src/lib/design-system/css-tokens.ts            |  236 ++
 web/src/lib/design-system/focus-lint.test.ts       |  143 +
 web/src/lib/design-system/focus-lint.ts            |  236 ++
 web/src/lib/design-system/state-matrix.svelte      |  335 ++
 web/src/lib/design-system/theme-color.test.ts      |   23 +
 web/src/lib/design-system/theme-color.ts           |   34 +
 web/src/lib/design-system/tokens.contrast.test.ts  |  116 +
 web/src/lib/design-system/tokens.groups.test.ts    |  129 +
 web/src/lib/design-system/tokens.hue.test.ts       |  177 +
 web/src/lib/embed/brand-root.test.ts               |   44 +
 web/src/lib/embed/brand-root.ts                    |   24 +
 web/src/lib/embed/embed-editor.svelte              |   94 +-
 web/src/lib/embed/session.svelte.ts                |   68 +-
 web/src/lib/embed/session.test.ts                  |  118 +-
 web/src/lib/workflow-editor/authoring.test.ts      |    6 +-
 web/src/lib/workflow-editor/canvas-actions.ts      |    2 +
 web/src/lib/workflow-editor/canvas-chrome.test.ts  |   96 +
 web/src/lib/workflow-editor/canvas-chrome.ts       |   62 +
 .../lib/workflow-editor/canvas-projection.test.ts  |  147 +
 web/src/lib/workflow-editor/catalog.test.ts        |   38 +
 web/src/lib/workflow-editor/catalog.ts             |   20 +-
 web/src/lib/workflow-editor/chat-markdown.test.ts  |  110 +
 web/src/lib/workflow-editor/chat-markdown.ts       |  211 ++
 web/src/lib/workflow-editor/chat-stream.test.ts    |   70 +
 web/src/lib/workflow-editor/chat-stream.ts         |  112 +
 web/src/lib/workflow-editor/chat.test.ts           |   69 +-
 web/src/lib/workflow-editor/chat.ts                |   86 +-
 web/src/lib/workflow-editor/clipboard.test.ts      |  219 +-
 web/src/lib/workflow-editor/clipboard.ts           |  276 +-
 web/src/lib/workflow-editor/code-editor.test.ts    |   90 +
 web/src/lib/workflow-editor/code-editor.ts         |  264 ++
 web/src/lib/workflow-editor/credentials.test.ts    |   36 +-
 web/src/lib/workflow-editor/credentials.ts         |   43 +
 web/src/lib/workflow-editor/document.test.ts       |   93 +-
 web/src/lib/workflow-editor/document.ts            |  282 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   79 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   41 +-
 .../lib/workflow-editor/execution-watch.test.ts    |   58 +
 web/src/lib/workflow-editor/execution-watch.ts     |   56 +
 web/src/lib/workflow-editor/execution.test.ts      |  169 +-
 web/src/lib/workflow-editor/execution.ts           |  181 +-
 .../lib/workflow-editor/expression-assist.test.ts  |    7 +
 web/src/lib/workflow-editor/expression-assist.ts   |   12 +-
 web/src/lib/workflow-editor/glyphs.json            |   58 +
 web/src/lib/workflow-editor/layout.ts              |    9 +-
 web/src/lib/workflow-editor/node-visual.test.ts    |  169 +-
 web/src/lib/workflow-editor/node-visual.ts         |  193 +-
 web/src/lib/workflow-editor/picker-rows.test.ts    |  100 +
 web/src/lib/workflow-editor/picker-rows.ts         |   38 +
 web/src/lib/workflow-editor/port-settle.test.ts    |   71 +
 web/src/lib/workflow-editor/port-settle.ts         |   42 +
 web/src/lib/workflow-editor/ports.golden.test.ts   |   77 +
 web/src/lib/workflow-editor/ports.imported.test.ts |  146 +
 web/src/lib/workflow-editor/ports.modes.test.ts    |  201 +
 web/src/lib/workflow-editor/ports.picked.test.ts   |  126 +
 web/src/lib/workflow-editor/ports.stale.test.ts    |  159 +
 web/src/lib/workflow-editor/ports.test.ts          |   37 +
 web/src/lib/workflow-editor/ports.ts               |  247 +-
 .../lib/workflow-editor/resource-mapper.test.ts    |  262 +-
 web/src/lib/workflow-editor/resource-mapper.ts     |  172 +-
 web/src/lib/workflow-editor/save-feedback.test.ts  |   52 +
 web/src/lib/workflow-editor/save-feedback.ts       |   41 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |  131 +-
 web/src/lib/workflow-editor/shortcuts.ts           |  110 +-
 web/src/lib/workflow-editor/sticky.test.ts         |   45 +-
 web/src/lib/workflow-editor/sticky.ts              |   87 +-
 web/src/lib/workflow-editor/validation.test.ts     |   24 +
 web/src/lib/workflow-editor/validation.ts          |   19 +-
 .../lib/workflow-editor/webhook-address.test.ts    |  151 +
 web/src/lib/workflow-editor/webhook-address.ts     |   82 +
 web/src/lib/workflow-editor/workflow-input.ts      |   27 +
 web/src/routes/(dashboard)/+layout.svelte          |   51 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |   81 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  131 +-
 .../app/workflows/[id]/export-dialog.svelte        |    2 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |    2 +-
 .../app/workflows/import-report-drawer.svelte      |    2 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  108 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |   88 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  245 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |    2 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |   32 +-
 web/src/routes/+page.svelte                        |   40 +-
 web/src/routes/approve/[token]/+page.svelte        |    8 +
 web/src/routes/design/states/+page.svelte          |    5 +
 web/src/routes/design/states/+page.ts              |   16 +
 web/src/routes/embed/[id]/+page.svelte             |   13 +-
 web/src/routes/login/+page.svelte                  |   70 +-
 web/vite.config.ts                                 |   10 +
 1311 files changed, 193629 insertions(+), 5981 deletions(-)
```
