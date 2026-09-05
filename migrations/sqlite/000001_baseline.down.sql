-- Reverse of 000001_baseline.up.sql.
--
-- Dropped child-first so the drop succeeds with foreign keys enforced, which
-- the SQLite DSN turns on with PRAGMA foreign_keys(1).

DROP TABLE `schedules`;

DROP TABLE `webhook_deliveries`;

DROP TABLE `webhook_routes`;

DROP TABLE `webhook_bindings`;

DROP TABLE `credentials`;

DROP TABLE `execution_node_runs`;

DROP TABLE `executions`;

DROP TABLE `workflow_versions`;

DROP TABLE `workflows`;
