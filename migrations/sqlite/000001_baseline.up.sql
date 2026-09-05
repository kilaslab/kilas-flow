-- Baseline: the schema KilasFlow shipped before migrations existed.
--
-- Every identifier here was captured from the DDL GORM's AutoMigrate issued for
-- repository.Models(), not written by hand. An index or constraint spelled any
-- other way would still look correct and still pass its own tests, and would
-- then make a later migration unable to find the object it has to rename or
-- drop. TestBaselineLeavesAutoMigrateNothingToDo keeps the two in step.

CREATE TABLE `workflows` (
  `id` text,
  `tenant_id` text NOT NULL,
  `name` text NOT NULL,
  `active` numeric NOT NULL DEFAULT false,
  `latest_revision` integer NOT NULL,
  `active_version_id` text,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  `deleted_at` datetime,
  PRIMARY KEY (`id`)
);

CREATE INDEX `idx_workflows_deleted_at` ON `workflows`(`deleted_at`);

CREATE INDEX `idx_workflows_tenant_updated` ON `workflows`(`tenant_id`,`updated_at`);

CREATE TABLE `workflow_versions` (
  `id` text,
  `tenant_id` text NOT NULL,
  `workflow_id` text NOT NULL,
  `revision` integer NOT NULL,
  `schema_version` integer NOT NULL,
  `definition` blob NOT NULL,
  `created_at` datetime NOT NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `fk_workflow_versions_workflow` FOREIGN KEY (`workflow_id`) REFERENCES `workflows`(`id`) ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE UNIQUE INDEX `uidx_workflow_versions_revision` ON `workflow_versions`(`tenant_id`,`workflow_id`,`revision`);

CREATE INDEX `idx_workflow_versions_tenant_workflow` ON `workflow_versions`(`tenant_id`,`workflow_id`);

CREATE TABLE `executions` (
  `id` text,
  `tenant_id` text NOT NULL,
  `workflow_id` text NOT NULL,
  `workflow_version_id` text NOT NULL,
  `status` text NOT NULL,
  `trigger` text NOT NULL,
  `trigger_node_id` text,
  `parent_execution_id` text DEFAULT "",
  `input` blob NOT NULL,
  `output` blob NOT NULL,
  `error` blob NOT NULL,
  `started_at` datetime NOT NULL,
  `finished_at` datetime,
  `lease_owner` text,
  `lease_expires_at` datetime,
  `cancellation_requested_at` datetime,
  PRIMARY KEY (`id`),
  CONSTRAINT `fk_executions_workflow` FOREIGN KEY (`workflow_id`) REFERENCES `workflows`(`id`) ON DELETE RESTRICT ON UPDATE CASCADE,
  CONSTRAINT `fk_executions_workflow_version` FOREIGN KEY (`workflow_version_id`) REFERENCES `workflow_versions`(`id`) ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE INDEX `idx_executions_cancellation_requested_at` ON `executions`(`cancellation_requested_at`);

CREATE INDEX `idx_executions_lease_expires_at` ON `executions`(`lease_expires_at`);

CREATE INDEX `idx_executions_lease_owner` ON `executions`(`lease_owner`);

CREATE INDEX `idx_executions_parent_execution_id` ON `executions`(`parent_execution_id`);

CREATE INDEX `idx_executions_workflow_version_id` ON `executions`(`workflow_version_id`);

CREATE INDEX `idx_executions_tenant_workflow` ON `executions`(`tenant_id`,`workflow_id`);

CREATE INDEX `idx_executions_tenant_started` ON `executions`(`tenant_id`,`started_at`);

CREATE TABLE `execution_node_runs` (
  `id` text,
  `tenant_id` text NOT NULL,
  `execution_id` text NOT NULL,
  `node_id` text NOT NULL,
  `run_index` integer NOT NULL DEFAULT 0,
  `attempt` integer NOT NULL,
  `sequence` integer NOT NULL,
  `status` text NOT NULL,
  `input` blob NOT NULL,
  `output` blob NOT NULL,
  `error` blob NOT NULL,
  `started_at` datetime NOT NULL,
  `finished_at` datetime,
  PRIMARY KEY (`id`),
  CONSTRAINT `fk_execution_node_runs_execution` FOREIGN KEY (`execution_id`) REFERENCES `executions`(`id`) ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE UNIQUE INDEX `uidx_node_runs_sequence` ON `execution_node_runs`(`execution_id`,`sequence`);

CREATE UNIQUE INDEX `uidx_node_runs_attempt` ON `execution_node_runs`(`execution_id`,`node_id`,`attempt`,`run_index`);

CREATE INDEX `idx_node_runs_tenant_execution` ON `execution_node_runs`(`tenant_id`,`execution_id`);

CREATE TABLE `credentials` (
  `id` text,
  `tenant_id` text NOT NULL,
  `name` text NOT NULL,
  `type` text NOT NULL,
  `payload` blob NOT NULL,
  `public_fields` blob NOT NULL,
  `allowed_domains` blob NOT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`)
);

CREATE INDEX `idx_credentials_tenant_name` ON `credentials`(`tenant_id`,`name`);

CREATE TABLE `webhook_bindings` (
  `id` integer PRIMARY KEY AUTOINCREMENT,
  `tenant_id` text NOT NULL,
  `workflow_id` text NOT NULL,
  `workflow_version_id` text NOT NULL,
  `node_id` text NOT NULL,
  `node_type` text NOT NULL DEFAULT "",
  `method` text NOT NULL,
  `route` text NOT NULL,
  `path` text NOT NULL,
  `parameters` blob NOT NULL,
  `created_at` datetime NOT NULL
);

CREATE UNIQUE INDEX `uidx_webhook_bindings_route` ON `webhook_bindings`(`method`,`route`);

CREATE UNIQUE INDEX `uidx_webhook_bindings_label` ON `webhook_bindings`(`tenant_id`,`path`);

CREATE INDEX `idx_webhook_bindings_workflow` ON `webhook_bindings`(`tenant_id`,`workflow_id`);

CREATE TABLE `webhook_routes` (
  `id` integer PRIMARY KEY AUTOINCREMENT,
  `tenant_id` text NOT NULL,
  `workflow_id` text NOT NULL,
  `node_id` text NOT NULL,
  `route` text NOT NULL,
  `created_at` datetime
);

-- Named idx_, not uidx_, because the model spells this one as a bare
-- `uniqueIndex` tag and GORM's naming strategy prefixes every generated index
-- name with idx_ regardless of uniqueness.
CREATE UNIQUE INDEX `idx_webhook_routes_route` ON `webhook_routes`(`route`);

CREATE UNIQUE INDEX `uidx_webhook_routes_node` ON `webhook_routes`(`tenant_id`,`workflow_id`,`node_id`);

CREATE TABLE `webhook_deliveries` (
  `id` integer PRIMARY KEY AUTOINCREMENT,
  `route` text NOT NULL,
  `delivery_id` text NOT NULL,
  `execution_id` text NOT NULL,
  `expires_at` datetime NOT NULL,
  `created_at` datetime NOT NULL
);

CREATE INDEX `idx_webhook_deliveries_expires_at` ON `webhook_deliveries`(`expires_at`);

CREATE UNIQUE INDEX `uidx_webhook_deliveries` ON `webhook_deliveries`(`route`,`delivery_id`);

CREATE TABLE `schedules` (
  `id` text,
  `tenant_id` text NOT NULL,
  `workflow_id` text NOT NULL,
  `node_id` text NOT NULL,
  `interval_index` integer NOT NULL DEFAULT 0,
  `cron` text NOT NULL,
  `timezone` text NOT NULL DEFAULT "",
  `active` numeric NOT NULL DEFAULT false,
  `last_run_at` datetime,
  `next_run_at` datetime,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `fk_schedules_workflow` FOREIGN KEY (`workflow_id`) REFERENCES `workflows`(`id`) ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE INDEX `idx_schedules_next_run` ON `schedules`(`next_run_at`);

CREATE INDEX `idx_schedules_tenant_workflow` ON `schedules`(`tenant_id`,`workflow_id`);
