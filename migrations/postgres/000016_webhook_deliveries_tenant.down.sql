-- Reverse of webhook_deliveries_tenant.up.sql.
--
-- The index is dropped explicitly, ahead of the column, so the two dialects run
-- the same steps in the same order (SQLite refuses to drop a column an index
-- still names). Dropping the column takes every recorded tenant with it, which
-- costs nothing a delivery row needs: the dedupe key never included it.
--
-- The delivery rows the up migration deleted because no route, binding or
-- execution could name their tenant are not restored. They were five-minute
-- dedupe tokens for routes that no longer exist.

DROP INDEX IF EXISTS "idx_webhook_deliveries_tenant";

ALTER TABLE "webhook_deliveries" DROP COLUMN "tenant_id";
