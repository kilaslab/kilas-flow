-- Leased poll cursors for Gmail/Drive triggers.
--
-- Telegram polling is single-process on purpose. Gmail and Drive cannot share
-- that: two replicas would deliver the same email twice into a RAG ingest.
-- One row per trigger node, claimed with a lease, is what keeps two workers
-- from overlapping a tick. The cursor is the trigger's own scratch (Gmail
-- history id, Drive page token / modifiedTime) and is never interpreted here.
--
-- Table and index names are quoted so database.table_prefix rewrites them.

CREATE TABLE `poll_cursors` (
    `id` TEXT PRIMARY KEY,
    `tenant_id` TEXT NOT NULL,
    `workflow_id` TEXT NOT NULL,
    `node_id` TEXT NOT NULL,
    `node_type` TEXT NOT NULL,
    `interval_ns` INTEGER NOT NULL,
    `cursor` TEXT NOT NULL DEFAULT '',
    `next_poll_at` DATETIME,
    `lease_until` DATETIME,
    `lease_owner` TEXT NOT NULL DEFAULT '',
    `last_poll_at` DATETIME,
    `created_at` DATETIME NOT NULL,
    `updated_at` DATETIME NOT NULL
);

CREATE UNIQUE INDEX `uidx_poll_cursors_node` ON `poll_cursors` (`tenant_id`, `workflow_id`, `node_id`);

CREATE INDEX `idx_poll_cursors_due` ON `poll_cursors` (`next_poll_at`);
