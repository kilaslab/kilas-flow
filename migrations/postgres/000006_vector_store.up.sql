-- Vector store: pgvector-backed collections plus one document table per
-- supported dimension.
--
-- pgvector fixes a column's dimension and an index cannot span dimensions, so
-- a single table with an untyped vector column cannot be indexed at all — it
-- answers every similarity query with an exact scan. A table per collection
-- created at run time is DDL inside a customer's shared database on a
-- workflow's behalf, which this product does not do. The shape here is the
-- third one: a vector_collections catalogue row per collection plus a small
-- fixed set of document tables, one per embedding dimension that actually
-- ships (384, 768, 1024 and 1536), each carrying a typed vector column.
--
-- CREATE EXTENSION needs a privilege a shared-database role very often does
-- not have. It is the first statement on purpose rather than hidden behind
-- one: when the role cannot install the extension the migration fails loudly
-- here instead of failing later on the unknown vector type, and an operator
-- who must have their database owner run CREATE EXTENSION vector first sees
-- exactly which step needs it. Never let a missing extension fail the
-- baseline migration; this one is outside it for that reason.
--
-- Every CREATE here carries IF NOT EXISTS, tables included, so the file is
-- re-runnable: a second Migrate over a database that already holds these
-- tables is a no-op instead of 42P07. IF NOT EXISTS is not a concurrency
-- fix on its own — two backends creating the same table at once still leave
-- the loser with 23505 on pg_type_typname_nsp_index — and that race is
-- closed one layer up, where the migration runner re-reads the applied
-- versions after any failure and treats a committed version row as a win.
--
-- Index budget: every document table carries an HNSW and an IVFFlat index for
-- each of the three distance operator classes (cosine, L2, inner product),
-- so whichever distance and index type a collection records, its ANN index
-- exists from the moment the table can hold rows. An index built for one
-- operator class does not serve another, which is why all three are present
-- rather than only the cosine default.
--
-- Name lengths are kept short on purpose: the longest identifier below is
-- 37 bytes, so even under the longest allowed table prefix (16 bytes) every
-- name stays within PostgreSQL's 63-byte limit.

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS "vector_collections" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "name" varchar(255) NOT NULL,
  "dimension" bigint NOT NULL,
  "distance" varchar(16) NOT NULL,
  "index_type" varchar(16) NOT NULL,
  "table_name" varchar(64) NOT NULL,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);

CREATE UNIQUE INDEX IF NOT EXISTS "uidx_vector_collections_tenant_name" ON "vector_collections" ("tenant_id","name");

CREATE TABLE IF NOT EXISTS "vector_documents_384" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "collection_id" varchar(64) NOT NULL,
  "content" text NOT NULL DEFAULT '',
  "metadata" jsonb NOT NULL DEFAULT '{}',
  "embedding" vector(384) NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_vdocs384_collection" FOREIGN KEY ("collection_id") REFERENCES "vector_collections"("id") ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS "idx_vdocs384_lookup" ON "vector_documents_384" ("tenant_id","collection_id");

