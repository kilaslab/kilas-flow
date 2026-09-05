-- Reverse of 000001_baseline.up.sql.
--
-- Dropped child-first rather than with CASCADE: KilasFlow may share a
-- customer's database, and CASCADE would silently take anything of theirs that
-- happens to reference one of these tables.

DROP TABLE "schedules";

DROP TABLE "webhook_deliveries";

DROP TABLE "webhook_routes";

DROP TABLE "webhook_bindings";

DROP TABLE "credentials";

DROP TABLE "execution_node_runs";

DROP TABLE "executions";

DROP TABLE "workflow_versions";

DROP TABLE "workflows";
