-- The per-tenant rate limit buckets: one row per (tenant, bucket), spent
-- inside a row lock so every process of a deployment draws from one
-- allowance instead of granting the full rate each.
--
-- The primary key is the pair, so the rows are bounded by the bucket
-- vocabulary times the tenant count and the table needs no sweeper: an idle
-- bucket refills in the query that reads it and costs nothing while nobody
-- asks. tokens is double precision rather than a float column trick: SQLite
-- keeps the value as-is and PostgreSQL's float8 matches, so the arithmetic a
-- spend does reads identically on both.
--
-- Table and index names are quoted so database.table_prefix rewrites them.

CREATE TABLE "rate_limit_buckets" (
  "tenant_id" varchar(64) NOT NULL,
  "bucket" varchar(32) NOT NULL,
  "tokens" double precision NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("tenant_id", "bucket")
);
