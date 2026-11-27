-- Variables: the tenant's own key/value pairs, read by every workflow as
-- `$vars.KEY` (n8n's Settings -> Variables).
--
-- One row per (tenant, key). The key is the name an expression reads, so it is
-- unique inside a tenant and never across tenants: two tenants may each have a
-- `$vars.API_BASE`. Values are plain text, as in n8n: a variable is
-- configuration a workflow reads, never a secret, which belongs in a
-- credential. They are read whole at the start of each execution, so there is
-- no index beyond the unique one.
--
-- Table and index names are quoted so database.table_prefix rewrites them.

CREATE TABLE "variables" (
  "id" varchar(64) NOT NULL,
  "tenant_id" varchar(64) NOT NULL,
  "key" varchar(64) NOT NULL,
  "value" text NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);

CREATE UNIQUE INDEX "uidx_variables_tenant_key" ON "variables"("tenant_id","key");
