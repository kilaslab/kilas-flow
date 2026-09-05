-- Identity: the tenants, dashboard users, and machine API keys that give a
-- request an owner.
--
-- Captured from the DDL GORM's AutoMigrate issued for the three new models in
-- repository.Models(), for the same reason as the baseline: an identifier
-- spelled any other way would still look correct and would then make a later
-- migration unable to find the object it has to alter.
--
-- Indented with spaces. glebarez/sqlite's DDL parser counts a tab as a quote
-- character, so a tab-indented table reads back as having no columns and every
-- boot decides the whole schema is missing.

CREATE TABLE `tenants` (
  `id` text,
  `name` text NOT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`)
);

-- Every tenant ID already written by an installation that predates this table
-- gets a row, so an upgrade leaves existing data reachable rather than orphaned
-- behind a foreign key. 'default' is inserted whether or not it has data,
-- because it is the tenant a fresh install bootstraps into.
INSERT INTO `tenants` (`id`, `name`, `created_at`, `updated_at`)
  VALUES ('default', 'Default', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);

INSERT INTO `tenants` (`id`, `name`, `created_at`, `updated_at`)
  SELECT DISTINCT `tenant_id`, `tenant_id`, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
  FROM `workflows`
  WHERE `tenant_id` NOT IN (SELECT `id` FROM `tenants`);

-- Credentials are covered separately because a tenant can hold credentials
-- without ever having saved a workflow, and executions and schedules can only
-- exist under a workflow's tenant.
INSERT INTO `tenants` (`id`, `name`, `created_at`, `updated_at`)
  SELECT DISTINCT `tenant_id`, `tenant_id`, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
  FROM `credentials`
  WHERE `tenant_id` NOT IN (SELECT `id` FROM `tenants`);

CREATE TABLE `users` (
  `id` text,
  `tenant_id` text NOT NULL,
  `email` text NOT NULL,
  `password_hash` text NOT NULL,
  `name` text NOT NULL DEFAULT "",
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  `disabled_at` datetime,
  PRIMARY KEY (`id`),
  CONSTRAINT `fk_users_tenant` FOREIGN KEY (`tenant_id`) REFERENCES `tenants`(`id`) ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE UNIQUE INDEX `uidx_users_email` ON `users`(`email`);

CREATE INDEX `idx_users_tenant` ON `users`(`tenant_id`);

CREATE TABLE `api_keys` (
  `id` text,
  `tenant_id` text NOT NULL,
  `prefix` text NOT NULL,
  `secret_hash` text NOT NULL,
  `label` text NOT NULL DEFAULT "",
  `created_at` datetime NOT NULL,
  `last_used_at` datetime,
  `revoked_at` datetime,
  PRIMARY KEY (`id`),
  CONSTRAINT `fk_api_keys_tenant` FOREIGN KEY (`tenant_id`) REFERENCES `tenants`(`id`) ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE UNIQUE INDEX `uidx_api_keys_prefix` ON `api_keys`(`prefix`);

CREATE INDEX `idx_api_keys_tenant` ON `api_keys`(`tenant_id`);
