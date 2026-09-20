-- Reverse of 000013_node_run_response.up.sql.
--
-- Dropping the column takes the stored answers with it. Nothing else holds
-- them: the response was deliberately kept out of the item stream so it would
-- not leak into every downstream node's `$json`, so a rollback costs a split
-- api+worker deployment the correct body of a responseNode webhook — it
-- answers an empty 200 again — and changes no workflow document or item.

ALTER TABLE `execution_node_runs` DROP COLUMN `response`;
