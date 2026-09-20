---
title: Install
description: A Docker Compose quickstart that takes you from nothing to a workflow you have run, plus the source build for people working on KilasFlow itself.
---

Docker is the only thing you need. No Go, no Node, no pnpm — every toolchain the
build touches runs inside a container.

## Quickstart

```sh
git clone https://github.com/kilaslab/kilas-flow
cd kilas-flow

cp .env.example .env
printf 'KILASFLOW_ENCRYPTION_KEY=%s\n' "$(openssl rand -base64 32)" >> .env

docker compose -f compose.yaml -f compose.build.yaml up -d --build
```

Wait for the server to answer, then open the editor:

```sh
curl -fsS http://localhost:8080/api/v1/ready
```

```json
{"status":"ok","database":"ok","datastores":{"schemaVersion":1,"spread":{},"behind":0,"ahead":0}}
```

The `datastores` block is absent from the `200` body only on an instance with no
datastore store: `schemaVersion` is the version this build serves, `spread`
counts datastores per version, and `behind` counts those still waiting on a
migration. A `503` that reports a datastore migration outstanding carries the
block too, so the spread stays readable while readiness refuses; a `503` for an
unreachable database carries none, because a catalogue that cannot be read has
no spread to report.
Every response also carries a `$schema` field pointing at the JSON Schema for
its body, which the sample above leaves out.

The editor is at **http://localhost:8080/app/workflows**, the API reference the
running server generates for itself is at **http://localhost:8080/docs**, and
`docker compose logs -f` follows the server.

:::note[Why this builds instead of pulling]
The multi-architecture image pipeline and the release workflow are both in the
tree, and `scripts/docker-tags.sh` already defines what each published tag
means. But no version tag has ever been pushed, so
`ghcr.io/kilaslab/kilasflow` holds nothing and there is no image to pull. The
`compose.build.yaml` overlay is how you get one until that changes.

When the first release lands, set `KILASFLOW_IMAGE` in `.env` to the exact
version and drop the overlay — `docker compose up -d` then pulls in seconds
rather than building in minutes:

```sh
KILASFLOW_IMAGE=ghcr.io/kilaslab/kilasflow:v1.2.3
```

Pin the full `vX.Y.Z`. The `vX.Y` and `latest` tags both move forward onto later
releases, so a stack tracking either can change during a restart you did not
mean as an upgrade.
:::

### How long it really takes

Measured on an Apple Silicon laptop whose Docker had already pulled the three
base images, from an empty state — no image, no `.env`, no data volume — to a
workflow that had run and returned its output: **36 seconds**, of which 13 were
the build and start, 9 were waiting for the first readiness poll, and the rest
was creating and running the workflow.

That run reused the layer cache. Forcing a genuine cold build with `--no-cache`
took **39 seconds** on its own, so a first-ever build is roughly a minute — plus
however long your connection takes to pull `node:24.16-alpine`,
`golang:1.27-alpine` and the distroless runtime base, which is the one part of
this that depends on your network rather than your machine.

## What the first boot tells you

```
INFO  starting kilasflow version=local
INFO  database connected driver=sqlite
INFO  applied migration version=1 name=baseline
INFO  applied migration version=2 name=workflow_history
INFO  applied migration version=3 name=identity
WARN  embed signing key is not set; embedded editor sessions are disabled variable=KILASFLOW_EMBED_SIGNING_KEY
WARN  the API is unauthenticated: anyone who can reach this port owns the installation enable_with=KILASFLOW_AUTH_ENABLED=true
INFO  http server listening addr=0.0.0.0:8080 docs=/docs openapi=/api/openapi.json
```

Two warnings on a correctly configured first run, and a third if you skipped the
key step:

```
WARN  credential encryption key is not set; credential storage is disabled variable=KILASFLOW_ENCRYPTION_KEY
```

