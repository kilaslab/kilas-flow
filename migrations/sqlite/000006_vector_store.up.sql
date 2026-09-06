-- Vector store catalogue on SQLite: the collection rows without storage.
--
-- SQLite has no pgvector extension, so there are no document tables here and
-- the embeddings and vector store nodes refuse to run on this driver with a
-- message saying what to install. The catalogue table ships anyway because
-- every migration version must exist for both dialects — a version present in
-- one and missing from the other would leave the same binary at two different
-- schema versions depending on which database it opened — and the catalogue
-- row is plain typed columns with no vector type, so it is the half of the
-- schema SQLite can honestly carry.
--
-- CREATE TABLE and CREATE INDEX both carry IF NOT EXISTS, so the file is
-- re-runnable like its PostgreSQL half: a second Migrate over a database
-- that already holds the catalogue is a no-op.

CREATE TABLE IF NOT EXISTS `vector_collections` (
  `id` text,
  `tenant_id` text NOT NULL,
  `name` text NOT NULL,
  `dimension` integer NOT NULL,
  `distance` text NOT NULL,
  `index_type` text NOT NULL,
  `table_name` text NOT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`)
);

CREATE UNIQUE INDEX IF NOT EXISTS `uidx_vector_collections_tenant_name` ON `vector_collections`(`tenant_id`,`name`);
