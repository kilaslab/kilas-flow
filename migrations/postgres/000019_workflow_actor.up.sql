-- Who wrote a revision and who published it.
--
-- An agent's actions have to be attributable after the fact: a revision saved
-- by an agent token, a revision saved by a person, and a publish either of them
-- performed were indistinguishable rows until now. created_by and actor held a
-- label and nothing else, and for every row written before this migration they
-- held nothing at all, because the API had no authenticated actor to record.
--
-- The vocabulary is the authentication path, not a role: 'user' is a signed-in
-- person, 'key' is an API key. actor_key_id is the key's own identifier, so a
-- label reused by two keys still names the one that acted, and actor_meta is
-- what the caller reported about the write — today the skills an agent listed
-- in X-KilasFlow-Skills-Used.
--
-- actor_meta is bytea rather than jsonb, like this schema's other JSON payload
-- columns (workflow_versions.definition and diagnostics, credentials.public_fields):
-- written once by the caller that produced it and read back whole, never
-- queried into, so jsonb would only add parse and validate work to every write.
--
-- The three versions columns are nullable, for the reason 000002 gives for
-- label and created_by: a row written before an actor was recorded has no
-- actor, and NULL is that answer. The publish-event columns mirror that
-- table's own actor column — NOT NULL DEFAULT '' — because an audit row there
-- is always written and a caller that supplied nothing still leaves the trace
-- that something happened.
--
-- The backfill is the label. A non-empty created_by or actor is the only
-- identity the old schema ever recorded, and only one path could have written
-- it: the person a session belongs to. Nothing recorded a key's name, so
-- actor_kind is 'user' for exactly those rows and actor_label repeats the
-- label. A row with no label keeps NULL rather than being called 'user': an
-- absent author is honest, an invented one is worse than an absent one, and
-- actor_key_id is never guessed because no column ever held one.

ALTER TABLE "workflow_versions" ADD COLUMN "actor_kind" varchar(16);

ALTER TABLE "workflow_versions" ADD COLUMN "actor_label" varchar(255);

ALTER TABLE "workflow_versions" ADD COLUMN "actor_key_id" varchar(64);

ALTER TABLE "workflow_versions" ADD COLUMN "actor_meta" bytea;

UPDATE "workflow_versions"
   SET "actor_kind" = 'user', "actor_label" = "created_by"
 WHERE "created_by" IS NOT NULL AND "created_by" <> '';

ALTER TABLE "workflow_publish_events" ADD COLUMN "actor_kind" varchar(16) NOT NULL DEFAULT '';

ALTER TABLE "workflow_publish_events" ADD COLUMN "actor_label" varchar(255) NOT NULL DEFAULT '';

ALTER TABLE "workflow_publish_events" ADD COLUMN "actor_key_id" varchar(64) NOT NULL DEFAULT '';

UPDATE "workflow_publish_events"
   SET "actor_kind" = 'user', "actor_label" = "actor"
 WHERE "actor" <> '';
