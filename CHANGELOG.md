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

- Open-source project files: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  `SECURITY.md`, this changelog, GitHub issue forms and a pull request template.
- Opt-in JavaScript sidecar (`sidecar.enabled`): operators can load programmatic
  community n8n packages and run their `execute()` in per-tenant Node processes,
  through the same egress policy as native nodes. Off by default; see the
  [JavaScript sidecar](/operate/javascript-sidecar/) page for the runtime image,
  the trust model and what it does not protect.

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
