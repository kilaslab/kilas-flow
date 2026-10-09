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

- **Credentials.** `googleSheetsOAuth2Api` and `googleSheetsTriggerOAuth2Api` (n8n's IDs, two types as the orchestrator decided), registered with `googleOAuthType`, covered by `IsGoogleOAuth` (so Connect and the token refresh work), with default scopes that mirror n8n's (space-separated; `drive.metadata` is restricted by Google, so the field stays editable and the url and id locator modes work without it). New `credentials.GoogleDefaultScope`; the OAuth start handler uses it instead of a Gmail-or-Drive guess, which would have asked a Sheets credential for Drive access. The credentials page shows Connect for both. `docs/.../concepts/credentials.md` has the two IDs in the empty-allowed-hosts table and in the file list; its catalogue table is already stale (it says thirteen types and lacks Drive and Gmail) and is left to the docs track.
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
- `drive.metadata` (restricted) is in the default scopes. Without Google's verification of the platform client, edit the credential's Scope field; the url and id locator modes need no Drive scope.
- The credential "Test" button uses the shared Google probe (`oauth2/v3/userinfo`), which a token without a profile scope may refuse; Drive and Gmail share the same probe, so a fix belongs to all three.
- The shared `resolveGoogleIR` also evaluates `{{ }}` inside already-resolved values (Track C's fix is in flight). A Sheets-level regression test for a cell value that contains `{{ }}` should be added after the rebase onto it.
- Wave 2: `googleSheetsToolNode()` over `toolVariantOf(...)`, `toolVariants["kilasflow.googleSheets"]`, the glyph variant, the e2e inventories, a fixture shaped like template 19348 and an agent run against the stub.

## Verification (2026-10-09)

- `go vet ./...` and `gofmt -l .`: clean.
- `go test ./... -race -skip TestLuxonMatchesTheRecordedNodeGoldens`: 54 packages ok; three failed, none in code this change touches. `nodes` fails under `-race` on a data race inside the test helper `stubCredentials` (`nodes/http_test.go:30`, appended to from parallel subtests): `TestAnOpenAIChatModelFallsBackToItsCredentialsBaseURL` fails alone under `-race` and the Sheets tests are race-clean (`go test ./nodes -race -run 'TestSheets|TestGoogleSheets|TestAnImportedSheets|TestASheetsFailure|TestPollHandlers'` passes). `jsrun` (two tests over their 10s limit) and `runcode` (10m package timeout) failed on a loaded machine under `-race`. Without `-race`: `go test ./nodes ./internal/jsrun ./internal/runcode -skip TestLuxonMatchesTheRecordedNodeGoldens` all ok.
- `make generate-api-check generate-types-check generate-api-reference-check generate-config-reference-check generate-skills-index-check generate-skills-command-reference-check corpus-check`: every generated artefact is fresh (no new endpoint, key, verb or skill); `corpus-check` skipped its scoreboard as "no corpus materialised".
- `pnpm check` (0 errors) and `pnpm test` (810 tests) from `web/`.
- e2e `tests/node-coverage.spec.ts` against a freshly built binary: 16 of 18 pass, including the Google nodes refusing to run without their own credential. The two failures (`the error workflow pair and the hosted form run against the binary`, `a sticky note saves and runs without affecting the result`) are in nodes this change does not touch (a node name where the test expects its id; a missing sticky-note trace row) and were not re-run on a clean main.
