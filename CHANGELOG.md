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

### Changed

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

### Removed

- `branding.favicon` and `branding.powered_by` (read by nothing; a configuration
  that still sets them is warned about at start).

### Changed

- The executions list asks for 50 runs a page, says how many it is showing and
  whether more are available, and the workflows page shows the workspace total
  in its heading and "N of M" while a search narrows the list.

### Fixed

- Auto-refresh on the executions list now keeps working, and keeps the runs you
  have already loaded, when the workspace has more runs than fit on one page,
  and again after the tab has been in the background.

### Changed

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

### Security

- `SECURITY.md` documents the private disclosure path, and private vulnerability
  reporting is enabled on the repository.
- An inbound webhook is matched by method and route only. A binding with no
  minted route used to be found by its `path` label, which two tenants may hold
  at once and which no request identifies a tenant by, so a caller who named a
  label could run another tenant's workflow. Such rows are backfilled with a
  route by the `webhook_route_backfill` migration, and a label is never a lookup
  key, including for a CORS preflight and for a hosted form page.
- The `examples/host-page` example served files outside its SDK directory: a
  `..%2f` request to its static route was joined onto the server's directory and
  answered, reaching `server.mjs` and — on Linux — `/proc/self/environ`, which
  holds the host's API key. The route now resolves each request against the
  installed SDK's `dist/` and answers 404 for anything outside it. A missing
  `index.html` crashed the same way one branch above it — the page route wrote
  its 200 before reading the file, so the failed read threw out of an async
  handler that had nothing left to answer with — and is a 500 that leaves the
  example serving now.
