-- Reverse of datastore_unique_names.up.sql.
--
-- Only the index goes; the renames are not reverted. Putting the old names back
-- would restore the very collisions the up migration removed, and nothing is
-- gained by it: the build being rolled back to resolves a unique name exactly
-- as it resolved a shared one, only without the guess.

DROP INDEX IF EXISTS `uidx_datastores_tenant_lower_name`;
