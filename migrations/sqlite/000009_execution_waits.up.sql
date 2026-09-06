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
-- reason as 000003: failing loudly on a collision is the only safe outcome
-- in a database KilasFlow may share with another application.

CREATE TABLE `execution_waits` (
  `id` integer PRIMARY KEY AUTOINCREMENT,
  `tenant_id` text NOT NULL,
  `execution_id` text NOT NULL,
  `workflow_id` text NOT NULL,
  `node_id` text NOT NULL,
  `mode` text NOT NULL,
  `token_hash` text NOT NULL,
  `resume_token` text NOT NULL,
  `checkpoint` blob NOT NULL,
  `resume_output` blob,
  `run_count` integer NOT NULL,
  `expires_at` datetime NOT NULL,
  `consumed_at` datetime,
  `outcome` text NOT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  CONSTRAINT `fk_execution_waits_execution` FOREIGN KEY (`execution_id`) REFERENCES `executions`(`id`) ON DELETE RESTRICT ON UPDATE CASCADE
);

CREATE UNIQUE INDEX `uidx_execution_waits_token` ON `execution_waits`(`token_hash`);

CREATE INDEX `idx_execution_waits_execution` ON `execution_waits`(`tenant_id`,`execution_id`);

CREATE INDEX `idx_execution_waits_expiry` ON `execution_waits`(`expires_at`);
