---
id: BUG-ykxfbe
title: 'PostgreSQL: a key label or email longer than 64 characters makes every save and publish fail (created_by / publish actor are varchar(64))'
status: done
priority: medium
labels:
    - persistence
    - postgres
created: "2026-11-26T07:41:56Z"
updated: "2026-11-26T08:51:49Z"
---

# Description

`workflow_versions.created_by` and `workflow_publish_events.actor` are `varchar(64)` on PostgreSQL (migrations/postgres/000002_workflow_history.up.sql; `size:64` in internal/repository/models.go). `requestActor` (internal/api/handlers/actor.go) writes the raw actor label into them through `appendVersion` / `appendPublishEvent` (internal/repository/workflow_history.go). That label is an API key's label (allowed up to 255 characters by internal/api/handlers/auth.go and admin.go) or a user's email (users.email is size 255).

Failure scenario (PostgreSQL only; SQLite ignores varchar length): a key whose label is longer than 64 characters gets `value too long for type character varying(64)` and a 500 on every save, publish, restore, activate or deactivate it performs. Found by the FEAT-s3sfx5 review (2026-11-26) by reading the code; not reproduced against a live PostgreSQL yet. The embed actor path added by FEAT-s3sfx5 caps display names at 64, so it is not affected.

# Acceptance Criteria
- [x] A regression test on PostgreSQL (eachDriver with KILASFLOW_TEST_POSTGRES_DSN) saves a revision and publishes with a 255-character key label and a 255-character user email.
- [x] Either widen both columns to varchar(255) in a new migration for both dialects (model tags updated so the AutoMigrate drift test stays green), or truncate only the legacy copy rune-safely; the full label stays in `actor_label`.

# Notes

Related: FEAT-s3sfx5 (embed actor identity).

## Fix notes

- Widened rather than truncated. Migration **000031_legacy_actor_width**: PostgreSQL `ALTER TABLE "workflow_versions" ALTER COLUMN "created_by" TYPE varchar(255)` and the same for `"workflow_publish_events"."actor"` (catalogue-only change, no rewrite). The down migration first cuts longer values with `left(col, 64)` and then narrows to varchar(64); its header says the rollback truncates and that `actor_label` (255 since 000029) keeps the whole label. SQLite files are documented no-op statements (`UPDATE ... WHERE 1 = 0`, the 000014 down precedent): the columns are `text` there and never enforce a length.
- Model tags `workflowVersionModel.CreatedBy` and `workflowPublishEventModel.Actor` are now `size:255`, so TestBaselineLeavesAutoMigrateNothingToDoOn{SQLite,Postgres} stay green.
- No other narrow column on this path: executions.actor_label and workflow_versions/workflow_publish_events.actor_label are 255; credential_events.actor_label is 255 and truncates via truncateEventText.
- Regression test `internal/repository/long_actor_test.go` (TestALongActorLabelIsStoredWholeOnEveryDriver, eachDriver): save, activate, deactivate and restore with a 255-character multibyte key label (510 bytes) and a 255-character email; asserts createdBy, actorLabel and every publish event's actor are the whole label. **Fails before the fix on a live PostgreSQL 17** (`SaveDraft() error = create workflow version: ERROR: value too long for type character varying(64) (SQLSTATE 22001)` for both the key and the user), passes after; the SQLite half passed before and after, as expected.
- Migration test `internal/database/legacy_actor_width_migration_test.go`: after Migrate both columns are varchar(255) and hold 255 characters; rollBackBelow("legacy_actor_width") narrows them to varchar(64) and truncates to the first 64 characters (rune-safe, PostgreSQL counts characters); Migrate again restores 255. SQLite: rollback leaves the value whole. The reads cast to text because pgx's cached plan refuses a result type that changed from varchar(255) to varchar(64).
- Verified: `KILASFLOW_TEST_POSTGRES_DSN=... CGO_ENABLED=0 go test -p 1 -count=1 ./internal/database/... ./internal/repository/...` (PostgreSQL 17 container) ok; `CGO_ENABLED=0 go test -count=1 ./internal/repository/... ./internal/database/... ./internal/api/... ./internal/tenantpurge/...` ok; gofmt -l internal clean; go vet ./internal/... clean; make generate-docs-facts-check up to date. CHANGELOG Fixed entry added.

