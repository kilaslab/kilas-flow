-- Leased poll cursors for Gmail/Drive triggers.
--
-- Telegram polling is single-process on purpose. Gmail and Drive cannot share
-- that: two replicas would deliver the same email twice into a RAG ingest.
-- One row per trigger node, claimed with a lease, is what keeps two workers
-- from overlapping a tick.
--
-- Table and index names are quoted so database.table_prefix rewrites them.

CREATE TABLE "poll_cursors" (
    "id" VARCHAR(64) PRIMARY KEY,
    "tenant_id" VARCHAR(64) NOT NULL,
    "workflow_id" VARCHAR(64) NOT NULL,
    "node_id" VARCHAR(64) NOT NULL,
    "node_type" VARCHAR(128) NOT NULL,
    "interval_ns" BIGINT NOT NULL,
    "cursor" TEXT NOT NULL DEFAULT '',
    "next_poll_at" TIMESTAMPTZ,
    "lease_until" TIMESTAMPTZ,
    "lease_owner" VARCHAR(128) NOT NULL DEFAULT '',
    "last_poll_at" TIMESTAMPTZ,
    "created_at" TIMESTAMPTZ NOT NULL,
    "updated_at" TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX "uidx_poll_cursors_node" ON "poll_cursors" ("tenant_id", "workflow_id", "node_id");

CREATE INDEX "idx_poll_cursors_due" ON "poll_cursors" ("next_poll_at");
