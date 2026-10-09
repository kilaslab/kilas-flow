---
id: FEAT-e898nz
title: 'Workflow tags: a stored, listed field that n8n import and export round-trip'
status: doing
priority: low
created: "2026-10-08T16:58:07Z"
updated: "2026-10-09T11:03:29Z"
---

# Description

A workflow carries an ordered list of short labels (tags). Tags group workflows in the dashboard, the CLI, MCP and the API, and n8n import and export carry them, which closes BUG-6gkd12's "tags round-trip".

- Tags are an optional `tags` field of the canonical document (`workflow.Document.Tags`, `schemas/workflow-v1.schema.json`, OpenAPI `Document` and `WorkflowDocumentInput`). Each revision has its own tags, the way it has its own name, so a restore brings its tags back.
- Every path that appends a revision rewrites a per-tenant `workflow_tags` index in the same transaction. The list reads tags and filters by tag from that index without loading documents (BUG-fv5fer is why the list never loads them).
- They live in the document rather than in a repository-only column so that n8n import and export stay inside `internal/interop/n8n`, validation has one authority (`workflow.ValidateDraft`), no API operation is added, and the get, edit, update loop agents use already carries the field.

# Acceptance Criteria
- [x] 1. The document has an optional `tags: string[]` (Go, JSON schema, OpenAPI). A document without tags encodes byte for byte as before (`omitempty`). Evidence: `TestADocumentWithoutTagsEncodesAsBefore`, `TestDecodeDocumentReadsTags`, `TestCanonicalJSONSchemaDeclaresTags` (internal/workflow), `TestTheOpenAPIDocumentsTags` (internal/api).
- [x] 2. `ValidateDraft` enforces at most 20 tags; each tag 1 to 24 characters with no leading or trailing whitespace, no comma and no control character; tags unique ignoring case; authored order and case kept. A bad tag gets a 422 that names it. The 24-character limit is n8n's tag-name limit, so an export never carries a tag n8n refuses. Evidence: `TestValidateDraftRefusesBadTags`, `TestValidateDraftAcceptsTwentyTagsOfTwentyFourCharacters` (internal/workflow); `TestABadTagIsRefusedAndNamed` (422 on create and update, a diagnostic on validate). The limit is counted in UTF-16 code units, which is how n8n counts it (see Progress).
- [x] 3. Save, restore and duplicate carry tags. `GET /workflows` returns `tags` on every summary, always an array (`[]` when none). `?tag=` filters case-insensitively and pages with the existing cursor. Deleting a workflow removes its tag rows. Evidence: `TestSavedTagsAreListedWithTheSummary`, `TestTheWorkflowListFiltersByTagCaseInsensitively` (Limit 1 plus cursor), `TestASaveWithoutATagUnlistsIt`, `TestRestoringAVersionRestoresItsTags`, `TestDeletingAWorkflowRemovesItsTags` (internal/repository, both drivers); `TestTheWorkflowListShowsTagsAndAnUntaggedWorkflowHasAnEmptyArray`, `TestTheWorkflowListFiltersByTag`, `TestADuplicatedWorkflowKeepsItsTags`, `TestRestoringARevisionRestoresItsTagsInTheList` (internal/api).
- [x] 4. Tenant scoping: one tenant's tags never list or filter in another tenant. `workflow_tags` is covered by the tenant purge and named on operate/tenant-deletion.md. Evidence: `TestTagsNeverCrossTenants`; `TestEveryTenantTableIsPurgedOrExplicitlyExempt` and `TestPurgeRemovesOneTenantAndLeavesTheOtherIntact` (internal/tenantpurge, which failed before the wiring); the docs tests `TestDocumentedOrderMatchesTheOrchestrator` and `TestDocumentedResponseExampleNamesEveryTableTheServiceReports`; `TestPrefixedStoreKeepsWorkflowTagsInThePrefixedTable`.
- [x] 5. n8n import reads `tags` (objects with `name`) and keeps the names in order. Instance-local `id`, `createdAt` and `updatedAt` are ignored without a report. Everything it cannot keep is reported with `field: "tags"` and the file is never refused for it: a bare string, a missing or empty name, too long, a comma or control character, a case-insensitive duplicate, more than 20, or a `tags` value that is not an array. Pasting a full export through convert reports nothing about tags. Evidence: `TestImportedTagsStayWithinWhatAWorkflowMayCarry` (14 cases, and what is kept passes `ValidateDraft`), `TestManyRefusedTagsAreReportedInBoundedSpace`, `TestAPastedExportReportsNothingAboutTags`, `TestAnImportedN8nWorkflowKeepsItsTagsAndExportsThem` (API). Severity: see Progress.
- [x] 6. n8n export writes `"tags":[{"name":...}]` in document order. It invents no ids and leaves the key out when there are no tags. Import, export, import keeps the names. Evidence: `TestAWorkflowsTagsRoundTrip`, `TestAnUntaggedExportHasNoTagsKey`, and the API export assertion in `TestAnImportedN8nWorkflowKeepsItsTagsAndExportsThem`.
- [x] 7. `kilasflow workflow list --tag <name>` filters, and the MCP tool derived from the verb gets the same argument. JSON output includes `tags`. Evidence: `TestWorkflowListSendsTheTagFilter`, `TestWorkflowListSendsNoTagParameterWithoutTheFlag`, `TestTheWorkflowListToolTakesATagArgument` (internal/cli).
- [x] 8. Dashboard: each row shows its tags as chips (up to 3, then "+N"); the filter bar has a Tag select (`?tag=`); the Rename dialog edits tags (comma-separated); editor saves (dashboard and embed), rename and duplicate never drop tags. Evidence: vitest `toWorkflowInput` and `workflow-list` tests (carries tags, query round trip, tag filter, `parseTagInput`, `workflowTagOptions`), `pnpm check` 0 errors, catalog parity test; driven by hand in a real browser against `bin/kilasflow` (see Progress).
- [x] 9. Generated artefacts are regenerated and their `-check` targets pass. Docs are updated, and the SDK CHANGELOG has an Additive entry. Evidence: `make generate-api-check generate-types-check generate-api-reference-check generate-skills-command-reference-check generate-skills-index-check generate-config-reference-check` all pass; docs: guides/n8n-migration.md, reference/cli.md, operate/tenant-deletion.md; sdk/CHANGELOG.md.

