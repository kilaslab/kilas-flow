# Changelog

Notable changes to KilasFlow are recorded here. The format is [Keep a
Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## How this file is kept

- **No release has been cut yet.** `git tag` is empty, nothing is published to
  `ghcr.io/kilaslab/kilasflow`, and `@kilasflow/sdk` is not on npm, so `main` is
  what everyone runs and `[Unreleased]` is currently the whole history.
- A change a user can observe gets its entry under `[Unreleased]` in the pull
  request that makes it, under one of `Added`, `Changed`, `Deprecated`,
  `Removed`, `Fixed` or `Security`.
- Cutting a release moves `[Unreleased]`'s entries under the new version's own
  heading. Two tag namespaces exist and each publishes exactly one artefact:
  `vX.Y.Z` publishes the container image and `sdk-vX.Y.Z` publishes
  `@kilasflow/sdk`. What each one does is in `.github/workflows/release.yml`.
- The SDK keeps its own history in [`sdk/CHANGELOG.md`](sdk/CHANGELOG.md); this
  file is the product.

## [Unreleased]

### Added

- The canvas Chat panel renders replies as markdown (lists, emphasis, code,
  links), streams the reply while the model writes it, shows each tool call as
  a collapsible step, and links every reply to its execution. Markup in a reply
  is shown as text, never rendered. A run refused by validation lists each
  blocking issue by node name, and clicking one opens that node. Sending from a
  canvas with unsaved edits saves them first.
- The execution event stream names every event it sends: `execution.waiting`,
  `webhook.response` and the eight `ai.*` progress events are now named frames,
  and a type a client does not know yet arrives as `execution.event` with its
  real name in `type`.
- Open-source project files: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  `SECURITY.md`, this changelog, GitHub issue forms and a pull request template.
- Opt-in JavaScript sidecar (`sidecar.enabled`): operators can load programmatic
  community n8n packages and run their `execute()` in per-tenant Node processes,
  through the same egress policy as native nodes. Off by default; see the
  [JavaScript sidecar](/operate/javascript-sidecar/) page for the runtime image,
  the trust model and what it does not protect.
- `code.go_binary`, `code.cache_dir` and `code.cache_max_bytes` configure the Go
  Code node: which `go` command compiles it, where compiled artifacts and
  wazero's translations of them are kept, and how much disk they may use. The
  cache directory defaults to `./data/codecache`, inside the data volume the
  container image already persists, so a Code node's compiled artifact survives a
  restart — which matters most on a deployment with no toolchain, where a lost
  artifact cannot be rebuilt. Both caches are keyed on the runtime version, so an
  artifact built against an older host contract is rebuilt rather than loaded.
  Setting `code.cache_dir` to an empty value keeps every cache in memory, exactly
  as before.
- Scoped agent tokens: `POST /api/v1/api-keys` accepts `scopes`, `workflowId` and
  `expiresAt`, so an agent gets a key that can do one family of things —
  `workflow:read|write|run`, `datastore:read|write` — optionally bound to a
  single workflow and with an expiry. `GET /auth/me` reports all three. One
  middleware arm beside the embed gate refuses a scoped key the operations a
  person keeps: activation and publishing, deletion, import, `/tenants`, key
  management, embed sessions, credential mutation, datastore schema work,
  approvals, and everything no arm names. A key bound to one workflow reads
  another as `404`, so it cannot probe for ids it does not hold; a tenant-wide
  key and a browser session are unaffected.

- Fourteen guarded verbs — `workflow activate|deactivate|delete`, `credential
  create|update|delete`, `datastore create|rename|delete|clear|columns …`,
  `tenant delete` — behind two gates: `--yes` is consent and a tenant-wide key
  is authority, so a scoped agent token is refused with `scope_denied` whatever
  the flag says, and neither gate sends a request when it refuses.

- The debug primitives an agent's loop was missing: `workflow validate --file`
  (compile a document without saving it), `workflow duplicate`,
  `exec retry`, `run --revision` (pin a revision) and `debug eval`
  (evaluate an expression against one execution's stored node outputs, read-only,
  under the runtime's own evaluator and environment allowlist).

- Every workflow revision and publish event now records who made it —
  `actorKind`, `actorLabel`, `actorKeyId`, exposed on both listings — and the
  `X-KilasFlow-Skills-Used` header a CLI sends on mutating calls is stored as
  `actorMeta`, so the skills that actually get used are measurable.

- `kilasflow mcp serve`: an MCP server over stdio whose tools are generated from
  the command tree — one per verb, its flags as the tool's properties, and
  `confirm: true` on the guarded ones. A tool call becomes the same invocation
  the CLI dispatches, so the adapter can never describe a capability the CLI does
  not have.

- `kilasflow skills list|show|install|check|export`: the agent skills bundle
  ships **inside the binary** and these five verbs read it, install it into a
  harness directory (`--target claude|codex|agents|dir:<path>`, `--scope
  project|user`, project by default), compare an installed copy with the binary's
  and exit non-zero on drift, and export it as the harness index or a
  deterministic tarball. They are local: no server, no credential, and no
  checkout — which is what makes them work from the distroless image. See
  [`kilasflow skills`](/reference/cli/#kilasflow-skills).

### Changed

- `execution.default_timeout` defaults to 2 minutes instead of 1. An AI agent on
  a local or reasoning model routinely needs longer than a minute. Set
  `execution.default_timeout` (`KILASFLOW_EXECUTION_DEFAULT_TIMEOUT`) or a
  workflow's own `executionTimeout` to change it.
- A streamed model request is bounded by silence rather than by total length:
  the node's Timeout option (or the outbound default) now limits the wait for
  the first byte and every pause between chunks, so a reasoning model that
  streams for minutes is no longer cut off mid-answer.
- The Code node's deployment diagnostic now names what an operator has to
  provide — the minimum Go version, the exact binary that was looked for, and the
  two ways to provide it (`KILASFLOW_CODE_GO_BINARY` / `code.go_binary`, or the
  toolchain's `bin` directory on `PATH`) — instead of saying only that no
  compiler was found. The same text is used by the node catalogue, the error a
  Code node run returns and the editor's compilation status.
- A Code node that hits one of its limits now says which one it hit. Running out
  of memory inside the sandbox used to surface as the Go runtime's own
  `fatal error: out of memory` (or as a bare "exited with status 2"); it now
  reads `code ran out of memory inside its 256-page (16 MiB) memory limit`, and
  a code body whose compiled module declares more memory than the deployment's
  limit allows is refused with `this module cannot start inside the
  N-page (N MiB) memory limit` before it runs. Each of the four limits — wall
  clock, linear memory, output bytes and host calls — is a named error a caller
  can test for rather than a message to read.

- `webhook.require_auth` (environment: `KILASFLOW_WEBHOOK_REQUIRE_AUTH`) refuses
  a delivery to any webhook trigger that does not authenticate its callers, with
  a `403` naming the workflow and the fix. It is off by default, a trigger that
  verifies its own senders counts as authenticated, and the boot log states the
  posture either way.

- Per-tenant node visibility: a pack can declare the tenants its node type is
  visible to with a `visibleTo` manifest field, and an operator can override it
  with `packs.visible_to` (environment: `KILASFLOW_PACKS_VISIBLE_TO`). A node
  type outside a tenant's set is invisible in `GET /node-types` and its
  siblings, and compiling or running a document that references it fails with
  the new diagnostic `node.not_available`, distinct from `node.unknown_type`.

- The `@kilasflow/sdk` tarball now ships its `LICENSE` and `CHANGELOG.md`,
  `make sdk-release-check` proves the package the way a consumer installs it,
  and the `sdk-vX.Y.Z` release runbook lives in `sdk/RELEASING.md`.

- `kilasflow <verb>` is a command line as well as a server: bare `kilasflow`
  still serves, every verb in
  [`reference/cli.md`](docs/src/content/docs/reference/cli.md) maps to exactly
  one API operation, and `kilasflow api <operation-id>` reaches every operation
  in the API contract. `--json` prints one stable envelope per invocation and
  the exit code says whether to fix the call, ask the user, wait or report — a
  refused scope is its own code, and a guarded verb refuses without `--yes`.
  `make smoke-cli` proves the tree against a booted server.

- Idempotent execution requests and datastore writes: an `Idempotency-Key`
  header on `POST /api/v1/workflows/{id}/run`, `POST /api/v1/datastores/{id}/rows`
  and `POST /api/v1/datastores/{id}/rows/upsert` makes a retry return the first
  request's outcome instead of repeating its side effect, with durable
  tenant-scoped keys in a new `idempotency_keys` table, retention set by
  `idempotency.retention`, and `Retry-After` on the in-flight 409. The host SDK
  exposes it as `idempotencyKey` on `runWorkflow`, `insertDatastoreRow` and
  `upsertDatastoreRow`.

- `DELETE /api/v1/tenants/{id}` on the operator surface: deletes a customer and
  everything it owns — executions and their payload files, workflows and
  versions, credentials, schedules, webhook deliveries, datastores with their
  physical tables, vector rows, accounts and keys — and answers with the counts
  it removed, per table. It is irreversible, needs the operator credential, and
  is idempotent: repeat it until it reports zeros. See
  [Tenant deletion](docs/src/content/docs/operate/tenant-deletion.md).

- Datastore: an `Increment` row operation on the Data table node, the
  `POST /api/v1/datastores/{id}/rows/increment` operation, and
  `incrementDatastoreRows` in the SDK. One statement adds to a number column on
  every matching row — atomic per row on SQLite and PostgreSQL alike — and
  returns each row as that statement left it, so a counter no longer loses a
  write to a concurrent reader-writer.

- Datastore: an optional `ifUpdatedAt` precondition on the row update and delete
  operations. The write lands only if the row still carries the stamp the caller
  read; a stale stamp answers 409 with the row's current `updatedAt` in
  `errors[0].value`, so the caller retries without a second read.

- Datastore: an upsert whose filter is a single `id eq` condition is one
  `INSERT ... ON CONFLICT (id) DO UPDATE` statement on both drivers. It creates a
  missing row at exactly that id and never inserts the same id twice. Explicit
  ids are bounded to 1..9007199254740991.


### Changed

- Datastore: `updatedAt` is strictly increasing per row. Every write takes the
  greater of the wall clock and one millisecond past the row's current stamp, so
  "untouched since I read it" is a question the column can answer. Under a
  sustained burst above about a thousand writes a second to one row, the stamp
  runs ahead of the wall clock by the length of the burst.

- A webhook binding that had no minted route (only a database that predates
  routes holds one) is given one at boot by the `webhook_route_backfill`
  migration, so its address changes from `/webhook/<label>` to
  `/webhook/<route>`. Read it with `GET /workflows/{id}/webhooks`, and activate a
  Telegram or WAHA workflow again so the sender is given the new address.
  Bindings that already have a route keep it.

- `embed.session_ttl` now sets the default lifetime of an embed session token
  (it was read by nothing); values below one second or above 30 minutes are
  refused at startup.

- `branding.name` and `branding.logo` now default the header of embedded editor
  sessions that carry no branding of their own (they were read by nothing); the
  default of `branding.name` changed from `KilasFlow` to empty, so existing
  embeds keep the headerless look they have today.

- `GET /node-types` and its sibling operations (icon, load-options,
  load-schema) are narrowed to the caller's tenant, embed sessions included, so
  a node type scoped to other tenants answers exactly as an unregistered one
  does. The catalogue responds `Cache-Control: private, no-cache` because its
  body now depends on who is asking.

- The executions list asks for 50 runs a page, says how many it is showing and
  whether more are available, and the workflows page shows the workspace total
  in its heading and "N of M" while a search narrows the list.

- Boot now migrates every datastore to the schema version the build serves,
  after the SQL migrations and before the listener opens, and **refuses to
  start** when it cannot: a datastore ahead of the build, or behind it with no
  step to reach it, is named on stderr and the process exits `1` instead of
  serving requests it would refuse anyway. A datastore an older peer process
  created behind is picked up by a retry every thirty seconds, without a
  restart.

- `GET /api/v1/ready` gained a `datastores` block (schema versions and counts
  only) and answers `503` while a datastore migration is outstanding. The same
  block is carried in that `503`'s problem document, so the spread stays
  machine-readable in the state that reports it.

- `DELETE /api/v1/tenants/{id}` on the operator surface: deletes a customer and
  everything it owns — executions and their payload files, workflows and
  versions, credentials, schedules, webhook deliveries, datastores with their
  physical tables, vector rows, accounts and keys — and answers with the counts
  it removed, per table. It is irreversible, needs the operator credential, and
  is idempotent: repeat it until it reports zeros. See
  [Tenant deletion](docs/src/content/docs/operate/tenant-deletion.md).


### Removed

- `branding.favicon` and `branding.powered_by` (read by nothing; a configuration
  that still sets them is warned about at start).


### Fixed

- A chat model node that never set "Stream output" now streams, as the editor
  already showed. An absent `stream` key used to mean off.
- An agent ended by the workflow's own execution timeout says so, and names
  `executionTimeout` and `execution.default_timeout`, instead of blaming the
  deployment's ten-minute model ceiling.
- Clicking a validation issue in the editor opens the node it names. Svelte
  Flow's stale selection report used to deselect it again at once.
- Datastore: `Insert` on SQLite read its id back with a second connection
  running `SELECT last_insert_rowid()`, which is per-connection state, so a
  concurrent insert could return another writer's row. Both drivers now use
  `INSERT ... RETURNING id`.

- Datastore: `updatedAt` could repeat within one millisecond, so a precondition
  holding a stale stamp was accepted and a compare-and-swap retry loop lost
  updates. The stamp no longer repeats.

- Datastore: `Increment` issued `SELECT`, `UPDATE`, `SELECT` while the node and
  API wording claimed one statement. It is one `UPDATE ... RETURNING` now, and
  the rows it returns are that statement's own images.

- Auto-refresh on the executions list now keeps working, and keeps the runs you
  have already loaded, when the workspace has more runs than fit on one page,
  and again after the tab has been in the background.
