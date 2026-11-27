-- Outbound events: the durable outbox behind signed deliveries to a host. The
-- PostgreSQL half of 000034 carries the full rationale; the two dialects share
-- the shape and differ in types.
--
-- Indent with spaces, never tabs: SQLite stores a CREATE TABLE verbatim and
-- glebarez/sqlite's DDL parser counts a tab as a quote character.

CREATE TABLE `outbound_events` (
  `id` text NOT NULL,
  `tenant_id` text NOT NULL,
  `event_type` text NOT NULL,
  `subject_id` text NOT NULL DEFAULT "",
  `endpoint` text NOT NULL,
  `payload` blob NOT NULL,
  `status` text NOT NULL,
  `attempts` integer NOT NULL DEFAULT 0,
  `next_attempt_at` datetime,
  `lease_owner` text NOT NULL DEFAULT "",
  `lease_until` datetime,
  `last_status_code` integer NOT NULL DEFAULT 0,
  `last_error` text NOT NULL DEFAULT "",
  `delivered_at` datetime,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`)
);

CREATE INDEX `idx_outbound_events_due` ON `outbound_events`(`status`,`next_attempt_at`);

CREATE INDEX `idx_outbound_events_tenant` ON `outbound_events`(`tenant_id`,`created_at`);

CREATE INDEX `idx_outbound_events_created` ON `outbound_events`(`created_at`);
