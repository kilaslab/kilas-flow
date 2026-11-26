-- Reverse of legacy_actor_width.up.sql: narrow the legacy actor copies back
-- to varchar(64).
--
-- THIS ROLLBACK TRUNCATES DATA. Narrowing a varchar fails outright while any
-- row holds a longer value, and a rollback that cannot run is no rollback, so
-- every created_by and actor longer than 64 characters is first cut to its
-- first 64 characters. Nothing is lost for good: actor_label (varchar(255)
-- since 000019) still holds the whole label on every row written since then,
-- and the older binary this rollback serves wrote no label longer than 64.

UPDATE "workflow_versions" SET "created_by" = left("created_by", 64) WHERE length("created_by") > 64;
UPDATE "workflow_publish_events" SET "actor" = left("actor", 64) WHERE length("actor") > 64;
ALTER TABLE "workflow_versions" ALTER COLUMN "created_by" TYPE varchar(64);
ALTER TABLE "workflow_publish_events" ALTER COLUMN "actor" TYPE varchar(64);
