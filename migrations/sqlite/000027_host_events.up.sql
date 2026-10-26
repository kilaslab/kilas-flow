-- Host events: subscriptions that make an active workflow reachable through
-- POST /api/v1/events, and the delivery claims that keep a retried publish
-- from running a workflow twice. The PostgreSQL half of 000027 carries the
-- full rationale; the two dialects share the shape and differ in types.
--
-- Indent with spaces, never tabs: SQLite stores a CREATE TABLE verbatim and
-- glebarez/sqlite's DDL parser counts a tab as a quote character.

CREATE TABLE `host_event_subscriptions` (
  `id` integer PRIMARY KEY AUTOINCREMENT,
  `tenant_id` text NOT NULL,
  `workflow_id` text NOT NULL,
  `workflow_version_id` text NOT NULL,
  `node_id` text NOT NULL,
  `node_type` text NOT NULL DEFAULT "",
  `event_type` text NOT NULL,
  `event_path` text NOT NULL DEFAULT "",
  `created_at` datetime NOT NULL
);

CREATE UNIQUE INDEX `uidx_host_event_subscriptions` ON `host_event_subscriptions`(`tenant_id`,`workflow_id`,`node_id`,`event_type`);

CREATE INDEX `idx_host_event_subscriptions_event` ON `host_event_subscriptions`(`tenant_id`,`event_type`);

CREATE TABLE `host_event_deliveries` (
  `id` integer PRIMARY KEY AUTOINCREMENT,
  `tenant_id` text NOT NULL,
  `event_id` text NOT NULL,
  `workflow_id` text NOT NULL,
  `execution_id` text NOT NULL,
  `expires_at` datetime NOT NULL,
  `created_at` datetime NOT NULL
);

CREATE UNIQUE INDEX `uidx_host_event_deliveries` ON `host_event_deliveries`(`tenant_id`,`event_id`,`workflow_id`);

CREATE INDEX `idx_host_event_deliveries_tenant` ON `host_event_deliveries`(`tenant_id`);

CREATE INDEX `idx_host_event_deliveries_expires` ON `host_event_deliveries`(`expires_at`);
