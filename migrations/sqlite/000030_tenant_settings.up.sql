-- A tenant's settings: the per-tenant values that change at runtime.
--
-- Two columns on tenants rather than a side table. A settings document belongs
-- to exactly one tenant and is read by primary key, so a second table would
-- only add a join and a second row for tenant purge to remember: purge already
-- deletes the tenants row, and the settings go with it.
--
-- settings is the whole document, written whole and read whole, never queried
-- into, so it is a byte column like the other JSON values in this schema
-- rather than jsonb. NULL means the tenant has no settings and every section
-- falls back to the deployment configuration.
--
-- settings_version counts the writes that changed the document. 0 means it
-- has never been written. Each change bumps it by one, and an update only
-- lands when the version it read is still the one stored, so two operators
-- patching the same tenant cannot lose each other's change. Additive with a
-- default, so an existing database opens without a rewrite.

ALTER TABLE `tenants` ADD COLUMN `settings` blob;
ALTER TABLE `tenants` ADD COLUMN `settings_version` integer NOT NULL DEFAULT 0;
