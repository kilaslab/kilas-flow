---
id: FEAT-m94hhx
title: Ship a five-minute Compose quickstart
status: done
priority: high
labels:
    - release
    - platform
deps:
    - FEAT-53fht8
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:47:35Z"
updated: "2026-09-05T18:03:00Z"
---

## Scope

`docker-compose.yml` today is a development file wearing a deployment file's name. It declares `build: {context: ., args: {VERSION: dev}}` and `image: kilasflow:dev`, so `docker compose up` compiles the SPA in Node, cross-copies it into `internal/web/dist`, compiles Go, and only then starts anything. That is minutes of work and a Docker toolchain requirement imposed on somebody whose only goal is to see the product. Once V2-p10-2 publishes an image, that build step is pure cost.

Three further things make it unsuitable as the install route a stranger follows.

Secrets are half-wired. The file passes `KILASFLOW_ENCRYPTION_KEY: ${KILASFLOW_ENCRYPTION_KEY:-}` with an inline comment reading "Generate with: openssl rand -base64 32" — good — but there is no `.env.example`, so the variable is empty unless the reader already knew to create one. `KILASFLOW_EMBED_SIGNING_KEY` is not mentioned at all, and `internal/config/config.go` names it in `Embed.SigningKeyEnv`. Both keys are optional at boot by design: a missing key logs a warning and disables the feature rather than failing startup, which is the right behaviour for a first run and exactly the behaviour that lets somebody deploy a credential store with encryption silently off.

The same comment is stale in a way that misleads: it says the key is "Required once credentials land (Milestone 2)". Credentials have landed — `internal/credentials` and `POST /api/v1/credentials` both ship — so the file tells a reader that a currently-required key is a future concern.

And the Postgres service is present but disconnected. It sits behind `profiles: ["postgres"]` with a `pg_isready` healthcheck and a commented DSN, and nothing wires `depends_on` between the app and it. That is defensible as an opt-in, but it means the tier switch the roadmap treats as a headline capability has no supported path through this file; `scripts/smoke-postgres.sh` reaches it by container network name instead, which a user will not think to copy.

## Acceptance criteria

- [ ] A reader with Docker and no Go, Node or pnpm toolchain reaches a working editor in the browser in under five minutes, with no compilation step.
- [ ] The quickstart pulls a published image by an explicit version tag rather than building from source or tracking a moving tag.
- [ ] A committed `.env.example` names every environment variable the compose file reads, including both `KILASFLOW_ENCRYPTION_KEY` and `KILASFLOW_EMBED_SIGNING_KEY`, each with the exact command that generates a valid value.
- [ ] Starting without an encryption key produces a warning the reader is told to expect and told the consequence of, rather than a silent capability loss.
- [ ] Switching to the PostgreSQL tier is one documented command, and the resulting stack passes the same health checks as the SQLite default.
- [ ] Both services declare healthchecks, and the application waits for a healthy database rather than racing it on the PostgreSQL path.
- [ ] Upgrading to a newer image tag is documented, including what happens to the data volume and what to do first.
- [ ] The stale "Required once credentials land (Milestone 2)" comment is gone, and no comment in the file describes a shipped capability as future work.

## Implementation Plan

Write it as `compose.yaml` — the modern filename Compose v2 prefers — and decide explicitly whether `docker-compose.yml` remains as a build-from-source developer file or is replaced. Recommend keeping both with distinct jobs: `compose.yaml` pulls and runs, `docker-compose.dev.yml` builds. Two files with clear names beat one file with a profile nobody reads.

Pin the image to an exact version tag in the committed file, not `latest`. A quickstart that silently follows a moving tag is a quickstart that breaks for a reader on a day they cannot diagnose.

For the Postgres path, add `depends_on` with `condition: service_healthy` — the healthcheck already exists, only the dependency is missing — and set the DSN through the environment rather than asking the reader to uncomment lines in a YAML file. Uncommenting is the single most error-prone instruction a quickstart can give.

