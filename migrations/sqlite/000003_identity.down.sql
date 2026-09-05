-- Reverse of 000002_identity.up.sql.
--
-- Dropped child-first so the drop succeeds with foreign keys enforced, which
-- the SQLite DSN turns on with PRAGMA foreign_keys(1).
--
-- Rolling this back takes every account and every API key with it. The rows in
-- the older tables keep their tenant_id strings and stay readable, because
-- nothing outside these three tables references `tenants`.

DROP TABLE `api_keys`;

DROP TABLE `users`;

DROP TABLE `tenants`;
