-- Reverse of tenant_settings.up.sql. Every tenant loses its settings and falls
-- back to the deployment configuration; the tenant rows themselves stay.

ALTER TABLE `tenants` DROP COLUMN `settings_version`;
ALTER TABLE `tenants` DROP COLUMN `settings`;
