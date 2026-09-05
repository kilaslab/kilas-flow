---
title: Deployment
description: Not yet written. What can be deployed today and how it is currently proven.
sidebar:
  order: 1
---

:::caution[This page has not been written yet]
:::

## What will be here

How to run KilasFlow in production: the container image, choosing between SQLite
and PostgreSQL, what to put in front of it, health checking, and what to back up.

## What exists today

The repository builds a three-stage distroless image that runs as a non-root
user, and `make docker` builds it. Nothing publishes it yet — there is no
registry namespace and no published tag — so deploying it means building it
yourself.

Two endpoints are meant for a load balancer or an orchestrator, and the
distinction between them is deliberate:

- `GET /api/v1/health` is liveness. It answers `200` for as long as the process
  is serving and deliberately touches no dependency, so a database outage does
  not get the process killed and restarted into the same outage.
- `GET /api/v1/ready` is readiness. It answers `503` when the database is
  unreachable, which is the signal to stop sending it traffic.

Four smoke checks in the Makefile prove the deployable artefacts on every
change, and they are the most reliable description of what actually works:
`smoke-sqlite` boots the binary against a fresh database, `smoke-dev` proves the
development proxy, `smoke-docker` proves the image can serve and write to a bind
mount as a non-root user, and `smoke-postgres` proves the image against a
disposable PostgreSQL under Compose.

Note that the `Code` node needs a Go toolchain at run time and the distroless
image does not have one. This is not a failure at execution time — the server
reports the node as unavailable through the node catalogue, so the editor can
say so.
