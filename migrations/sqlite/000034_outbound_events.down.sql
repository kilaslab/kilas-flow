-- Reverse of outbound_events.up.sql. Pending deliveries are lost with the
-- table; nothing else references it.

DROP TABLE IF EXISTS `outbound_events`;
