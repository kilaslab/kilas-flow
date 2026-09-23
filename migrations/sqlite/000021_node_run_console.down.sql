-- Reverse of 000021_node_run_console.up.sql.
--
-- Dropping the column takes the stored console output with it. Nothing else
-- holds it: the live event is never persisted, so a rollback costs an
-- execution read later the lines its Code nodes printed, and changes no
-- workflow document or item.

ALTER TABLE `execution_node_runs` DROP COLUMN `console`;
