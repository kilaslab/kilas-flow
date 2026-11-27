-- Variables: the tenant's own key/value pairs, read by every workflow as
-- `$vars.KEY`. The PostgreSQL half of 000033 carries the full rationale; the
-- two dialects share the shape and differ in types.
--
-- Indent with spaces, never tabs: SQLite stores a CREATE TABLE verbatim and
-- glebarez/sqlite's DDL parser counts a tab as a quote character.

CREATE TABLE `variables` (
  `id` text NOT NULL,
  `tenant_id` text NOT NULL,
  `key` text NOT NULL,
  `value` text NOT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`)
);

CREATE UNIQUE INDEX `uidx_variables_tenant_key` ON `variables`(`tenant_id`,`key`);
