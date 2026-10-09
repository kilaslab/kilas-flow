-- Reverse of 000025_workflow_tags.up.sql. The tags stay in each revision's
-- document; only the index the list reads is dropped.

DROP INDEX IF EXISTS "idx_workflow_tags_key";
DROP TABLE IF EXISTS "workflow_tags";