CREATE INDEX IF NOT EXISTS "idx_vdocs384_hnsw_cos" ON "vector_documents_384" USING hnsw ("embedding" vector_cosine_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs384_hnsw_l2" ON "vector_documents_384" USING hnsw ("embedding" vector_l2_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs384_hnsw_ip" ON "vector_documents_384" USING hnsw ("embedding" vector_ip_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs384_ivf_cos" ON "vector_documents_384" USING ivfflat ("embedding" vector_cosine_ops) WITH (lists = 100);

CREATE INDEX IF NOT EXISTS "idx_vdocs384_ivf_l2" ON "vector_documents_384" USING ivfflat ("embedding" vector_l2_ops) WITH (lists = 100);

CREATE INDEX IF NOT EXISTS "idx_vdocs384_ivf_ip" ON "vector_documents_384" USING ivfflat ("embedding" vector_ip_ops) WITH (lists = 100);

CREATE TABLE IF NOT EXISTS "vector_documents_768" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "collection_id" varchar(64) NOT NULL,
  "content" text NOT NULL DEFAULT '',
  "metadata" jsonb NOT NULL DEFAULT '{}',
  "embedding" vector(768) NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_vdocs768_collection" FOREIGN KEY ("collection_id") REFERENCES "vector_collections"("id") ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS "idx_vdocs768_lookup" ON "vector_documents_768" ("tenant_id","collection_id");

CREATE INDEX IF NOT EXISTS "idx_vdocs768_hnsw_cos" ON "vector_documents_768" USING hnsw ("embedding" vector_cosine_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs768_hnsw_l2" ON "vector_documents_768" USING hnsw ("embedding" vector_l2_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs768_hnsw_ip" ON "vector_documents_768" USING hnsw ("embedding" vector_ip_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs768_ivf_cos" ON "vector_documents_768" USING ivfflat ("embedding" vector_cosine_ops) WITH (lists = 100);

CREATE INDEX IF NOT EXISTS "idx_vdocs768_ivf_l2" ON "vector_documents_768" USING ivfflat ("embedding" vector_l2_ops) WITH (lists = 100);

CREATE INDEX IF NOT EXISTS "idx_vdocs768_ivf_ip" ON "vector_documents_768" USING ivfflat ("embedding" vector_ip_ops) WITH (lists = 100);

CREATE TABLE IF NOT EXISTS "vector_documents_1024" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "collection_id" varchar(64) NOT NULL,
  "content" text NOT NULL DEFAULT '',
  "metadata" jsonb NOT NULL DEFAULT '{}',
  "embedding" vector(1024) NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_vdocs1024_collection" FOREIGN KEY ("collection_id") REFERENCES "vector_collections"("id") ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS "idx_vdocs1024_lookup" ON "vector_documents_1024" ("tenant_id","collection_id");

CREATE INDEX IF NOT EXISTS "idx_vdocs1024_hnsw_cos" ON "vector_documents_1024" USING hnsw ("embedding" vector_cosine_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs1024_hnsw_l2" ON "vector_documents_1024" USING hnsw ("embedding" vector_l2_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs1024_hnsw_ip" ON "vector_documents_1024" USING hnsw ("embedding" vector_ip_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs1024_ivf_cos" ON "vector_documents_1024" USING ivfflat ("embedding" vector_cosine_ops) WITH (lists = 100);

CREATE INDEX IF NOT EXISTS "idx_vdocs1024_ivf_l2" ON "vector_documents_1024" USING ivfflat ("embedding" vector_l2_ops) WITH (lists = 100);

CREATE INDEX IF NOT EXISTS "idx_vdocs1024_ivf_ip" ON "vector_documents_1024" USING ivfflat ("embedding" vector_ip_ops) WITH (lists = 100);

CREATE TABLE IF NOT EXISTS "vector_documents_1536" (
  "id" varchar(64),
  "tenant_id" varchar(64) NOT NULL,
  "collection_id" varchar(64) NOT NULL,
  "content" text NOT NULL DEFAULT '',
  "metadata" jsonb NOT NULL DEFAULT '{}',
  "embedding" vector(1536) NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_vdocs1536_collection" FOREIGN KEY ("collection_id") REFERENCES "vector_collections"("id") ON DELETE CASCADE ON UPDATE CASCADE
);

CREATE INDEX IF NOT EXISTS "idx_vdocs1536_lookup" ON "vector_documents_1536" ("tenant_id","collection_id");

CREATE INDEX IF NOT EXISTS "idx_vdocs1536_hnsw_cos" ON "vector_documents_1536" USING hnsw ("embedding" vector_cosine_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs1536_hnsw_l2" ON "vector_documents_1536" USING hnsw ("embedding" vector_l2_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs1536_hnsw_ip" ON "vector_documents_1536" USING hnsw ("embedding" vector_ip_ops);

CREATE INDEX IF NOT EXISTS "idx_vdocs1536_ivf_cos" ON "vector_documents_1536" USING ivfflat ("embedding" vector_cosine_ops) WITH (lists = 100);

CREATE INDEX IF NOT EXISTS "idx_vdocs1536_ivf_l2" ON "vector_documents_1536" USING ivfflat ("embedding" vector_l2_ops) WITH (lists = 100);

CREATE INDEX IF NOT EXISTS "idx_vdocs1536_ivf_ip" ON "vector_documents_1536" USING ivfflat ("embedding" vector_ip_ops) WITH (lists = 100);
