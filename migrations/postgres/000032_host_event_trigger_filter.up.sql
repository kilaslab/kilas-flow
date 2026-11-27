-- A host-event subscription carries its trigger's filter (the triggerFilter
-- parameter), so the fan-out can drop an event the workflow would discard
-- before an execution is queued for it.
--
-- The filter is copied onto the subscription at activation, inside the same
-- transaction that writes the row, the way webhook bindings carry their
-- trigger's parameters: the publish path then reads one table and never
-- recompiles a document per event. It is written whole and read whole, never
-- queried into, so it is a byte column like the other JSON values in this
-- schema rather than jsonb. NULL means the trigger has no filter and every
-- event of the subscribed type runs, which is what every subscription written
-- before this column did. Additive and nullable, so an existing database opens
-- without a rewrite; a workflow activated before it existed gains its filter
-- the next time it is activated.
--
-- The column is trigger_filter rather than filter, which is a keyword in
-- PostgreSQL's aggregate syntax.

ALTER TABLE "host_event_subscriptions" ADD COLUMN "trigger_filter" bytea;
