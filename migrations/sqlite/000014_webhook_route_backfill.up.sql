-- Give every webhook binding that has no route the route it should have had.
--
-- A binding with an empty route used to be found by its path label instead: the
-- lookup matched the row when the request named the label. The label is not an
-- identity. Migration 000011 made it non-unique so that two tenants can hold the
-- same one, and a request carries no tenant, so a caller who named a label could
-- run whichever tenant's workflow the database returned first, with that
-- tenant's credentials and datastores. The lookup no longer falls back to the
-- label, so a row with no route would stop answering at all; this backfills the
-- route so that it answers on an address that names exactly one node.
--
-- Rows are only ever written with a route today (activation mints one per trigger
-- node), so this reaches nothing on an install that has only run this build. It
-- is for a database an older build populated.
--
-- Two steps, in this order.
--
-- One records a route in webhook_routes for each node that has an empty-route
-- binding and no route there yet. That table is what activation reads before it
-- mints, keyed by (tenant, workflow, node): a binding given a route that this
-- table did not know about would be given a different one the next time the
-- workflow was deactivated and activated, and the address just handed out would
-- change again. A node that already has a route there (an import mints at import
-- time) keeps it. GROUP BY, not DISTINCT: one node bound on several methods has
-- several bindings and must get one route, and DISTINCT over a random column
-- would give each its own and then fail the unique index on
-- (tenant_id, workflow_id, node_id).
--
-- Two copies each node's route onto its bindings. Rows that already have a route
-- are not touched, and both steps select on route = '', so running this again
-- changes nothing.
--
-- The route is sixteen random bytes as lowercase hex, which is what the
-- application mints, so a backfilled route cannot be told apart from any other.
--
-- Addresses change. A row that answered on /webhook/<label> now answers on
-- /webhook/<route>, which GET /workflows/{id}/webhooks reports. A trigger that
-- registers its own address with the sender at activation (Telegram, WAHA) has to
-- be activated again to register the new one.

INSERT INTO `webhook_routes` (`tenant_id`, `workflow_id`, `node_id`, `route`, `created_at`)
SELECT `b`.`tenant_id`, `b`.`workflow_id`, `b`.`node_id`, lower(hex(randomblob(16))), CURRENT_TIMESTAMP
FROM `webhook_bindings` AS `b`
WHERE `b`.`route` = ''
  AND NOT EXISTS (
    SELECT 1 FROM `webhook_routes` AS `r`
    WHERE `r`.`tenant_id` = `b`.`tenant_id`
      AND `r`.`workflow_id` = `b`.`workflow_id`
      AND `r`.`node_id` = `b`.`node_id`)
GROUP BY `b`.`tenant_id`, `b`.`workflow_id`, `b`.`node_id`;

UPDATE `webhook_bindings`
SET `route` = (
  SELECT `r`.`route` FROM `webhook_routes` AS `r`
  WHERE `r`.`tenant_id` = `webhook_bindings`.`tenant_id`
    AND `r`.`workflow_id` = `webhook_bindings`.`workflow_id`
    AND `r`.`node_id` = `webhook_bindings`.`node_id`)
WHERE `route` = '';