One trap to carry over rather than rediscover: `scripts/smoke-docker.sh` creates its bind-mount directory `chmod 777` because the image runs as `nonroot` and cannot write into a host directory owned by the invoking user. A named volume avoids this entirely, which is why the current file uses one. If the quickstart offers a bind mount for easier backups, it must document the ownership problem, or a reader will get a permission error on first run with nothing in the file to explain it.

This ticket owns only the artifact and its correctness. The prose that surrounds it — installation page, tier explanation, upgrade guide — belongs to V2-p10-12, which depends on this. Keep the compose file and `.env.example` self-explanatory anyway, since they are read far more often than any page about them.

## References

- Roadmap plan, p10 section, entry V2-p10-3: `.pine/roadmap.md`.
- `docker-compose.yml` — the `build:`/`image: kilasflow:dev` pair, the `postgres` profile with its `pg_isready` healthcheck and commented DSN, and the stale Milestone 2 comment.
- `Dockerfile` — `VOLUME ["/app/data"]`, `USER nonroot:nonroot`, and the baked `KILASFLOW_SERVER_HOST`, `KILASFLOW_SERVER_PORT`, `KILASFLOW_DATABASE_DSN` and `KILASFLOW_LOG_FORMAT` defaults the compose file must not contradict.
- `internal/config/config.go` — `Security.EncryptionKeyEnv`, `Embed.SigningKeyEnv`, and the boot behaviour when either is absent.
- `config.example.yaml` — the file-based equivalent of what the quickstart sets through the environment.
- `scripts/smoke-docker.sh` — the `chmod 777` bind-mount workaround and the health assertions the quickstart should satisfy.
- `scripts/smoke-postgres.sh` — the working PostgreSQL wiring, reachable today only by reading the script.

## Work evidence

### What shipped

`docker-compose.yml` is gone, replaced by three files with one job each. The
ticket recommended `compose.yaml` plus a `docker-compose.dev.yml`; two naming
families in one directory would have been worse than one, so both companions
follow the Compose v2 name:

- `compose.yaml` — pulls and runs. One service, a named volume, `env_file`.
- `compose.build.yaml` — overlay that builds the image from the checkout.
- `compose.postgres.yaml` — overlay that swaps SQLite for PostgreSQL.

Plus `.env.example` (new, and `.gitignore` already anticipated it with
`!.env.example`), `auth` and `embed` sections in `config.example.yaml`, a
rewritten `docs/src/content/docs/start/install.md`, and a README quickstart that
leads with Docker instead of `make setup`.

`scripts/smoke-postgres.sh` had to change, and the reason is a trap worth
naming: Compose v2 prefers `compose.yaml` over `docker-compose.yml`, so merely
adding the new file silently changed what every bare `docker compose` in the
repository resolved to. That script reached PostgreSQL through
`--profile postgres` against the default file; it now names both files
explicitly and pins the three PostgreSQL credentials it also spells out in its
DSNs, so a developer's own `.env` cannot point the container at one password
while the test connects with another.

### The run, start to finish

From an empty state — image deleted, no `.env`, no data volume:

```
=== T0 17:57:21 — nothing built, no .env, no volume ===
$ cp .env.example .env
$ printf 'KILASFLOW_ENCRYPTION_KEY=%s\n' "$(openssl rand -base64 32)" >> .env
=== T1 17:57:21 — .env written, building and starting ===
$ docker compose -f compose.yaml -f compose.build.yaml up -d --build
 Volume kilasflow_kilasflow-data Created
 Container kilasflow-kilasflow-1 Started
=== T2 17:57:34 — container started, polling readiness ===
=== T3 17:57:43 — ready after 0s of polling ===
{"status":"ok","database":"ok"}

$ curl -X POST localhost:8080/api/v1/workflows -d @workflow.json
POST /api/v1/workflows -> HTTP 201
wf_01a072b8-1a5c-7603-b136-bca3e6d68186
$ curl -X POST localhost:8080/api/v1/workflows/wf_…/run -d '{}'
POST .../run -> HTTP 202
exec_01a072b8-3230-7371-8511-df1552d1e2ed
=== T4 17:57:51 — reading the execution ===
{
  "status": "succeeded",
  "output": {"greet": [[{"json": {"greeting": "hello from the quickstart"}, …}]]}
}
=== T5 17:57:57 — done ===
```

