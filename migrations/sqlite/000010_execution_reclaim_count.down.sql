-- Reverse of 000010_execution_reclaim_count.up.sql.
--
-- The column carries no state anything else depends on: it is reclaim
-- bookkeeping for the queue, and a claim that finds it missing is a claim this
-- binary is not running. Dropping it takes the count with it.

ALTER TABLE `executions` DROP COLUMN `reclaim_count`;
