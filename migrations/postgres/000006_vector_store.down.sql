-- Reverse of 000006_vector_store.up.sql.
--
-- Dropped child-first rather than with CASCADE: KilasFlow may share a
-- customer's database, and CASCADE would silently take anything of theirs that
-- happens to reference one of these tables. Every DROP carries IF EXISTS, so
-- the down file is re-runnable symmetrically with the up file. The vector
-- extension itself is deliberately not dropped: it may serve other
-- applications in the same database, and removing it is the operator's
-- decision, not a rollback's.

DROP TABLE IF EXISTS "vector_documents_1536";

DROP TABLE IF EXISTS "vector_documents_1024";

DROP TABLE IF EXISTS "vector_documents_768";

DROP TABLE IF EXISTS "vector_documents_384";

DROP TABLE IF EXISTS "vector_collections";
