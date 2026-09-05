-- Baseline: the schema KilasFlow shipped before migrations existed.
--
-- Every identifier here was captured from the DDL GORM's AutoMigrate issued for
-- repository.Models(), not written by hand. An index or constraint spelled any
-- other way would still look correct and still pass its own tests, and would
-- then make a later migration unable to find the object it has to rename or
-- drop. TestBaselineLeavesAutoMigrateNothingToDo keeps the two in step.
--
-- This is the artifact an operator reviews before pointing KilasFlow at a
-- database it shares with something else, which is why it is checked in as
-- reviewable DDL rather than derived from struct tags at boot.

CREATE TABLE "workflows" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "name" varchar(255) NOT NULL,
  "active" boolean NOT NULL DEFAULT false,
  "latest_revision" bigint NOT NULL,
  "active_version_id" varchar(64),
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  "deleted_at" timestamptz,
  PRIMARY KEY ("id")
);

CREATE INDEX IF NOT EXISTS "idx_workflows_deleted_at" ON "workflows" ("deleted_at");

CREATE INDEX IF NOT EXISTS "idx_workflows_tenant_updated" ON "workflows" ("tenant_id","updated_at");

CREATE TABLE "workflow_versions" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "workflow_id" varchar(64) NOT NULL,
  "revision" bigint NOT NULL,
  "schema_version" bigint NOT NULL,
  "definition" bytea NOT NULL,
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_workflow_versions_workflow" FOREIGN KEY ("workflow_id") REFERENCES "workflows"("id") ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_workflow_versions_revision" ON "workflow_versions" ("tenant_id","workflow_id","revision");

CREATE INDEX IF NOT EXISTS "idx_workflow_versions_tenant_workflow" ON "workflow_versions" ("tenant_id","workflow_id");

CREATE TABLE "executions" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "workflow_id" varchar(64) NOT NULL,
  "workflow_version_id" varchar(64) NOT NULL,
  "status" varchar(32) NOT NULL,
  "trigger" varchar(32) NOT NULL,
  "trigger_node_id" varchar(64),
  "parent_execution_id" varchar(64) DEFAULT '',
  "input" bytea NOT NULL,
  "output" bytea NOT NULL,
  "error" bytea NOT NULL,
  "started_at" timestamptz NOT NULL,
  "finished_at" timestamptz,
  "lease_owner" varchar(128),
  "lease_expires_at" timestamptz,
  "cancellation_requested_at" timestamptz,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_executions_workflow" FOREIGN KEY ("workflow_id") REFERENCES "workflows"("id") ON DELETE RESTRICT ON UPDATE CASCADE,
  CONSTRAINT "fk_executions_workflow_version" FOREIGN KEY ("workflow_version_id") REFERENCES "workflow_versions"("id") ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS "idx_executions_cancellation_requested_at" ON "executions" ("cancellation_requested_at");

CREATE INDEX IF NOT EXISTS "idx_executions_lease_expires_at" ON "executions" ("lease_expires_at");

CREATE INDEX IF NOT EXISTS "idx_executions_lease_owner" ON "executions" ("lease_owner");

CREATE INDEX IF NOT EXISTS "idx_executions_parent_execution_id" ON "executions" ("parent_execution_id");

CREATE INDEX IF NOT EXISTS "idx_executions_workflow_version_id" ON "executions" ("workflow_version_id");

CREATE INDEX IF NOT EXISTS "idx_executions_tenant_workflow" ON "executions" ("tenant_id","workflow_id");

CREATE INDEX IF NOT EXISTS "idx_executions_tenant_started" ON "executions" ("tenant_id","started_at");

