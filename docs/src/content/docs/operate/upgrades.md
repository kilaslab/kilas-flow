---
title: Backups and upgrades
description: What the migration machinery does today, and how to back up, restore and upgrade each driver.
sidebar:
  order: 5
---

## What the migration machinery does

Schema changes are versioned SQL migration files under `migrations/`, one
directory per dialect (`migrations/sqlite`, `migrations/postgres`), each with
an up and a down, applied at boot by a runner that records what it has
applied in a `schema_migrations` table it creates itself. A rollback is
expressible rather than theoretical — but only through the down migrations;
there is no compatibility policy between releases yet, because there has been
no release to keep one against.

KilasFlow exits if it cannot reach the database at boot, so a failed
migration is a container that does not start rather than a server running
against the wrong schema. The Compose overlay orders on the database
healthcheck for exactly this reason.

## Datastore schema versions

The SQL migrations above version the installation's own tables. A second,
per-datastore version covers the tables KilasFlow creates *for a tenant*: each
datastore's row in the `datastores` catalogue carries a `schema_version`, and
`internal/datastore` ships the steps that move a datastore from one version to
the next.

- After the SQL migrations, boot runs a pass over the whole fleet and each
  **step** runs in its own transaction together with the version stamp — one
  transaction per step per datastore, not per datastore. A run that is killed
  therefore leaves every datastore fully at the old version or fully at the
  new one, and the next boot resumes from there.
- Every process role runs the pass, and two processes booting together apply
  each step once: the runner re-reads the catalogue row inside the step's
  transaction under `SELECT ... FOR UPDATE` on PostgreSQL. SQLite takes no
  row lock — the driver discards it — but a SQLite install is single-process
  by construction (role validation refuses a split role) on a one-connection
  pool, so the same re-read is safe there.
- The listener does not open until the first pass ends. A step failure, a
  datastore behind the build with no step to reach it, or a datastore **ahead**
  of the build (a newer binary migrated it) refuses the boot and names the
  datastore and both versions, exactly the way a failed SQL migration does.
  Run a build at or above that datastore's version, or restore the backup.
- After boot the pass repeats every 30 seconds. That is what clears a
  datastore an older peer process created behind during a rolling deploy,
  without a restart.
- `GET /api/v1/ready` reports the spread and answers `503` while a datastore is
  **behind** — a migration is outstanding — and also when the database is
  unreachable or the fleet status cannot be read. The spread is in the
  migration-outstanding `503`'s problem document as well as in the `200` body,
  so what is outstanding stays machine-readable in the state that reports it;
  the other two `503`s carry no block, because neither can read the catalogue.
  A datastore **ahead** is reported in the block but does not fail readiness,
  so the old replicas keep answering ready during a rolling upgrade while they
  refuse exactly the datastores the newer build migrated.

The version this build serves is **1**, and no shipped step has been needed
yet, so today the pass migrates nothing: it exists so that the first bump
migrates rather than takes every datastore offline.

## Backup and restore: SQLite

The database is one file (default `./data/kilasflow.db`, `/app/data/` in the
image) plus its WAL sidecars. Back up the whole directory:

1. Stop the container, or checkpoint first — copying a live WAL without one
   can hand you a backup that needs recovery on open.
2. Copy the data directory, file and `-wal`/`-shm` siblings together.
3. Confirm the copy is readable by the non-root UID before trusting it: the
   image runs as `nonroot`, and a backup restored with root-only permissions
   is a database the server cannot open.

Restore is the reverse: stop the container, put the files back, fix
ownership, start.

## Backup and restore: PostgreSQL

Use the database's own tools against the Compose service (the port is not
published to the host, so run through the stack):

```sh
docker compose -f compose.yaml -f compose.postgres.yaml exec postgres \
  pg_dump -U kilasflow kilasflow > kilasflow.sql
```

Restore into a server that can provide the same extensions, on the same major
version. PostgreSQL does not read a data directory written by a newer major,
which is why the Compose service pins a major at all; it pins
`pgvector/pgvector:pg17` rather than stock PostgreSQL because migration
`000006_vector_store` creates the `vector` extension, and a dump taken from it
contains that `CREATE EXTENSION`. Restoring such a dump into a
`postgres:17-alpine` container fails on the first statement, because that image
ships no pgvector to install — see [PostgreSQL
requirements](/start/install/#postgresql-requirements) for the two ways to
satisfy it. The named volume `kilasflow-postgres` can also be backed up at the
volume level, but a dump is the form that survives a move between machines and
majors.

## Upgrading

1. Back up first, per the driver above. There is no release-to-release
   compatibility promise yet; the backup is the rollback plan.
2. If the data lives on a bind mount, confirm the mount is writable by the
   container user before pulling a newer image — an upgrade that cannot write
   its data directory fails exactly like a fresh install that cannot.
3. Pull or build the new image and start it. Boot applies any pending
   migrations, SQL first and then the per-datastore pass, and the listener
   opens only when both succeed; watch the first start, because a migration
   failure stops the container rather than degrading it. A datastore the new
   build cannot reach (no step for its version, or a version ahead of the
   build) refuses the boot and names it.
4. Check `/api/v1/ready` before sending traffic: it answers `503` while the
   database is unreachable or while a datastore migration is outstanding, and
   its `datastores` block shows the schema-version spread the instance sees.

A database populated by a build that predates webhook routes can hold webhook
bindings with no route, which used to answer on their path label. The
`webhook_route_backfill` migration gives each of them a minted route on the first
boot (the log line is `applied migration` with that name), and a webhook is only
ever matched by its route, never by its path label. Such a binding's address
therefore changes from `/webhook/<label>` to `/webhook/<route>`: read the new
one with `GET /workflows/{id}/webhooks`, and activate again any workflow whose
trigger registers its own address with the sender (Telegram, WAHA), so that the
sender learns it. Bindings that already have a route are not touched.

Switching drivers (SQLite to PostgreSQL or back) is not an upgrade path:
there is no migration between backends, the new side comes up empty, and
anything worth keeping must be exported first.
