-- Durable execution waits: one row per suspension, holding the checkpoint a
-- resumed run continues from.
--
-- The checkpoint column carries the exact upstream data the run held at
-- suspension, stored verbatim. It is deliberately outside the executions
-- write path that redacts by construction: redaction here would hand the
-- resumed run "[redacted]" where its data was. Lookup is by SHA-256 of the
-- resume token, never the token itself, so a dump, a replica, or a support
-- export discloses no resume capability.
--
-- CREATE TABLE is written without IF NOT EXISTS on purpose, for the same
-- reason as 000003: failing loudly on the collision is the only safe outcome
-- in a database KilasFlow may share with another application.

CREATE TABLE "execution_waits" (
  "id" bigserial,
  "tenant_id" varchar(64) NOT NULL,
  "execution_id" varchar(64) NOT NULL,
  "workflow_id" varchar(64) NOT NULL,
  "node_id" varchar(64) NOT NULL,
  "mode" varchar(32) NOT NULL,
  "token_hash" varchar(64) NOT NULL,
  "resume_token" varchar(64) NOT NULL,
  "checkpoint" bytea NOT NULL,
  "resume_output" bytea,
  "run_count" bigint NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "consumed_at" timestamptz,
  "outcome" varchar(32) NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_execution_waits_execution" FOREIGN KEY ("execution_id") REFERENCES "executions"("id") ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_execution_waits_token" ON "execution_waits" ("token_hash");

CREATE INDEX IF NOT EXISTS "idx_execution_waits_execution" ON "execution_waits" ("tenant_id","execution_id");

CREATE INDEX IF NOT EXISTS "idx_execution_waits_expiry" ON "execution_waits" ("expires_at");
