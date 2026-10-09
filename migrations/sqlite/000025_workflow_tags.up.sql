-- Workflow tags: a derived, per-tenant index of the tags on each workflow's
-- latest revision, so the workflow list can show them and filter by one
-- without loading a single document (BUG-fv5fer is why the list never does).
--
-- The tags themselves are part of the workflow document (its "tags" field), so
-- every revision has its own, like its name. This table is only the index of
-- the newest revision's: every path that appends a revision (a save, an import,
-- a restore) rewrites a workflow's rows here in the same transaction, and a
-- deleted workflow's rows are removed with it. Nothing reads it to learn what a
-- revision held, and rebuilding it means replaying each workflow's latest
-- document.
--
-- ordinal is the tag's place in the authored list, so the list shows tags in
-- the order they were written. tag_key is the tag lowercased: it is the
-- identity (Prod and prod are one tag, which is why it is in the primary key)
-- and what the filter compares. The column is named ordinal, not position,
-- because POSITION is a PostgreSQL keyword.
--
-- tenant_id is a real, plain column: the tenant purge finds tenant-scoped
-- tables by that column name. There is no foreign key to workflows, because a
-- workflow is soft-deleted and a foreign key would never fire; the repository
-- deletes the rows instead.
--
-- Table and index names are quoted so database.table_prefix rewrites them.

CREATE TABLE `workflow_tags` (
    `tenant_id` text NOT NULL,
    `workflow_id` text NOT NULL,
    `ordinal` integer NOT NULL,
    `tag` text NOT NULL,
    `tag_key` text NOT NULL,
    PRIMARY KEY (`tenant_id`, `workflow_id`, `tag_key`)
);

CREATE INDEX `idx_workflow_tags_key` ON `workflow_tags` (`tenant_id`, `tag_key`);
