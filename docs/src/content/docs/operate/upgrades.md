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
   migrations; watch the first start, because a migration failure stops the
   container rather than degrading it.
4. Check `/api/v1/ready` before sending traffic: it answers `503` while the
   database is unreachable.

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
