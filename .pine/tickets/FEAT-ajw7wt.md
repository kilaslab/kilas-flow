---
id: FEAT-ajw7wt
title: Store workflow version history and pin the published version
status: todo
priority: medium
labels:
    - versioning
    - history
deps:
    - FEAT-gvn62x
parent: EPIC-m42s3g
phase: p7
created: "2026-09-05T05:01:00Z"
updated: "2026-09-05T05:01:00Z"
---

## Scope

KilasFlow already stores a snapshot per save. `SaveDraft` in `internal/repository/workflows.go` appends a `workflowVersionModel` row on every write, unique on `(tenant_id, workflow_id, revision)`, and `workflows.active_version_id` pins the one revision production traffic runs: `QueueTriggered` in `internal/repository/executions.go` refuses a request whose version is no longer the pinned one, and `internal/repository/schedules.go` reads the same column when it queues a cron run. What is missing is everything that turns those rows into history a person can use.

The gaps are concrete. `WorkflowRepository` exposes `GetVersion` and `GetVersionByID` and no listing at all, so the only version a caller can name is one whose ID it already holds — today that is only the execution detail page, replaying the revision an execution pinned. `Activate` selects `revision = model.LatestRevision` inside its transaction and its own comment states the consequence: "Earlier snapshots can never be reactivated through this API." A bad save therefore cannot be rolled back except by rebuilding the canvas by hand from a copy nobody kept. `Deactivate` flips `active` to false and deliberately leaves `active_version_id` where it was, so the row cannot distinguish a version that is serving now from one that served once. And nothing prunes: the only retention logic in the tree is `internal/ai/memory.go`, which bounds chat memory, so `workflow_versions` grows without limit for the life of the installation.

Two things here are deliberately better than n8n and must stay that way. An n8n workflow version carries only `nodes`, `connections` and `nodeGroups` — `downloadVersion` in the reference checkout's `workflowHistory.store.ts` reconstructs a workflow from exactly those three fields — so restoring an n8n version does not restore workflow settings. A KilasFlow snapshot is the marshalled `workflow.Document`, which includes `Settings`, so a restore here is complete by construction. And n8n gates retention behind its licence (`licensePruneTime` compared against the evaluated `pruneTime`, with an upgrade footer shown when the licence is what is binding); KilasFlow's retention is a configuration knob with "keep everything" available, which matters for a white-label deployment where the operator, not the vendor, decides how much history a customer keeps.

This ticket is the storage and API half: listing, publishing a named version, restoring one, the publish audit trail, and retention. The editor surface is p7-2.

## Acceptance criteria

- [ ] `GET /api/v1/workflows/{id}/versions` returns a tenant-scoped, newest-first page of version summaries — id, revision, created time, optional label, author where one is known — using the same opaque cursor pagination the executions listing already uses, and never carrying the full document.
- [ ] Every summary states its role: draft (the latest revision), published (the pinned `active_version_id`), or neither, so a client never infers publication by comparing identifiers.
- [ ] A version other than the latest can be published. Publishing compiles the chosen snapshot against the live catalogue first, refuses it with the same structured `WorkflowValidationIssue` payload activation returns today, and resyncs webhook bindings in the same transaction, so an active workflow never keeps serving paths belonging to a version it no longer runs.
- [ ] Restoring an old version appends a new revision carrying that snapshot's document. History is append-only: the restored-from version stays in the list unchanged and no row is ever rewritten.
- [ ] Every publish, unpublish and restore appends an audit row naming the workflow, the version, the actor the request context carries, the reason and the time, and a publish period — including the moment deactivation ended it — can be reconstructed from those rows alone.
- [ ] Retention is configurable by age and by count, "keep everything" is available and is what an existing deployment gets on upgrade, and both keys are reachable through YAML and through a `KILASFLOW_*` environment override.
- [ ] Pruning never removes the published version, the latest revision, or any version referenced by a surviving execution or webhook binding: after a prune runs, every retained execution still replays its exact graph through `GET /workflows/{id}/versions/{versionId}`.

## Implementation Plan

Work in this order: schema, repository, API, config, prune.

Schema first, in `internal/repository/models.go`. `workflowVersionModel` gains nullable `Label` and `CreatedBy` columns — nullable because the main API has no authentication yet (every request resolves to tenant `default`; V2-p8-1 is the ticket that changes that), so the author is genuinely unknowable for most writes today and a NOT NULL column would force the code to invent one. Add `workflowPublishEventModel` over table `workflow_publish_events` with tenant, workflow, version, action (`published` | `unpublished` | `restored`), actor, reason and timestamp, indexed on `(tenant_id, workflow_id, created_at)`, and register it in `Models()`. Note that `migrations/` holds nothing but `.gitkeep` and the schema is still created by `db.AutoMigrate(models...)` in `internal/database/database.go`; if V2-p6-1 has landed by the time this is picked up, the same change is a numbered migration instead. Either way new columns must be nullable so an existing SQLite file upgrades in place.

