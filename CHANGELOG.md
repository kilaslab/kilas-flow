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

### Security

- `SECURITY.md` documents the private disclosure path, and private vulnerability
  reporting is enabled on the repository.
