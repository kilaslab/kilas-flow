---
title: Install
description: Running KilasFlow from source, and what each of the two builds gives you.
---

There is no published binary and no published container image yet, so today
every install is a build from source. Both of the routes below are exercised by
the repository's own smoke checks on every change, so they are known to work
rather than merely written down.

:::note
A packaged Docker Compose quickstart is planned and does not exist yet. When it
lands it will be the recommended route and this page will lead with it.
:::

## What you need

Go 1.27, Node 24 and pnpm 10. The versions are pinned in `devbox.json`, and
[Devbox](https://www.jetify.com/devbox) will install exactly those for you if
you would rather not manage them yourself — but it is optional, and the Makefile
works fine against a locally installed toolchain.

Node is needed only to build the editor. If you are working on the Go side alone
you can skip it, because every Go target depends on a committed placeholder file
that stands in for the compiled editor.

## Development

```sh
git clone https://github.com/kilaslabs/k-flow
cd k-flow
make setup   # Go modules, Air for hot reload, pnpm packages
make dev     # Go on :8080, Vite on :5173
```

Open **http://localhost:5173** — the Vite port, not the Go one. The page there
calls the backend's liveness and readiness endpoints through Vite's proxy and
shows what came back, so a stopped backend or a broken proxy is visible
immediately instead of appearing as an empty editor. The editor itself is at
`/app/workflows`.

If port 5173 is already taken, `make dev KILASFLOW_WEB_PORT=5180` moves it.

## Production

```sh
make build-all
./bin/kilasflow
```

`build-all` compiles the editor, copies it into the embed directory and then
builds the binary around it. Everything is served from
**http://localhost:8080** — the API, the editor, and the OpenAPI document.

Running the binary with no configuration at all is supported and is the intended
first experience: it will create `./data/kilasflow.db` and start.

## Two things to set before this is real

Neither is required to start, and both matter.

**An encryption key.** Credentials are encrypted at rest with AES-256-GCM and
the key comes from the environment, never from a config file:

```sh
export KILASFLOW_ENCRYPTION_KEY="$(openssl rand -base64 32)"
```

Without it the server still starts, but credential storage is switched off and
says so in the log. That is deliberate — refusing to boot would make the key
mandatory for somebody who only wants to look at the editor, and shipping a
default key would mean shipping secrets encrypted with a public one.

**Something in front that authenticates.** The API has no authentication of its
own. See [What KilasFlow is](/start/what-kilasflow-is/) for what that does and
does not mean.

## Configuration

Copy `config.example.yaml` to `config.yaml`, or set environment variables:

```sh
KILASFLOW_SERVER_PORT=9090 \
KILASFLOW_DATABASE_DRIVER=postgres \
KILASFLOW_DATABASE_DSN='postgres://kilasflow:pw@localhost:5432/kilasflow' \
./bin/kilasflow
```

Environment beats file, file beats defaults. The variable name is
`KILASFLOW_<SECTION>_<KEY>`, and only the first underscore after the prefix
separates the section from the key — so `KILASFLOW_SERVER_READ_HEADER_TIMEOUT`
sets `server.read_header_timeout`, not `server.read.header.timeout`, which would
match nothing and be discarded silently.

Be aware that `config.example.yaml` is not complete: it documents seven of the
twelve sections the code defines. The five it omits include `outbound`,
`webhook` and `embed`, which are the security-relevant ones. A generated
configuration reference that cannot fall behind the code is planned for the
[Operate](/operate/configuration/) section.
