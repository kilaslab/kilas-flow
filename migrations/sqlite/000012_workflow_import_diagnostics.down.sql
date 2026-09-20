-- Reverse of 000012_workflow_import_diagnostics.up.sql.
--
-- Dropping the column takes every stored import report with it; the documents
-- themselves are untouched, so a workflow imported before this reversal still
-- opens and runs exactly as it did. What is lost is the record of what the
-- import could not carry, which no other table holds — the n8n source file is
-- never kept.

ALTER TABLE `workflow_versions` DROP COLUMN `diagnostics`;
