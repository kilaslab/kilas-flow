-- Reverse of workflow_actor.up.sql.
--
-- The columns go together: a row that kept its actor's kind but lost the label
-- it was recorded under names nobody, which is not a state any caller asked
-- for. The legacy label columns are untouched — created_by and actor are where
-- the backfill read from, so a reversal leaves the same evidence the database
-- had before, minus the attribution this migration added.

ALTER TABLE `workflow_versions` DROP COLUMN `actor_meta`;

ALTER TABLE `workflow_versions` DROP COLUMN `actor_key_id`;

ALTER TABLE `workflow_versions` DROP COLUMN `actor_label`;

ALTER TABLE `workflow_versions` DROP COLUMN `actor_kind`;

ALTER TABLE `workflow_publish_events` DROP COLUMN `actor_key_id`;

ALTER TABLE `workflow_publish_events` DROP COLUMN `actor_label`;

ALTER TABLE `workflow_publish_events` DROP COLUMN `actor_kind`;
