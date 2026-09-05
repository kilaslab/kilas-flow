-- Workflow version history: who wrote a revision, what it was called, and the
-- publish audit trail.
--
-- Every identifier here was captured from the DDL GORM's AutoMigrate issued for
-- the changed repository.Models(), for the same reason the baseline was: an
-- index or column spelled any other way would still pass its own tests and then
-- make a later migration unable to find the object it has to alter.
--
-- Both new columns are nullable, and that is the point. The main API has no
-- authentication yet, so the author of an existing revision is genuinely
-- unknowable; a NOT NULL column would force a backfill to invent one, and an
-- invented author in an audit trail is worse than an absent one.

ALTER TABLE `workflow_versions` ADD `label` text;

ALTER TABLE `workflow_versions` ADD `created_by` text;

CREATE TABLE `workflow_publish_events` (
  `id` integer PRIMARY KEY AUTOINCREMENT,
  `tenant_id` text NOT NULL,
  `workflow_id` text NOT NULL,
  `version_id` text NOT NULL,
  `action` text NOT NULL,
  `actor` text NOT NULL DEFAULT "",
  `reason` text NOT NULL DEFAULT "",
  `created_at` datetime NOT NULL
);

CREATE INDEX `idx_workflow_publish_events_workflow` ON `workflow_publish_events`(`tenant_id`,`workflow_id`,`created_at`);