# Implementation Plan

The plan was written by a planner (scratchpad plans/E-tags.md) and followed; the deviations are listed under Progress.

# Notes

- The tags are in the document, so tagging appends a revision, exactly as a rename does. The version-history diff (history-diff.ts) compares only nodes and connections, so a tags-only revision shows no node changes there, as a rename does today. That is a follow-up, not in scope.
- `strings.ToLower` is simple case mapping, not full case folding (a sharp s and SS stay different tags). That is deliberate and noted in `workflow.TagKey`.
- A save replaces a workflow's tags with the ones in its body, so a client that rewrites a document must send back the `tags` it read (the SDK CHANGELOG entry says so). The editor's save is protected by the same base-version check as every other field.

# Related Files

- `internal/workflow/tags.go`, `internal/workflow/document.go`, `schemas/workflow-v1.schema.json`
- `migrations/{sqlite,postgres}/000025_workflow_tags.{up,down}.sql`
- `internal/repository/workflow_tags.go`, `workflows.go`, `workflow_history.go`, `tenant_rows.go`
- `internal/tenantpurge/purge.go`, `docs/src/content/docs/operate/tenant-deletion.md`
- `internal/api/handlers/workflows.go`
- `internal/interop/n8n/tags.go` (and four small hunks in `n8n.go`)
- `internal/cli/verbs_workflow.go`
- `web/src/lib/workflow-editor/document.ts`, `web/src/lib/dashboard/workflow-list.ts`, `web/src/lib/components/dashboard/workflow-tag-chips.svelte`, `web/src/routes/(dashboard)/app/workflows/+page.svelte`, `web/messages/{en,id}/workflows.json`

