-- A webhook path is a label, not an address, so it must not be unique.
--
-- The public URL carries the opaque route, and the route is what an inbound
-- request resolves on: uidx_webhook_bindings_route is the identity, spanning
-- (method, route) globally. The path column is only what the workflow's author
-- called the endpoint. The per-tenant unique index on (tenant_id, path)
-- therefore refused layouts n8n accepts — one document binding GET and POST on
-- the same path is two bindings, and importing the same template twice is two
-- workflows with two different routes — and activation failed with an opaque
-- 500 instead of a message (BUG-cq4yk3).
--
-- The replacement is deliberately non-unique. Tenant listing and activation
-- diagnostics still read bindings by tenant and path, which is the lookup the
-- old index also served; duplicates are now the point rather than the error.
--
-- IF EXISTS on the drop because a database whose schema came from the baseline
-- replayed under a different index name must not fail here, and IF NOT EXISTS
-- on the create for the same reason in reverse.

DROP INDEX IF EXISTS "uidx_webhook_bindings_label";

CREATE INDEX IF NOT EXISTS "idx_webhook_bindings_path" ON "webhook_bindings" ("tenant_id","path");
