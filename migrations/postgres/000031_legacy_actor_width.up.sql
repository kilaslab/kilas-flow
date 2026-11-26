-- Widen the legacy actor copies to the width of the label written into them.
--
-- workflow_versions.created_by and workflow_publish_events.actor were created
-- as varchar(64) by 000002, before authentication existed. The value written
-- into them is the actor's label: an API key's label or a signed-in user's
-- email, both of which the API accepts up to 255 characters. A key or a user
-- whose label is longer than 64 characters therefore failed every save,
-- publish, unpublish and restore with "value too long for type character
-- varying(64)" on PostgreSQL (BUG-ykxfbe). SQLite does not enforce varchar
-- lengths and never saw it.
--
-- 255 is the width actor_label already has on both tables (000029), so the
-- legacy copy and the full label now hold exactly the same value. Widening a
-- varchar is a catalogue-only change on PostgreSQL: no row is rewritten and
-- no index is rebuilt, so the lock it takes is brief.

ALTER TABLE "workflow_versions" ALTER COLUMN "created_by" TYPE varchar(255);
ALTER TABLE "workflow_publish_events" ALTER COLUMN "actor" TYPE varchar(255);