Each one names a capability that is switched off rather than a failure. That is
deliberate: refusing to boot without three secrets would make all three
mandatory for somebody who only wanted to look at the editor. The cost of that
choice is that a capability can be quietly missing, which is why the server
repeats these at every start and why they are worth reading once.

**The encryption key** is the one the quickstart sets, because without it
credentials cannot be stored at all — creating one fails, and any node that
needs one cannot run. It encrypts credentials at rest with AES-256-GCM.
Changing it later makes everything already stored undecryptable; there is no
re-encryption pass, so back it up somewhere other than `.env`.

**The embed signing key** only matters if you are embedding the editor in your
own product. Until it is set, the embed endpoints report themselves as
unconfigured and the middleware refuses every token. See
[Embedding](/guides/embedding/).

**The unauthenticated warning** is the one to take seriously the moment this
instance is reachable by anything other than you. It is accurate: with
authentication off there is no account, no API key and no check — reaching the
port is the whole of the authorisation model.

## Turning authentication on

Off is the right default for a first run and the wrong one for anything else.
Turning it on needs four variables set together, on the same start, because
enabling it against an installation that has no account locks you out of your
own server. Add them to `.env`:

```sh
KILASFLOW_AUTH_BOOTSTRAP_EMAIL=you@example.com
KILASFLOW_BOOTSTRAP_PASSWORD=a-real-password
KILASFLOW_AUTH_SIGNING_KEY=       # openssl rand -base64 32
KILASFLOW_AUTH_ENABLED=true
```

Then `docker compose up -d` to recreate the container. The log says what
happened:

```
INFO  created the first account email=you@example.com tenant=default
```

and the unauthenticated warning is gone. Anonymous requests now get `401`, and
signing in works:

```sh
curl -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"a-real-password"}'
```

```json
{"tenantId":"default","kind":"session","userId":"usr_…","email":"you@example.com"}
```

Three things are worth knowing. The account is created only on an installation
that has none, so these variables can stay in `.env` across restarts without
ever handing a second owner to a deployment that already has users — but remove
the password line once you have logged in anyway, because a password in a file
is a password. KilasFlow refuses to start with authentication on and no signing
key rather than coming up and answering every request with `401`. And signing in
over plain `http://localhost` also needs `KILASFLOW_AUTH_COOKIE_INSECURE=true`,
because the session cookie otherwise carries the `__Host-` prefix that browsers
will not accept over an insecure origin — never set that on anything reachable
over a network.

## PostgreSQL instead of SQLite

One command, and nothing to uncomment:

```sh
docker compose -f compose.yaml -f compose.postgres.yaml up -d
```

The overlay adds the database service, waits for its healthcheck before starting
KilasFlow, and passes the connection string through the environment. The
resulting stack answers the same checks as the default one — `/api/v1/ready`
reports `"database":"ok"`, and the log shows `driver=postgres` followed by one
line per applied migration from `migrations/postgres/` (ten files today:
`000001`–`000006` and `000008`–`000011`, so the numbering is not contiguous).

Decide before your first run. There is no migration path between the two
backends: the PostgreSQL schema is created fresh and the stack comes up empty,
so anything already in the SQLite volume stays there and is not visible.

Change `KILASFLOW_POSTGRES_PASSWORD` in `.env` before this is reachable by
anything but you. The database port is deliberately not published to the host,
so the default is contained rather than safe; reach it with `docker compose exec
postgres psql -U kilasflow`.

## PostgreSQL requirements

Three things a PostgreSQL server has to give KilasFlow, in the order they bite:

**pgvector, for vector collections.** Migration `000006_vector_store` runs
`CREATE EXTENSION IF NOT EXISTS vector` and then creates one typed document
table per embedding dimension it supports (384, 768, 1024 and 1536), so a
collection's dimension has to be one of those. The Compose overlay therefore
pins `pgvector/pgvector:pg17`, and an image built from it is what the backup and
restore steps in [Upgrades](/operate/upgrades/) assume.

