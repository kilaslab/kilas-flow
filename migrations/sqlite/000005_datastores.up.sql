-- Datastore catalogue: one row per datastore plus its column definitions.
--
-- A datastore is one physical table created by runtime DDL under the
-- configured table prefix (internal/datastore owns the DDL). The catalogue
-- rows live in the migrated schema because every install needs them and they
-- are bounded and known up front; the physical tables deliberately do not,
-- because they are tenant-created, unbounded, and versioned per table by a
-- second runner (FEAT-gxppx1) rather than by these files.
--
-- CREATE TABLE is written without IF NOT EXISTS on purpose, for the same
-- reason as 000003: failing loudly on a collision is the only safe outcome
-- in a database KilasFlow may share with another application.
--
-- The engine writes the catalogue row first and issues the DDL second inside
-- one transaction (both drivers run DDL transactionally), so a DDL failure
-- leaves no catalogue row and a catalogue failure leaves no physical table.

CREATE TABLE `datastores` (
  `id` text,
  `tenant_id` text NOT NULL,
  `name` text NOT NULL,
  `surrogate` text NOT NULL,
  `schema_version` integer NOT NULL DEFAULT 1,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`)
);

-- The surrogate names the physical table (prefix + ds_ + sixteen hex
-- characters), never the public id: a public id is 39 bytes, which leaves no
-- room under PostgreSQL's 63-byte limit once a table prefix is applied.
CREATE UNIQUE INDEX `uidx_datastores_surrogate` ON `datastores`(`surrogate`);

CREATE INDEX `idx_datastores_tenant_name` ON `datastores`(`tenant_id`,`name`);

CREATE TABLE `datastore_columns` (
  `id` integer PRIMARY KEY AUTOINCREMENT,
  `datastore_id` text NOT NULL,
  `name` text NOT NULL,
  `type` text NOT NULL,
  `position` integer NOT NULL DEFAULT 0,
  CONSTRAINT `fk_datastore_columns_datastore` FOREIGN KEY (`datastore_id`) REFERENCES `datastores`(`id`) ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE UNIQUE INDEX `uidx_datastore_columns_datastore_name` ON `datastore_columns`(`datastore_id`,`name`);

CREATE INDEX `idx_datastore_columns_datastore` ON `datastore_columns`(`datastore_id`);
