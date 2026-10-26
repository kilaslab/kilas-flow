-- Host events: subscriptions that make an active workflow reachable through
-- POST /api/v1/events, and the delivery claims that keep a retried publish
-- from running a workflow twice.
--
-- Subscriptions exist only while the workflow is active, exactly like webhook
-- bindings, so the publish query never has to re-check activation. One row per
-- (node, event type): a trigger listening for three types holds three rows and
-- the fan-out stays a plain equality on event_type. event_path records where
-- the event's type sits in the trigger item for a trigger that routes per
-- event, read at publish time so the item lands on the port the author wired.
--
-- Deliveries are keyed (tenant, event_id, workflow): the dedupe the events API
-- promises is per (eventId, workflow), and the tenant is part of the key so a
-- claim can never be reached -- or purged -- across tenants. Like webhook
-- deliveries the row is an insert-fails-on-conflict claim written before the
-- execution is queued, and it expires after a window rather than living for
-- ever.
--
-- Table and index names are quoted so database.table_prefix rewrites them.

CREATE TABLE "host_event_subscriptions" (
  "id" bigserial,
  "tenant_id" varchar(64) NOT NULL,
  "workflow_id" varchar(64) NOT NULL,
  "workflow_version_id" varchar(64) NOT NULL,
  "node_id" varchar(64) NOT NULL,
  "node_type" varchar(128) NOT NULL DEFAULT '',
  "event_type" varchar(255) NOT NULL,
  "event_path" varchar(128) NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);

CREATE UNIQUE INDEX "uidx_host_event_subscriptions" ON "host_event_subscriptions" ("tenant_id","workflow_id","node_id","event_type");

CREATE INDEX "idx_host_event_subscriptions_event" ON "host_event_subscriptions" ("tenant_id","event_type");

CREATE TABLE "host_event_deliveries" (
  "id" bigserial,
  "tenant_id" varchar(64) NOT NULL,
  "event_id" varchar(128) NOT NULL,
  "workflow_id" varchar(64) NOT NULL,
  "execution_id" varchar(64) NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);

CREATE UNIQUE INDEX "uidx_host_event_deliveries" ON "host_event_deliveries" ("tenant_id","event_id","workflow_id");

CREATE INDEX "idx_host_event_deliveries_tenant" ON "host_event_deliveries" ("tenant_id");

CREATE INDEX "idx_host_event_deliveries_expires" ON "host_event_deliveries" ("expires_at");
