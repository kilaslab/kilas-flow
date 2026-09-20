-- Reverse of the webhook_route_backfill up migration: deliberately nothing.
--
-- The backfill cannot be undone and should not be. Undoing it would mean writing
-- the empty route back onto every row it filled, which is the state the lookup
-- refuses to answer on; and it cannot be told apart from a route the application
-- minted, so an undo would also blank rows that were never empty. An older build
-- reads a routed row exactly as it reads any other, so a downgrade needs nothing
-- from this file.
--
-- It still has to contain a statement: the runner refuses to roll back a migration
-- whose down file is empty, and every migration ships both directions. This one
-- matches no row.

UPDATE `webhook_bindings` SET `route` = `route` WHERE 1 = 0;
