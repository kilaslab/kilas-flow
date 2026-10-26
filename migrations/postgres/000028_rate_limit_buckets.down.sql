-- Reverse of 000028_rate_limit_buckets.up.sql. The allowances were throttle
-- state, not evidence; nothing else references them.

DROP TABLE IF EXISTS "rate_limit_buckets";