**The privilege to create that extension — or someone who has it.** pgvector is
not a trusted extension, so `CREATE EXTENSION` needs a superuser. The image
above makes the Compose user one, which is why the quickstart just works. In a
shared or managed database the role KilasFlow connects as usually is not, and
the migration fails at that first statement with a permission error. The fix is
one statement by whoever owns the database, run once:

```sql
CREATE EXTENSION IF NOT EXISTS vector;
```

**A boot without it is not a failed boot.** When the extension is absent,
KilasFlow records the vector migration as skipped, logs a warning naming
`CREATE EXTENSION vector`, and starts: everything except the vector nodes works,
and those refuse with the install message until the extension exists. A later
boot on a server that has it applies the migration normally. This is deliberate
— a database where an owner must be asked for a privilege should not cost you
the rest of the product — but it does mean `/api/v1/ready` reporting `ok` is not
by itself proof that vector search is available.

## Upgrading

Data lives in the `kilasflow_kilasflow-data` volume, not in the container, so
replacing the container does not touch it. Migrations are versioned files under
`migrations/`, applied at startup and recorded, so a newer image applies only
what it has to and says so in the log.

Back up first. Migrations move the schema forward and there is no automatic
rollback, so the only reliable way back from an upgrade you dislike is the copy
you took before it:

```sh
docker compose stop
docker run --rm -v kilasflow_kilasflow-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/kilasflow-data.tar.gz -C /data .
```

Then change the version in `.env` and recreate:

```sh
# KILASFLOW_IMAGE=ghcr.io/kilaslab/kilasflow:v1.3.0
docker compose pull
docker compose up -d
```

Watch the log for the migration lines, and check `/api/v1/health` — it reports
the version actually running, which is the only claim about what you upgraded to
that cannot be wrong.

Going back means restoring the backup as well as the tag: an older binary is not
expected to understand a newer schema.

## Stopping and removing

```sh
docker compose down      # stop, keep the data
docker compose down -v   # stop and delete the data volume too
```

`down -v` is not recoverable. Use it when you are finished trying KilasFlow out,
not to fix a start-up problem.

## Building from source

For working on KilasFlow itself rather than running it. Go 1.27, Node 24 and
pnpm 10; the versions are pinned in `devbox.json`, and
[Devbox](https://www.jetify.com/devbox) will install exactly those if you would
rather not manage them yourself. Node is needed only to build the editor — every
Go target depends on a committed placeholder that stands in for it, so the Go
side alone needs no Node.

```sh
make setup   # Go modules, Air for hot reload, pnpm packages
make dev     # Go on :8080, Vite on :5173
```

Open **http://localhost:5173** — the Vite port, not the Go one. The page there
calls the backend's liveness and readiness endpoints through Vite's proxy and
shows what came back, so a stopped backend or a broken proxy is visible
immediately instead of appearing as an empty editor. The editor itself is at
`/app/workflows`. If port 5173 is taken, `make dev KILASFLOW_WEB_PORT=5180`
moves it.

For a single binary containing the API and the editor:

```sh
make build-all
./bin/kilasflow
```

Running the binary with no configuration at all is supported: it creates
`./data/kilasflow.db` and starts.

## Configuration

`.env.example` covers what a container stack reads and is the file to start
from. `config.example.yaml` is the fuller reference, and the two are
interchangeable — every key has an environment variable, and environment beats
file beats default.

The variable name is `KILASFLOW_<SECTION>_<KEY>`, and only the first underscore
after the prefix separates the section from the key. So
`KILASFLOW_SERVER_READ_HEADER_TIMEOUT` sets `server.read_header_timeout`, not
`server.read.header.timeout` — which would match nothing and be discarded
without a word.

Be aware that `config.example.yaml` is still not complete: it documents ten of
the fourteen sections the code defines, and the four it omits — `outbound`,
`webhook`, `sql` and `credential` — include the ones that bound what a workflow
can reach. A generated configuration reference that cannot fall behind the code
is planned for the [Operate](/operate/configuration/) section.

Next: [Your first workflow](/start/first-workflow/).
