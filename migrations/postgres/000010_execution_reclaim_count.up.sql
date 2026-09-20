-- How many times an abandoned execution has been handed to a fresh worker.
--
-- ClaimNext reclaims an execution whose worker lease expired, clears the
-- partial trace it left behind, and hands the graph to another worker. That is
-- the right recovery for a process that died once, and an unbounded loop for
-- one that dies for a reason the re-run reproduces — the trace collision in
-- BUG-hfhzq6, an out-of-memory node, a crash on the same item. Every lease
-- period the whole graph runs again, side effects included, for ever.
--
-- The count is what bounds it: the claim that would exceed MaxExecutionReclaims
-- settles the row as failed (execution.crashed) instead of claiming it. The
-- row then leaves the claim predicate for good, so a poison execution costs one
-- crashed record rather than a permanently wedged worker.
--
-- Additive with a default, so an existing database opens without a rewrite.

ALTER TABLE "executions" ADD COLUMN "reclaim_count" bigint NOT NULL DEFAULT 0;