CREATE TABLE "execution_node_runs" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "execution_id" varchar(64) NOT NULL,
  "node_id" varchar(64) NOT NULL,
  "run_index" bigint NOT NULL DEFAULT 0,
  "attempt" bigint NOT NULL,
  "sequence" bigint NOT NULL,
  "status" varchar(32) NOT NULL,
  "input" bytea NOT NULL,
  "output" bytea NOT NULL,
  "error" bytea NOT NULL,
  "started_at" timestamptz NOT NULL,
  "finished_at" timestamptz,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_execution_node_runs_execution" FOREIGN KEY ("execution_id") REFERENCES "executions"("id") ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_node_runs_sequence" ON "execution_node_runs" ("execution_id","sequence");

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_node_runs_attempt" ON "execution_node_runs" ("execution_id","node_id","attempt","run_index");

CREATE INDEX IF NOT EXISTS "idx_node_runs_tenant_execution" ON "execution_node_runs" ("tenant_id","execution_id");

CREATE TABLE "credentials" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "name" varchar(255) NOT NULL,
  "type" varchar(64) NOT NULL,
  "payload" bytea NOT NULL,
  "public_fields" bytea NOT NULL,
  "allowed_domains" bytea NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);

CREATE INDEX IF NOT EXISTS "idx_credentials_tenant_name" ON "credentials" ("tenant_id","name");

CREATE TABLE "webhook_bindings" (
  "id" bigserial,
  "tenant_id" varchar(64) NOT NULL,
  "workflow_id" varchar(64) NOT NULL,
  "workflow_version_id" varchar(64) NOT NULL,
  "node_id" varchar(64) NOT NULL,
  "node_type" varchar(128) NOT NULL DEFAULT '',
  "method" varchar(8) NOT NULL,
  "route" varchar(64) NOT NULL,
  "path" varchar(255) NOT NULL,
  "parameters" bytea NOT NULL,
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_webhook_bindings_route" ON "webhook_bindings" ("method","route");

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_webhook_bindings_label" ON "webhook_bindings" ("tenant_id","path");

CREATE INDEX IF NOT EXISTS "idx_webhook_bindings_workflow" ON "webhook_bindings" ("tenant_id","workflow_id");

CREATE TABLE "webhook_routes" (
  "id" bigserial,
  "tenant_id" varchar(64) NOT NULL,
  "workflow_id" varchar(64) NOT NULL,
  "node_id" varchar(64) NOT NULL,
  "route" varchar(64) NOT NULL,
  "created_at" timestamptz,
  PRIMARY KEY ("id")
);

-- Named idx_, not uidx_, because the model spells this one as a bare
-- `uniqueIndex` tag and GORM's naming strategy prefixes every generated index
-- name with idx_ regardless of uniqueness.
CREATE UNIQUE INDEX IF NOT EXISTS "idx_webhook_routes_route" ON "webhook_routes" ("route");

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_webhook_routes_node" ON "webhook_routes" ("tenant_id","workflow_id","node_id");

CREATE TABLE "webhook_deliveries" (
  "id" bigserial,
  "route" varchar(64) NOT NULL,
  "delivery_id" varchar(128) NOT NULL,
  "execution_id" varchar(64) NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);

CREATE INDEX IF NOT EXISTS "idx_webhook_deliveries_expires_at" ON "webhook_deliveries" ("expires_at");

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_webhook_deliveries" ON "webhook_deliveries" ("route","delivery_id");

CREATE TABLE "schedules" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "workflow_id" varchar(64) NOT NULL,
  "node_id" varchar(64) NOT NULL,
  "interval_index" bigint NOT NULL DEFAULT 0,
  "cron" varchar(255) NOT NULL,
  "timezone" varchar(64) NOT NULL DEFAULT '',
  "active" boolean NOT NULL DEFAULT false,
  "last_run_at" timestamptz,
  "next_run_at" timestamptz,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_schedules_workflow" FOREIGN KEY ("workflow_id") REFERENCES "workflows"("id") ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS "idx_schedules_next_run" ON "schedules" ("next_run_at");

CREATE INDEX IF NOT EXISTS "idx_schedules_tenant_workflow" ON "schedules" ("tenant_id","workflow_id");
