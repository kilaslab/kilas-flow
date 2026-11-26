-- Widen the legacy actor copies: deliberately nothing to do on SQLite.
--
-- On PostgreSQL this migration widens workflow_versions.created_by and
-- workflow_publish_events.actor from varchar(64) to varchar(255), because a
-- key label or email longer than 64 characters made every save and publish
-- fail there (BUG-ykxfbe). SQLite created both columns as text (000002) and
-- never enforces a length, so the columns already hold a label of any width.
--
-- The file still has to exist and contain a statement: every migration version
-- ships for both dialects, so the same binary is at the same schema version
-- whichever database it opened, and the runner refuses an empty file. This one
-- matches no row.

UPDATE `workflow_versions` SET `created_by` = `created_by` WHERE 1 = 0;
