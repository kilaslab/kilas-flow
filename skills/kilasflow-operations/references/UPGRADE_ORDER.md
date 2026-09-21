# Backups, upgrades, and the order they run in

This is the depth behind the operations skill: what a boot actually does, in
which order, what "ready" is really reporting, how each driver is backed up and
restored, how to upgrade without guessing, and the order the tenant deletion
runs in. Source of truth is the code — `internal/database/**` (the migration
runner), `internal/datastore/**` (the per-datastore pass and fleet status),
`internal/api/handlers/system.go` (the probes), `internal/tenantpurge/**` (the
deletion), `migrations/{sqlite,postgres}/**`.

## What a boot does, in order

1. **Configuration and database.** The process exits if it cannot reach the
   database, so a failed migration is a container that does not start rather
   than a server running against the wrong schema. A Compose stack orders on the
   database healthcheck for exactly this reason.
2. **SQL migrations.** Versioned files under `migrations/sqlite` and
   `migrations/postgres`, each with an up and a down, applied by a runner that
   records what it applied in a `schema_migrations` table it creates itself. A
   rollback is expressible through the down files; there is no release-to-release
   compatibility policy, because there has been no release to keep one against.
3. **The per-datastore pass.** This is separate from the SQL migrations and is
   the part that surprises people: it versions the tables KilasFlow creates *for
   a tenant*. Each datastore's catalogue row carries a `schema_version`, and
   `internal/datastore` ships the steps that move a datastore from one version to
   the next.
   - One **transaction per step per datastore**, with the version stamp in the
     same transaction. A kill therefore leaves each datastore fully at its old
     version or fully at its new one, and the next boot resumes from there.
   - Every process role runs the pass, and two processes booting together apply
     each step once: the runner re-reads the catalogue row inside the step's
     transaction (`SELECT ... FOR UPDATE` on PostgreSQL). SQLite takes no row
     lock — the driver discards it — but a SQLite install is single-process by
     construction and runs on a one-connection pool, so the re-read is safe.
   - The listener does not open until the first pass ends.
   - After boot the pass repeats every 30 seconds, which is what clears a
     datastore an older peer created behind during a rolling deploy, without a
     restart.
   - Three refusals stop the boot: a step failure, a datastore **behind** the
     build with no step to reach it, and a datastore **ahead** of the build (a
     newer binary migrated it). Each names the datastore and both versions.
4. **The listener.** Only now does the API answer.

## What readiness reports

`kilasflow ready` (`get-ready`, `GET /api/v1/ready`) is the probe a load
balancer should use, and `kilasflow health` (`get-health`, `GET /api/v1/health`)
is the one it must not: liveness touches no dependency, so it stays `200`
through a database outage and through a migration still running.

Readiness answers `503` — exit code 6 in the CLI — in three states: the database
is unreachable, the fleet status cannot be read, or a datastore is **behind**.
Only the last carries a `datastores` block, and that same block is on the `200`
body as well, so a monitor reads the spread from a field rather than parsing
problem text:

| Field | Meaning |
| --- | --- |
| `schemaVersion` | the datastore schema version this build serves |
| `spread` | datastores per schema version, keyed by version |
| `behind` | datastores below the served version: a migration is outstanding |
| `ahead` | datastores above it: a newer build migrated them and this build refuses them |

A datastore **ahead** is reported but does not fail readiness, precisely so the
old replicas keep answering ready during a rolling upgrade while they refuse the
datastores the newer build migrated. `/ready` is public and the block therefore
discloses the installation's datastore count to anyone who can reach the port.

## Roles

One binary, three shapes via `--role`: `both` (the default) runs API, workers and
scheduler; `api` serves the API and the scheduler with no workers; `worker` runs
workers alone. Each process identifies itself as
`kilasflow-<host>-<pid>-<random>` in `lease_owner` and the logs (override with
`--worker-id`). The scheduler stays single by role, and several `api`/`both`
processes stay safe because the due claim advances `next_run_at` in the same
transaction that reads it.

Two things break it:

- **SQLite.** One writer, no `LISTEN`/`NOTIFY`, one file. Never point two
  processes at the same SQLite database, and a split role is refused at startup.
  Multi-worker topologies require PostgreSQL.
- **Clock skew.** Leases compare wall clocks across processes, so a worker whose
  clock runs ahead reclaims executions that are still running; persistence stays
  at-most-once, but a graph's side effects happen twice. Run NTP everywhere and
  keep `execution.default_timeout` comfortably above both the skew and the
  longest run, because the same timeout bounds one run and the lease it holds.

## Backup and restore: SQLite

The database is one file (default `./data/kilasflow.db`, `/app/data/` in the
image) plus its WAL sidecars.

1. Stop the container, or checkpoint first — copying a live WAL without one can
   hand you a backup that needs recovery on open.
2. Copy the data directory, the file and its `-wal`/`-shm` siblings together.
3. Confirm the copy is readable by the non-root UID before trusting it: the image
   runs as `nonroot`, and a backup restored root-only is a database the server
   cannot open.

Restore is the reverse: stop, put the files back, fix ownership, start.

## Backup and restore: PostgreSQL

Use the database's own tools against the Compose service (its port is not
published to the host):

```sh
docker compose -f compose.yaml -f compose.postgres.yaml exec postgres \
  pg_dump -U kilasflow kilasflow > kilasflow.sql
```

