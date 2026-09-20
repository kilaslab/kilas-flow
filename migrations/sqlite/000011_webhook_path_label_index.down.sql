-- Reverse of 000011_webhook_path_label_index.up.sql.
--
-- This restores the uniqueness the up migration removed, and it will fail on a
-- database that has since stored two bindings on one (tenant_id, path) — the
-- very layouts the up migration exists to allow. That failure is the honest
-- answer for a downgrade: the alternative is to make CREATE UNIQUE INDEX
-- succeed by deleting or rewriting rows, and a downgrade that silently drops
-- somebody's routes is worse than one an operator has to think about.

DROP INDEX IF EXISTS `idx_webhook_bindings_path`;

CREATE UNIQUE INDEX IF NOT EXISTS `uidx_webhook_bindings_label` ON `webhook_bindings`(`tenant_id`,`path`);
