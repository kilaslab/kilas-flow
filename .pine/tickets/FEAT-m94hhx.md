---
id: FEAT-m94hhx
title: Ship a five-minute Compose quickstart
status: todo
priority: high
labels:
    - release
    - platform
deps:
    - FEAT-53fht8
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:47:35Z"
updated: "2026-09-05T11:47:35Z"
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