Then `internal/workflow/lifecycle.go`: add `VersionSummary` and `PublishEvent` beside `StoredWorkflow` and `Version`, so the handler layer never learns a GORM shape — that separation is already the rule in this package and the repository is where it is enforced.

Then `internal/repository/workflows.go`. Add `ListVersions`, `PublishVersion`, `RestoreVersion` and `ListPublishEvents` to the `WorkflowRepository` interface. `PublishVersion` is `Activate` generalised: keep the `clause.Locking{Strength: "UPDATE"}` row lock, the `workflow.Compile` gate and the `syncWebhookBindings` call inside one transaction, and take a version ID instead of reading `model.LatestRevision`. Reimplement `Activate` on top of it so there is one publish path rather than two that can drift. `Deactivate` gains an `unpublished` audit row; keep `active_version_id` populated as it is today, because the audit row is now what says whether it is live and the retained pointer is what keeps the last-published document findable. `RestoreVersion` reads the snapshot and writes it through the same append path `SaveDraft` uses, inside one transaction, so a concurrent save cannot interleave and leave a revision whose document nobody asked for.

Then `internal/api/handlers/workflows.go`: register `list-workflow-versions` (`GET /workflows/{id}/versions`), `publish-workflow-version` (`POST /workflows/{id}/versions/{versionId}/publish`) and `restore-workflow-version` (`POST /workflows/{id}/versions/{versionId}/restore`) next to the existing `get-workflow-version`, each with its own response resource beside `WorkflowVersionResource`. Every field added here is an OpenAPI change: run `pnpm generate:api` in `web/` and `pnpm generate:types` in `sdk/`, or `pnpm generate:api:check` and the SDK drift check fail.

Configuration goes in `internal/config/config.go` as a new **single-word** section — `history`, holding `retention` (a `time.Duration`, zero meaning keep everything) and `max_versions` (zero meaning unbounded). The single word is not a style preference: `envKeyToPath` cuts an environment key at its *first* underscore, so a section named `workflow_history` could never be reached by `KILASFLOW_WORKFLOW_HISTORY_RETENTION`. The `OutboundHTTP` doc comment records the same trap for the same reason. Add both keys to `config.example.yaml` and default them to unbounded, so an upgrade never silently deletes a customer's history.

The trap is the prune itself. `executionModel.WorkflowVersion` declares `constraint:OnUpdate:CASCADE,OnDelete:RESTRICT`, so a delete that touches a version any execution pins fails the whole statement — and circumventing the constraint is worse, because `web/src/routes/(dashboard)/executions/[id]/+page.svelte` fetches the pinned version by ID to draw the graph that actually ran, and a missing row turns a finished execution into an error page. `webhook_bindings.workflow_version_id` is a bare string column with no foreign key at all, so nothing protects it either. The prune query must exclude both sets explicitly, plus the published pin and the latest revision, and a test must assert each exclusion separately rather than only asserting a row count.

Where the prune runs is the one design decision left open. Recommendation: enforce the count bound inside `SaveDraft`'s existing transaction — it is exactly the moment a new row appears, the work is bounded to one workflow, and it needs no scheduler — and run the age bound from a low-frequency sweeper started in `cmd/kilasflow/main.go` alongside the scheduler, because an age bound must still fire for workflows nobody is saving. The alternative, a single sweeper doing both, leaves the count bound enforced late so a burst of saves outruns it. If V2-p6-7 ever puts more than one process on the same database, the sweeper needs the advisory-lock treatment the scheduler already has.

## References

- Roadmap plan, p7 section, entry V2-p7-1: `.pine/roadmap.md`.
- PRD `gflow-prd-v1.md`: §4.1 lists workflow versioning among the V1 goals; §64 defers Git-based versioning, which is the decision this DB-stored history implements the alternative to.
- Current storage: `internal/repository/workflows.go` (`SaveDraft`, `Activate`, `Deactivate`, `GetVersion`, `GetVersionByID`), `internal/repository/models.go` (`workflowVersionModel`, `workflowModel.ActiveVersionID`, `executionModel.WorkflowVersion`), `internal/repository/executions.go` (`QueueTriggered`'s pin check), `internal/database/database.go` (`AutoMigrate`), `internal/config/config.go` (`envKeyToPath`).
- n8n 2.34.0 reference checkout, read-only and outside this repository: `packages/frontend/editor-ui/src/features/workflows/workflowHistory/workflowHistory.store.ts` — `downloadVersion` shows a version carries only `nodes`, `connections` and `nodeGroups`, and `licensePruneTime`/`pruneTime` show retention is licence-gated there.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 15 — the version drawer: author and timestamp per version, a version count, a Publish Timeline tab, and n8n's own licence cap stated in-product ("limited to 1 day"). Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
