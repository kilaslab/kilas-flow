---
id: BUG-jx94q9
title: 'n8n export then import is not a fixed point: settings.executionOrder v1 comes back as a dropped issue'
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T12:25:15Z"
updated: "2026-10-09T12:25:15Z"
---

# Description

A KilasFlow → n8n export writes `settings.executionOrder: "v1"` (BUG-6gkd12), but the importer then reports that key back as a **dropped** `settings` issue. So export → import is not a fixed point: a workflow round-tripped through n8n's format gains an import issue it did not have, and the skills' round-trip test has to allow exactly that one entry.

Reported by the Track H2 coder in the 2026-10-09 parallel wave.

# Steps to Reproduce

1. Export any KilasFlow workflow to n8n JSON.
2. Import that JSON.

# Expected

`executionOrder: "v1"` imports silently (it is the order KilasFlow runs in). Only `"v0"` (legacy order), or a value KilasFlow cannot honour, is reported.

# Actual

A `dropped` issue for `settings.executionOrder`.

# Acceptance Criteria
- [x] `executionOrder: "v1"` produces no import issue; `"v0"` (or anything else) is reported as lossy with a reason saying KilasFlow runs v1 order. Evidence: `TestExecutionOrderIsSilentForV1AndLossyForAnythingElse` (v1 silent; v0, "v2", a number, a 300-byte object and a 300-byte string each give exactly one lossy `settings.executionOrder` issue whose reason says KilasFlow always runs the v1 order, is previewed and cut, and no dropped `settings` issue); `TestAnAbsentExecutionOrderStaysSilent` pins the absent key.
- [x] Export → import of a workflow reports no settings issue; the skills round-trip test no longer needs its allowance. Evidence: `TestAnExportedWorkflowDeclaresTheExecutionOrderItRunsIn` imports its own export, asserts no `settings*` issue and identical settings on a second export. On the merged tree the allowance in internal/skills/examples_test.go `TestImportExportExampleRoundTripsThroughN8n` is removed (the review's follow-up patch), so any import issue now fails that test; skills/kilasflow-import-export/references/EXAMPLE.md says the order survives silently.
- [x] `TestAnImportedWorkflowKeepsItsTimezone` and the round-trip fidelity tests are updated accordingly. Evidence: `TestAnImportedWorkflowKeepsItsTimezone` asserts no settings issue for v1 + timezone; `annotatedFixture` gained `saveDataSuccessExecution` so `TestImportReportsEveryDroppedElement` still covers the dropped-settings path.


## Plan (2026-10-09)

Root cause. `exportSettings` (internal/interop/n8n/n8n.go:2410) always writes `executionOrder: "v1"` (BUG-6gkd12), but the importer never learned to read it back: `importSettings` (n8n.go:1225) carries only `timezone`, `executionTimeout` and `errorWorkflow`, and `hasUncarriedSettings` (n8n.go:1281) answers true for any other key, so `documentIssues` (n8n.go:1297) emits a **dropped** `settings` issue whose reason even lists "execution order" as something with no equivalent. Export then import is therefore not a fixed point.

The change, kept to a few lines in n8n.go (other tracks edit that file).
1. `importSettings` reads `executionOrder`: `"v1"` is the order KilasFlow runs in, so it imports silently; `"v0"` or any other value (including a non-string) gets one **lossy** `settings.executionOrder` issue whose reason says KilasFlow always runs v1 order, so branch ordering may differ from what the workflow did in n8n. An absent key stays silent, as today (n8n's own legacy default is not something the file asked for).
2. `hasUncarriedSettings` stops counting `executionOrder` (it now has its own handling), so the dropped `settings` issue is raised only for keys that really are not carried; its reason text loses "execution order" from the list of things with no equivalent.
3. Docs that quote the old behaviour move with it: skills/kilasflow-import-export/references/MAPPING_LIMITS.md (two places) and docs/src/content/docs/guides/n8n-migration.md.

Proof (test first).
- `TestAnImportedWorkflowKeepsItsTimezone` (n8n_test.go): its `hasReason(result.Unsupported, "execution order")` assertion is the bug; it becomes "no issue at all for `settings` when only v1 and the timezone are present".
- `annotatedFixture` / `TestImportReportsEveryDroppedElement` rely on `executionOrder: v1` being the key that makes `settings` dropped; the fixture gets a really uncarried key (a save-data flag) so the dropped-settings path stays covered.
- New: a table test of `executionOrder` values (`v1` silent; `v0`, `v2`, a number: one lossy `settings.executionOrder` issue with the v1 sentence and no `settings` dropped issue).
- `TestAnExportedWorkflowDeclaresTheExecutionOrderItRunsIn` (roundtrip_fidelity_test.go) gains the fixed-point assertion: export a workflow, import the export, and no issue with Field `settings...` comes back; a second export is identical in settings.
- The skills' own round-trip test lives on another track's branch (no such test on main), so there is no allowance to remove here; I will say so in the report.


## Progress (2026-10-09)

What changed (internal/interop/n8n/n8n.go, four small hunks).
- `importSettings` reads `executionOrder` first (before the timezone early-return, so a bad zone does not hide it): `"v1"` is silent; anything else, including a non-string, adds one lossy `settings.executionOrder` issue ("this workflow ran in n8n's v0 execution order; KilasFlow always runs the v1 order, so the order its branches run in may differ") and is not carried. An absent key stays silent, as before.
- `hasUncarriedSettings` no longer counts `executionOrder`, so the dropped `settings` issue appears only for keys really not carried (the save-data flags and so on); its reason text no longer lists "execution order" as unsupported.
- Docs that quoted the old behaviour: skills/kilasflow-import-export/references/MAPPING_LIMITS.md (table row, a short paragraph, the limits bullet) and docs/src/content/docs/guides/n8n-migration.md (importer table row and limits bullet). `make generate-skills-index-check` is still clean.

Deviations and notes.
- The "skills round-trip test with its one-entry allowance" is not on main; whoever merges the track that owns it has to delete the allowance. Nothing on main depended on it.
- The checked-in benchmark result files under e2e/benchmark/ still quote the old reason text; they are dated measurement records, so I left them.
- docs/.../n8n-migration.md around the export limits still says the exported document carries an empty `settings` object, which stopped being true with BUG-6gkd12 (the export writes the timezone, timeout, error workflow and executionOrder). That is export-side docs drift outside this ticket; not touched.

Left: nothing.

## Review round (2026-10-09)

Merged main into the branch; the skills conflicts went to main's side and the reviewer's follow-up patch removed the `settings` allowance from the skills round-trip test and rewrote import-export references/EXAMPLE.md, so the Progress note above that says the allowance is not on main is superseded: it was on main after the merge, and is gone now (`go test ./internal/skills/...` passes).

Review fixes.
- A non-string or very long `executionOrder` was formatted with `%v` of the whole value into the reason. It is now JSON-encoded and cut with the importer's existing `preview` helper (60 bytes), so a hostile file cannot make the message enormous.
- An absent `executionOrder` (n8n's legacy v0 default) stays silent; MAPPING_LIMITS.md and guides/n8n-migration.md now say so.
- CHANGELOG.md [Unreleased] has the Fixed entry.
