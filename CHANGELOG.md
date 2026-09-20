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

### Changed

- A webhook binding that had no minted route (only a database that predates
  routes holds one) is given one at boot by the `webhook_route_backfill`
  migration, so its address changes from `/webhook/<label>` to
  `/webhook/<route>`. Read it with `GET /workflows/{id}/webhooks`, and activate a
  Telegram or WAHA workflow again so the sender is given the new address.
  Bindings that already have a route keep it.

### Security

- `SECURITY.md` documents the private disclosure path, and private vulnerability
  reporting is enabled on the repository.
- An inbound webhook is matched by method and route only. A binding with no
  minted route used to be found by its `path` label, which two tenants may hold
  at once and which no request identifies a tenant by, so a caller who named a
  label could run another tenant's workflow. Such rows are backfilled with a
  route by the `webhook_route_backfill` migration, and a label is never a lookup
  key, including for a CORS preflight and for a hosted form page.