**36 seconds** from nothing to a workflow that had run and returned its output.
13 of those were the build and start, 9 were waiting for readiness, and the rest
was creating and running a two-node workflow (manual trigger into a Set node).

That reused the layer cache. A genuine cold build measured separately with
`--no-cache` took **39 seconds**, so a first-ever build is around a minute on
this machine — an Apple Silicon laptop that had already pulled
`node:24.16-alpine`, `golang:1.27-alpine` and the distroless base. Those pulls
are the one part that depends on the reader's connection rather than their CPU,
and they are why the five-minute budget is the right one to promise even though
the measured number is under a minute.

### The first-boot log, unedited

With no `.env` at all — which works, because `env_file` is declared
`required: false`:

```
{"level":"INFO","msg":"starting kilasflow","version":"local"}
{"level":"INFO","msg":"database connected","driver":"sqlite"}
{"level":"INFO","msg":"applied migration","version":1,"name":"baseline"}
{"level":"INFO","msg":"applied migration","version":2,"name":"workflow_history"}
{"level":"INFO","msg":"applied migration","version":3,"name":"identity"}
{"level":"WARN","msg":"credential encryption key is not set; credential storage is disabled","variable":"KILASFLOW_ENCRYPTION_KEY"}
{"level":"WARN","msg":"embed signing key is not set; embedded editor sessions are disabled","variable":"KILASFLOW_EMBED_SIGNING_KEY"}
{"level":"WARN","msg":"the API is unauthenticated: anyone who can reach this port owns the installation","enable_with":"KILASFLOW_AUTH_ENABLED=true"}
{"level":"INFO","msg":"http server listening","addr":"0.0.0.0:8080"}
```

All three warnings are quoted verbatim in `.env.example` and on the install
page, next to what each one costs. With the encryption key set, the first
disappears and `POST /api/v1/credentials` returns a stored credential with its
value masked — which is the proof the key reached the container through
`env_file` rather than being read as an empty string.

### Authentication, proven rather than described

Four variables added to `.env` and `docker compose up -d`:

```
{"level":"INFO","msg":"created the first account","email":"you@example.com","tenant":"default"}
GET /api/v1/workflows -> HTTP 401
$ curl -X POST .../api/v1/auth/login -d '{"email":"you@example.com","password":"…"}'
{"tenantId":"default","kind":"session","userId":"usr_01a072b5-…","email":"you@example.com"}
GET /api/v1/workflows -> HTTP 200   (with the session cookie)
```

The unauthenticated warning is gone and the operator is not locked out. The
default stays off, as the code intends.

### PostgreSQL

```
$ docker compose -f compose.yaml -f compose.postgres.yaml up -d
 Container kilasflow-postgres-1 Waiting
 Container kilasflow-postgres-1 Healthy
 Container kilasflow-kilasflow-1 Started
{"level":"INFO","msg":"database connected","driver":"postgres"}
{"level":"INFO","msg":"applied migration","version":1,"name":"baseline"} …
{"status":"ok","database":"ok"}
```

`depends_on: condition: service_healthy` works — the `Waiting`/`Healthy` pair is
the race the old file had no way to avoid. `KILASFLOW_SMOKE_SKIP_BUILD=1 sh
scripts/smoke-postgres.sh` also passes against the rewritten wiring
(`smoke-postgres: passed`).

`go build ./...` and `go test ./... -count=1` both pass (40 packages ok, 0 FAIL);
no Go file was touched. `config.example.yaml` was verified by starting the server
against it — it loads, serves, and honours `auth.enabled: false`.

### Acceptance criteria: three cannot be met, and why

