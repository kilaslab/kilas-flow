-- Reverse of 000002_workflow_history.up.sql.
--
-- The index goes with its table, so only the table is named. The two columns
-- are dropped last so the audit table is gone before workflow_versions changes
-- shape.

DROP TABLE `workflow_publish_events`;

ALTER TABLE `workflow_versions` DROP COLUMN `created_by`;

ALTER TABLE `workflow_versions` DROP COLUMN `label`;
