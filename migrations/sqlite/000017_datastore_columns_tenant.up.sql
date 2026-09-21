-- The tenant a datastore column belongs to.
--
-- datastore_columns was safe only by convention: every read was preceded by a
-- tenant-scoped lookup of the datastore, and nothing on the row itself said
-- whose it was. A read that forgot the lookup could cross tenants by datastore
-- id alone, and a tenant purge had to find the columns through a list of
-- datastore ids instead of deleting by the tenant.
--
-- The column repeats the tenant of the datastore the row hangs off, and is
-- backfilled from it.
--
-- The DELETE of rows with no datastore is defence in depth and can only be
-- reached by a database where foreign keys are not enforced: datastore_id is a
-- foreign key to datastores ON DELETE CASCADE, so with them enforced a column
-- with no datastore cannot exist (inserting one fails). A row left with an
-- empty tenant is what a purge could never reach, so it is removed rather than
-- kept.
--
-- The index is (tenant_id, datastore_id) so that the scoped column read and the
-- purge's delete by tenant both use it.
--
-- DEFAULT "" and NOT NULL, and the default is deliberate: SQLite cannot drop a
-- column default without rebuilding the table, and the model's default:'' tag
-- has to match the schema on both dialects or the drift test fails on one of
-- them. The schema therefore does not stop an insert that forgets the tenant;
-- the Go layer does (the engine sets it on every column it writes and filters
-- every read on it), and the tenant purge test asserts the column.

ALTER TABLE `datastore_columns` ADD COLUMN `tenant_id` text NOT NULL DEFAULT "";

UPDATE `datastore_columns` SET `tenant_id` = COALESCE(
  (SELECT `tenant_id` FROM `datastores` WHERE `datastores`.`id` = `datastore_columns`.`datastore_id`),
  ''
);

DELETE FROM `datastore_columns` WHERE `tenant_id` = '';

CREATE INDEX `idx_datastore_columns_tenant` ON `datastore_columns`(`tenant_id`,`datastore_id`);
