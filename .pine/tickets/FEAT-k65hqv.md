---
id: FEAT-k65hqv
title: Add pgvector-backed vector store and embedding nodes
status: todo
priority: medium
labels:
    - persistence
    - postgres
    - tier
deps:
    - FEAT-gvn62x
    - FEAT-r6xhnp
    - FEAT-ybm2pd
parent: EPIC-m42s3g
phase: p6
created: "2026-09-05T05:03:22Z"
updated: "2026-09-05T05:03:22Z"
---

## Scope

KilasFlow has no vector or embedding support of any kind: nothing in `internal/ai` or `nodes/` mentions either, and `internal/ai/ai.go` defines only chat-shaped contracts. This ticket makes retrieval a PostgreSQL-tier unlock rather than a new external service — with the PostgreSQL driver selected and the `vector` extension available, a workflow gets an embeddings node and a vector store node; on SQLite it gets a clear diagnostic instead of a node that appears to work and returns nothing.

Both nodes have to reuse what already exists rather than growing their own stack. Embeddings go through an adapter in `internal/ai` built on the same injected `*http.Client` that `NewOpenAICompatible` takes, so `internal/safehttp`'s dial-time SSRF check and the credential's `AllowedDomains` scope apply to an embeddings call exactly as they do to a chat completion. The store's SQL belongs to the internal database and therefore to p6-1's migrations and p6-2's table prefix — not to a workflow-authored `kilasflow.postgres` node, which p6-5 forbids from touching the internal database at all.

The one thing to do better than n8n is the schema. pgvector's ANN indexes accept `vector` only up to 2,000 dimensions and `halfvec` up to 4,000, and a `vector` column declared with no dimension modifier cannot be indexed at all — so a store built on an unbounded column answers every similarity query with an exact scan of the whole table, which looks fine on a demo corpus and falls over on a real one. The roadmap's research records n8n's PGVector store as creating exactly that shape in a table named `n8n_vectors`; confirm the detail against the widened reference checkout from p0-1 before repeating it anywhere user-facing, but the constraint it runs into is a documented pgvector limit and is not in doubt.

## Acceptance criteria

- [ ] An embeddings node produces vectors through the existing outbound HTTP policy and credential store; no new HTTP client is constructed anywhere in the path.
- [ ] A vector store node inserts, deletes and similarity-queries documents, scoped by tenant and by a caller-named collection, with a top-k and a metadata filter.
- [ ] A collection records its dimension, distance function and index type, and an ANN index for that distance function exists for every collection's storage from the moment it can hold rows.
- [ ] A dimension that the chosen column type cannot index is refused when the collection is created, with a message naming the limit — never accepted and then silently left unindexed.
- [ ] Inserting a vector whose dimension does not match its collection fails with a diagnostic naming both numbers.
- [ ] On SQLite, or on a PostgreSQL where the `vector` extension is absent, both nodes fail configuration validation with a message saying what to install.
- [ ] Similarity search over a populated collection uses the ANN index, with the query plan captured as evidence.
- [ ] The tables carry the configured table prefix and are created by a migration, not at run time.

## Implementation Plan

The schema decision is the whole ticket, because pgvector fixes a column's dimension and an index cannot span dimensions. Three shapes are on the table. Creating a table per collection at run time means DDL executed inside a customer's shared database on a workflow's behalf, which contradicts both p6-1 and p6-5 — reject it. A single table with an untyped `vector` column is the n8n shape and cannot be indexed — reject it. Recommend the third: a small fixed set of document tables, one per supported dimension, created by migration, plus a `vector_collections` table recording each collection's dimension, distance function and which storage table it lives in. Support the dimensions that actually ship from embedding APIs — 384, 768, 1024 and 1536 — and note that 3072 (OpenAI's `text-embedding-3-large` at full width) is past `vector`'s 2,000-dimension index limit, so it needs `halfvec(3072)` with `halfvec_cosine_ops`, or the operator reduces width through the API's own `dimensions` parameter. Say which of those two you chose in the migration comment; a reader will ask.

Distance function is per collection and is recorded, not inferred: an index built for one operator class does not serve another, so the collection row has to drive both the `CREATE INDEX` and the operator in the query. Default to cosine (`vector_cosine_ops`, `<=>`), since embedding APIs return normalised vectors.

The extension is the trap. `CREATE EXTENSION IF NOT EXISTS vector` needs privileges a shared-database role very often does not have, and a migration that fails on it takes the whole install down at boot for a feature nobody asked for. Recommend an explicit opt-in — a `database.vector` flag — so the operator states the intent, the vector migrations are skipped entirely when it is off, and the nodes report plainly why they are unavailable. Never let a missing extension fail the baseline migration.

`docker-compose.yml` runs `postgres:17-alpine`, which does not ship pgvector, so the smoke path needs `pgvector/pgvector:pg17` or an equivalent under the existing `postgres` profile; without that change none of this is provable by `make smoke-postgres`.

For binding values, the repository layer is GORM over pgx. Recommend formatting the vector as a `[a,b,c]` literal with an explicit `::vector` cast rather than adding `github.com/pgvector/pgvector-go` for one type — but measure bulk insert before committing, because a 1536-float text literal per row is not free, and the dependency is the right answer if it is not.

Import mapping for `@n8n/n8n-nodes-langchain` vector store and embeddings nodes is p5-8's work, not this ticket's. This ticket only has to make the native nodes exist, be indexed, and be reachable.

## References

- Roadmap plan, p6 section, entry V2-p6-6: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- pgvector documentation, `indexes.md` and `README.md` (via Context7 `/pgvector/pgvector`): HNSW and IVFFlat syntax, the `m`/`ef_construction`/`lists` parameters, the operator classes `vector_l2_ops`/`vector_ip_ops`/`vector_cosine_ops`/`vector_l1_ops`, and the indexable limits — `vector` to 2,000 dimensions, `halfvec` to 4,000.
- `internal/ai/openai.go` — `NewOpenAICompatible` and its injected `*http.Client`, the pattern an embeddings adapter must follow.
- `internal/ai/ai.go` — the package boundary comment that keeps provider code inside adapters.
- `internal/safehttp/safehttp.go` — the outbound policy the embeddings call inherits.
- `docker-compose.yml` — the `postgres` profile currently pinned to `postgres:17-alpine`.
