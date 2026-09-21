-- The tenant a webhook delivery belongs to.
--
-- A delivery row is a five-minute dedupe token: the sender's own delivery id,
-- remembered against a route so a retry does not run the workflow twice. It
-- carried no tenant, which made it the one tenant-owned table a tenant purge
-- could not reach by any key it holds, and made every read of a claim depend on
-- nobody ever guessing another tenant's route and delivery id.
--
-- The column repeats the tenant of the route the delivery arrived on. It is not
-- part of the dedupe key: the unique index on (route, delivery_id) stays,
-- because a route is globally unique and is what a sender addresses.
--
-- Existing rows are attributed by the first of these that matches:
--
--   1. the execution the delivery queued (execution_id names a row in
--      executions, which has always carried its tenant);
--   2. the route in webhook_routes, where routes are minted;
--   3. the route in webhook_bindings, where an activation puts it;
--   4. for a delivery whose route is a legacy label, a binding with no route of
--      its own whose path equals the label. bindingFromModel gives such a
--      binding its path as its route, so the webhook handler stored the label
--      as the delivery's route, and this branch is the only way to find its
--      owner. 000011 made path non-unique, so several tenants can hold the same
--      label; the lowest binding id wins, which at worst gives one five-minute
--      token to the wrong tenant.
--
-- A row none of them can attribute belongs to a route that no longer exists. It
-- is deleted rather than left with an empty tenant, because an empty tenant is
-- exactly the row a purge could never reach, and losing a five-minute token
-- costs at most one duplicate run of a workflow that can no longer be reached
-- anyway.
--
-- DEFAULT '' and NOT NULL, and the default is deliberate: SQLite cannot drop a
-- column default without rebuilding the table, so its twin of this file keeps
-- one, and the model's default:'' tag has to match the schema on both dialects
-- or the drift test fails on one of them. The schema therefore does not stop an
-- insert that forgets the tenant; the Go layer does (ClaimDelivery refuses an
-- empty tenant), and the tenant purge test asserts the column.

ALTER TABLE "webhook_deliveries" ADD COLUMN "tenant_id" varchar(64) NOT NULL DEFAULT '';

UPDATE "webhook_deliveries" SET "tenant_id" = COALESCE(
  (SELECT "tenant_id" FROM "executions" WHERE "executions"."id" = "webhook_deliveries"."execution_id"),
  (SELECT "tenant_id" FROM "webhook_routes" WHERE "webhook_routes"."route" = "webhook_deliveries"."route"),
  (SELECT "tenant_id" FROM "webhook_bindings" WHERE "webhook_bindings"."route" = "webhook_deliveries"."route" ORDER BY "webhook_bindings"."id" LIMIT 1),
  (SELECT "tenant_id" FROM "webhook_bindings" WHERE "webhook_bindings"."route" = '' AND "webhook_bindings"."path" = "webhook_deliveries"."route" ORDER BY "webhook_bindings"."id" LIMIT 1),
  ''
);

DELETE FROM "webhook_deliveries" WHERE "tenant_id" = '';

CREATE INDEX IF NOT EXISTS "idx_webhook_deliveries_tenant" ON "webhook_deliveries" ("tenant_id");
