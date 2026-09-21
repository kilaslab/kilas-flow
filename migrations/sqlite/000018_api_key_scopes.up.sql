-- Scopes, a workflow binding and an expiry on an API key.
--
-- An agent token is an API key with a scope list rather than a new credential
-- kind, so every path that already exists — the kfa1_ format, the prefix
-- lookup, the constant-time compare, revocation — is reused unchanged.
--
-- NULL scopes is the legacy tenant-wide key: every key minted before this
-- migration keeps exactly the authority it had, and a scoped key is one a
-- caller asked for by name. A workflow binding narrows the key to one workflow,
-- the way an embed session is narrowed to one document; expires_at is optional
-- and absent means no expiry, which is today's behaviour.
--
-- Nothing is backfilled: a key with no scopes is the tenant-wide key, so there
-- is nothing to infer and nothing to get wrong.

ALTER TABLE `api_keys` ADD COLUMN `scopes` text;
ALTER TABLE `api_keys` ADD COLUMN `workflow_id` text;
ALTER TABLE `api_keys` ADD COLUMN `expires_at` datetime;
