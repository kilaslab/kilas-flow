-- Outbound events: the durable outbox behind signed deliveries to a host.
--
-- A row is written in the same transaction as the state change it reports (an
-- execution reaching succeeded or failed, a workflow being published or
-- unpublished), so a committed change always has its event and a rolled-back
-- one never does. The in-process broker in internal/events cannot promise
-- either: it lives in one process and drops what it holds when that process
-- dies. A dispatcher claims due rows under a lease, the way poll cursors are
-- claimed, delivers each one at least once and records the outcome on the row.
--
-- One row is one event bound for one endpoint, and it doubles as the tenant's
-- delivery log: attempts, the last response status and error, and when the
-- next attempt is due. There is no per-attempt table on purpose; the row says
-- what a host needs to diagnose a failing endpoint, and a log that grows by a
-- row per retry would need its own retention for little more.
--
-- id is the stable event id. It is sent with every attempt (webhook-id), so a
-- receiver deduplicates the redeliveries at-least-once implies.
--
-- endpoint is the destination as it was when the event was written. Changing
-- a tenant's endpoint does not re-route events already queued for the old one.
--
-- payload is the request body, fixed at write time: every attempt sends the
-- same bytes, which is what makes the event id meaningful to a receiver.
--
-- status is pending, delivered or dead. A dead row exhausted its attempts and
-- is kept for the log until retention removes it. next_attempt_at is NULL
-- once a row is no longer pending.
--
-- lease_owner and lease_until fence one delivery attempt to one dispatcher.
-- An expired lease is claimable again, which is the crash recovery: a worker
-- that died mid-delivery leaves a row another one picks up.
--
-- No foreign keys: tenant purge deletes the rows by tenant_id, and retention
-- deletes them by created_at.

CREATE TABLE "outbound_events" (
  "id" varchar(64) NOT NULL,
  "tenant_id" varchar(64) NOT NULL,
  "event_type" varchar(64) NOT NULL,
  "subject_id" varchar(64) NOT NULL DEFAULT '',
  "endpoint" text NOT NULL,
  "payload" bytea NOT NULL,
  "status" varchar(16) NOT NULL,
  "attempts" bigint NOT NULL DEFAULT 0,
  "next_attempt_at" timestamptz,
  "lease_owner" varchar(128) NOT NULL DEFAULT '',
  "lease_until" timestamptz,
  "last_status_code" bigint NOT NULL DEFAULT 0,
  "last_error" text NOT NULL DEFAULT '',
  "delivered_at" timestamptz,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);

CREATE INDEX "idx_outbound_events_due" ON "outbound_events" ("status","next_attempt_at");

CREATE INDEX "idx_outbound_events_tenant" ON "outbound_events" ("tenant_id","created_at");

CREATE INDEX "idx_outbound_events_created" ON "outbound_events" ("created_at");
