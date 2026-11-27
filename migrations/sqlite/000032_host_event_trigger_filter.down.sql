-- Reverse of host_event_trigger_filter.up.sql. Every subscription loses its
-- filter, so every published event reaches every listening workflow again.

ALTER TABLE `host_event_subscriptions` DROP COLUMN `trigger_filter`;
