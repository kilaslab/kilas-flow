-- Reverse of embed_actor.up.sql.
--
-- The kind and label columns on revisions and publish events predate this
-- migration and stay; an 'embed' kind already written there keeps naming the
-- session it came from, minus the host's identifier for its user. Rows
-- written with actor_kind 'embed' therefore remain after a rollback, and a
-- build that predates this migration reads them with a kind it does not list.

ALTER TABLE `executions` DROP COLUMN `actor_id`;

ALTER TABLE `executions` DROP COLUMN `actor_key_id`;

ALTER TABLE `executions` DROP COLUMN `actor_label`;

ALTER TABLE `executions` DROP COLUMN `actor_kind`;

ALTER TABLE `workflow_publish_events` DROP COLUMN `actor_id`;

ALTER TABLE `workflow_versions` DROP COLUMN `actor_id`;