# Attachments

## Progress (2026-10-09)

What changed, in commit order:

1. `workflow.Document.Tags` with `MaxTags = 20`, `MaxTagLength = 24`, `TagKey`, `CheckTag` and the validation loop in `ValidateDraft` (internal/workflow/tags.go); the optional `tags` property in the published JSON schema, pinned to the Go constants by a test.
2. Migration 000025 `workflow_tags` on both dialects (quoted identifiers so `table_prefix` applies, no foreign key because workflows are soft-deleted, column `ordinal` not `position`). `replaceWorkflowTags` runs in the same transaction as `saveDraft` and `RestoreVersion`; `Delete` removes the rows; `ListSummaries` filters through an `id IN (subquery)` built from the model (so the prefix applies) and reads the page's tags in one extra query. Tenant purge, its tests and the docs name the table.
3. API: `tags` on `WorkflowDocumentInput` (maxItems 20), a required, non-nullable `tags` array on `WorkflowSummary`, and `?tag=` on list-workflows. No operation was added, so there is no api-contract row and `contractRowCount` is unchanged.
4. n8n: tags.go reads and writes tags; the n8n.go diff is four small hunks (a `Tags json.RawMessage` field at the end of `Document`, the import call, the export call, `field == "tags"` in `workflowOnlyField`).
5. CLI: `workflow list --tag`; the MCP tool follows from the flag set.
6. Regenerated web client, SDK types, API reference and the skills command reference; SDK CHANGELOG entry.
7. Web: `toWorkflowInput` carries `tags`; chips component; Tag select; tags in the Rename dialog; duplicate carries tags; catalogs in en and id.
8. Docs: n8n-migration.md (document-level elements, export), cli.md.

### n8n wire format: what was checked (2026-10-09, in my own words)

Read as a behaviour reference only, from n8n's public source on GitHub (master), per memory/licensing.md 2026-09-24. Nothing was copied; the importer was written from this description.

- **A tag entry with only a name imports cleanly.** The CLI workflow import walks each entry of a workflow's tags and skips an entry that has no name. For one that has a name, it first looks for an existing tag with the same id and creation time, then for one with exactly the same name (case-sensitive), and otherwise it creates a new tag from the entry. The tag entity generates its own id just before it is inserted when it has none, so a missing `id` is fine. The editor's "import from file" path matches by name too and creates each missing tag from its name alone; it never reads the id. Conclusion: `[{"name": "..."}]` without an id is a valid, clean input, and the plan's export shape stands. No ids are invented.
- **The limit is 24, counted in JavaScript string length.** The tag entity stores the name in a 24-character column and validates it as 1 to 24 characters with a string-length check, which counts UTF-16 code units, so an emoji counts two. KilasFlow's `CheckTag` counts code units for the same reason, so that an export never carries a tag n8n would refuse. JSON Schema counts code points, so the published schema's `maxLength: 24` is a bound every valid tag meets, not an exact one; the schema's description says so.
- **Names are case-sensitive in n8n.** n8n would keep "Prod" and "prod" as two tags; KilasFlow compares tags ignoring case, so the second is reported as a duplicate on import rather than silently merged.
- **A bare string in a file's `tags` is an id.** The only place n8n sends bare strings is its editor's own traffic, where they are tag ids, which is why a string entry is reported rather than taken as a name.
- **What was not checked:** n8n's editor "Download" button and the public API's GET were taken from the plan (they write `{id, name, createdAt, updatedAt}` per tag); the CLI export reads the tags relation, which is consistent with that. The import side, which decides whether our export opens cleanly, was the part read.

### Deviations from the plan

