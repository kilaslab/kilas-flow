-- A host-event subscription carries its trigger's filter, so the fan-out can
-- drop an event the workflow would discard before queueing an execution for
-- it. The PostgreSQL half of 000032 carries the full rationale.

ALTER TABLE `host_event_subscriptions` ADD COLUMN `trigger_filter` blob;
