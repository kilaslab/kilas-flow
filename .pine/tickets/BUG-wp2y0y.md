---
id: BUG-wp2y0y
title: visibleWhen multi-value AND-ed; 22 properties never appear
status: doing
priority: critical
labels:
    - editor
    - ndv
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T12:40:44Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:ui-ndv.

---
### visibleWhen with several values for one key is AND-ed, so 22 properties (Datastore columns and filters, Postgres/MySQL v2 table, Schedule hour and minute, Date & Time date) never appear [find:ui-ndv] (critical/bug) · area: NDV / property visibility · confidence: high

Node definitions express 'one of these operations' by listing the same key several times in visibleWhen (datastoreShownFor(a,b,c)). The shorthand becomes show-conditions that must all match, both in web/visibility.ts and in internal/property/property.go, so these properties can never be visible. Users cannot map Datastore columns or set its filters, pick a Postgres/MySQL table, set the time of day for a daily Schedule, or choose which date a Date & Time node formats.

Evidence: web/src/lib/workflow-editor/visibility.ts:74-81 and :23-33. internal/property/property.go:341-353. nodes/datastore.go:59-65 and :122-178. A scan of the live /node-types found 22 never-visible properties: datastore and datastoreTool name/columns/match/filters; dateTime date/duration/unit; mysql@2 table/columns/where/combineConditions; postgres@2 schema/table/columns/where/combineConditions; schedule rule.triggerAtHour/triggerAtMinute. Live in '[ui-ndv] visibility probe' (wf5.id): a Schedule stored as days, triggerAtHour 9, triggerAtMinute 30 shows only 'Days Between Triggers' (38-schedule-no-hour.png). Date & Time formatDate has no Date field. Postgres v2 select has no Schema, Table or Where (39-postgres-no-table.png). Datastore Row: Insert/Update shows no columns mapper or filters (25-crop.png, 26-datastore-update-no-fields.png).

n8n behavior: displayOptions.show {operation:['insert','update','upsert']} means any of those values. The Data table NDV shows Mapping Column Mode plus columns after a table is chosen (design-refs 27-30). Schedule shows 'Trigger at Hour' and 'Trigger at Minute' for day, week and month intervals.

Impact: The Datastore (flagship) insert/update/upsert/get/delete, Postgres and MySQL v2 CRUD, and 'every day at 09:00' schedules cannot be configured in the editor. Stored values are invisible.

Suggested fix: Merge same-key visibleWhen entries into one condition with values [a,b,c] (OR within a key, AND across keys, as n8n displayOptions does) in both the Go and TS evaluators and in the shared fixture, or migrate these definitions to displayOptions.show {operation:[…]}. Add a registry test that fails when any property can never be visible.

Files: web/src/lib/workflow-editor/visibility.ts, internal/property/property.go, nodes/datastore.go, nodes/datetime.go

Existing tickets: FEAT-3xqky1, FEAT-n5fdz3, FEAT-12s0e5, FEAT-q81bq4, FEAT-68zzqs

## Acceptance criteria

- [ ] visibleWhen with several values for one key is AND-ed, so 22 properties (Datastore columns and filters, Postgr
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Work (WebFormsOps 2026-09-19)
- status: doing. Owns internal/property/property.go + visibility.ts per Main.
- Fix: visibilityOf (Go) + propertyVisible (TS) merge same-key visibleWhen entries into one OR condition; AND across keys (n8n displayOptions semantics).
- Tests: added TestVisiblePropertyMergesSameKeyShorthand (fails pre-fix, passes post-fix); shared fixture suite still green; vitest visibility/conditions/assignments/key-value 47 passed.
- Remaining: registry never-visible sweep test + commit.
