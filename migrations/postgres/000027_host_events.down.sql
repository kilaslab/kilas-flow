-- Reverse of 000027_host_events.up.sql. The executions a published event
-- started are history and stay; only the routing and dedupe rows go.

DROP TABLE IF EXISTS "host_event_deliveries";

DROP TABLE IF EXISTS "host_event_subscriptions";
