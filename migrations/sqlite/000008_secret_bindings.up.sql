-- Tenant-scoped external secret manager bindings: which manager a stored
-- ext://<binding>/<key> reference resolves against.
--
-- The row names the environment variable holding the manager credential,
-- never the credential itself, so a dump, a replica, or a support export
-- discloses which manager to ask but nothing that answers. The unique index
-- spans tenant and name: a reference authored under one tenant resolves only
-- against that tenant's bindings, and a name that exists nowhere under the
-- caller resolves to nothing rather than to another tenant's row.
--
-- CREATE TABLE is written without IF NOT EXISTS on purpose, for the same
-- reason as 000003: failing loudly on a collision is the only safe outcome
-- in a database KilasFlow may share with another application.

CREATE TABLE `secret_bindings` (
  `id` text,
  `tenant_id` text NOT NULL,
  `name` text NOT NULL,
  `provider` text NOT NULL,
  `address` text NOT NULL,
  `token_env` text NOT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`)
);

CREATE UNIQUE INDEX `uidx_secret_bindings_tenant_name` ON `secret_bindings`(`tenant_id`,`name`);

CREATE INDEX `idx_secret_bindings_tenant` ON `secret_bindings`(`tenant_id`);
