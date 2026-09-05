# KilasFlow

An embeddable open-source workflow engine for APIs, AI agents, and SaaS products.

**Repository:** https://github.com/kilaslabs/kilas-flow

Go-native · single binary · API-first · embeddable · white-label · SQLite by default

> **Status: scaffolding.** The HTTP server, configuration, persistence, generated
> API documentation and the SPA shell are in place and wired together. The
> workflow engine itself starts at Milestone 1 — see [Roadmap](#roadmap).

## Quick start

```bash
make setup          # go modules, Air, pnpm packages
make dev            # Go on :8080, Vite on :5173
```

`make setup` installs Air into the Go tool bin directory. `make dev` resolves
that directory itself, so it does not require adding `GOBIN` or `GOPATH/bin`
to your shell `PATH`. Set `AIR=/path/to/air make dev` only to override it.

Open **<http://localhost:5173>**. The page calls the Go backend through the Vite
proxy and reports what it gets back, so a broken proxy or a stopped backend is
visible immediately.

Production build — one binary containing the API and the editor:

```bash
make build-all
./bin/kilasflow
```

Everything is then served from <http://localhost:8080>.

## Endpoints

| Path | Description |
| --- | --- |
| `GET /api/v1/health` | Liveness. Always 200 while the process serves. |
| `GET /api/v1/ready` | Readiness. 503 when the database is unreachable. |
| `GET /docs` | API reference, rendered with Scalar |
| `GET /api/openapi.json` | OpenAPI 3.1 document (also `.yaml`, and 3.0.3 variants) |
| `POST /webhook/:id` | Reserved for workflow triggers — currently 501 |
| `GET /*` | The editor SPA, with history-API fallback |

The OpenAPI document is generated from the Go handler types rather than
maintained alongside them, so the published contract cannot drift from the code
that serves it.

## Configuration

KilasFlow runs with no configuration at all. To change something, copy
`config.example.yaml` to `config.yaml`, or set an environment variable:

```bash
KILASFLOW_SERVER_PORT=9090 \
KILASFLOW_DATABASE_DRIVER=postgres \
KILASFLOW_DATABASE_DSN='postgres://kilasflow:pw@localhost:5432/kilasflow' \
./bin/kilasflow
```

Environment variables take precedence over the file, which takes precedence over
the defaults. The variable name is `KILASFLOW_<SECTION>_<KEY>`.

Credentials are encrypted at rest with AES-256-GCM. The master key comes from
the environment, never from the config file:

```bash
export KILASFLOW_ENCRYPTION_KEY="$(openssl rand -base64 32)"
```

## Layout

```
cmd/kilasflow/            entrypoint; wiring only
internal/
  api/                HTTP transport, routes, generated OpenAPI
  config/             layered configuration
  database/           GORM setup for SQLite and PostgreSQL
  web/                embeds and serves the built SPA
  engine/             workflow execution            (Milestone 1)
  node/               node contract and registry    (Milestone 1)
  workflow/           canonical workflow document   (Milestone 1)
  execution/          run and node-run records      (Milestone 1)
  repository/         persistence interfaces        (Milestone 1)
  expression/         {{ $json.x }} evaluation      (Milestone 2)
  credentials/        encrypted secret storage      (Milestone 2)
  webhook/            inbound trigger routing       (Milestone 2)
  scheduler/          cron triggers                 (Milestone 2+)
  ai/                 agent contracts               (Milestone 4)
  ai/maf/             Microsoft Agent Framework adapter
  runcode/            Go Code node, WASM sandbox    (Milestone 5)
  embed/              iframe session security       (Milestone 6)
nodes/                built-in node implementations
web/                  SvelteKit SPA
```

The engine deliberately does not import `internal/api`, GORM, or any agent
framework. Persistence is reached through repository interfaces and the agent
runtime through `internal/ai`, so either can be replaced without touching
execution semantics.

## Development

| Command | Effect |
| --- | --- |
| `make dev` | Backend and frontend with hot reload |
| `make test` | Go tests with the race detector |
| `make lint` | `go vet`, `gofmt`, and `svelte-check` |
| `make build-all` | SPA + binary |
| `make docker` | Container image |
| `make smoke-sqlite` | Embedded binary against a fresh temporary SQLite database |
| `make smoke-dev` | Vite development proxy against a temporary Go server |
| `make smoke-docker` | Non-root Docker image with a temporary persisted SQLite bind mount |
| `make smoke-postgres` | Docker image against a temporary Compose PostgreSQL service |

Devbox is supported but not required — `devbox.json` pins the toolchain if you
want it, and the Makefile works with a local Go, Node and pnpm otherwise.

### Production smoke checks

The `smoke-*` commands are intentionally independent and clean up only
their own temporary directory, container, and Compose project. They verify the
same origin serves liveness, readiness, OpenAPI, and the SPA fallback. Docker
checks require a running Docker daemon; `smoke-postgres` starts a disposable
PostgreSQL service rather than touching a developer database, and verifies the
GORM AutoMigrate probe against it before starting KilasFlow.

By default the Docker checks rebuild `kilasflow:latest`. Set
`KILASFLOW_SMOKE_SKIP_BUILD=1` only when rerunning a diagnostic against the
already-built image.

### Two rules worth knowing

**Frontend code always uses relative URLs.** `fetch('/api/v1/workflows')`, never
`fetch('http://localhost:8080/...')`. Production serves the API and the SPA from
one origin, and an embedded editor runs on whichever origin the host mounts it
on, so an absolute URL breaks both.

**`internal/web/embed.go` uses `//go:embed all:dist`.** The `all:` prefix is
required: SvelteKit emits every asset into `_app/`, and `go:embed` skips
underscore-prefixed paths without it. Dropping the prefix still compiles and
still serves `index.html` — the only symptom is an unstyled, non-interactive
page in production. `TestEmbedIncludesUnderscoreAndDotPaths` guards this.

### The docs page is self-hosted on purpose

`/docs` is rendered by `internal/api/docs.go` rather than by Huma's built-in
endpoint, because every built-in renderer loads its JavaScript from unpkg. The
Scalar bundle is copied out of `node_modules` into the frontend build by
`web/scripts/vendor-docs.mjs` (on install and before every build), and a strict
`Content-Security-Policy` on the response blocks the webfont and registry calls
Scalar still attempts at runtime.

The result is a documentation page that makes **zero external requests** — it
works air-gapped, and an embedding customer's traffic never reaches a third
party. `TestDocsUIHasNoExternalDependencies` guards the page; the CSP guards
the runtime. Cost: about 3.6 MB of the binary.

### Receiving Telegram updates on a laptop

The Telegram trigger has a **Delivery** parameter with two values, and which one
you want depends on whether the machine has a public HTTPS address.

**Webhook** is the default and the only one for production. Activating the
workflow calls `setWebhook` with the workflow's own URL — built from
`server.public_url`, so that has to be the address Telegram can reach — plus the
updates you selected and a secret derived from the bot token and the route.
Telegram returns that secret in `X-Telegram-Bot-Api-Secret-Token` on every
delivery and the endpoint refuses anything that does not match, so a leaked URL
is not enough to inject updates. Telegram accepts only HTTPS; activation says so
by name rather than passing along the Bot API's own error.

To use it from a laptop, put a tunnel in front:

```sh
cloudflared tunnel --url http://localhost:8080   # or: ngrok http 8080
KILASFLOW_SERVER_PUBLIC_URL=https://<the-tunnel-host> make run
```

**Polling** needs no public address at all. The server calls `getUpdates` in a
long poll for as long as the workflow is active, and everything downstream of
the update is identical — same item shape, same restriction filters, same
downloads. Two things to know: it runs in **one process**, so it is wrong for a
deployment running several workers, and Telegram refuses `getUpdates` while a
webhook is registered — so activating a polling trigger deletes the webhook
first, and re-activating a webhook trigger puts it back.

Either way the bot token is a `telegramApi` credential. Its Base URL field is
normally empty; set it only if you run [Telegram's own local Bot API
server](https://core.telegram.org/bots/api#using-a-local-bot-api-server).

## Roadmap

| Milestone | Scope |
| --- | --- |
| 0 | Foundation — configuration, persistence, HTTP, SPA, single binary |
| 1 | Workflow core — CRUD, node registry, graph runner, Manual/Set/IF/Merge |
| 2 | API automation — HTTP Request, webhooks, credentials, expressions |
| 3 | Database nodes — PostgreSQL, MySQL, SQLite |
| 4 | AI — agent runtime adapter, chat model, memory, tools |
| 5 | Go Code node — WASM compilation and sandboxed execution |
| 6 | Embedding — `/embed/:id`, sessions, postMessage, white-label |
| 7 | Interop — n8n JSON import and export |

## License

Apache-2.0. See [LICENSE](LICENSE).
