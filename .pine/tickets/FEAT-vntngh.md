---
id: FEAT-vntngh
title: Google Sheets node, trigger and tool (the most-used n8n app node)
status: doing
priority: critical
labels:
    - n8n
    - parity
    - node-catalog
    - google
parent: EPIC-8rbys7
created: "2026-09-23T01:17:54Z"
updated: "2026-10-09T11:03:29Z"
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
- [ ] `googleSheetsTrigger` (rowAdded/rowUpdate/anyUpdate) runs on the leased poll framework, and `googleSheetsTool` is usable by the AI Agent. **Trigger half met in wave 1** (`nodes/google_sheets_trigger_test.go`: baseline tick, the three events, columnsToWatch, deleted rows, row cap, `TestPollHandlersCoverEveryTriggerExtractPollsRecognises`). **Tool half open: `googleSheetsTool` is wave 2**, after the generic base-executor-as-tool mechanism lands, so this box stays unticked.
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
