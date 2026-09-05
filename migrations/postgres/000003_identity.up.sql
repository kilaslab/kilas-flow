-- Identity: the tenants, dashboard users, and machine API keys that give a
-- request an owner.
--
-- Captured from the DDL GORM's AutoMigrate issued for the three new models in
-- repository.Models(), for the same reason as the baseline: an identifier
-- spelled any other way would still look correct and would then make a later
-- migration unable to find the object it has to alter.
--
-- CREATE TABLE is written without IF NOT EXISTS on purpose. KilasFlow may share
-- an operator's database, and "users" is a name their own application is likely
-- to have taken; failing loudly on the collision is the only safe outcome,
-- because silently adopting somebody else's users table would then store
-- password hashes into it.

CREATE TABLE "tenants" (
  "id" varchar(64),
  "name" varchar(255) NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);

-- Every tenant ID already written by an installation that predates this table
-- gets a row, so an upgrade leaves existing data reachable rather than orphaned
-- behind a foreign key. 'default' is inserted whether or not it has data,
-- because it is the tenant a fresh install bootstraps into.
INSERT INTO "tenants" ("id", "name", "created_at", "updated_at")
  VALUES ('default', 'Default', now(), now());

INSERT INTO "tenants" ("id", "name", "created_at", "updated_at")
  SELECT DISTINCT "tenant_id", "tenant_id", now(), now()
  FROM "workflows"
  WHERE "tenant_id" NOT IN (SELECT "id" FROM "tenants");

-- Credentials are covered separately because a tenant can hold credentials
-- without ever having saved a workflow, and executions and schedules can only
-- exist under a workflow's tenant.
INSERT INTO "tenants" ("id", "name", "created_at", "updated_at")
  SELECT DISTINCT "tenant_id", "tenant_id", now(), now()
  FROM "credentials"
  WHERE "tenant_id" NOT IN (SELECT "id" FROM "tenants");

CREATE TABLE "users" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "email" varchar(255) NOT NULL,
  "password_hash" varchar(255) NOT NULL,
  "name" varchar(255) NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "disabled_at" timestamptz,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_users_tenant" FOREIGN KEY ("tenant_id") REFERENCES "tenants"("id") ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_users_email" ON "users" ("email");

CREATE INDEX IF NOT EXISTS "idx_users_tenant" ON "users" ("tenant_id");

CREATE TABLE "api_keys" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "prefix" varchar(32) NOT NULL,
  "secret_hash" varchar(64) NOT NULL,
  "label" varchar(255) NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL,
  "last_used_at" timestamptz,
  "revoked_at" timestamptz,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_api_keys_tenant" FOREIGN KEY ("tenant_id") REFERENCES "tenants"("id") ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_api_keys_prefix" ON "api_keys" ("prefix");

CREATE INDEX IF NOT EXISTS "idx_api_keys_tenant" ON "api_keys" ("tenant_id");
