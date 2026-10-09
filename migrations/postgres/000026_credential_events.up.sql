-- The credential audit trail: who created, updated, deleted, tested or
-- connected a credential, and when.
--
-- One row per event, appended and never updated. The credential's name and
-- type are copied onto the row because the credential itself may be gone: a
-- deletion is exactly the event somebody comes looking for afterwards, and a
-- row that could only name a credential id nothing points at any more would
-- not answer the question. There is no foreign key to credentials for the same
-- reason.
--
-- The actor columns use the vocabulary workflow_publish_events uses: 'user'
-- for a signed-in person, 'key' for an API key, and the key's own id so a
-- reused label still names the key that acted. Every descriptive column is
-- NOT NULL DEFAULT '' rather than nullable, as there: an event nobody is named
-- for is still a fact worth recording, and an empty actor says so.
--
-- tenant_id is a real, plain column: the tenant purge finds tenant-scoped
-- tables by that column name. Table and index names are quoted so
-- database.table_prefix rewrites them.

CREATE TABLE "credential_events" (
  "id" bigserial,
  "tenant_id" varchar(64) NOT NULL,
  "credential_id" varchar(64) NOT NULL,
  "credential_name" varchar(255) NOT NULL DEFAULT '',
  "credential_type" varchar(64) NOT NULL DEFAULT '',
  "action" varchar(32) NOT NULL,
  "actor_kind" varchar(16) NOT NULL DEFAULT '',
  "actor_label" varchar(255) NOT NULL DEFAULT '',
  "actor_key_id" varchar(64) NOT NULL DEFAULT '',
  "detail" varchar(255) NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);

CREATE INDEX "idx_credential_events_credential" ON "credential_events" ("tenant_id","credential_id","created_at");