## Work Evidence

Recorded from commits by `pine close --evidence --commit` on 2026-11-26.

- Commits (5):
  - `ad5dc274` — BUG-ykxfbe: correct the migration comments, the test's byte count and the cached-plan learning after review
  - `73c459fb` — merge: widen the legacy actor columns to varchar(255) (BUG-ykxfbe)
  - `ac6e1e2e` — chore(pine): BUG-ykxfbe learning, pgx cached plans refuse a changed column type mid-test
  - `07f1ab2b` — chore(pine): BUG-ykxfbe moves to testing with its fix notes
  - `f8921063` — BUG-ykxfbe: widen created_by and publish actor to varchar(255) so a long key label or email no longer fails every save and publish on PostgreSQL
- Files changed:

```
ad5dc274 BUG-ykxfbe: correct the migration comments, the test's byte count and the cached-plan learning after review

 .pine/memory/persistence.md                            | 2 +-
 internal/repository/long_actor_test.go                 | 2 +-
 migrations/postgres/000031_legacy_actor_width.down.sql | 2 +-
 migrations/postgres/000031_legacy_actor_width.up.sql   | 2 +-
 migrations/sqlite/000031_legacy_actor_width.up.sql     | 4 ++--
 5 files changed, 6 insertions(+), 6 deletions(-)
73c459fb merge: widen the legacy actor columns to varchar(255) (BUG-ykxfbe)

 .pine/memory/persistence.md                        |   3 +-
 .pine/tickets/BUG-ykxfbe.md                        |  17 ++-
 CHANGELOG.md                                       |   7 +
 .../database/legacy_actor_width_migration_test.go  | 160 +++++++++++++++++++++
 internal/repository/long_actor_test.go             | 126 ++++++++++++++++
 internal/repository/models.go                      |  11 +-
 .../postgres/000031_legacy_actor_width.down.sql    |  14 ++
 .../postgres/000031_legacy_actor_width.up.sql      |  18 +++
 .../sqlite/000031_legacy_actor_width.down.sql      |  12 ++
 migrations/sqlite/000031_legacy_actor_width.up.sql |  14 ++
 10 files changed, 373 insertions(+), 9 deletions(-)
ac6e1e2e chore(pine): BUG-ykxfbe learning, pgx cached plans refuse a changed column type mid-test

 .pine/memory/persistence.md | 3 ++-
 1 file changed, 2 insertions(+), 1 deletion(-)
07f1ab2b chore(pine): BUG-ykxfbe moves to testing with its fix notes

 .pine/tickets/BUG-ykxfbe.md | 17 +++++++++++++----
 1 file changed, 13 insertions(+), 4 deletions(-)
f8921063 BUG-ykxfbe: widen created_by and publish actor to varchar(255) so a long key label or email no longer fails every save and publish on PostgreSQL

 CHANGELOG.md                                       |   7 +
 .../database/legacy_actor_width_migration_test.go  | 160 +++++++++++++++++++++
 internal/repository/long_actor_test.go             | 126 ++++++++++++++++
 internal/repository/models.go                      |  11 +-
 .../postgres/000031_legacy_actor_width.down.sql    |  14 ++
 .../postgres/000031_legacy_actor_width.up.sql      |  18 +++
 .../sqlite/000031_legacy_actor_width.down.sql      |  12 ++
 migrations/sqlite/000031_legacy_actor_width.up.sql |  14 ++
 8 files changed, 358 insertions(+), 4 deletions(-)
```
