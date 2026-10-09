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
- [ ] `executionOrder: "v1"` produces no import issue; `"v0"` (or anything else) is reported as lossy with a reason saying KilasFlow runs v1 order.
- [ ] Export → import of a workflow reports no settings issue; the skills round-trip test no longer needs its allowance.
- [ ] `TestAnImportedWorkflowKeepsItsTimezone` and the round-trip fidelity tests are updated accordingly.


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
