-- Reverse of datastore_columns_tenant.up.sql.
--
-- The index is dropped explicitly, ahead of the column, so the two dialects run
-- the same steps in the same order (SQLite refuses to drop a column an index
-- still names). Dropping the column takes the recorded tenants with it and
-- nothing else: each row still hangs off its datastore, which carries the
-- tenant.
--
-- Any row the up migration deleted for having no datastore is not restored; with
-- foreign keys enforced there were none.

DROP INDEX IF EXISTS "idx_datastore_columns_tenant";

ALTER TABLE "datastore_columns" DROP COLUMN "tenant_id";
