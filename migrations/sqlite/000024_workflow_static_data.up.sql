-- Workflow static data: the small JSON document a workflow keeps from one run
-- to the next, which n8n's $getWorkflowStaticData reads and writes from a
-- Code node. One row per workflow; the document holds one object per entry,
-- "global" for the workflow and "node:<name>" for each node that keeps its
-- own, as n8n lays it out.
--
-- It is saved when a run that is not a manual one changed it, as n8n saves
-- it: when the run ends, succeeded or failed, and when it parks at a Wait;
-- never for a cancelled run. So the row is written rarely and read once per
-- execution that asks for it. The engine caps the document at 256 KiB.
--
-- tenant_id is a real, plain column: the tenant purge finds tenant-scoped
-- tables by that column name. The row is deleted with its workflow, by the
-- repository rather than by a foreign key, because a workflow is
-- soft-deleted and a foreign key would never fire.
--
-- Table and index names are quoted so database.table_prefix rewrites them.

CREATE TABLE `workflow_static_data` (
  `tenant_id` text NOT NULL,
  `workflow_id` text NOT NULL,
  `data` blob NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`tenant_id`, `workflow_id`)
);
