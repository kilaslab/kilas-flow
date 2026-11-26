-- Reverse of legacy_actor_width.up.sql: deliberately nothing on SQLite.
--
-- The PostgreSQL rollback narrows created_by and actor back to varchar(64)
-- and truncates any longer value first. SQLite's columns are text with no
-- length, so there is nothing to narrow, and truncating here would destroy
-- labels for no schema change at all. An older build reads a long value in a
-- text column exactly as it reads a short one.
--
-- It still has to contain a statement: the runner refuses to roll back a
-- migration whose down file is empty. This one matches no row.

UPDATE `workflow_versions` SET `created_by` = `created_by` WHERE 1 = 0;
