-- Indexes for the two whole-table scans of the executions table.
--
-- The first is the queue. ClaimNext filters on status — `status = 'queued' OR
-- ((status = 'running' OR status = 'cancelling') AND lease_expires_at <= ?)` —
-- and orders by started_at, id. The baseline indexed the tenant, the workflow,
-- the lease columns and the cancellation flag, and never status, so every claim
-- scanned the table. Each idle worker polls every 100 ms, so a stock install
-- runs on the order of a hundred of these a second while doing nothing at all,
-- against a table that only ever grows.
--
-- The columns are in the order the query needs them: status narrows, started_at
-- and id then supply the ordering without a sort. Measured against 500,003 rows
-- on PostgreSQL 16, this turns the claim's parallel sequential scan into a
-- bitmap index scan: 23.583 ms and 18,188 buffers become 0.123 ms and 13. A
-- partial index over the three non-terminal statuses was considered and is not
-- needed — the planner already touches only the rows those statuses cover, and
-- a second index would cost a write on every execution to save nothing.

CREATE INDEX "idx_executions_status_started" ON "executions" ("status","started_at","id");

-- The second is retention. A prune selects by finished_at and never by
-- started_at: a running execution has no finished_at, and that is exactly the
-- row that must survive. id rides along so a prune reads whole batches in a
-- stable order from the index alone.

CREATE INDEX "idx_executions_finished_at" ON "executions" ("finished_at","id");
