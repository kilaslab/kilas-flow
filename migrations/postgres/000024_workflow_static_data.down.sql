-- Reverse of 000024_workflow_static_data.up.sql. Every workflow's static data
-- goes with it; a workflow that reads it again starts from nothing.

DROP TABLE IF EXISTS "workflow_static_data";