**"Pulls a published image by an explicit version tag."** Not possible. `git tag
-l` is empty, `git ls-remote --tags origin` returns nothing, and
`ghcr.io/kilaslabs/kilasflow` holds no image — the release pipeline exists but
has never run. Writing `docker pull ghcr.io/…` would have told readers to run a
command that fails. The quickstart therefore builds through
`compose.build.yaml` and says plainly why, in the compose file, in
`.env.example` and in a note on the install page. The pull path is already
wired: set `KILASFLOW_IMAGE` in `.env` and drop the overlay. When the first
`vX.Y.Z` lands, this becomes a one-line change plus deleting three paragraphs.

**"No compilation step."** Same cause. Nothing is compiled on the *host* — the
reader still needs only Docker — but a build does run, and it is honestly
labelled and measured rather than hidden.

**"Both services declare healthchecks."** PostgreSQL does. KilasFlow cannot.
The runtime image is `gcr.io/distroless/static-debian12:nonroot`: no shell, no
curl, no wget, and `cmd/kilasflow/main.go` defines only `-config` and
`-version`. Every healthcheck expressible against that image either fails always
or passes the instant the container starts, and the second is worse than none —
a `depends_on: service_healthy` pointing at it would report a server ready
before it had opened its listener. The compose file says this in place of the
healthcheck, and readiness is asserted from outside with `/api/v1/ready`, which
is what `scripts/smoke-docker.sh` has always done. Giving the binary a probe
subcommand, or adding `HEALTHCHECK` to the Dockerfile, is Go and Dockerfile work
this ticket did not own.

The other five criteria are met: `.env.example` is committed and names every
variable with its generating command, the missing-key warnings are quoted with
their consequences, the PostgreSQL tier is one command with nothing to
uncomment, `depends_on: service_healthy` closes the race, upgrading is documented
including the volume and the backup to take first, and the stale "Required once
credentials land (Milestone 2)" comment left with the file that carried it.

### Stale premises found in the ticket

- "Once V2-p10-2 publishes an image, that build step is pure cost" — that
  ticket's pipeline shipped, but it has published nothing, so the build step is
  still the only way to get an image.
- The ticket's plan to keep `docker-compose.yml` alongside `compose.yaml` would
  have created a file Compose silently ignores. Compose v2 resolves
  `compose.yaml` first, so the old name can only ever be dead weight now.

### Found while running it: a critical defect on the PostgreSQL tier

Creating and running the identical workflow against PostgreSQL leaves the
execution at `"status":"running"` for ever, with both node runs reporting
`succeeded` and nothing logged. `UpdateRuntime` builds a `CASE` of untyped
placeholders that PostgreSQL types as `text` and refuses to assign into the
`bytea` `output`/`error` columns; the whole `UPDATE` is rejected at parse time,
the worker loop discards the error, and after the 60-second lease expires the
row is reclaimed and the workflow runs again — for ever. Filed as **BUG-br7ggc**
with the root cause, the exact lines and the missing test coverage. It is
unrelated to this ticket's changes: it reproduces on any PostgreSQL install and
was simply unreachable before, because nothing gave the tier a documented path.

### Files changed

`pine close --evidence` generated a diffstat of 538 files here, spanning every
session that has committed to this branch. It was replaced with the accurate
list, because evidence that credits one ticket with another's work is worse than
no evidence at all:

- `compose.yaml`, `compose.build.yaml`, `compose.postgres.yaml` — new.
- `docker-compose.yml` — deleted.
- `.env.example` — new.
- `config.example.yaml` — `auth` and `embed` sections added.
- `scripts/smoke-postgres.sh` — names both compose files, pins its credentials.
- `docs/src/content/docs/start/install.md` — rewritten around the quickstart.
- `README.md` — the `## Quick start` section only.
- `.pine/tickets/BUG-br7ggc.md` — new, the defect found while running this.

No Go file, nothing under `web/` or `sdk/`, no Makefile target, no
`.github/` workflow, no `docs/astro.config.mjs`, and no documentation section
other than `start/`.
