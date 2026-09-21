-- Durable request idempotency: one row per Idempotency-Key a tenant has sent,
-- recording what the first request with that key did so a retry can be answered
-- with the same outcome instead of repeating the side effect.
--
-- A key is CLAIMED by inserting its row before the side effect runs and
-- COMPLETED by recording the outcome afterwards. The unique index on
-- (tenant_id, idempotency_key) is what makes two concurrent requests with one
-- key collapse to one: the loser's insert conflicts and it reads the winner's
-- row instead. Nothing here is held in a process, so a retry may land on any
-- replica and survives a restart.
--
-- expires_at is two things at once. While state is in_progress it is the
-- lease: a claim whose owner crashed stops blocking retries once it passes.
-- Once state is completed it is the retention deadline. Either way a row
-- whose expires_at has passed behaves as if it did not exist, whether or not
-- the sweeper has deleted it yet.
--
-- tenant_id is a real, plain column on purpose. The tenant purge finds
-- tenant-scoped tables by that column name, and the recorded outcome names
-- the tenant's execution ids and datastore rows, so it has to be deleted with
-- them.
--
-- There is deliberately NO foreign key to executions. Keys and executions are
-- retained independently, and a replay may name an execution that
-- execution.retention has since pruned; a key must not block that prune or be
-- deleted by it.
--
-- CREATE TABLE is written without IF NOT EXISTS on purpose, for the same
-- reason as 000003: failing loudly on the collision is the only safe outcome
-- in a database KilasFlow may share with another application.

CREATE TABLE "idempotency_keys" (
  "id" bigserial,
  "tenant_id" varchar(64) NOT NULL,
  "idempotency_key" varchar(255) NOT NULL,
  "operation" varchar(64) NOT NULL,
  "request_hash" varchar(64) NOT NULL,
  "state" varchar(16) NOT NULL,
  "claim_token" varchar(32) NOT NULL,
  "status_code" bigint NOT NULL DEFAULT 0,
  "response" bytea,
  "created_at" timestamptz NOT NULL,
  "expires_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_idempotency_keys" ON "idempotency_keys" ("tenant_id","idempotency_key");

CREATE INDEX IF NOT EXISTS "idx_idempotency_keys_expires_at" ON "idempotency_keys" ("expires_at");