Restore into a server that can provide the same extensions on the same major
version. PostgreSQL does not read a data directory written by a newer major,
which is why the Compose service pins one — and it pins
`pgvector/pgvector:pg17` rather than stock PostgreSQL because migration
`000006_vector_store` creates the `vector` extension, so a dump contains that
`CREATE EXTENSION` and a restore into `postgres:17-alpine` fails on the first
statement. The named volume can also be backed up at the volume level, but a dump
is the form that survives a move between machines and majors.

## Upgrade order

1. **Back up first**, per the driver above. There is no release-to-release
   compatibility promise yet; the backup is the rollback plan.
2. **Check the data path is writable** by the container user when it is a bind
   mount. An upgrade that cannot write its data directory fails exactly like a
   fresh install that cannot.
3. **Start the new build and watch the first boot.** It applies pending SQL
   migrations and then the per-datastore pass; the listener opens only when both
   succeed, and either failure stops the container rather than degrading it.
4. **Check readiness before sending traffic**, then read the `datastores` block
   to see the spread the instance reports.
5. **Know what the build cannot serve.** Every process role that boots must carry
   what the catalogue needs: the `kilasflow.code` node wants a Go toolchain and
   `code.go_binary`/`PATH` pointed at it, the opt-in JavaScript sidecar wants
   Node 24 plus the operator's community packages beside the binary. A node the
   deployment cannot run is reported as unavailable in the catalogue rather than
   failing at execution time, so an upgrade that drops a runtime shows up as a
   catalogue entry, not as a broken run.

## One upgrade that changes an address

A database populated by a build that predates webhook routes can hold webhook
bindings with no route, and a webhook is only ever matched by its route, never by
its path label. The `webhook_route_backfill` migration gives each such binding a
minted route on the first boot (the log line is `applied migration` with that
name), so the address changes from `/webhook/<label>` to `/webhook/<route>`.
Bindings that already have a route are not touched.

Read the new address with the `list-workflow-webhooks` operation
(`GET /workflows/{id}/webhooks`, reached as `kilasflow api
list-workflow-webhooks --path id=<workflowId>`), and activate again any workflow
whose trigger registers its own address with the sender (Telegram, WAHA) so the
sender learns it.

## Switching drivers is not an upgrade

There is no migration between SQLite and PostgreSQL or back: the new side comes
up empty, and anything worth keeping has to be exported through the API first.

## Offboarding a tenant: the order the deletion runs in

`delete-tenant` (`DELETE /api/v1/tenants/{id}`, `kilasflow api delete-tenant
--path id=<tenantId>`) removes the customer and everything KilasFlow holds for
it. It is irreversible, has no export-before-delete and no soft delete, and it
runs on the operator credential alone — any other principal, including the
customer's own valid key, gets `403` and cannot learn from the answer whether the
tenant exists. A deletion that fails still leaves the tenant locked out; that is
the safe direction.

The order in `internal/tenantpurge` is forced by foreign keys, by in-flight
triggers and by the fact that a filesystem has no transaction to join:

| Step | Empties | Why there |
| --- | --- | --- |
| `lock-out` | `api_keys`, `users` (an update) | the tenant cannot write while its rows go |
| `stop-triggers` | — | remote registrations are unregistered while their credentials still exist |
| `triggers` | `schedules`, `webhook_deliveries`, `webhook_routes`, `webhook_bindings` | intake closes before the definitions go |
| `binaries` | — | the payload directory precedes the rows that name its payloads |
| `runs` | `execution_node_runs`, `execution_waits`, `executions`, `idempotency_keys` | waits are deleted before executions (`RESTRICT`) |
| `definitions` | `secret_bindings`, `credentials`, `workflow_versions`, `workflow_publish_events`, `workflows` | versions before workflows (`RESTRICT`) |
| `sessions` | — | the process's in-memory agent sessions, best effort |
| `datastores` | `datastore_columns`, `datastores` | catalogue rows and their physical tables together |
| `vectors` | the four `vector_documents_*` tables, `vector_collections` | each is guarded: SQLite, or PostgreSQL without the `vector` extension, has none |
| `identity` | `api_keys`, `users`, `tenants` | forced by `RESTRICT`; the tenant row goes last |

One transaction **per step**, not one for the whole deletion, because
`DROP TABLE` takes exclusive locks and a single transaction would hold them
across every other tenant's reads. Binaries and in-memory state cannot join a
transaction at all, which is why the deletion is safe to repeat rather than
atomic.

Retry semantics: repeat the request until it converges. A failed step answers
`500` with a problem naming the step and saying the request can be repeated;
steps after it are not attempted. An unknown id answers `200` with zero counts,
deliberately, so a stale embed session's leftovers are cleaned up too.
`tenantRemoved: false` with every count at zero is the confirmation that nothing
is left. The deletion is detached from the request context, so a client timeout
is not a stopped deletion — send it again.

What it cannot reach, and what an operator must handle beside it: backups, WAL
archives and replicas; logs; embed sessions already issued (they keep their
scopes until they expire — 15 minutes by default, 30 at most); secrets in an
external secret manager (only the binding rows go); registrations
`stop-triggers` could not remove; triggers owned by another replica; agent memory
in other processes; payloads on another host's local disk when `binary.root` is
not shared; and a worker still executing a run, which can recreate a payload
directory until its next database write fails.
