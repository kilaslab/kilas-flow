-- Reverse of 000002_identity.up.sql.
--
-- Dropped child-first rather than with CASCADE: KilasFlow may share a
-- customer's database, and CASCADE would silently take anything of theirs that
-- happens to reference one of these tables.
--
-- Rolling this back takes every account and every API key with it. The rows in
-- the older tables keep their tenant_id strings and stay readable, because
-- nothing outside these three tables references "tenants".

DROP TABLE "api_keys";

DROP TABLE "users";

DROP TABLE "tenants";
