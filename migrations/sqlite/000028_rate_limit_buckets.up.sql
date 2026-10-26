-- The per-tenant rate limit buckets: one row per (tenant, bucket), spent
-- inside a row lock so every process of a deployment draws from one
-- allowance instead of granting the full rate each. The PostgreSQL half of
-- 000028 carries the full rationale; the two dialects share the shape and
-- differ in types.
--
-- Indent with spaces, never tabs: SQLite stores a CREATE TABLE verbatim and
-- glebarez/sqlite's DDL parser counts a tab as a quote character.

CREATE TABLE `rate_limit_buckets` (
  `tenant_id` text NOT NULL,
  `bucket` text NOT NULL,
  `tokens` real NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`tenant_id`, `bucket`)
);
