-- Reverse of api_key_scopes.up.sql.
--
-- The three columns go together: a key that kept its binding but lost its
-- scopes would be a tenant-wide key narrowed to one workflow, which is not a
-- state any caller asked for. Revoked and expired keys are unaffected — they
-- are rows, not columns.

ALTER TABLE `api_keys` DROP COLUMN `expires_at`;
ALTER TABLE `api_keys` DROP COLUMN `workflow_id`;
ALTER TABLE `api_keys` DROP COLUMN `scopes`;
