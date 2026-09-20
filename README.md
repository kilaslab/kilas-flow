# KilasFlow

An embeddable open-source workflow engine for APIs, AI agents, and SaaS products.

[![CI](https://github.com/kilaslab/kilas-flow/actions/workflows/ci.yml/badge.svg)](https://github.com/kilaslab/kilas-flow/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8.svg?logo=go&logoColor=white)](go.mod)

**Repository:** https://github.com/kilaslab/kilas-flow

Go-native · single binary · API-first · embeddable · white-label · SQLite by default

The V1 platform is built. The execution engine, the canvas editor, the node
registry, encrypted credentials, the expression evaluator, webhook and cron
triggers, the database and AI nodes, the WebAssembly Go Code node, the
white-label embedded editor, the n8n importer and exporter, the WAHA and
Telegram node packs and the declarative routing interpreter that runs them are
all in the tree and covered by tests. Current work is n8n workflow
compatibility; `.pine/roadmap.md` is where that is planned and tracked.

## Quick start

Docker is the only toolchain you need — no Go, no Node, no pnpm.

```bash
cp .env.example .env
printf 'KILASFLOW_ENCRYPTION_KEY=%s\n' "$(openssl rand -base64 32)" >> .env

# No release has been published yet, so build the image once:
docker compose -f compose.yaml -f compose.build.yaml up -d --build

curl -fsS http://localhost:8080/api/v1/ready
```

Measured from an empty state on an Apple Silicon laptop, that is 36 seconds to a
workflow you have run — around a minute if nothing is cached, plus whatever your
connection takes to pull the three base images the first time.

Then open **<http://localhost:8080/app/workflows>**.

The build overlay is temporary. The multi-architecture image pipeline and the
release workflow are both in the tree, but no version tag has ever been pushed,
so there is nothing in `ghcr.io/kilaslab/kilasflow` to pull yet. Once there is,
set `KILASFLOW_IMAGE` in `.env` to the exact `vX.Y.Z` tag and the whole thing
becomes `docker compose up -d` against a pulled image, which is seconds rather
than minutes.

PostgreSQL instead of the default SQLite is one command and nothing to
uncomment — but decide before your first run, because there is no migration
between the two backends and the stack comes up empty:

```bash
docker compose -f compose.yaml -f compose.postgres.yaml up -d
```

`.env.example` is the reference for what a running stack reads, including the
three keys that each silently disable a capability while they are unset, and the
four variables that turn authentication on together. Authentication is off by
default: with it off, anyone who can reach the port owns the installation, and
the server says so in its log at every start. The full walkthrough, including
what to expect on the first boot and how to upgrade, is
[docs/src/content/docs/start/install.md](docs/src/content/docs/start/install.md).

Working on KilasFlow itself rather than running it?

```bash
make setup          # go modules, Air, pnpm packages
make dev            # Go on :8080, Vite on :5173
```

Open **<http://localhost:5173>** — the Vite port, not the Go one. The page there
calls the backend's liveness and readiness endpoints through the Vite proxy and
shows what came back, so a broken proxy or a stopped backend is visible
immediately rather than as an empty editor.

`make setup` installs Air into the Go tool bin directory. `make dev` resolves
that directory itself, so it does not require adding `GOBIN` or `GOPATH/bin` to
your shell `PATH`. Set `AIR=/path/to/air make dev` only to override it. For a
single binary containing the API and the editor, `make build-all` then
`./bin/kilasflow`, served from <http://localhost:8080>. See
[Development](#development) for the rest.

## Endpoints

| Path | What it is |
| --- | --- |
| `GET /api/v1/health` | Liveness. 200 while the process serves; touches no dependency. |
| `GET /api/v1/ready` | Readiness. 503 when the database is unreachable or a datastore migration is outstanding; the `datastores` block reports the schema-version spread, on the 200 and on the migration-outstanding 503. |
| `GET /docs` | The API reference, rendered from the OpenAPI document. |
| `GET /api/openapi.json` | OpenAPI 3.1. Also `.yaml`, and `/api/openapi-3.0.json` / `.yaml` for tools that cannot read 3.1. |
| `/webhook/{route}` | Inbound workflow triggers. |
| `/resume/{token}` | Where a suspended execution resumes: a single-use token minted per wait, and what the run's own `$execution.resumeUrl` points at. |
| `/approve/{token}` | The page a human decides a `Wait` at; it calls the resume URL on their behalf. |
| `GET /*` | The editor SPA, with history-API fallback. |

Everything else is under `/api/v1` — 63 operations in 13 tags at this commit,
across workflows, executions, credentials, schedules, datastores, node types,
auth, embed sessions and n8n import/export — and is deliberately not listed
here. This table used to name two operations and give no sign that the rest
existed, and it described the webhook route as returning 501 long after it had
stopped doing so — the ordinary fate of a hand-maintained index of an API that is
still growing. `/docs` and `/api/openapi.json` are generated from the same Go
types that serve the requests, so they cannot drift the way this table did; ask
the server for `/api/openapi.json` rather than trusting a count in prose, or run
`make generate-api-reference` to regenerate
`docs/src/content/docs/reference/api.md` from a freshly built binary.

### The webhook route

`/webhook/{route}` is the live inbound surface. `{route}` is an opaque hex
segment minted per trigger node the first time its workflow is activated, and
then reused for the life of that node — reissuing it on each activation would
change the public URL every time a workflow was toggled off and on, breaking
every sender already configured against it. The route carries 16 bytes of
entropy because this endpoint is very often unauthenticated, and being
unguessable is then the only defence it has.

Every request that does not resolve to an active binding gets the same 404 with
the same body. An inactive workflow, a deleted one, one that was never
activated, and a request with the wrong HTTP method are indistinguishable from
outside, so the endpoint cannot be used to enumerate which workflows exist. The
accepted method comes from the trigger node's own configuration rather than
being fixed at `POST`. A request is matched by method and route only, never by
the trigger's `path` label, which is display metadata that two tenants can
share.

Beyond that, a trigger type can verify a delivery before it becomes an
execution — Telegram's `X-Telegram-Bot-Api-Secret-Token`, WAHA's HMAC over the
raw request body — and a failed check is a 401 with no run recorded. A delivery
the trigger was configured to filter out is answered `200` instead, because it
was received correctly and deliberately not acted on, and telling the sender
otherwise would make it retry. Retries carrying a delivery identifier the
trigger names are deduplicated, so a sender that gives up waiting and repeats
itself does not run the workflow again. Where unguessability is not enough, the
`webhook.require_auth` deployment setting refuses every delivery to a trigger
that does not authenticate its own callers, with a `403` naming the workflow and
the fix.

Two limits bound a request: `webhook.max_body_bytes` (1 MiB) and
`webhook.response_timeout` (30 seconds, for a workflow configured to answer
from its own graph).

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
the defaults. The variable name is `KILASFLOW_<SECTION>_<KEY>`, and only the
first underscore after the prefix separates the section from the key — the rest
belong to the key. `KILASFLOW_SERVER_READ_HEADER_TIMEOUT` therefore sets
`server.read_header_timeout`, not `server.read.header.timeout`, which would
match no field and be discarded without a word.

Credentials are encrypted at rest with AES-256-GCM. The master key comes from
the environment, never from the config file:

```bash
export KILASFLOW_ENCRYPTION_KEY="$(openssl rand -base64 32)"
```

Without it the server still starts, but credential storage is switched off and
says so in the log at startup. Refusing to boot would make the key mandatory for
anyone who only wants to look at the editor; defaulting to a built-in key would
mean shipping secrets encrypted with a key that is public.

## Layout

```
cmd/kilasflow/          entrypoint; wiring only
cmd/nodepackgen/        generates a node pack from an OpenAPI document
internal/
  api/                  HTTP transport, routes, generated OpenAPI, docs page
  api/handlers/         the operations under /api/v1
  api/middleware/       request identity, access logging, panic recovery
  config/               layered configuration
  database/             GORM setup for SQLite and PostgreSQL
  repository/           persistence interfaces and their GORM implementations
  web/                  embeds and serves the built SPA

  workflow/             the canonical workflow document: shape, validation, versions
  node/                 the node contract and the registry
  property/             the description language for a configurable field
  engine/               executes a compiled workflow graph
  execution/            a run and its per-node runs
  events/               the execution event contract and the in-process broker
  expression/           evaluates the `{{ … }}` templates a parameter may carry
  conditions/           the filter language IF, Filter and Switch share
  datetime/             the one place instants become text and text becomes instants
  binary/               payload storage for items that refer to files

  credentials/          stores and resolves the secrets workflows reference
  auth/                 sessions, API keys and the principal a request carries
  datastore/            the workflow-facing data store: columns, rows, filters
  webhook/              maps an inbound request to its workflow and trigger node
  scheduler/            runs cron-triggered workflows
  embed/                issues and validates iframe editor sessions
  safehttp/             outbound clients that refuse to reach internal infrastructure

  routing/              interprets declarative node metadata as an HTTP request
  nodepack/             the on-disk format of a generated node pack
  loadoptions/          resolves a property's selectable values at edit time
  sqlbuild/             turns a described operation into a bound SQL statement
  sqlnode/              connects workflows to databases the user configures
  runcode/              compiles and executes user-supplied Go for the Code node
  ai/                   agent contracts and the built-in tool loop
  interop/n8n/          converts between n8n workflow JSON and our document
  guardrails/           checks for invariants no single package owns

nodes/                  the built-in node definitions and executors
packs/                  declarative node packs — WAHA (generated), Telegram (hand-written)
third_party/            vendored upstream specs the packs are generated from
sdk/                    @kilasflow/sdk, the TypeScript host SDK
pkg/sdk/                reserved for the guest-side Go module a WASM pack author will import
schemas/                the published workflow JSON Schema
web/                    SvelteKit SPA
```

`internal/ai/maf/` is a `doc.go` and nothing else. It reserves the one place
allowed to import Microsoft Agent Framework for Go, so that when the adapter is
written the churn of a preview-stage dependency is confined to a single package.
The runtime that actually serves the AI nodes today is `ai.LoopRuntime`, a
deterministic tool loop behind the same `ai.AgentRuntime` interface.

The engine does not import `internal/api` or `internal/ai`: it reaches
persistence through the `internal/repository` interfaces and the agent runtime
through an injected `ai.AgentRuntime`, so either can be replaced without
touching execution semantics. GORM is nonetheless in the engine's transitive
closure, because `internal/repository` holds the interfaces and their GORM
implementations in one package. `internal/workflow` and `internal/execution` —
the document and the run record — are free of it.

## Development

`make help` lists every target; the ones worth knowing:

| Command | Effect |
| --- | --- |
| `make dev` | Backend and frontend with hot reload |
| `make test` | Go tests with the race detector |
| `make lint` | `go vet`, `gofmt`, and `svelte-check` |
| `make build-all` | SPA + binary |
| `make docker` | Container image |
| `make node-packs` | Regenerate the committed packs from their vendored specs |
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
the runtime. Cost: 3.6 MB of the binary, which is what `vendor-docs.mjs` reports
for the bundle it copies. A binary built without a frontend build serves a
fallback page pointing at the raw specification, rather than a blank screen.

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

## Documentation

The documentation site is an Astro Starlight project in `docs/` — `make docs`
serves it, `make docs-build` builds it and validates every internal link. It has
no public URL yet, so it is read from the tree:

| Where to start | Path |
| --- | --- |
| What KilasFlow is, and what it deliberately is not | [`docs/src/content/docs/start/what-kilasflow-is.md`](docs/src/content/docs/start/what-kilasflow-is.md) |
| Install, and a first workflow that runs | [`docs/src/content/docs/start/`](docs/src/content/docs/start/) |
| Execution model, items and lineage, expressions, the node registry | [`docs/src/content/docs/concepts/`](docs/src/content/docs/concepts/) |
| Embedding the editor, migrating from n8n, authoring nodes | [`docs/src/content/docs/guides/`](docs/src/content/docs/guides/) |
| Configuration, deployment, upgrades, security | [`docs/src/content/docs/operate/`](docs/src/content/docs/operate/) |
| API reference, expression grammar, node packs | [`docs/src/content/docs/reference/`](docs/src/content/docs/reference/) |

The API reference under `docs/src/content/docs/reference/api/` is generated from
the running binary's OpenAPI document by `make generate-api-reference`; CI fails
if it is stale.

## Community

- **[Contributing](CONTRIBUTING.md)** — setup, the checks a change has to pass,
  and the commit, test and documentation conventions.
- **[Code of conduct](CODE_OF_CONDUCT.md)** — Contributor Covenant 3.0, and it
  applies to issues and pull requests as much as to any other project space.
- **[Security](SECURITY.md)** — the private disclosure path, what is in scope,
  and what the project does and does not promise. Never a public issue.
- **[Changelog](CHANGELOG.md)** — no release tag has been cut yet, so it is a
  single `[Unreleased]` section.
- **Where the work is tracked** — Pine tickets in
  [`.pine/tickets/`](.pine/tickets), committed with the code, so the reasoning
  and the rejected alternatives behind a change are readable from a clone.
  [`.pine/roadmap.md`](.pine/roadmap.md) is the plan.
- **Maintainer** — [@underworld14](https://github.com/underworld14), for the
  `kilaslab` organization that owns this repository.

## Design history

[`gflow-prd-v1.md`](gflow-prd-v1.md) is the product requirements document V1 was
built from. It is kept for its reasoning, not as a description of the system:
`gflow` was this project's working name, and where the document and the code
disagree the code is right. Its own header says so and gives examples. Current
planning lives in `.pine/roadmap.md` and the tickets under `.pine/tickets/`.

## License

Apache-2.0. See [LICENSE](LICENSE).
