-- Reverse of webhook_route_lifecycle_state.up.sql.
--
-- The route stays, and with it the public URL a sender is configured with;
-- only what its registration answered with goes. A trigger that captured
-- values registers again on its next activation and captures them anew.

ALTER TABLE "webhook_routes" DROP COLUMN "lifecycle_state";