- **Severity is "dropped", not "lossy", for a tag that is not carried.** The code's own vocabulary (n8n.go, `SeverityDropped`) says dropped is for an element that was not carried at all and that calling it lossy "would imply a setting was applied when it was ignored". A bare string, an entry with no name, a rule-breaking name, a repeat, and everything past the 20th are dropped; one more line (not one per tag) covers the overflow. A name that arrived with whitespace around it is carried trimmed, which is the case "carried, but differently", so that one is lossy.
- **Length is counted in UTF-16 code units, not code points.** See the wire-format section. The test suite pins thirteen emoji (26 units) as refused.
- **The repository normalises the filter.** `WorkflowFilter.Tag` takes the raw tag; `ListSummaries` trims and lower-cases it (`workflow.TagKey`), so every caller (API, tests, future callers) gets the same matching. The handler passes the query value through.
- **The validate operation answers 200 with a diagnostic, not 422,** for a bad tag, as it does for every other draft problem (the plan said 422 for create, update and validate). More than 20 tags is refused by the request schema (`maxItems`) with a 422 before the dry run starts, on all three operations.
- **`WorkflowSummary.tags` is non-nullable in OpenAPI** (`nullable:"false"`), so the generated TypeScript type is `string[]` rather than `string[] | null`.
- **New test files instead of extending existing ones** (internal/interop/n8n/tags_test.go, internal/cli/verbs_workflow_tag_test.go, internal/workflow/tags_test.go), to keep the diff clear of the three other tracks that edit roundtrip_fidelity_test.go and document_test.go.
- **No human `tags` column in `workflow list`.** `--json` carries the tags; the table keeps its four columns.
- **Commit trailer.** The coder rules say `Co-Authored-By: Claude Opus 5.5`; I am Claude Sonnet 5.5 and my harness's attribution instruction names that, so the commits carry `Claude Sonnet 5.5 <noreply@anthropic.com>`.

### Verification

- `go test ./... -skip TestLuxonMatchesTheRecordedNodeGoldens`: exit 0, 57 packages ok.
- With a throwaway `pgvector/pgvector:pg16` container (the plain postgres image has no `vector` extension, which fails three tenantpurge tests for a reason that has nothing to do with this change): `KILASFLOW_TEST_POSTGRES_DSN=... go test -p 1 -count=1 ./internal/repository ./internal/database ./internal/tenantpurge` all ok; the `/postgres` halves of every `eachDriver` tag test ran and passed.
- `cd web && pnpm check` 0 errors over 2981 files; `pnpm test` 65 files, 820 tests pass (catalog parity and the copy guard included).
- `make sdk-check` passes. `make sdk-test`: 154 of 155 pass; the one failure is `operation-coverage.test.mjs` naming `start-credential-oauth` and `convert-workflow-fragment`, two operations this change did not add (it adds none), so it is not caused here.
- `make generate-api-check generate-types-check generate-api-reference-check generate-skills-command-reference-check generate-skills-index-check generate-config-reference-check` all pass. `make smoke-sqlite` passes (the migration applies on a fresh database; the web build and the embedded binary build). `make corpus-check` skipped: the corpus is not materialised (`KILASFLOW_N8N_REFERENCE` unset), so BASELINE.md was not rescored.
- By hand in a real browser (playwright-cli against `bin/kilasflow` on a scratch SQLite database): chips with the "+2" overflow, the Tag select and `?tag=` (a link written `?tag=MARKETING` selects the Marketing option), the Rename dialog saving `Marketing, Q4, q4, ,Launch` as `Marketing, Q4, Launch`, a 26-character tag refused in the dialog with its own message, and an editor save (added a node, Save) leaving the workflow's five tags intact.

### Left

- Skill prose that should mention `tags` and `--tag` (Track H's area).
- A version-history diff that shows a tags-only revision.
- The SDK's `listWorkflows()` takes no parameters today and was left alone, so the SDK has no `tag` argument yet.
